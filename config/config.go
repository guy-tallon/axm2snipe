package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for axm2snipe.
type Config struct {
	ABM    ABMConfig    `yaml:"abm"`
	SnipeIT SnipeITConfig `yaml:"snipe_it"`
	Sync   SyncConfig   `yaml:"sync"`
}

// ABMConfig holds Apple Business/School Manager settings.
type ABMConfig struct {
	ClientID   string `yaml:"client_id"`
	KeyID      string `yaml:"key_id"`
	PrivateKey string `yaml:"private_key"` // path to PEM file or raw PEM string
}

// SnipeITConfig holds Snipe-IT API settings.
type SnipeITConfig struct {
	URL                      string `yaml:"url"`
	APIKey                   string `yaml:"api_key"`
	ManufacturerID           int    `yaml:"manufacturer_id"`              // Apple manufacturer ID in Snipe
	DefaultStatusID          int    `yaml:"default_status_id"`            // Status for newly created assets
	CategoryID               int    `yaml:"category_id"`                  // Default category for new models (fallback)
	ComputerCategoryID       int    `yaml:"computer_category_id"`         // Category for computer models (Mac)
	MobileCategoryID         int    `yaml:"mobile_category_id"`           // Category for mobile models (iPhone, iPad)
	CustomFieldsetID         int    `yaml:"custom_fieldset_id"`           // Optional fieldset for new models
}

// SyncConfig holds sync behavior settings.
type SyncConfig struct {
	DryRun           bool              `yaml:"dry_run"`
	Force            bool              `yaml:"force"`             // ignore timestamps, always update
	RateLimit        bool              `yaml:"rate_limit"`        // enable rate limiting
	UpdateOnly       bool              `yaml:"update_only"`       // only update existing assets, never create new ones
	ProductFamilies  []string          `yaml:"product_families"`  // filter by product family (Mac, iPhone, iPad, etc.)
	SetName          bool              `yaml:"set_name"`          // set asset name on create (default false)
	FieldMapping     map[string]string `yaml:"field_mapping"`     // snipe field -> ABM attribute mapping
}

// Load reads configuration from a YAML file and applies environment variable overrides.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Environment variable overrides
	if v := os.Getenv("AXM_ABM_CLIENT_ID"); v != "" {
		cfg.ABM.ClientID = v
	}
	if v := os.Getenv("AXM_ABM_KEY_ID"); v != "" {
		cfg.ABM.KeyID = v
	}
	if v := os.Getenv("AXM_ABM_PRIVATE_KEY"); v != "" {
		cfg.ABM.PrivateKey = v
	}
	if v := os.Getenv("AXM_SNIPE_URL"); v != "" {
		cfg.SnipeIT.URL = v
	}
	if v := os.Getenv("AXM_SNIPE_API_KEY"); v != "" {
		cfg.SnipeIT.APIKey = v
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all required fields are set.
func (c *Config) Validate() error {
	if c.ABM.ClientID == "" {
		return fmt.Errorf("abm.client_id is required")
	}
	if c.ABM.KeyID == "" {
		return fmt.Errorf("abm.key_id is required")
	}
	if c.ABM.PrivateKey == "" {
		return fmt.Errorf("abm.private_key is required")
	}
	if c.SnipeIT.URL == "" {
		return fmt.Errorf("snipe_it.url is required")
	}
	if c.SnipeIT.APIKey == "" {
		return fmt.Errorf("snipe_it.api_key is required")
	}
	if c.SnipeIT.ManufacturerID == 0 {
		return fmt.Errorf("snipe_it.manufacturer_id is required")
	}
	if c.SnipeIT.DefaultStatusID == 0 {
		return fmt.Errorf("snipe_it.default_status_id is required")
	}
	if c.SnipeIT.CategoryID == 0 && c.SnipeIT.ComputerCategoryID == 0 && c.SnipeIT.MobileCategoryID == 0 {
		return fmt.Errorf("snipe_it.category_id (or computer_category_id/mobile_category_id) is required")
	}
	return nil
}

// CategoryIDForFamily returns the appropriate Snipe-IT category ID for a given
// ABM product family (e.g. "Mac", "iPhone", "iPad"). Falls back to CategoryID.
func (c *SnipeITConfig) CategoryIDForFamily(productFamily string) int {
	switch productFamily {
	case "Mac":
		if c.ComputerCategoryID != 0 {
			return c.ComputerCategoryID
		}
	case "iPhone", "iPad", "Watch", "Vision":
		if c.MobileCategoryID != 0 {
			return c.MobileCategoryID
		}
	}
	if c.CategoryID != 0 {
		return c.CategoryID
	}
	// Last resort: return whichever specific one is set
	if c.ComputerCategoryID != 0 {
		return c.ComputerCategoryID
	}
	return c.MobileCategoryID
}
