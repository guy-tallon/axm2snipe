package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	axmsync "github.com/CampusTech/axm2snipe/sync"
)

// NewDownloadCmd creates the download command.
func NewDownloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download ABM/ASM data to local cache",
		Long:  "Fetches all devices and AppleCare coverage from Apple Business Manager / Apple School Manager and saves them as JSON files in the cache directory. Use 'sync --use-cache' to sync from the cache without hitting ABM API rate limits.",
		RunE:  runDownload,
	}
	cmd.Flags().Bool("progress", false, "Show progress bar during AppleCare coverage download")
	return cmd
}

func runDownload(cmd *cobra.Command, args []string) error {
	if err := Cfg.ValidateABM(); err != nil {
		return err
	}

	ctx, cancel := contextWithSignal()
	defer cancel()

	abmClient, err := newABMClient(ctx)
	if err != nil {
		return err
	}

	engine := axmsync.NewDownloadEngine(abmClient, Cfg)
	engine.ShowProgress, _ = cmd.Flags().GetBool("progress")

	if err := engine.FetchAndSaveCache(ctx); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	fmt.Printf("ABM data saved to %s/\n", engine.CacheDir())
	return nil
}
