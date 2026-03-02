package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/datastore"
	"github.com/suarezc/errata/internal/recipestore"
	"github.com/suarezc/errata/internal/headless"
	"github.com/suarezc/errata/internal/logging"
	"github.com/suarezc/errata/internal/mcp"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/session"
	"github.com/suarezc/errata/internal/tools"
	"github.com/suarezc/errata/internal/ui"
	"github.com/suarezc/errata/internal/uid"
)

var (
	recipePath    string
	debugLogPath  string
	continueFlag  bool
	resumeID      string
)

const sessionsBaseDir = "data/sessions"

func main() {
	root := &cobra.Command{
		Use:   "errata",
		Short: "A/B testing tool for agentic AI models",
		RunE:  runREPL,
	}
	root.PersistentFlags().StringVarP(&recipePath, "recipe", "r", "", "recipe file (default: auto-discover recipe.md)")
	root.PersistentFlags().StringVar(&debugLogPath, "debug-log", "", "path to JSONL debug log")
	root.Flags().BoolVarP(&continueFlag, "continue", "c", false, "resume the most recent session")
	root.Flags().StringVar(&resumeID, "resume", "", "resume a session by ID or prefix (empty = most recent)")

	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show preference win summary",
		RunE:  runStats,
	}
	statsCmd.Flags().Bool("detail", false, "Show detailed analytics (win rate, avg latency, avg cost)")
	statsCmd.Flags().String("recipe", "", "Filter stats by recipe name")
	statsCmd.Flags().String("config", "", "Filter stats by config hash")

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run recipe tasks headlessly (no user interaction)",
		RunE:  runHeadless,
	}
	runCmd.Flags().Bool("json", false, "Print report to stdout as JSON")
	runCmd.Flags().String("output-dir", "", "Output directory (default: data/outputs/)")
	runCmd.Flags().Bool("verbose", false, "Show model text events in progress")

	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "List all sessions",
		RunE:  runSessions,
	}

	root.AddCommand(
		statsCmd,
		runCmd,
		sessionsCmd,
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

// applyRecipeToolSettings applies recipe-level tool package settings
// that are not represented in config.Config (they configure the tools package directly).
func applyRecipeToolSettings(rec *recipe.Recipe) {
	if rec == nil {
		return
	}
	if rec.Constraints.BashTimeout > 0 {
		tools.SetBashTimeout(rec.Constraints.BashTimeout)
	}
	if rec.Sandbox.AllowLocalFetch {
		tools.SetAllowLocalFetch(true)
	}
}

// setupAdapters loads config, pricing, adapters, and MCP servers.
// Returns adapters, warnings, MCP state (defs + dispatchers),
// and a cleanup function the caller must defer.
func setupAdapters(cfg config.Config, debugLog, sessionID string) (
	ads []models.ModelAdapter,
	warnings []string,
	mcpDefs []tools.ToolDef,
	mcpDispatchers map[string]tools.MCPDispatcher,
	cleanup func(),
) {
	pricing.LoadPricing(cfg.PricingCachePath)
	ads, warnings = adapters.ListAdapters(cfg)
	cleanup = func() {}

	// Apply custom system prompt if configured.
	if cfg.SystemPromptExtra != "" {
		tools.SetSystemPromptExtra(cfg.SystemPromptExtra)
	}

	// Apply custom tool guidance if configured.
	if cfg.ToolGuidance != "" {
		tools.SetToolGuidance(cfg.ToolGuidance)
	}

	if debugLog != "" {
		logger, err := logging.NewLogger(debugLog)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not open log file %q: %v\n", debugLog, err)
		} else {
			cleanup = func() { _ = logger.Close() }
			ads = logging.WrapAll(ads, sessionID, logger)
			fmt.Fprintf(os.Stderr, "logging runs to %s\n", debugLog)
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
	applyRecipeToolSettings(rec)

	// Resolve session: fresh, --continue, or --resume <id>.
	resuming := false
	var sessionID string
	var sp session.Paths

	// --resume with no value acts like --continue.
	if cmd.Flags().Changed("resume") && resumeID == "" {
		continueFlag = true
	}

	switch {
	case resumeID != "":
		// --resume <id> — resolve by exact match or prefix.
		resolved, err := session.Resolve(sessionsBaseDir, resumeID)
		if err != nil {
			return fmt.Errorf("session resolve: %w", err)
		}
		sessionID = resolved
		sp = session.PathsFor(sessionsBaseDir, sessionID)
		resuming = true

	case continueFlag:
		// --continue — resume most recent session.
		latest, err := session.LatestID(sessionsBaseDir)
		if err != nil {
			return fmt.Errorf("no previous session to continue: %w", err)
		}
		sessionID = latest
		sp = session.PathsFor(sessionsBaseDir, sessionID)
		resuming = true

	default:
		// Fresh session.
		sessionID, sp = session.New(sessionsBaseDir)
	}

	// If resuming and a session recipe exists, load it instead of the base recipe.
	if resuming {
		if _, err := os.Stat(sp.RecipePath); err == nil {
			sessionRec, parseErr := recipe.Parse(sp.RecipePath)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "warning: could not load session recipe: %v (using base recipe)\n", parseErr)
			} else {
				rec = sessionRec
				// Re-apply on top of config.
				cfg = config.Load()
				rec.ApplyTo(&cfg)
				applyRecipeToolSettings(rec)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Session: %s\n", sessionID)

	ads, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg, debugLogPath, sessionID)
	defer cleanup()

	// Fetch available models from all configured providers (best-effort).
	availableModels := flattenAvailableModels(ads, adapters.ListAvailableModels(context.Background(), cfg))

	// Build initial session metadata.
	now := time.Now()
	modelIDs := make([]string, len(ads))
	for i, ad := range ads {
		modelIDs[i] = ad.ID()
	}
	meta := session.Meta{
		ID:           sessionID,
		CreatedAt:    now,
		LastActiveAt: now,
		Models:       modelIDs,
	}
	// If resuming, load existing metadata.
	if resuming {
		if existing, err := session.LoadMeta(sp.MetaPath); err == nil && existing != nil {
			meta = *existing
			meta.Models = modelIDs // update with current adapters
		}
	}

	store, err := datastore.New(datastore.Options{
		HistoryPath:    sp.HistoryPath,
		PromptHistPath: cfg.PromptHistoryPath,
		SessionPaths:   sp,
		SessionID:      sessionID,
		PrefPath:       cfg.PreferencesPath,
		Meta:           meta,
		RecipeStore:    recipestore.New("data/configs.json"),
	})
	if err != nil {
		return fmt.Errorf("datastore init: %w", err)
	}
	err = ui.Run(ads, cfg, warnings, mcpDefs, mcpDispatchers, rec, resuming, availableModels, debugLogPath != "", store)
	fmt.Fprintf(os.Stderr, "To continue this session: errata --resume %s\n", sessionID)
	return err
}

// flattenAvailableModels collects all model IDs from provider listings into a
// deduplicated sorted slice. Configured adapter IDs are always included.
func flattenAvailableModels(ads []models.ModelAdapter, providerModels []adapters.ProviderModels) []string {
	seen := make(map[string]bool)
	for _, ad := range ads {
		seen[ad.ID()] = true
	}
	for _, pm := range providerModels {
		for _, id := range pm.Models {
			seen[id] = true
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func runHeadless(cmd *cobra.Command, args []string) error {
	rec := loadRecipe()
	if len(rec.Tasks) == 0 {
		return fmt.Errorf("recipe has no tasks — ## Tasks section is required for `errata run`")
	}

	cfg := config.Load()
	rec.ApplyTo(&cfg)
	applyProjectRoot(rec)
	applyRecipeToolSettings(rec)

	sessionID := uid.New("ses_")
	ads, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg, debugLogPath, sessionID)
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ninterrupted — saving checkpoint…")
		cancel()
	}()

	_, err := headless.Run(ctx, &headless.Options{
		Recipe:         rec,
		Adapters:       ads,
		SessionID:      sessionID,
		Cfg:            cfg,
		OutputDir:      outputDir,
		Verbose:        verbose,
		JSON:           jsonFlag,
		DebugLog:       debugLogPath != "",
		MCPDefs:        mcpDefs,
		MCPDispatchers: mcpDispatchers,
	})
	return err
}

func runStats(cmd *cobra.Command, args []string) error {
	detail, _ := cmd.Flags().GetBool("detail")
	recipeFilter, _ := cmd.Flags().GetString("recipe")
	configFilter, _ := cmd.Flags().GetString("config")
	rec := loadRecipe()
	cfg := config.Load()
	rec.ApplyTo(&cfg)

	filter := resolveStatsFilter(recipeFilter, configFilter)

	if detail {
		return runStatsDetailed(cfg, filter)
	}

	tally := preferences.Summarize(cfg.PreferencesPath, filter)
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

// resolveStatsFilter builds a StatsFilter from CLI flags.
// --config takes precedence; --recipe resolves to matching hashes via the config store.
func resolveStatsFilter(recipeName, configHash string) *preferences.StatsFilter {
	if configHash != "" {
		return &preferences.StatsFilter{ConfigHash: configHash}
	}
	if recipeName != "" {
		cs := recipestore.New("data/configs.json")
		hashes := cs.HashesForName(recipeName)
		if len(hashes) == 1 {
			return &preferences.StatsFilter{ConfigHash: hashes[0]}
		}
		if len(hashes) > 1 {
			fmt.Fprintf(os.Stderr, "warning: recipe %q has %d configs; showing all. Use --config to filter by specific hash.\n", recipeName, len(hashes))
		} else {
			fmt.Fprintf(os.Stderr, "warning: no config found for recipe %q; showing all.\n", recipeName)
		}
	}
	return nil
}

func runStatsDetailed(cfg config.Config, filter *preferences.StatsFilter) error {
	stats := preferences.SummarizeDetailed(cfg.PreferencesPath, filter)
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

func runSessions(cmd *cobra.Command, args []string) error {
	metas, err := session.List(sessionsBaseDir)
	if err != nil {
		return fmt.Errorf("could not list sessions: %w", err)
	}
	if len(metas) == 0 {
		fmt.Println("No sessions yet.")
		return nil
	}

	fmt.Printf("%-24s %5s  %-20s  %s\n", "Session ID", "Runs", "Last Active", "First Prompt")
	fmt.Printf("%-24s %5s  %-20s  %s\n",
		strings.Repeat("-", 24), "-----",
		strings.Repeat("-", 20), strings.Repeat("-", 30))
	for _, m := range metas {
		prompt := m.FirstPrompt
		if runes := []rune(prompt); len(runes) > 50 {
			prompt = string(runes[:50]) + "..."
		}
		fmt.Printf("%-24s %5d  %-20s  %s\n",
			m.ID,
			m.PromptCount,
			m.LastActiveAt.Format("2006-01-02 15:04:05"),
			prompt,
		)
	}
	return nil
}
