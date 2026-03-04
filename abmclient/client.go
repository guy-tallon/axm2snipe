// Package abmclient provides a flexible ABM API client that handles
// Apple's inconsistent JSON types (e.g. MAC addresses returned as string
// instead of []string). It uses the abm library only for authentication.
package abmclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/zchee/abm"
	"golang.org/x/oauth2"
)

const baseURL = "https://api-business.apple.com"

// Client wraps an authenticated HTTP client for the ABM API.
type Client struct {
	http *http.Client
}

// NewClient creates a new ABM client using the abm library for auth.
func NewClient(ctx context.Context, clientID, keyID, privateKey string) (*Client, error) {
	assertion, err := abm.NewAssertion(ctx, clientID, keyID, privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating ABM assertion: %w", err)
	}

	ts, err := abm.NewTokenSource(ctx, nil, clientID, assertion, "")
	if err != nil {
		return nil, fmt.Errorf("creating ABM token source: %w", err)
	}

	httpClient := oauth2.NewClient(ctx, ts)

	return &Client{http: httpClient}, nil
}

// StringOrStrings handles ABM fields that may be a string or []string.
type StringOrStrings []string

func (s *StringOrStrings) UnmarshalJSON(data []byte) error {
	// Try array first
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*s = arr
		return nil
	}
	// Try single string
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		if str != "" {
			*s = []string{str}
		} else {
			*s = nil
		}
		return nil
	}
	return fmt.Errorf("cannot unmarshal %s into string or []string", string(data))
}

// Device represents an ABM organization device with flexible field types.
type Device struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Attributes    *DeviceAttributes `json:"attributes,omitempty"`
	Relationships *struct {
		AssignedServer *struct {
			Data *struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"data"`
		} `json:"assignedServer"`
	} `json:"relationships,omitempty"`
}

// DeviceAttributes holds device attributes with flexible MAC/IMEI types.
type DeviceAttributes struct {
	AddedToOrgDateTime      time.Time        `json:"addedToOrgDateTime,omitempty"`
	ReleasedFromOrgDateTime time.Time        `json:"releasedFromOrgDateTime,omitempty"`
	Color                   string           `json:"color,omitempty"`
	DeviceCapacity          string           `json:"deviceCapacity,omitempty"`
	DeviceModel             string           `json:"deviceModel,omitempty"`
	EID                     string           `json:"eid,omitempty"`
	IMEI                    StringOrStrings  `json:"imei,omitempty"`
	MEID                    StringOrStrings  `json:"meid,omitempty"`
	WifiMacAddress          StringOrStrings  `json:"wifiMacAddress,omitempty"`
	BluetoothMacAddress     StringOrStrings  `json:"bluetoothMacAddress,omitempty"`
	EthernetMacAddress      StringOrStrings  `json:"ethernetMacAddress,omitempty"`
	OrderDateTime           time.Time        `json:"orderDateTime,omitempty"`
	OrderNumber             string           `json:"orderNumber,omitempty"`
	PartNumber              string           `json:"partNumber,omitempty"`
	ProductFamily           string           `json:"productFamily,omitempty"`
	ProductType             string           `json:"productType,omitempty"`
	PurchaseSourceType      string           `json:"purchaseSourceType,omitempty"`
	PurchaseSourceID        string           `json:"purchaseSourceId,omitempty"`
	SerialNumber            string           `json:"serialNumber,omitempty"`
	Status                  string           `json:"status,omitempty"`
	UpdatedDateTime         time.Time        `json:"updatedDateTime,omitempty"`
	AssignedServer          string           `json:"-"` // populated from relationships
}

// IncludedResource represents an included JSON:API resource (e.g. MDM server).
type IncludedResource struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		ServerName string `json:"serverName,omitempty"`
	} `json:"attributes"`
}

// DevicesResponse is the paginated response from GET /v1/orgDevices.
type DevicesResponse struct {
	Data     []Device           `json:"data"`
	Included []IncludedResource `json:"included,omitempty"`
	Links    struct {
		Self  string `json:"self"`
		First string `json:"first,omitempty"`
		Next  string `json:"next,omitempty"`
	} `json:"links"`
	Meta struct {
		Paging struct {
			Total int `json:"total"`
			Limit int `json:"limit"`
		} `json:"paging"`
	} `json:"meta"`
}

// AppleCareCoverage holds AppleCare coverage data.
type AppleCareCoverage struct {
	AgreementNumber        string    `json:"agreementNumber,omitempty"`
	ContractCancelDateTime time.Time `json:"contractCancelDateTime,omitempty"`
	Description            string    `json:"description,omitempty"`
	EndDateTime            time.Time `json:"endDateTime,omitempty"`
	IsCanceled             bool      `json:"isCanceled,omitempty"`
	IsRenewable            bool      `json:"isRenewable,omitempty"`
	PaymentType            string    `json:"paymentType,omitempty"`
	StartDateTime          time.Time `json:"startDateTime,omitempty"`
	Status                 string    `json:"status,omitempty"`
}

// AppleCareCoverageResponse is the response from the AppleCare endpoint.
type AppleCareCoverageResponse struct {
	Data []struct {
		ID         string             `json:"id"`
		Type       string             `json:"type"`
		Attributes *AppleCareCoverage `json:"attributes,omitempty"`
	} `json:"data"`
}

// GetDevices fetches a page of devices with the given limit, including assigned MDM server.
func (c *Client) GetDevices(ctx context.Context, limit int) (*DevicesResponse, error) {
	url := fmt.Sprintf("%s/v1/orgDevices?limit=%d", baseURL, limit)
	return c.getDevicesFromURL(ctx, url)
}

// GetDevicesFromURL fetches devices from an absolute pagination URL.
func (c *Client) GetDevicesFromURL(ctx context.Context, url string) (*DevicesResponse, error) {
	return c.getDevicesFromURL(ctx, url)
}

func (c *Client) getDevicesFromURL(ctx context.Context, url string) (*DevicesResponse, error) {
	body, err := c.doGet(ctx, url)
	if err != nil {
		return nil, err
	}
	var resp DevicesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding devices response: %w", err)
	}
	return &resp, nil
}

// GetAllDevices fetches all devices, following pagination.
// Resolves assigned MDM server names from included resources.
func (c *Client) GetAllDevices(ctx context.Context) ([]Device, int, error) {
	var all []Device
	serverNames := make(map[string]string) // server ID -> name

	resp, err := c.GetDevices(ctx, 1000)
	if err != nil {
		return nil, 0, err
	}
	total := resp.Meta.Paging.Total
	for _, inc := range resp.Included {
		if inc.Type == "mdmServers" && inc.Attributes.ServerName != "" {
			serverNames[inc.ID] = inc.Attributes.ServerName
		}
	}
	all = append(all, resp.Data...)

	nextURL := resp.Links.Next
	for nextURL != "" {
		if err := ctx.Err(); err != nil {
			return all, total, err
		}
		resp, err := c.GetDevicesFromURL(ctx, nextURL)
		if err != nil {
			return all, total, fmt.Errorf("fetching next page: %w", err)
		}
		for _, inc := range resp.Included {
			if inc.Type == "mdmServers" && inc.Attributes.ServerName != "" {
				serverNames[inc.ID] = inc.Attributes.ServerName
			}
		}
		all = append(all, resp.Data...)
		nextURL = resp.Links.Next
	}

	// Resolve server IDs to names
	for i := range all {
		d := &all[i]
		if d.Relationships != nil && d.Relationships.AssignedServer != nil &&
			d.Relationships.AssignedServer.Data != nil && d.Attributes != nil {
			serverID := d.Relationships.AssignedServer.Data.ID
			if name, ok := serverNames[serverID]; ok {
				d.Attributes.AssignedServer = name
			} else {
				d.Attributes.AssignedServer = serverID
			}
		}
	}

	return all, total, nil
}

// GetAppleCareCoverage fetches AppleCare coverage for a device.
func (c *Client) GetAppleCareCoverage(ctx context.Context, deviceID string) (*AppleCareCoverage, error) {
	url := fmt.Sprintf("%s/v1/orgDevices/%s/appleCareCoverage", baseURL, deviceID)
	body, err := c.doGet(ctx, url)
	if err != nil {
		return nil, err
	}
	var resp AppleCareCoverageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decoding applecare response: %w", err)
	}
	if len(resp.Data) > 0 && resp.Data[0].Attributes != nil {
		return resp.Data[0].Attributes, nil
	}
	return nil, nil
}

func (c *Client) doGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
