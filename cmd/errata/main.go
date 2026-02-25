package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/headless"
	"github.com/suarezc/errata/internal/logging"
	"github.com/suarezc/errata/internal/mcp"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/tools"
	"github.com/suarezc/errata/internal/ui"
	"github.com/suarezc/errata/internal/web"
)

var recipePath string

func main() {
	root := &cobra.Command{
		Use:   "errata",
		Short: "A/B testing tool for agentic AI models",
		RunE:  runREPL,
	}
	root.PersistentFlags().StringVarP(&recipePath, "recipe", "r", "", "recipe file (default: auto-discover recipe.md)")

	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show preference win summary",
		RunE:  runStats,
	}
	statsCmd.Flags().Bool("detail", false, "Show detailed analytics (win rate, avg latency, avg cost)")

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run recipe tasks headlessly (no user interaction)",
		RunE:  runHeadless,
	}
	runCmd.Flags().Bool("json", false, "Print report to stdout as JSON")
	runCmd.Flags().String("output-dir", "", "Output directory (default: data/outputs/)")
	runCmd.Flags().Bool("verbose", false, "Show model text events in progress")

	root.AddCommand(
		statsCmd,
		runCmd,
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

// loadRecipe discovers and parses the recipe for the current invocation.
// Falls back to the built-in default on any error.
func loadRecipe() *recipe.Recipe {
	rec, err := recipe.Discover(recipePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load recipe: %v\n", err)
		return recipe.Default()
	}
	return rec
}

// applyProjectRoot changes the process working directory to rec.Metadata.ProjectRoot
// when it is set. All cwd-based path guards in file tools then automatically
// enforce the project boundary without further changes.
func applyProjectRoot(rec *recipe.Recipe) {
	if rec == nil || rec.Metadata.ProjectRoot == "" {
		return
	}
	if err := os.Chdir(rec.Metadata.ProjectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not set project_root %q: %v\n", rec.Metadata.ProjectRoot, err)
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
		var mcpWarnings []string
		mcpDefs, mcpDispatchers, mcpWarnings, mgr = mcp.StartServers(serverConfigs, os.Environ())
		warnings = append(warnings, mcpWarnings...)
		prev := cleanup
		cleanup = func() { prev(); mgr.Shutdown() }
	}
	return
}

func runREPL(cmd *cobra.Command, args []string) error {
	rec := loadRecipe()
	cfg := config.Load()
	rec.ApplyTo(&cfg)
	applyProjectRoot(rec)
	ads, sessionID, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg)
	defer cleanup()
	if len(ads) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}
	return ui.Run(ads, cfg.PreferencesPath, cfg.HistoryPath, cfg.PromptHistoryPath, sessionID, cfg, warnings, mcpDefs, mcpDispatchers, rec)
}

func runServe(cmd *cobra.Command, args []string) error {
	rec := loadRecipe()
	cfg := config.Load()
	rec.ApplyTo(&cfg)
	applyProjectRoot(rec)
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
	return web.New(ads, cfg.PreferencesPath, cfg.HistoryPath, cfg, mcpDefs, mcpDispatchers, warnings, rec).Start(addr)
}

func runHeadless(cmd *cobra.Command, args []string) error {
	rec := loadRecipe()
	if len(rec.Tasks) == 0 {
		return fmt.Errorf("recipe has no tasks — ## Tasks section is required for `errata run`")
	}

	cfg := config.Load()
	rec.ApplyTo(&cfg)
	applyProjectRoot(rec)

	ads, sessionID, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg)
	defer cleanup()

	if len(ads) == 0 {
		return fmt.Errorf("no models available — set at least one API key in .env")
	}

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	jsonFlag, _ := cmd.Flags().GetBool("json")
	outputDir, _ := cmd.Flags().GetString("output-dir")
	verbose, _ := cmd.Flags().GetBool("verbose")

	_, err := headless.Run(context.Background(), headless.Options{
		Recipe:         rec,
		Adapters:       ads,
		SessionID:      sessionID,
		Cfg:            cfg,
		OutputDir:      outputDir,
		Verbose:        verbose,
		JSON:           jsonFlag,
		MCPDefs:        mcpDefs,
		MCPDispatchers: mcpDispatchers,
	})
	return err
}

func runStats(cmd *cobra.Command, args []string) error {
	detail, _ := cmd.Flags().GetBool("detail")
	rec := loadRecipe()
	cfg := config.Load()
	rec.ApplyTo(&cfg)

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
