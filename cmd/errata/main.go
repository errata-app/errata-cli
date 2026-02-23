package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/logging"
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

func runREPL(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	pricing.LoadPricing(cfg.PricingCachePath)
	adapters, warnings := adapters.ListAdapters(cfg)
	if len(adapters) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}

	sessionID := newSessionID()

	if cfg.DebugLogPath != "" {
		logger, err := logging.NewLogger(cfg.DebugLogPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open log file %q: %v\n", cfg.DebugLogPath, err)
		} else {
			defer logger.Close()
			adapters = logging.WrapAll(adapters, sessionID, logger)
			fmt.Fprintf(os.Stderr, "logging runs to %s\n", cfg.DebugLogPath)
		}
	}

	return ui.Run(adapters, cfg.PreferencesPath, sessionID, warnings)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	pricing.LoadPricing(cfg.PricingCachePath)
	adapters, warnings := adapters.ListAdapters(cfg)
	if len(adapters) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	sessionID := newSessionID()

	if cfg.DebugLogPath != "" {
		logger, err := logging.NewLogger(cfg.DebugLogPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open log file %q: %v\n", cfg.DebugLogPath, err)
		} else {
			defer logger.Close()
			adapters = logging.WrapAll(adapters, sessionID, logger)
			fmt.Fprintf(os.Stderr, "logging runs to %s\n", cfg.DebugLogPath)
		}
	}

	addr := ":8080"
	fmt.Fprintf(os.Stderr, "Errata running at http://localhost%s\n", addr)
	return web.New(adapters, cfg.PreferencesPath).Start(addr)
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

// newSessionID returns a random 16-byte hex string usable as a session ID.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
