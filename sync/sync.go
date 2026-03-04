// Package sync implements the core synchronization logic between
// Apple Business Manager and Snipe-IT.
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/CampusTech/axm2snipe/abmclient"
	"github.com/CampusTech/axm2snipe/config"
	"github.com/CampusTech/axm2snipe/snipe"
)

var log = logrus.New()

// SetLogLevel sets the logger level.
func SetLogLevel(level logrus.Level) {
	log.SetLevel(level)
}

// Stats tracks sync operation counts.
type Stats struct {
	Total     int
	Created   int
	Updated   int
	Skipped   int
	Errors    int
	ModelNew  int
}

// Engine performs the sync between ABM and Snipe-IT.
type Engine struct {
	abm       *abmclient.Client
	snipe     *snipe.Client
	cfg       *config.Config
	models    map[string]int // model identifier -> snipe model ID
	suppliers map[string]int // supplier name (lowercased) -> snipe supplier ID
	stats     Stats
}

// NewEngine creates a new sync engine.
func NewEngine(abmClient *abmclient.Client, snipeClient *snipe.Client, cfg *config.Config) *Engine {
	return &Engine{
		abm:       abmClient,
		snipe:     snipeClient,
		cfg:       cfg,
		models:    make(map[string]int),
		suppliers: make(map[string]int),
	}
}

// RunSingle syncs a single device identified by serial number.
func (e *Engine) RunSingle(ctx context.Context, serial string) (*Stats, error) {
	serial = strings.ToUpper(serial)
	log.Infof("Syncing single device: %s", serial)

	if err := e.loadModels(ctx); err != nil {
		return nil, fmt.Errorf("loading snipe models: %w", err)
	}
	if err := e.loadSuppliers(ctx); err != nil {
		return nil, fmt.Errorf("loading snipe suppliers: %w", err)
	}

	devices, err := e.fetchABMDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching ABM devices: %w", err)
	}

	var found bool
	for _, device := range devices {
		if strings.EqualFold(deviceSerial(device), serial) {
			found = true
			if err := e.processDevice(ctx, device); err != nil {
				log.WithError(err).WithField("serial", serial).Error("Failed to process device")
				e.stats.Errors++
			}
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("device %s not found in Apple Business Manager", serial)
	}

	return &e.stats, nil
}

// Run executes the full sync process.
func (e *Engine) Run(ctx context.Context) (*Stats, error) {
	log.Info("Starting axm2snipe sync")

	// Step 1: Load existing Snipe-IT models and suppliers into cache
	if err := e.loadModels(ctx); err != nil {
		return nil, fmt.Errorf("loading snipe models: %w", err)
	}
	log.Infof("Loaded %d existing models from Snipe-IT", len(e.models))

	if err := e.loadSuppliers(ctx); err != nil {
		return nil, fmt.Errorf("loading snipe suppliers: %w", err)
	}
	log.Infof("Loaded %d existing suppliers from Snipe-IT", len(e.suppliers))

	// Step 2: Fetch all devices from ABM
	devices, err := e.fetchABMDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching ABM devices: %w", err)
	}
	log.Infof("Fetched %d devices from Apple Business Manager", len(devices))

	// Step 3: Process each device
	for i, device := range devices {
		if err := ctx.Err(); err != nil {
			return &e.stats, err
		}

		if err := e.processDevice(ctx, device); err != nil {
			log.WithError(err).WithField("serial", deviceSerial(device)).Error("Failed to process device")
			e.stats.Errors++
		}

		if (i+1)%50 == 0 {
			log.Infof("Progress: %d/%d devices processed", i+1, len(devices))
		}
	}

	log.Infof("Sync complete: total=%d created=%d updated=%d skipped=%d errors=%d new_models=%d",
		e.stats.Total, e.stats.Created, e.stats.Updated, e.stats.Skipped, e.stats.Errors, e.stats.ModelNew)

	return &e.stats, nil
}

// loadModels fetches all models from Snipe-IT and builds a lookup map.
// Models are indexed by both model number and name for flexible matching.
func (e *Engine) loadModels(ctx context.Context) error {
	models, err := e.snipe.ListModels(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		if m.ModelNumber != "" {
			e.models[m.ModelNumber] = m.ID
		}
		if m.Name != "" {
			e.models[m.Name] = m.ID
		}
	}
	return nil
}

// loadSuppliers fetches all suppliers from Snipe-IT and builds a lookup map.
func (e *Engine) loadSuppliers(ctx context.Context) error {
	suppliers, err := e.snipe.ListSuppliers(ctx)
	if err != nil {
		return err
	}
	for _, s := range suppliers {
		if s.Name != "" {
			e.suppliers[strings.ToLower(s.Name)] = s.ID
		}
	}
	return nil
}

// ensureSupplier checks if a supplier exists in Snipe-IT by name, creating it if needed.
func (e *Engine) ensureSupplier(ctx context.Context, name string) (int, error) {
	if name == "" {
		return 0, nil
	}

	if id, ok := e.suppliers[strings.ToLower(name)]; ok {
		return id, nil
	}

	if e.cfg.Sync.UpdateOnly {
		log.WithField("supplier", name).Debug("Supplier not found in Snipe-IT (update_only mode), skipping")
		return 0, nil
	}

	if e.cfg.Sync.DryRun {
		log.WithField("supplier", name).Info("[DRY RUN] Would create supplier")
		return 0, nil
	}

	newSupplier, err := e.snipe.CreateSupplier(ctx, name)
	if err != nil {
		return 0, err
	}

	log.WithFields(logrus.Fields{
		"supplier": name,
		"snipe_id": newSupplier.ID,
	}).Info("Created new supplier in Snipe-IT")

	e.suppliers[strings.ToLower(name)] = newSupplier.ID
	return newSupplier.ID, nil
}

// fetchABMDevices retrieves all devices from ABM, with optional product family filtering.
func (e *Engine) fetchABMDevices(ctx context.Context) ([]abmclient.Device, error) {
	allDevices, _, err := e.abm.GetAllDevices(ctx)
	if err != nil {
		return nil, err
	}

	// Filter by product family if configured
	if len(e.cfg.Sync.ProductFamilies) > 0 {
		families := make(map[string]bool)
		for _, f := range e.cfg.Sync.ProductFamilies {
			families[strings.ToLower(f)] = true
		}
		var filtered []abmclient.Device
		for _, d := range allDevices {
			if d.Attributes != nil && families[strings.ToLower(d.Attributes.ProductFamily)] {
				filtered = append(filtered, d)
			}
		}
		log.Infof("Filtered to %d devices (from %d) by product family: %v", len(filtered), len(allDevices), e.cfg.Sync.ProductFamilies)
		allDevices = filtered
	}

	return allDevices, nil
}

// processDevice handles a single ABM device - creating or updating it in Snipe-IT.
func (e *Engine) processDevice(ctx context.Context, device abmclient.Device) error {
	e.stats.Total++

	attrs := device.Attributes
	if attrs == nil {
		log.Debug("Skipping device with nil attributes")
		e.stats.Skipped++
		return nil
	}

	serial := attrs.SerialNumber
	if serial == "" || serial == "Not Available" {
		log.WithField("device_id", device.ID).Debug("Skipping device with no serial number")
		e.stats.Skipped++
		return nil
	}

	logger := log.WithField("serial", serial)

	// Look up asset in Snipe-IT by serial first to decide create vs update
	existing, err := e.snipe.GetAssetBySerial(ctx, serial)
	if err != nil {
		return fmt.Errorf("looking up serial %s: %w", serial, err)
	}

	if existing.Total == 0 && e.cfg.Sync.UpdateOnly {
		logger.Info("Skipping asset not found in Snipe-IT (update_only mode)")
		e.stats.Skipped++
		return nil
	}

	// Fetch AppleCare coverage for this device
	var appleCare *abmclient.AppleCareCoverage
	ac, err := e.abm.GetAppleCareCoverage(ctx, device.ID)
	if err != nil {
		logger.WithError(err).Warn("Could not fetch AppleCare coverage, continuing without it")
	} else if ac != nil {
		appleCare = ac
		logger.WithField("applecare_status", appleCare.Status).Debug("Found AppleCare coverage")
	}

	// Ensure supplier exists in Snipe-IT (from ABM purchase source)
	supplierID, err := e.ensureSupplier(ctx, attrs.PurchaseSourceType)
	if err != nil {
		logger.WithError(err).Warn("Could not resolve supplier, continuing without it")
	}

	switch existing.Total {
	case 0:
		// Create new asset — need to resolve model
		modelID, err := e.ensureModel(ctx, attrs)
		if err != nil {
			return fmt.Errorf("ensuring model for %s: %w", serial, err)
		}
		return e.createAsset(ctx, logger, attrs, modelID, supplierID, appleCare)
	case 1:
		// Update existing asset — model already assigned in Snipe-IT
		return e.updateAsset(ctx, logger, attrs, existing.Rows[0], supplierID, appleCare)
	default:
		logger.Warnf("Multiple assets (%d) found for serial, skipping", existing.Total)
		e.stats.Skipped++
		return nil
	}
}

// ensureModel checks if the device model exists in Snipe-IT, creating it if needed.
// It tries matching by DeviceModel (marketing name like "Mac mini (2024)") and
// PartNumber against both Snipe-IT model numbers and names.
func (e *Engine) ensureModel(ctx context.Context, attrs *abmclient.DeviceAttributes) (int, error) {
	// Try matching DeviceModel (e.g. "Mac mini (2024)") against model numbers and names
	if attrs.DeviceModel != "" {
		if id, ok := e.models[attrs.DeviceModel]; ok {
			return id, nil
		}
	}

	// Try matching PartNumber (e.g. "MW0Y3LL/A") against model numbers
	if attrs.PartNumber != "" {
		if id, ok := e.models[attrs.PartNumber]; ok {
			return id, nil
		}
	}

	if attrs.DeviceModel == "" && attrs.PartNumber == "" {
		return 0, fmt.Errorf("device has no model identifier")
	}

	// Use DeviceModel as the display name, PartNumber as the model number
	modelName := attrs.DeviceModel
	modelNumber := attrs.PartNumber
	if modelName == "" {
		modelName = modelNumber
	}
	if modelNumber == "" {
		modelNumber = modelName
	}

	if e.cfg.Sync.UpdateOnly {
		log.WithFields(logrus.Fields{
			"model_name":   modelName,
			"model_number": modelNumber,
		}).Warn("Model not found in Snipe-IT and update_only mode is enabled, skipping")
		return 0, fmt.Errorf("model %q not found (update_only mode)", modelName)
	}

	if e.cfg.Sync.DryRun {
		log.WithFields(logrus.Fields{
			"model_name":   modelName,
			"model_number": modelNumber,
		}).Info("[DRY RUN] Would create model")
		e.stats.ModelNew++
		return 0, nil
	}

	newModel, err := e.snipe.CreateModel(ctx, snipe.SnipeModel{
		Name:           modelName,
		ModelNumber:    modelNumber,
		CategoryID:     e.cfg.SnipeIT.CategoryIDForFamily(attrs.ProductFamily),
		ManufacturerID: e.cfg.SnipeIT.ManufacturerID,
		FieldsetID:     e.cfg.SnipeIT.CustomFieldsetID,
	})
	if err != nil {
		return 0, err
	}

	log.WithFields(logrus.Fields{
		"model_name":   modelName,
		"model_number": modelNumber,
		"snipe_id":     newModel.ID,
	}).Info("Created new model in Snipe-IT")

	e.models[modelName] = newModel.ID
	e.models[modelNumber] = newModel.ID
	e.stats.ModelNew++
	return newModel.ID, nil
}

// createAsset creates a new asset in Snipe-IT from ABM device data.
func (e *Engine) createAsset(ctx context.Context, logger *logrus.Entry, attrs *abmclient.DeviceAttributes, modelID int, supplierID int, appleCare *abmclient.AppleCareCoverage) error {
	payload := map[string]any{
		"serial":    attrs.SerialNumber,
		"model_id":  modelID,
		"status_id": e.cfg.SnipeIT.DefaultStatusID,
		"asset_tag": attrs.SerialNumber,
	}

	if e.cfg.Sync.SetName {
		name := attrs.DeviceModel
		if attrs.Color != "" {
			name = fmt.Sprintf("%s (%s)", name, titleCase(attrs.Color))
		}
		if name != "" {
			payload["name"] = name
		}
	}

	if supplierID > 0 {
		payload["supplier_id"] = supplierID
	}

	e.applyFieldMapping(payload, attrs, appleCare)

	if e.cfg.Sync.DryRun {
		logger.WithField("payload", payload).Info("[DRY RUN] Would create asset")
		e.stats.Created++
		return nil
	}

	resp, err := e.snipe.CreateAsset(ctx, payload)
	if err != nil {
		return err
	}

	status, _ := resp["status"].(string)
	if status != "success" {
		msgs, _ := json.Marshal(resp["messages"])
		return fmt.Errorf("asset creation failed: %s", string(msgs))
	}

	logger.Info("Created asset in Snipe-IT")
	e.stats.Created++
	return nil
}

// updateAsset updates an existing Snipe-IT asset with current ABM data.
func (e *Engine) updateAsset(ctx context.Context, logger *logrus.Entry, attrs *abmclient.DeviceAttributes, existing map[string]any, supplierID int, appleCare *abmclient.AppleCareCoverage) error {
	snipeID, ok := existing["id"].(float64)
	if !ok {
		return fmt.Errorf("could not get asset ID from Snipe response")
	}

	if !e.cfg.Sync.Force && !e.cfg.Sync.DryRun {
		abmUpdated := attrs.UpdatedDateTime
		if snipeUpdatedRaw, ok := existing["updated_at"].(map[string]any); ok {
			if snipeTimeStr, ok := snipeUpdatedRaw["datetime"].(string); ok {
				snipeUpdated, err := time.Parse("2006-01-02 15:04:05", snipeTimeStr)
				if err == nil && !abmUpdated.After(snipeUpdated) {
					logger.Info("Skipping update, ABM data not newer than Snipe-IT")
					e.stats.Skipped++
					return nil
				}
			}
		}
	}

	updates := make(map[string]any)
	if supplierID > 0 {
		updates["supplier_id"] = supplierID
	}
	e.applyFieldMapping(updates, attrs, appleCare)

	if len(updates) == 0 {
		logger.Info("No updates needed")
		e.stats.Skipped++
		return nil
	}

	if e.cfg.Sync.DryRun {
		logger.WithFields(logrus.Fields{
			"snipe_id": int(snipeID),
			"updates":  updates,
		}).Info("[DRY RUN] Would update asset")
		e.stats.Updated++
		return nil
	}

	logger.WithField("payload", updates).Debug("Sending update to Snipe-IT")

	resp, err := e.snipe.UpdateAsset(ctx, int(snipeID), updates)
	if err != nil {
		return err
	}

	logger.WithFields(logrus.Fields{
		"fields":   len(updates),
		"response": resp,
	}).Debug("Snipe-IT update response")
	logger.WithField("fields", len(updates)).Info("Updated asset in Snipe-IT")
	e.stats.Updated++
	return nil
}

// applyFieldMapping applies user-configured field mappings from config.
// All field mappings — ABM device attributes, AppleCare coverage, and standard
// Snipe-IT fields like purchase_date — are driven entirely by settings.yaml.
func (e *Engine) applyFieldMapping(payload map[string]any, attrs *abmclient.DeviceAttributes, ac *abmclient.AppleCareCoverage) {
	for snipeField, abmField := range e.cfg.Sync.FieldMapping {
		var value string
		switch strings.ToLower(abmField) {
		// --- ABM device attributes ---
		case "serialnumber", "serial_number":
			value = attrs.SerialNumber
		case "devicemodel", "device_model":
			value = attrs.DeviceModel
		case "color":
			value = titleCase(attrs.Color)
		case "devicecapacity", "device_capacity":
			value = attrs.DeviceCapacity
		case "partnumber", "part_number":
			value = attrs.PartNumber
		case "productfamily", "product_family":
			value = attrs.ProductFamily
		case "producttype", "product_type":
			value = attrs.ProductType
		case "ordernumber", "order_number":
			if attrs.OrderNumber != "" {
				value = cleanOrderNumber(attrs.OrderNumber)
			}
		case "orderdate", "order_date":
			if !attrs.OrderDateTime.IsZero() {
				value = attrs.OrderDateTime.Format("2006-01-02")
			}
		case "purchasesource", "purchase_source":
			value = attrs.PurchaseSourceType
		case "status":
			value = attrs.Status
		case "imei":
			if len(attrs.IMEI) > 0 {
				value = strings.Join(attrs.IMEI, ", ")
			}
		case "meid":
			if len(attrs.MEID) > 0 {
				value = strings.Join(attrs.MEID, ", ")
			}
		case "wifi_mac", "wifimac":
			if len(attrs.WifiMacAddress) > 0 {
				value = formatMAC(strings.Join(attrs.WifiMacAddress, ", "))
			}
		case "bluetooth_mac", "bluetoothmac":
			if len(attrs.BluetoothMacAddress) > 0 {
				value = formatMAC(strings.Join(attrs.BluetoothMacAddress, ", "))
			}
		case "ethernet_mac", "ethernetmac":
			if len(attrs.EthernetMacAddress) > 0 {
				value = formatMAC(strings.Join(attrs.EthernetMacAddress, ", "))
			}
		case "eid":
			value = attrs.EID
		case "added_to_org", "addedtoorg":
			if !attrs.AddedToOrgDateTime.IsZero() {
				value = attrs.AddedToOrgDateTime.Format("2006-01-02")
			}
		case "assigned_server", "assignedserver", "mdm_server":
			value = attrs.AssignedServer
		case "released_from_org", "releasedfromorg":
			if !attrs.ReleasedFromOrgDateTime.IsZero() {
				value = attrs.ReleasedFromOrgDateTime.Format("2006-01-02")
			}

		// --- AppleCare coverage fields ---
		case "applecare_status":
			if ac != nil {
				value = titleCase(ac.Status)
			}
		case "applecare_agreement":
			if ac != nil {
				value = ac.AgreementNumber
			}
		case "applecare_description":
			if ac != nil {
				value = ac.Description
			}
		case "applecare_start":
			if ac != nil && !ac.StartDateTime.IsZero() {
				value = ac.StartDateTime.Format("2006-01-02")
			}
		case "applecare_end":
			if ac != nil && !ac.EndDateTime.IsZero() {
				value = ac.EndDateTime.Format("2006-01-02")
			}
		case "applecare_renewable":
			if ac != nil {
				value = fmt.Sprintf("%t", ac.IsRenewable)
			}
		case "applecare_payment_type":
			if ac != nil {
				value = titleCase(ac.PaymentType)
			}
		}
		if value != "" {
			payload[snipeField] = value
		}
	}

	// warranty_months: calculated from purchase_date to AppleCare end so that
	// Snipe-IT's auto-calculated "Warranty Expires" matches the actual coverage end.
	if ac != nil && !ac.EndDateTime.IsZero() && !attrs.OrderDateTime.IsZero() {
		months := int(ac.EndDateTime.Sub(attrs.OrderDateTime).Hours() / (24 * 30))
		if months > 0 {
			payload["warranty_months"] = months
		}
	}
}

// cleanOrderNumber extracts the middle segment from CDW-style order numbers
// like "CDW/1CJ6QLW/002" → "1CJ6QLW". Other formats are returned as-is.
func cleanOrderNumber(order string) string {
	parts := strings.Split(order, "/")
	if len(parts) == 3 {
		return parts[1]
	}
	return order
}

// titleCase converts "SPACE GRAY" to "Space Gray".
func titleCase(s string) string {
	// Replace underscores with spaces so "Paid_up_front" becomes "Paid Up Front"
	s = strings.ReplaceAll(s, "_", " ")
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// formatMAC inserts colons into a raw MAC address (e.g. "2CCA164BD29D" -> "2C:CA:16:4B:D2:9D").
// If the input already contains colons or is not 12 hex chars, it's returned as-is.
func formatMAC(s string) string {
	raw := strings.ReplaceAll(strings.ReplaceAll(s, ":", ""), "-", "")
	if len(raw) != 12 {
		return s
	}
	return strings.ToUpper(fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		raw[0:2], raw[2:4], raw[4:6], raw[6:8], raw[8:10], raw[10:12]))
}

func deviceSerial(d abmclient.Device) string {
	if d.Attributes != nil {
		return d.Attributes.SerialNumber
	}
	return d.ID
}
