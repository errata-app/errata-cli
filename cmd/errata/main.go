package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/logging"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/ui"
	"github.com/suarezc/errata/internal/web"
)

func main() {
	root := &cobra.Command{
		Use:   "errata",
		Short: "A/B testing tool for agentic AI models",
		RunE:  runREPL,
	}

	root.AddCommand(
		&cobra.Command{
			Use:   "stats",
			Short: "Show preference win summary",
			RunE:  runStats,
		},
		&cobra.Command{
			Use:   "serve",
			Short: "Start web interface on localhost:8080",
			RunE:  runServe,
		},
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// setupAdapters loads config, pricing, and adapters, wires optional run
// logging, and returns the ready-to-use adapter slice, a session ID, any
// startup warnings, and a cleanup function the caller must defer.
func setupAdapters(cfg config.Config) (ads []models.ModelAdapter, sessionID string, warnings []string, cleanup func()) {
	pricing.LoadPricing(cfg.PricingCachePath)
	ads, warnings = adapters.ListAdapters(cfg)
	sessionID = logging.RandomHex(16)
	cleanup = func() {}

	if cfg.DebugLogPath != "" {
		logger, err := logging.NewLogger(cfg.DebugLogPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open log file %q: %v\n", cfg.DebugLogPath, err)
		} else {
			cleanup = func() { logger.Close() }
			ads = logging.WrapAll(ads, sessionID, logger)
			fmt.Fprintf(os.Stderr, "logging runs to %s\n", cfg.DebugLogPath)
		}
	}
	return
}

func runREPL(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	ads, sessionID, warnings, cleanup := setupAdapters(cfg)
	defer cleanup()
	if len(ads) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}
	return ui.Run(ads, cfg.PreferencesPath, cfg.HistoryPath, sessionID, cfg, warnings)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	ads, sessionID, warnings, cleanup := setupAdapters(cfg)
	defer cleanup()
	if len(ads) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	_ = sessionID // web server creates its own session ID per connection
	addr := ":8080"
	fmt.Fprintf(os.Stderr, "Errata running at http://localhost%s\n", addr)
	return web.New(ads, cfg.PreferencesPath, cfg.HistoryPath, cfg).Start(addr)
}

func runStats(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	tally := preferences.Summarize(cfg.PreferencesPath)
	if len(tally) == 0 {
		fmt.Println("No preference data yet.")
		return nil
	}

	fmt.Printf("%-30s %6s\n", "Model", "Wins")
	fmt.Printf("%-30s %6s\n", strings.Repeat("-", 30), "----")
	for modelID, wins := range tally {
		fmt.Printf("%-30s %6d\n", modelID, wins)
	}
	return nil
}
