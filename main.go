package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"

	"github.com/CampusTech/axm2snipe/abmclient"
	"github.com/CampusTech/axm2snipe/config"
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
		showVersion    bool
		serial         string
	)

	flag.StringVar(&configPath, "config", "settings.yaml", "Path to configuration file")
	flag.BoolVar(&verbose, "v", false, "Verbose output (INFO level)")
	flag.BoolVar(&debug, "d", false, "Debug output (DEBUG level)")
	flag.BoolVar(&dryRun, "dry-run", false, "Simulate sync without making changes")
	flag.BoolVar(&force, "force", false, "Ignore timestamps, always update")
	flag.BoolVar(&connectionTest, "connection-test", false, "Test connections to ABM and Snipe-IT, then exit")
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.StringVar(&serial, "serial", "", "Sync a single device by serial number or asset tag")
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
		axmsync.SetLogLevel(logrus.DebugLevel)
	case verbose:
		log.SetLevel(logrus.InfoLevel)
		axmsync.SetLogLevel(logrus.InfoLevel)
	default:
		log.SetLevel(logrus.WarnLevel)
		axmsync.SetLogLevel(logrus.WarnLevel)
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

	// Initialize ABM client
	log.Info("Connecting to Apple Business Manager...")
	abmClient, err := abmclient.NewClient(ctx, cfg.ABM.ClientID, cfg.ABM.KeyID, cfg.ABM.PrivateKey)
	if err != nil {
		log.Fatalf("Failed to create ABM client: %v", err)
	}

	// Initialize Snipe-IT client
	log.Info("Connecting to Snipe-IT...")
	snipeClient, err := snipe.NewClient(cfg.SnipeIT.URL, cfg.SnipeIT.APIKey)
	if err != nil {
		log.Fatalf("Failed to create Snipe-IT client: %v", err)
	}
	snipeClient.DryRun = cfg.Sync.DryRun

	// Connection test mode
	if connectionTest {
		log.Info("Testing ABM connection...")
		resp, err := abmClient.GetDevices(ctx, 1)
		if err != nil {
			log.Fatalf("ABM connection failed: %v", err)
		}
		log.Infof("ABM connection OK (%d total devices)", resp.Meta.Paging.Total)

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
