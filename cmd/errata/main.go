package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/ui"
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
			Short: "Start web server (not yet implemented)",
			RunE: func(cmd *cobra.Command, args []string) error {
				return fmt.Errorf("web server not yet implemented")
			},
		},
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runREPL(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	adapters, warnings := models.ListAdapters(cfg)
	if len(adapters) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}

	sessionID := newSessionID()
	return ui.Run(adapters, cfg.PreferencesPath, sessionID, warnings)
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
