package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/errata-app/errata-cli/internal/adapters"
	"github.com/errata-app/errata-cli/internal/api"
	"github.com/errata-app/errata-cli/internal/config"
	"github.com/errata-app/errata-cli/internal/datastore"
	"github.com/errata-app/errata-cli/internal/headless"
	"github.com/errata-app/errata-cli/internal/logging"
	"github.com/errata-app/errata-cli/internal/mcp"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/paths"
	"github.com/errata-app/errata-cli/internal/pricing"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/pkg/recipestore"
	"github.com/errata-app/errata-cli/internal/session"
	"github.com/errata-app/errata-cli/internal/tools"
	"github.com/errata-app/errata-cli/internal/ui"
	"github.com/errata-app/errata-cli/internal/uid"
)

var (
	recipePath   string
	debugLogPath string
	continueFlag bool
	resumeID     string
)

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

	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to errata.app via GitHub OAuth",
		RunE: func(cmd *cobra.Command, args []string) error {
			return api.Login()
		},
	}

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out of errata.app",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := api.NewClient()
			if !client.IsLoggedIn() {
				fmt.Println("Not logged in.")
				return nil
			}
			if err := client.Logout(); err != nil {
				return fmt.Errorf("logout failed: %w", err)
			}
			fmt.Println("Logged out.")
			return nil
		},
	}

	whoamiCmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show current errata.app user",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := api.NewClient()
			if !client.IsLoggedIn() {
				fmt.Println("Not logged in. Run: errata login")
				return nil
			}
			user, err := client.Me()
			if err != nil {
				return fmt.Errorf("could not fetch user: %w", err)
			}
			if user.DisplayName != "" {
				fmt.Printf("%s (%s)\n", user.DisplayName, user.Username)
			} else {
				fmt.Println(user.Username)
			}
			return nil
		},
	}

	publishCmd := &cobra.Command{
		Use:   "publish [path]",
		Short: "Publish a recipe to errata.app",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runPublish,
	}

	pullCmd := &cobra.Command{
		Use:   "pull <author/slug>",
		Short: "Pull a recipe from errata.app",
		Args:  cobra.ExactArgs(1),
		RunE:  runPull,
	}

	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Upload preference data to errata.app",
		RunE:  runSync,
	}
	syncCmd.Flags().Bool("full", false, "Include full session content (one-shot override)")

	privacyCmd := &cobra.Command{
		Use:   "privacy [metadata|full]",
		Short: "View or set upload privacy mode",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runPrivacy,
	}

	root.AddCommand(
		statsCmd,
		runCmd,
		sessionsCmd,
		loginCmd,
		logoutCmd,
		whoamiCmd,
		publishCmd,
		pullCmd,
		syncCmd,
		privacyCmd,
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// resolveRecipePath resolves the -r flag to a file path.
// Short names (no path separators, no .md suffix) resolve to data/recipes/<name>.md.
// Paths with separators or .md suffix are used as-is.
func resolveRecipePath(flag, recipesDir string) string {
	if flag == "" {
		return ""
	}
	if !strings.ContainsAny(flag, "/\\") && !strings.HasSuffix(flag, ".md") {
		return filepath.Join(recipesDir, flag+".md")
	}
	return flag
}

// loadRecipe discovers and parses the recipe for the current invocation.
// Falls back to the built-in default on any error.
func loadRecipe() *recipe.Recipe {
	cfg := config.Load()
	layout := paths.New(cfg.DataDir)
	rec, err := recipe.Discover(resolveRecipePath(recipePath, layout.Recipes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load recipe: %v\n", err)
		return recipe.Default()
	}
	return rec
}

// applyProjectRoot changes the process working directory to rec.Constraints.ProjectRoot
// when it is set. All cwd-based path guards in file tools then automatically
// enforce the project boundary without further changes.
func applyProjectRoot(rec *recipe.Recipe) {
	if rec == nil || rec.Constraints.ProjectRoot == "" {
		return
	}
	if err := os.Chdir(rec.Constraints.ProjectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not set project_root %q: %v\n", rec.Constraints.ProjectRoot, err)
	}
}

// mcpConfigsFromRecipe converts recipe MCP server entries to mcp.ServerConfig.
func mcpConfigsFromRecipe(rec *recipe.Recipe) []mcp.ServerConfig {
	if rec == nil || len(rec.MCPServers) == 0 {
		return nil
	}
	out := make([]mcp.ServerConfig, 0, len(rec.MCPServers))
	for _, entry := range rec.MCPServers {
		args := strings.Fields(entry.Command)
		if len(args) == 0 {
			continue
		}
		out = append(out, mcp.ServerConfig{Name: entry.Name, Args: args})
	}
	return out
}

// setupAdapters loads pricing, adapters, and MCP servers.
// Returns adapters, warnings, MCP state (defs + dispatchers),
// and a cleanup function the caller must defer.
func setupAdapters(cfg config.Config, rec *recipe.Recipe, pricingCachePath, debugLog, sessionID string) (
	ads []models.ModelAdapter,
	warnings []string,
	mcpDefs []tools.ToolDef,
	mcpDispatchers map[string]tools.MCPDispatcher,
	cleanup func(),
) {
	pricing.LoadPricing(pricingCachePath)
	ads, warnings = adapters.ListAdapters(cfg)
	cleanup = func() {}

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
	if serverConfigs := mcpConfigsFromRecipe(rec); len(serverConfigs) > 0 {
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
	config.ApplyRecipe(rec, &cfg)
	layout := paths.New(cfg.DataDir)
	applyProjectRoot(rec)
	// BashTimeout and AllowLocalFetch are wired through context at run sites.

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
		resolved, err := session.Resolve(layout.Sessions, resumeID)
		if err != nil {
			return fmt.Errorf("session resolve: %w", err)
		}
		sessionID = resolved
		sp = session.PathsFor(layout.Sessions, sessionID)
		resuming = true

	case continueFlag:
		// --continue — resume most recent session.
		latest, err := session.LatestID(layout.Sessions)
		if err != nil {
			return fmt.Errorf("no previous session to continue: %w", err)
		}
		sessionID = latest
		sp = session.PathsFor(layout.Sessions, sessionID)
		resuming = true

	default:
		// Fresh session.
		sessionID, sp = session.New(layout.Sessions)
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
				config.ApplyRecipe(rec, &cfg)
				layout = paths.New(cfg.DataDir)
				// BashTimeout and AllowLocalFetch are wired through context at run sites.
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Session: %s\n", sessionID)

	ads, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg, rec, layout.PricingCache, debugLogPath, sessionID)
	defer cleanup()

	// Fetch available models from all configured providers (best-effort).
	providerModels := adapters.ListAvailableModels(context.Background(), cfg)

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
		if existing, err := session.LoadMetadata(sp.MetadataPath); err == nil && existing != nil {
			meta = *existing
			meta.Models = modelIDs // update with current adapters
		}
	}

	store, err := datastore.New(datastore.Options{
		PromptHistPath: layout.PromptHistory,
		SessionPaths:   sp,
		SessionID:      sessionID,
		Meta:           meta,
		RecipeStore:    recipestore.New(layout.ConfigStore),
		Recipe:         rec,
	})
	if err != nil {
		return fmt.Errorf("datastore init: %w", err)
	}
	err = ui.Run(ads, cfg, warnings, mcpDefs, mcpDispatchers, resuming, providerModels, debugLogPath != "", store)
	fmt.Fprintf(os.Stderr, "To continue this session: errata --resume %s\n", sessionID)
	return err
}

func runHeadless(cmd *cobra.Command, args []string) error {
	rec := loadRecipe()
	if len(rec.Models) == 0 {
		return fmt.Errorf("recipe has no models — ## Models section is required for `errata run`")
	}
	if len(rec.Tasks) == 0 {
		return fmt.Errorf("recipe has no tasks — ## Tasks section is required for `errata run`")
	}

	cfg := config.Load()
	config.ApplyRecipe(rec, &cfg)
	layout := paths.New(cfg.DataDir)
	applyProjectRoot(rec)
	// BashTimeout and AllowLocalFetch are wired through context at run sites.

	sessionID := uid.New("ses_")
	ads, warnings, mcpDefs, mcpDispatchers, cleanup := setupAdapters(cfg, rec, layout.PricingCache, debugLogPath, sessionID)
	defer cleanup()

	if len(ads) == 0 {
		return fmt.Errorf("no models available — check that API keys are set for the models in your recipe")
	}

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	jsonFlag, _ := cmd.Flags().GetBool("json")
	outputDir, _ := cmd.Flags().GetString("output-dir")
	if outputDir == "" {
		outputDir = layout.Outputs
	}
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
		OutputDir:      outputDir,
		CheckpointPath: layout.Checkpoint,
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
	config.ApplyRecipe(rec, &cfg)
	layout := paths.New(cfg.DataDir)

	filter := resolveStatsFilter(layout.ConfigStore, recipeFilter, configFilter)

	if detail {
		return runStatsDetailed(layout.Sessions, filter)
	}

	tally := session.SummarizeAcrossSessions(layout.Sessions, filter)
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
func resolveStatsFilter(configStorePath, recipeName, configHash string) *session.StatsFilter {
	if configHash != "" {
		return &session.StatsFilter{ConfigHash: configHash}
	}
	if recipeName != "" {
		cs := recipestore.New(configStorePath)
		hashes := cs.HashesForName(recipeName)
		if len(hashes) == 1 {
			return &session.StatsFilter{ConfigHash: hashes[0]}
		}
		if len(hashes) > 1 {
			fmt.Fprintf(os.Stderr, "warning: recipe %q has %d configs; showing all. Use --config to filter by specific hash.\n", recipeName, len(hashes))
		} else {
			fmt.Fprintf(os.Stderr, "warning: no config found for recipe %q; showing all.\n", recipeName)
		}
	}
	return nil
}

func runStatsDetailed(sessionsDir string, filter *session.StatsFilter) error {
	stats := session.SummarizeDetailedAcrossSessions(sessionsDir, filter)
	if len(stats) == 0 {
		fmt.Println("No preference data yet.")
		return nil
	}

	type row struct {
		model string
		s     session.ModelStats
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
	cfg := config.Load()
	layout := paths.New(cfg.DataDir)
	metas, err := session.List(layout.Sessions)
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

func runPublish(cmd *cobra.Command, args []string) error {
	client := api.NewClient()
	if !client.IsLoggedIn() {
		return fmt.Errorf("not logged in — run: errata login")
	}

	// Determine recipe path: positional arg or -r flag.
	cfg := config.Load()
	layout := paths.New(cfg.DataDir)
	path := resolveRecipePath(recipePath, layout.Recipes)
	if len(args) > 0 {
		path = args[0]
	}
	if path == "" {
		return fmt.Errorf("no recipe specified — use: errata publish <path> or errata -r <name> publish")
	}
	rec, err := recipe.Parse(path)
	if err != nil {
		return fmt.Errorf("could not load recipe: %w", err)
	}

	markdown := rec.MarshalMarkdown()
	entry, err := client.CreateRecipe(markdown)
	if err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}
	fmt.Printf("Published: %s\n", entry.Ref())
	return nil
}

func runPull(cmd *cobra.Command, args []string) error {
	ref := args[0]
	client := api.NewClient()

	raw, err := client.GetRecipeRaw(ref)
	if err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}

	slug := api.SlugFromRef(ref)

	cfg := config.Load()
	layout := paths.New(cfg.DataDir)
	dir := layout.Recipes
	if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
		return fmt.Errorf("could not create recipes directory: %w", mkErr)
	}

	dest := paths.NextAvailable(dir, slug+".md")
	if writeErr := os.WriteFile(dest, []byte(raw), 0o600); writeErr != nil {
		return fmt.Errorf("could not write recipe: %w", writeErr)
	}

	// Print the basename without .md extension for the -r shortcut.
	base := strings.TrimSuffix(filepath.Base(dest), ".md")
	fmt.Printf("Saved to %s — run with: errata -r %s\n", dest, base)
	return nil
}

func runSync(cmd *cobra.Command, args []string) error {
	client := api.NewClient()
	if !client.IsLoggedIn() {
		return fmt.Errorf("not logged in — run: errata login")
	}

	cfg := config.Load()
	layout := paths.New(cfg.DataDir)
	since := api.LoadLastSync()

	rs := recipestore.New(layout.ConfigStore)
	nameLookup := func(hash string) string {
		if snap := rs.Get(hash); snap != nil {
			return snap.Name
		}
		return ""
	}

	sessions := session.CollectForUpload(layout.Sessions, since, nameLookup)
	if len(sessions) == 0 {
		fmt.Println("Nothing new to sync.")
		return nil
	}

	// Resolve config snapshots for all referenced hashes.
	var configs map[string]recipestore.RecipeSnapshot
	if hashes := session.CollectConfigHashes(sessions); len(hashes) > 0 {
		configs = make(map[string]recipestore.RecipeSnapshot, len(hashes))
		for _, h := range hashes {
			if snap := rs.Get(h); snap != nil {
				configs[h] = *snap
			}
		}
		if len(configs) == 0 {
			configs = nil
		}
	}

	totalRuns := 0
	for _, s := range sessions {
		totalRuns += len(s.Runs)
	}
	fmt.Printf("Uploading %d runs across %d sessions…\n", totalRuns, len(sessions))

	payload := api.PreferenceUpload{Configs: configs, Sessions: sessions}

	// Determine whether to include full content.
	fullFlag, _ := cmd.Flags().GetBool("full")
	privacy := api.LoadPrivacy()
	if fullFlag || privacy.Mode == api.PrivacyFull {
		sessionIDs := make([]string, len(sessions))
		for i, s := range sessions {
			sessionIDs[i] = s.ID
		}
		payload.Content = session.CollectContentForUpload(layout.Sessions, sessionIDs)
		if payload.Content != nil {
			fmt.Printf("Including full content for %d sessions (privacy=full).\n", len(payload.Content))
		}
	}

	accepted, err := client.UploadPreferences(payload)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	if saveErr := api.SaveLastSync(time.Now()); saveErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save sync timestamp: %v\n", saveErr)
	}
	fmt.Printf("Synced: %d runs accepted.\n", accepted)
	return nil
}

func runPrivacy(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		s := api.LoadPrivacy()
		fmt.Printf("Upload privacy mode: %s\n", s.Mode)
		return nil
	}
	mode := api.PrivacyMode(args[0])
	if mode != api.PrivacyMetadata && mode != api.PrivacyFull {
		return fmt.Errorf("invalid mode %q — use \"metadata\" or \"full\"", args[0])
	}
	if err := api.SavePrivacy(api.PrivacySettings{Mode: mode}); err != nil {
		return fmt.Errorf("could not save privacy setting: %w", err)
	}
	fmt.Printf("Upload privacy mode set to: %s\n", mode)
	return nil
}
