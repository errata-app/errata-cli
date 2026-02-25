package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/logging"
	"github.com/suarezc/errata/internal/mcp"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/tools"
	"github.com/suarezc/errata/internal/ui"
	"github.com/suarezc/errata/internal/web"
)

func main() {
	root := &cobra.Command{
		Use:   "errata",
		Short: "A/B testing tool for agentic AI models",
		RunE:  runREPL,
	}

	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show preference win summary",
		RunE:  runStats,
	}
	statsCmd.Flags().Bool("detail", false, "Show detailed analytics (win rate, avg latency, avg cost)")
	root.AddCommand(
		statsCmd,
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

// setupAdapters loads config, pricing, adapters, and MCP servers.
// Returns adapters, session ID, warnings, MCP state (defs + dispatchers),
// and a cleanup function the caller must defer.
func setupAdapters(cfg config.Config) (
	ads []models.ModelAdapter,
	sessionID string,
	warnings []string,
	mcpDefs []tools.ToolDef,
	mcpDispatchers map[string]tools.MCPDispatcher,
	cleanup func(),
) {
	pricing.LoadPricing(cfg.PricingCachePath)
	ads, warnings = adapters.ListAdapters(cfg)
	sessionID = logging.RandomHex(16)
	cleanup = func() {}

	// Apply custom system prompt if configured.
	if cfg.SystemPromptExtra != "" {
		tools.SetSystemPromptExtra(cfg.SystemPromptExtra)
	}

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

	// Start MCP servers (non-fatal — missing/broken servers are skipped with a warning).
	if serverConfigs := mcp.ParseServerConfigs(cfg.MCPServers); len(serverConfigs) > 0 {
		var mgr *mcp.Manager
		mcpDefs, mcpDispatchers, mgr = mcp.StartServers(serverConfigs, os.Environ())
		prev := cleanup
		cleanup = func() { prev(); mgr.Shutdown() }
	}
	return
}

func runREPL(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	ads, sessionID, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg)
	defer cleanup()
	if len(ads) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}
	return ui.Run(ads, cfg.PreferencesPath, cfg.HistoryPath, cfg.PromptHistoryPath, sessionID, cfg, warnings, mcpDefs, mcpDispatchers)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg := config.Load()
	ads, sessionID, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg)
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
	return web.New(ads, cfg.PreferencesPath, cfg.HistoryPath, cfg, mcpDefs, mcpDispatchers).Start(addr)
}

func runStats(cmd *cobra.Command, args []string) error {
	detail, _ := cmd.Flags().GetBool("detail")
	cfg := config.Load()

	if detail {
		return runStatsDetailed(cfg)
	}

	tally := preferences.Summarize(cfg.PreferencesPath)
	if len(tally) == 0 {
		fmt.Println("No preference data yet.")
		return nil
	}

	type row struct {
		model string
		wins  int
	}
	rows := make([]row, 0, len(tally))
	for m, w := range tally {
		rows = append(rows, row{m, w})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].wins != rows[j].wins {
			return rows[i].wins > rows[j].wins
		}
		return rows[i].model < rows[j].model
	})

	fmt.Printf("%-30s %6s\n", "Model", "Wins")
	fmt.Printf("%-30s %6s\n", strings.Repeat("-", 30), "----")
	for _, r := range rows {
		fmt.Printf("%-30s %6d\n", r.model, r.wins)
	}
	return nil
}

func runStatsDetailed(cfg config.Config) error {
	stats := preferences.SummarizeDetailed(cfg.PreferencesPath)
	if len(stats) == 0 {
		fmt.Println("No preference data yet.")
		return nil
	}

	type row struct {
		model string
		s     preferences.ModelStats
	}
	rows := make([]row, 0, len(stats))
	for m, s := range stats {
		rows = append(rows, row{m, s})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].s.Wins != rows[j].s.Wins {
			return rows[i].s.Wins > rows[j].s.Wins
		}
		return rows[i].model < rows[j].model
	})

	fmt.Printf("%-32s %5s %5s %4s %7s %12s %10s %6s\n",
		"Model", "Wins", "Loss", "Bad", "WinRate", "Avg Latency", "Avg Cost", "Runs")
	fmt.Printf("%-32s %5s %5s %4s %7s %12s %10s %6s\n",
		strings.Repeat("-", 32), "-----", "-----", "----", "-------", "------------", "----------", "------")
	for _, r := range rows {
		cost := "         -"
		if r.s.AvgCostUSD > 0 {
			cost = fmt.Sprintf("  $%.4f", r.s.AvgCostUSD)
		}
		fmt.Printf("%-32s %5d %5d %4d %6.1f%% %10dms %10s %6d\n",
			r.model,
			r.s.Wins,
			r.s.Losses,
			r.s.ThumbsDown,
			r.s.WinRate,
			int64(r.s.AvgLatencyMS),
			cost,
			r.s.Participations,
		)
	}
	return nil
}
