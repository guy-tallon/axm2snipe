// Package snipe wraps the go-snipeit library and adds missing API endpoints
// (models, users) needed for the sync process.
package snipe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	snipeit "github.com/michellepellon/go-snipeit"
)

// ErrDryRun is returned when a write operation is attempted in dry-run mode.
var ErrDryRun = fmt.Errorf("write blocked: dry-run mode is enabled")

// Client wraps the go-snipeit client and adds additional API methods.
type Client struct {
	Assets    *snipeit.AssetsService
	Fields    *snipeit.FieldsService
	Fieldsets *snipeit.FieldsetsService
	DryRun    bool // when true, all non-GET requests are blocked
	baseURL   string
	apiKey    string
	http      *http.Client
}

// NewClient creates a new Snipe-IT client.
func NewClient(baseURL, apiKey string) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")

	sc, err := snipeit.NewClient(baseURL, apiKey)
	if err != nil {
		return nil, fmt.Errorf("creating snipe-it client: %w", err)
	}

	return &Client{
		Assets:    sc.Assets,
		Fields:    sc.Fields,
		Fieldsets: sc.Fieldsets,
		baseURL:   baseURL,
		apiKey:    apiKey,
		http:      &http.Client{},
	}, nil
}

// do performs an authenticated API request and decodes the JSON response.
// In dry-run mode, all non-GET requests are blocked and return ErrDryRun.
func (c *Client) do(ctx context.Context, method, path string, body interface{}, result interface{}) (*http.Response, error) {
	if c.DryRun && method != http.MethodGet {
		return nil, ErrDryRun
	}

	url := c.baseURL + "/" + strings.TrimLeft(path, "/")

	const maxRetries = 5

	var resp *http.Response
	var respBody []byte

	for attempt := 0; ; attempt++ {
		var bodyReader io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("marshaling request body: %w", err)
			}
			bodyReader = bytes.NewReader(data)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		resp, err = c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("executing request: %w", err)
		}

		respBody, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return resp, fmt.Errorf("reading response body: %w", err)
		}

		if resp.StatusCode == 429 && attempt < maxRetries {
			// Parse retryAfter from Snipe-IT response if available
			wait := time.Duration(attempt+1) * 2 * time.Second
			var retryResp struct {
				RetryAfter int `json:"retryAfter"`
			}
			if json.Unmarshal(respBody, &retryResp) == nil && retryResp.RetryAfter > 0 {
				wait = time.Duration(retryResp.RetryAfter) * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}

		break
	}

	if resp.StatusCode >= 400 {
		return resp, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return resp, fmt.Errorf("decoding response: %w (body: %s)", err, string(respBody))
		}
	}

	// Snipe-IT returns 200 with {"status":"error"} for validation failures
	var statusCheck struct {
		Status   string         `json:"status"`
		Messages map[string]any `json:"messages"`
	}
	if json.Unmarshal(respBody, &statusCheck) == nil && statusCheck.Status == "error" {
		return resp, fmt.Errorf("validation error: %s", string(respBody))
	}

	return resp, nil
}

// --- Models API ---

// SnipeModel represents a Snipe-IT asset model.
type SnipeModel struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	ModelNumber  string `json:"model_number"`
	CategoryID   int    `json:"category_id,omitempty"`
	ManufacturerID int  `json:"manufacturer_id,omitempty"`
	FieldsetID   int    `json:"fieldset_id,omitempty"`
	Notes        string `json:"notes,omitempty"`
}

// ModelsResponse is the response from GET /api/v1/models.
type ModelsResponse struct {
	Total int          `json:"total"`
	Rows  []SnipeModel `json:"rows"`
}

// ModelResponse is the response from POST/PUT /api/v1/models.
type ModelResponse struct {
	Status  string     `json:"status"`
	Messages interface{} `json:"messages"`
	Payload *SnipeModel `json:"payload"`
}

// ListModels returns all models from Snipe-IT, handling pagination.
func (c *Client) ListModels(ctx context.Context) ([]SnipeModel, error) {
	var allModels []SnipeModel
	offset := 0
	limit := 500

	for {
		var resp ModelsResponse
		path := fmt.Sprintf("api/v1/models?limit=%d&offset=%d", limit, offset)
		if _, err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("listing models: %w", err)
		}
		allModels = append(allModels, resp.Rows...)
		if len(allModels) >= resp.Total {
			break
		}
		offset += limit
	}

	return allModels, nil
}

// CreateModel creates a new asset model in Snipe-IT.
func (c *Client) CreateModel(ctx context.Context, model SnipeModel) (*SnipeModel, error) {
	var resp ModelResponse
	if _, err := c.do(ctx, http.MethodPost, "api/v1/models", model, &resp); err != nil {
		return nil, fmt.Errorf("creating model: %w", err)
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("creating model failed: %v", resp.Messages)
	}
	return resp.Payload, nil
}

// --- Users API ---

// SnipeUser represents a Snipe-IT user.
type SnipeUser struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// UsersResponse is the response from GET /api/v1/users.
type UsersResponse struct {
	Total int         `json:"total"`
	Rows  []SnipeUser `json:"rows"`
}

// ListUsers returns all users from Snipe-IT, handling pagination.
func (c *Client) ListUsers(ctx context.Context) ([]SnipeUser, error) {
	var allUsers []SnipeUser
	offset := 0
	limit := 500

	for {
		var resp UsersResponse
		path := fmt.Sprintf("api/v1/users?limit=%d&offset=%d", limit, offset)
		if _, err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("listing users: %w", err)
		}
		allUsers = append(allUsers, resp.Rows...)
		if len(allUsers) >= resp.Total {
			break
		}
		offset += limit
	}

	return allUsers, nil
}

// --- Suppliers API ---

// SnipeSupplier represents a Snipe-IT supplier.
type SnipeSupplier struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// SuppliersResponse is the response from GET /api/v1/suppliers.
type SuppliersResponse struct {
	Total int              `json:"total"`
	Rows  []SnipeSupplier  `json:"rows"`
}

// SupplierResponse is the response from POST /api/v1/suppliers.
type SupplierResponse struct {
	Status   string         `json:"status"`
	Messages interface{}    `json:"messages"`
	Payload  *SnipeSupplier `json:"payload"`
}

// ListSuppliers returns all suppliers from Snipe-IT, handling pagination.
func (c *Client) ListSuppliers(ctx context.Context) ([]SnipeSupplier, error) {
	var all []SnipeSupplier
	offset := 0
	limit := 500

	for {
		var resp SuppliersResponse
		path := fmt.Sprintf("api/v1/suppliers?limit=%d&offset=%d", limit, offset)
		if _, err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("listing suppliers: %w", err)
		}
		all = append(all, resp.Rows...)
		if len(all) >= resp.Total {
			break
		}
		offset += limit
	}

	return all, nil
}

// CreateSupplier creates a new supplier in Snipe-IT.
func (c *Client) CreateSupplier(ctx context.Context, name string) (*SnipeSupplier, error) {
	var resp SupplierResponse
	body := map[string]string{"name": name}
	if _, err := c.do(ctx, http.MethodPost, "api/v1/suppliers", body, &resp); err != nil {
		return nil, fmt.Errorf("creating supplier: %w", err)
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("creating supplier failed: %v", resp.Messages)
	}
	return resp.Payload, nil
}

// --- Asset operations using raw HTTP (for custom fields support) ---

// AssetBySerialResponse wraps the byserial endpoint response.
type AssetBySerialResponse struct {
	Total int              `json:"total"`
	Rows  []map[string]any `json:"rows"`
}

// GetAssetBySerial looks up an asset by serial number, returning raw JSON
// to preserve custom fields.
func (c *Client) GetAssetBySerial(ctx context.Context, serial string) (*AssetBySerialResponse, error) {
	var resp AssetBySerialResponse
	path := fmt.Sprintf("api/v1/hardware/byserial/%s", serial)
	if _, err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateAsset creates a new hardware asset.
func (c *Client) CreateAsset(ctx context.Context, payload map[string]any) (map[string]any, error) {
	var resp map[string]any
	if _, err := c.do(ctx, http.MethodPost, "api/v1/hardware", payload, &resp); err != nil {
		return nil, fmt.Errorf("creating asset: %w", err)
	}
	return resp, nil
}

// UpdateAsset updates an existing hardware asset by ID.
func (c *Client) UpdateAsset(ctx context.Context, id int, payload map[string]any) (map[string]any, error) {
	var resp map[string]any
	path := fmt.Sprintf("api/v1/hardware/%d", id)
	if _, err := c.do(ctx, http.MethodPatch, path, payload, &resp); err != nil {
		return nil, fmt.Errorf("updating asset %d: %w", id, err)
	}
	return resp, nil
}

// --- Custom fields setup ---

// FieldDef defines a custom field to create in Snipe-IT.
type FieldDef struct {
	Name        string // display name (will be prefixed with "AXM: ")
	Element     string // form element type: text, textarea, radio, listbox, checkbox
	Format      string // validation format: ANY, DATE, BOOLEAN, etc.
	HelpText    string // help text shown to users
	FieldValues string // newline-separated list of allowed values (for radio/listbox)
}

// SetupFields creates or updates custom fields in Snipe-IT and associates them
// with the given fieldset. If a field with the same name already exists, it is
// updated to match the definition. Returns a map of field name -> db_column_name
// for use in field_mapping configuration.
func (c *Client) SetupFields(fieldsetID int, fields []FieldDef) (map[string]string, error) {
	// Fetch all existing fields to check for duplicates
	existing, _, err := c.Fields.List(nil)
	if err != nil {
		return nil, fmt.Errorf("listing existing fields: %w", err)
	}
	existingByName := make(map[string]snipeit.Field)
	for _, f := range existing.Rows {
		existingByName[f.Name] = f
	}

	results := make(map[string]string)

	for _, f := range fields {
		field := snipeit.Field{}
		field.Name = f.Name
		field.Element = f.Element
		field.Format = f.Format
		field.HelpText = f.HelpText
		field.FieldValues = f.FieldValues

		var fieldID int
		var dbColumn string

		if ex, ok := existingByName[f.Name]; ok {
			// Field already exists — update it
			resp, _, err := c.Fields.Update(ex.ID, field)
			if err != nil {
				return results, fmt.Errorf("updating field %q: %w", f.Name, err)
			}
			if resp.Status != "success" {
				return results, fmt.Errorf("updating field %q: %s", f.Name, resp.Message)
			}
			fieldID = resp.Payload.ID
			dbColumn = resp.Payload.DBColumnName
			// Update response may not include db_column_name — use the one from List
			if dbColumn == "" {
				dbColumn = ex.DBColumnName
			}
		} else {
			// Create new field
			resp, _, err := c.Fields.Create(field)
			if err != nil {
				return results, fmt.Errorf("creating field %q: %w", f.Name, err)
			}
			if resp.Status != "success" {
				return results, fmt.Errorf("creating field %q: %s", f.Name, resp.Message)
			}
			fieldID = resp.Payload.ID
			dbColumn = resp.Payload.DBColumnName
		}

		results[f.Name] = dbColumn

		if fieldsetID > 0 {
			if _, err := c.Fields.Associate(fieldID, fieldsetID); err != nil {
				return results, fmt.Errorf("associating field %q (ID %d) with fieldset %d: %w", f.Name, fieldID, fieldsetID, err)
			}
		}
	}

	// Re-fetch field list to fill in any missing db_column_name values
	hasMissing := false
	for _, v := range results {
		if v == "" {
			hasMissing = true
			break
		}
	}
	if hasMissing {
		refreshed, _, err := c.Fields.List(nil)
		if err == nil {
			byName := make(map[string]string)
			for _, f := range refreshed.Rows {
				byName[f.Name] = f.DBColumnName
			}
			for name, dbCol := range results {
				if dbCol == "" {
					if col, ok := byName[name]; ok && col != "" {
						results[name] = col
					}
				}
			}
		}
	}

	return results, nil
}
