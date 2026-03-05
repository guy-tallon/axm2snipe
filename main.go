package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"

	"github.com/CampusTech/axm2snipe/abmclient"
	"github.com/CampusTech/axm2snipe/config"
	"github.com/CampusTech/axm2snipe/notify"
	"github.com/CampusTech/axm2snipe/snipe"
	axmsync "github.com/CampusTech/axm2snipe/sync"
)

var version = "dev"

func main() {
	var (
		configPath     string
		verbose        bool
		debug          bool
		dryRun         bool
		force          bool
		connectionTest bool
		setupFields    bool
		showVersion    bool
		serial         string
		saveCache      string
		useCache       string
	)

	flag.StringVar(&configPath, "config", "settings.yaml", "Path to configuration file")
	flag.BoolVar(&verbose, "v", false, "Verbose output (INFO level)")
	flag.BoolVar(&debug, "d", false, "Debug output (DEBUG level)")
	flag.BoolVar(&dryRun, "dry-run", false, "Simulate sync without making changes")
	flag.BoolVar(&force, "force", false, "Ignore timestamps, always update")
	flag.BoolVar(&connectionTest, "connection-test", false, "Test connections to ABM and Snipe-IT, then exit")
	flag.BoolVar(&setupFields, "setup-fields", false, "Create AXM custom fields in Snipe-IT and associate with fieldset, then exit")
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.StringVar(&serial, "serial", "", "Sync a single device by serial number or asset tag")
	flag.StringVar(&saveCache, "save-cache", "", "Save ABM data to JSON cache file after fetching")
	flag.StringVar(&useCache, "use-cache", "", "Use cached ABM data from JSON file instead of API")
	flag.Parse()

	if showVersion {
		fmt.Printf("axm2snipe %s\n", version)
		os.Exit(0)
	}

	// Set up logging
	log := logrus.New()
	log.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	switch {
	case debug:
		log.SetLevel(logrus.DebugLevel)
		abmclient.SetLogLevel(logrus.DebugLevel)
		axmsync.SetLogLevel(logrus.DebugLevel)
		notify.SetLogLevel(logrus.DebugLevel)
	case verbose:
		log.SetLevel(logrus.InfoLevel)
		abmclient.SetLogLevel(logrus.InfoLevel)
		axmsync.SetLogLevel(logrus.InfoLevel)
		notify.SetLogLevel(logrus.InfoLevel)
	default:
		log.SetLevel(logrus.WarnLevel)
		abmclient.SetLogLevel(logrus.WarnLevel)
		axmsync.SetLogLevel(logrus.WarnLevel)
		notify.SetLogLevel(logrus.WarnLevel)
	}

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Apply CLI overrides
	if dryRun {
		cfg.Sync.DryRun = true
	}
	if force {
		cfg.Sync.Force = true
	}

	if cfg.Sync.DryRun {
		log.Info("Running in DRY RUN mode - no changes will be made")
	}

	// Set up context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Infof("Received signal %v, shutting down...", sig)
		cancel()
	}()

	// Initialize Snipe-IT client
	log.Info("Connecting to Snipe-IT...")
	snipeClient, err := snipe.NewClient(cfg.SnipeIT.URL, cfg.SnipeIT.APIKey)
	if err != nil {
		log.Fatalf("Failed to create Snipe-IT client: %v", err)
	}
	snipeClient.DryRun = cfg.Sync.DryRun

	// Initialize ABM client (needed for both setup-fields and sync)
	log.Info("Connecting to Apple Business Manager...")
	abmClient, err := abmclient.NewClient(ctx, cfg.ABM.ClientID, cfg.ABM.KeyID, cfg.ABM.PrivateKey)
	if err != nil {
		log.Fatalf("Failed to create ABM client: %v", err)
	}

	// Setup fields mode
	if setupFields {
		if cfg.SnipeIT.CustomFieldsetID == 0 {
			log.Fatal("snipe_it.custom_fieldset_id must be set to use --setup-fields")
		}

		// Fetch MDM server names from ABM for the Assigned MDM Server field options
		log.Info("Fetching MDM servers from ABM...")
		mdmServerNames, err := abmClient.GetMDMServers(ctx)
		if err != nil {
			log.Warnf("Could not fetch MDM servers: %v (Assigned MDM Server field will be a text field)", err)
		}

		mdmServerField := snipe.FieldDef{Name: "AXM: Assigned MDM Server", Element: "text", Format: "ANY", HelpText: "MDM server assigned in Apple Business/School Manager"}
		if len(mdmServerNames) > 0 {
			var names []string
			for _, name := range mdmServerNames {
				if name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				mdmServerField.Element = "listbox"
				mdmServerField.FieldValues = strings.Join(names, "\n")
				log.Infof("Found %d MDM servers: %s", len(names), strings.Join(names, ", "))
			}
		}

		fields := []snipe.FieldDef{
			{Name: "AXM: MDM Assigned?", Element: "text", Format: "BOOLEAN", HelpText: "Whether this device is assigned to an MDM server in ABM/ASM"},
			{Name: "AXM: Added to Org", Element: "text", Format: "DATE", HelpText: "Date device was added to ABM/ASM organization"},
			{Name: "AXM: AppleCare Description", Element: "text", Format: "ANY", HelpText: "AppleCare coverage description"},
			{Name: "AXM: AppleCare Payment Type", Element: "radio", Format: "ANY", HelpText: "AppleCare payment type", FieldValues: "Paid Up Front\nFree\nIncluded\nNone"},
			{Name: "AXM: AppleCare Renewable", Element: "listbox", Format: "BOOLEAN", HelpText: "Whether AppleCare coverage is renewable", FieldValues: "true\nfalse"},
			{Name: "AXM: AppleCare Start Date", Element: "text", Format: "DATE", HelpText: "AppleCare coverage start date"},
			{Name: "AXM: AppleCare Status", Element: "radio", Format: "ANY", HelpText: "AppleCare coverage status", FieldValues: "Active\nInactive\nExpired"},
			mdmServerField,
			{Name: "AXM: Released from Org", Element: "text", Format: "DATE", HelpText: "Date device was released from ABM/ASM organization"},
		}

		log.Info("Creating custom fields in Snipe-IT...")
		results, err := snipeClient.SetupFields(cfg.SnipeIT.CustomFieldsetID, fields)
		if err != nil {
			log.Fatalf("Failed to setup fields: %v", err)
		}

		// Map field names to their suggested ABM attribute
		abmAttr := map[string]string{
			"AXM: Added to Org":           "added_to_org",
			"AXM: Released from Org":      "released_from_org",
			"AXM: MDM Assigned?":          "status",
			"AXM: AppleCare Status":       "applecare_status",
			"AXM: AppleCare Description":  "applecare_description",
			"AXM: AppleCare Start Date":   "applecare_start",
			"AXM: AppleCare Renewable":    "applecare_renewable",
			"AXM: AppleCare Payment Type": "applecare_payment_type",
			"AXM: Assigned MDM Server":    "assigned_server",
		}

		// Build field mapping: DB column -> ABM attribute
		fieldMapping := make(map[string]string)
		for name, dbCol := range results {
			if attr, ok := abmAttr[name]; ok {
				fieldMapping[dbCol] = attr
			}
		}

		// Save to config file
		if err := config.MergeFieldMapping(configPath, fieldMapping); err != nil {
			log.Warnf("Could not save field mappings to %s: %v", configPath, err)
			fmt.Println("\nAdd these to your settings.yaml field_mapping manually:")
			for dbCol, attr := range fieldMapping {
				fmt.Printf("    %s: %s\n", dbCol, attr)
			}
		} else {
			fmt.Printf("\nField mappings saved to %s\n", configPath)
		}

		fmt.Println("\nCustom fields created and associated with fieldset:")
		for name, dbCol := range results {
			if attr, ok := abmAttr[name]; ok {
				fmt.Printf("  %s: %s -> %s\n", name, dbCol, attr)
			} else {
				fmt.Printf("  %s: %s\n", name, dbCol)
			}
		}
		os.Exit(0)
	}

	// Connection test mode
	if connectionTest {
		log.Info("Testing ABM connection...")
		total, err := abmClient.ConnectionTest(ctx)
		if err != nil {
			log.Fatalf("ABM connection failed: %v", err)
		}
		log.Infof("ABM connection OK (%d total devices)", total)

		log.Info("Testing Snipe-IT connection...")
		models, err := snipeClient.ListModels(ctx)
		if err != nil {
			log.Fatalf("Snipe-IT connection failed: %v", err)
		}
		log.Infof("Snipe-IT connection OK (%d models found)", len(models))

		fmt.Println("All connections successful!")
		os.Exit(0)
	}

	// Run sync
	engine := axmsync.NewEngine(abmClient, snipeClient, cfg)

	if useCache != "" {
		if err := engine.LoadCache(useCache); err != nil {
			log.Fatalf("Failed to load cache: %v", err)
		}
	}
	if saveCache != "" {
		engine.SetSaveCache(saveCache)
	}

	if serial != "" {
		cfg.Sync.Force = true // always force when targeting a single device
	}
	var stats *axmsync.Stats
	if serial != "" {
		stats, err = engine.RunSingle(ctx, serial)
	} else {
		stats, err = engine.Run(ctx)
	}
	if err != nil {
		log.Fatalf("Sync failed: %v", err)
	}

	fmt.Printf("\nSync Results:\n")
	fmt.Printf("  Total devices processed: %d\n", stats.Total)
	fmt.Printf("  Assets created:          %d\n", stats.Created)
	fmt.Printf("  Assets updated:          %d\n", stats.Updated)
	fmt.Printf("  Assets skipped:          %d\n", stats.Skipped)
	fmt.Printf("  Errors:                  %d\n", stats.Errors)
	fmt.Printf("  New models created:      %d\n", stats.ModelNew)
}
