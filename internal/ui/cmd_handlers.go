package ui

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/errata-app/errata-cli/internal/adapters"
	"github.com/errata-app/errata-cli/internal/api"
	"github.com/errata-app/errata-cli/internal/checkpoint"
	"github.com/errata-app/errata-cli/internal/commands"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
	"github.com/errata-app/errata-cli/internal/paths"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/internal/prompt"
	"github.com/errata-app/errata-cli/internal/runner"
	"github.com/errata-app/errata-cli/internal/sandbox"
	"github.com/errata-app/errata-cli/internal/session"
	"github.com/errata-app/errata-cli/internal/tools"
)

func (a App) handlePrompt(userPrompt string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea value-receiver pattern
	trimmed := strings.TrimSpace(userPrompt)
	lower := strings.ToLower(trimmed)

	if lower == "/config" || strings.HasPrefix(lower, "/config ") {
		return a.handleConfigCommand(strings.TrimSpace(trimmed[len("/config"):]))
	}
	if lower == "/save" || strings.HasPrefix(lower, "/save ") {
		return a.handleSaveCommand(strings.TrimSpace(trimmed[len("/save"):]))
	}
	if lower == "/load" || strings.HasPrefix(lower, "/load ") {
		return a.handleLoadCommand(strings.TrimSpace(trimmed[len("/load"):]))
	}
	if lower == "/export" || strings.HasPrefix(lower, "/export ") {
		return a.handleExportCommand(strings.TrimSpace(trimmed[len("/export"):]))
	}
	if lower == "/publish" {
		return a.handlePublishCommand()
	}
	if lower == "/pull" || strings.HasPrefix(lower, "/pull ") {
		return a.handlePullCommand(strings.TrimSpace(trimmed[len("/pull"):]))
	}
	if lower == "/sync" {
		return a.handleSyncCommand()
	}
	if lower == "/privacy" || strings.HasPrefix(lower, "/privacy ") {
		return a.handlePrivacyCommand(strings.TrimSpace(trimmed[len("/privacy"):]))
	}
	switch lower {
	case "/exit", "/quit":
		return a, tea.Quit
	case "/verbose":
		return a.handleVerboseCmd()
	case "/clear":
		return a.handleClearCmd()
	case "/wipe":
		return a.handleWipeCmd()
	case "/compact":
		return a.handleCompactCmd()
	case "/resume":
		return a.handleResumeCmd()
	case "/rewind":
		return a.handleRewindCmd()
	case "/stats":
		return a.handleStatsCmd()
	case "/help":
		return a.withMessage(helpText())
	}
	// Parse @mentions for transient per-message model targeting.
	mention := ParseMentions(trimmed, a.modelIDCandidates())
	if len(mention.Errors) > 0 {
		return a.withMessage(fmt.Sprintf("No model matching %q in current recipe.", mention.Errors[0]))
	}
	if len(mention.ModelIDs) > 0 {
		if mention.Prompt == "" {
			return a.withMessage("No prompt text after @mention(s).")
		}
		var mentionAdapters []models.ModelAdapter
		for _, id := range mention.ModelIDs {
			var found models.ModelAdapter
			for _, ad := range a.adapters {
				if ad.ID() == id {
					found = ad
					break
				}
			}
			if found == nil {
				return a.withMessage(fmt.Sprintf("model %q not active — enable it in /config models", id))
			}
			mentionAdapters = append(mentionAdapters, found)
		}
		return a.launchRunTargeted(mention.Prompt, mentionAdapters)
	}
	return a.launchRun(trimmed)
}

func (a App) handleVerboseCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea value-receiver pattern
	a.verbose = !a.verbose
	state := "off"
	if a.verbose {
		state = "on"
	}
	return a.withMessage(fmt.Sprintf("Verbose mode %s.", state))
}

func (a App) handleClearCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	a.feed = nil
	a.lastRunInView = false
	a.pastedText = ""
	a.pastedLineCount = 0
	return a, clearScreenAndScrollback()
}

func (a App) handleWipeCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	a.feed = nil
	a.lastRunInView = false
	a.store.ClearRewindStack()
	a.pastedText = ""
	a.pastedLineCount = 0
	a.store.ClearHistories()
	return a, clearScreenAndScrollback()
}

// clearScreenAndScrollback returns a tea.Cmd that clears the visible screen
// and the terminal scrollback buffer. It uses tea.Exec to safely write ANSI
// escape sequences while the renderer is paused, then ClearScreen to repaint.
func clearScreenAndScrollback() tea.Cmd {
	return tea.Exec(&clearScrollbackCmd{}, func(error) tea.Msg {
		return tea.ClearScreen()
	})
}

// clearScrollbackCmd implements tea.ExecCommand to write the ANSI clear
// sequences directly to the terminal output.
type clearScrollbackCmd struct{ out io.Writer }

func (c *clearScrollbackCmd) Run() error {
	// \033[H  — cursor home
	// \033[2J — erase entire visible screen
	// \033[3J — erase scrollback buffer
	_, err := c.out.Write([]byte("\033[H\033[2J\033[3J"))
	return err
}
func (c *clearScrollbackCmd) SetStdin(io.Reader)      {}
func (c *clearScrollbackCmd) SetStdout(w io.Writer)    { c.out = w }
func (c *clearScrollbackCmd) SetStderr(io.Writer)      {}

func (a App) handleCompactCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	toCompact := a.activeAdapters
	histories := a.store.Histories()
	prog := a.prog
	var compactSumPrompt string
	if compactRec := a.store.ActiveRecipe(); compactRec != nil {
		compactSumPrompt = compactRec.Context.SummarizationPrompt
	}
	app, printCmd := a.withMessage("Compacting conversation history…")
	return app, tea.Batch(printCmd, func() tea.Msg {
		ctx := prompt.WithSummarizationPrompt(context.Background(), compactSumPrompt)
		updated := runner.CompactHistories(
			ctx, toCompact, histories,
			func(modelID string, e models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: e})
			},
		)
		return compactCompleteMsg{histories: updated}
	})
}

func (a App) handleStatsCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	meta := a.store.Metadata()
	stats := session.SummarizeRunsDetailed(meta.Runs, nil)
	var sb strings.Builder
	sb.WriteString("Stats (session):\n")
	if len(stats) == 0 {
		sb.WriteString("  No preference data yet.\n")
	} else {
		sb.WriteString("  Preference wins:\n")
		type row struct {
			id string
			s  session.ModelStats
		}
		rows := make([]row, 0, len(stats))
		for id, s := range stats {
			rows = append(rows, row{id, s})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].s.Wins != rows[j].s.Wins {
				return rows[i].s.Wins > rows[j].s.Wins
			}
			return rows[i].id < rows[j].id
		})
		for _, r := range rows {
			cost := ""
			if r.s.AvgCostUSD > 0 {
				cost = fmt.Sprintf("  avg cost $%.4f", r.s.AvgCostUSD)
			}
			signals := fmt.Sprintf("%dW / %dL", r.s.Wins, r.s.Losses)
			if r.s.ThumbsDown > 0 {
				signals += fmt.Sprintf(" / %d👎", r.s.ThumbsDown)
			}
			fmt.Fprintf(&sb, "    %s: %s  %.1f%% win  avg %dms%s  (%d runs)\n",
				r.id,
				signals,
				r.s.WinRate,
				int64(r.s.AvgLatencyMS),
				cost,
				r.s.Participations,
			)
		}
	}
	costPerModel := a.store.CostPerModel()
	if len(costPerModel) > 0 {
		sb.WriteString("  Session cost:\n")
		ids := make([]string, 0, len(costPerModel))
		for id := range costPerModel {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return costPerModel[ids[i]] > costPerModel[ids[j]]
		})
		for _, id := range ids {
			fmt.Fprintf(&sb, "    %s: $%.4f\n", id, costPerModel[id])
		}
		fmt.Fprintf(&sb, "  Total: $%.4f\n", a.store.TotalCost())
	}
	return a.withMessage(strings.TrimRight(sb.String(), "\n"))
}

func (a App) launchRun(trimmed string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	return a.launchRunTargeted(trimmed, nil)
}

func (a App) launchRunTargeted(trimmed string, mentionTargets []models.ModelAdapter) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	toRun := a.activeAdapters
	if mentionTargets != nil {
		toRun = mentionTargets
	}

	if len(toRun) == 0 {
		return a.withMessage("No models active. Use /config to add models.")
	}

	// Flush the previous run's output from View() to scrollback.
	var flushCmd tea.Cmd
	a, flushCmd = a.flushLastRunToScrollback()

	// Record in prompt history (deduplicate consecutive identical entries).
	a.historyIdx = -1
	a.historyInputBuf = ""
	a.store.RecordPrompt(trimmed)

	a.lastPrompt = trimmed
	a.mode = modeRunning
	a.panels = nil
	a.panelIdx = make(map[string]int)
	for i, ad := range toRun {
		ps := newPanelState(ad.ID(), i)
		ps.histTokens = runner.EstimateHistoryTokens(a.store.Histories()[ad.ID()])
		a.panels = append(a.panels, ps)
		a.panelIdx[ad.ID()] = i
	}

	// Push the run entry immediately. The feed item shares *panelState pointers
	// with a.panels, so live updates to panel state propagate automatically.
	a.feed = append(a.feed, feedItem{
		kind:   "run",
		prompt: trimmed,
		panels: a.panels,
	})

	// Print the prompt to scrollback.
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	promptPrintCmd := tea.Println(wrapText("> "+trimmed, max(a.width, 40), 0, promptStyle))

	params := a.captureRunParams()
	ads := toRun
	verbose := a.verbose
	prog := a.prog
	histories := a.store.Histories() // read-only in goroutine; written only by main loop
	mcpDispatchers := a.mcpDispatchers

	baseCtx, cancelFn := context.WithCancel(context.Background())
	a.cancelRun = cancelFn
	if a.debugLog {
		baseCtx = adapters.WithDebugRequests(baseCtx)
	}

	var batchCmds []tea.Cmd
	if flushCmd != nil {
		batchCmds = append(batchCmds, flushCmd)
	}
	batchCmds = append(batchCmds, promptPrintCmd, panelTick(), func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		// Skip auto-compact when context strategy is "manual" or "off".
		if params.contextStrategy != "manual" && params.contextStrategy != "off" {
			compactCtx := prompt.WithSummarizationPrompt(baseCtx, params.sumPrompt)
			for _, ad := range ads {
				if runner.ShouldAutoCompact(effectiveHistories, ad.ID(), params.compactThreshold) {
					prog.Send(agentEventMsg{modelID: ad.ID(), event: models.AgentEvent{
						Type: models.EventText, Data: "[auto-compacting history…]",
					}})
					effectiveHistories = runner.CompactHistories(
						compactCtx, []models.ModelAdapter{ad},
						effectiveHistories, func(id string, e models.AgentEvent) {
							prog.Send(agentEventMsg{modelID: id, event: e})
						},
					)
					compacted = effectiveHistories
				}
			}
		}
		runCtx := wireRunContext(baseCtx, params, mcpDispatchers)
		collector := output.NewCollector()
		responses := runner.RunAll(
			runCtx, ads, effectiveHistories, trimmed,
			collector.WrapOnEvent(func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			}),
			func(idx int, resp models.ModelResponse) {
				prog.Send(modelDoneMsg{idx: idx, response: resp})
			},
			verbose,
		)

		// Save checkpoint immediately if interrupted (before bubbletea processes
		// the message — critical for surviving SIGTERM).
		if baseCtx.Err() != nil {
			panelIDs := make([]string, len(ads))
			for i, ad := range ads {
				panelIDs[i] = ad.ID()
			}
			if cp := checkpoint.Build(trimmed, panelIDs, responses, verbose); cp != nil {
				if err := checkpoint.Save(params.checkpointPath, *cp); err != nil {
					log.Printf("warning: failed to save checkpoint: %v", err)
				}
			}
		}

		toolNames := make([]string, len(params.activeDefs))
		for i, d := range params.activeDefs {
			toolNames[i] = d.Name
		}

		return runCompleteMsg{responses: responses, compactedHistories: compacted, collector: collector, toolNames: toolNames}
	})
	return a, tea.Batch(batchCmds...)
}

// runLaunchParams holds all recipe-derived settings captured once at launch time.
type runLaunchParams struct {
	activeDefs        []tools.ToolDef
	toolGuidanceMap   map[string]string
	bashPrefixes      []string
	contextStrategy   string
	sumPrompt         string
	systemPrompt      string
	bashTimeout       time.Duration
	agentTimeout      time.Duration
	allowLocalFetch   bool
	compactThreshold  float64
	maxHistoryTurns   int
	maxSteps          int
	sandboxFilesystem string
	sandboxNetwork    string
	projectRoot       string
	checkpointPath    string
}

// captureRunParams reads ActiveRecipe() once and builds runLaunchParams with
// all recipe-derived settings needed for a run.
func (a App) captureRunParams() runLaunchParams { //nolint:gocritic // called from bubbletea value-receiver methods
	rec := a.store.ActiveRecipe()
	if rec == nil {
		rec = &recipe.Recipe{}
	}

	var allowlist []string
	var bashPrefixes []string
	var toolGuidanceMap map[string]string
	if rec.Tools != nil {
		allowlist = rec.Tools.Allowlist
		bashPrefixes = rec.Tools.BashPrefixes
		toolGuidanceMap = rec.Tools.Guidance
	}

	activeDefs := tools.DefinitionsAllowed(allowlist, a.disabledTools)
	activeDefs = append(activeDefs, tools.FilterDefs(a.mcpDefs, a.disabledTools)...)

	// Apply sandbox restrictions.
	if rec.Sandbox.Filesystem == "read_only" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WriteToolName: true,
			tools.EditToolName:  true,
		})
	}
	if rec.Sandbox.Network == "none" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WebFetchToolName:  true,
			tools.WebSearchToolName: true,
		})
	}

	// Apply recipe-level tool description overrides.
	if rec.Tools != nil {
		activeDefs = tools.ApplyDescriptions(activeDefs, rec.Tools.Guidance)
	}

	return runLaunchParams{
		activeDefs:        activeDefs,
		toolGuidanceMap:   toolGuidanceMap,
		bashPrefixes:      bashPrefixes,
		contextStrategy:   rec.Context.Strategy,
		sumPrompt:         rec.Context.SummarizationPrompt,
		systemPrompt:      rec.SystemPrompt,
		bashTimeout:       rec.Constraints.BashTimeout,
		agentTimeout:      rec.Constraints.Timeout,
		allowLocalFetch:   rec.Sandbox.AllowLocalFetch,
		compactThreshold:  rec.Context.CompactThreshold,
		maxHistoryTurns:   rec.Context.MaxHistoryTurns,
		maxSteps:          rec.Constraints.MaxSteps,
		sandboxFilesystem: rec.Sandbox.Filesystem,
		sandboxNetwork:    rec.Sandbox.Network,
		projectRoot:       rec.Constraints.ProjectRoot,
		checkpointPath:    a.store.CheckpointPath(),
	}
}

// wireRunContext applies all recipe-derived settings from runLaunchParams onto
// a base context, returning the fully-wired context for runner.RunAll.
func wireRunContext(baseCtx context.Context, p runLaunchParams, mcpDispatchers map[string]tools.MCPDispatcher) context.Context {
	ctx := tools.WithActiveTools(baseCtx, p.activeDefs)
	ctx = tools.WithMCPDispatchers(ctx, mcpDispatchers)
	ctx = tools.WithBashPrefixes(ctx, p.bashPrefixes)
	ctx = sandbox.WithConfig(ctx, sandbox.Config{
		Filesystem:      p.sandboxFilesystem,
		Network:         p.sandboxNetwork,
		ProjectRoot:     p.projectRoot,
		AllowLocalFetch: p.allowLocalFetch,
	})
	if p.bashTimeout > 0 {
		ctx = tools.WithBashTimeout(ctx, p.bashTimeout)
	}
	ctx = runner.WithRunOptions(ctx, runner.RunOptions{
		Timeout:          p.agentTimeout,
		CompactThreshold: p.compactThreshold,
		MaxHistoryTurns:  p.maxHistoryTurns,
		MaxSteps:         p.maxSteps,
		CheckpointPath:   p.checkpointPath,
	})
	ctx = tools.WithSystemPromptExtra(ctx, p.systemPrompt)
	ctx = tools.WithToolGuidanceMap(ctx, p.toolGuidanceMap)
	return ctx
}

func (a App) handleResumeCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	cp, err := a.store.LoadCheckpoint()
	if err != nil {
		return a.withMessage(fmt.Sprintf("Error loading checkpoint: %v", err))
	}
	if cp == nil {
		return a.withMessage("No interrupted run to resume.")
	}

	var completedResponses []models.ModelResponse
	var toRerunIDs []string
	for _, snap := range cp.Responses {
		if snap.Completed {
			completedResponses = append(completedResponses, snap.ToModelResponse())
		} else {
			toRerunIDs = append(toRerunIDs, snap.ModelID)
		}
	}

	if len(toRerunIDs) == 0 {
		a.store.ClearCheckpoint()
		return a.withMessage("All models from the last run completed. No resume needed.")
	}

	var rerunAdapters []models.ModelAdapter
	for _, id := range toRerunIDs {
		var found models.ModelAdapter
		for _, ad := range a.adapters {
			if ad.ID() == id {
				found = ad
				break
			}
		}
		if found == nil {
			newAd, err := adapters.NewAdapter(id, a.cfg)
			if err != nil {
				return a.withMessage(fmt.Sprintf("Cannot create adapter for %q: %v", id, err))
			}
			found = newAd
		}
		rerunAdapters = append(rerunAdapters, found)
	}

	a.store.ClearCheckpoint()
	return a.launchResumeRun(cp.Prompt, rerunAdapters, completedResponses, cp.Verbose)
}

func (a App) handleRewindCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if !a.store.CanRewind() {
		return a.withMessage("Nothing to rewind.")
	}

	result, err := a.store.Rewind()
	if err != nil {
		return a.withMessage(fmt.Sprintf("Rewind failed: %v", err))
	}

	// Annotate the UI display feed.
	if result.FeedIndex >= 0 && result.FeedIndex < len(a.feed) {
		a.feed[result.FeedIndex].note = result.Note
	}

	return a.withMessage("Rewound last run." + result.FileMsg)
}

func (a App) launchResumeRun(userPrompt string, rerunAdapters []models.ModelAdapter, completedResponses []models.ModelResponse, verbose bool) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	// Flush the previous run's output from View() to scrollback.
	var flushCmd tea.Cmd
	a, flushCmd = a.flushLastRunToScrollback()

	a.lastPrompt = userPrompt
	a.mode = modeRunning
	a.panels = nil
	a.panelIdx = make(map[string]int)

	// Add panels for already-completed responses (marked done immediately).
	for i, resp := range completedResponses {
		ps := newPanelState(resp.ModelID, i)
		ps.done = true
		ps.latencyMS = resp.LatencyMS
		ps.inputTokens = resp.InputTokens
		ps.outputTokens = resp.OutputTokens
		ps.reasoningTokens = resp.ReasoningTokens
		ps.costUSD = resp.CostUSD
		a.panels = append(a.panels, ps)
		a.panelIdx[resp.ModelID] = i
	}
	// Add panels for models being re-run.
	for j, ad := range rerunAdapters {
		idx := len(completedResponses) + j
		ps := newPanelState(ad.ID(), idx)
		ps.histTokens = runner.EstimateHistoryTokens(a.store.Histories()[ad.ID()])
		a.panels = append(a.panels, ps)
		a.panelIdx[ad.ID()] = idx
	}

	a.feed = append(a.feed, feedItem{
		kind:   "run",
		prompt: "[resume] " + userPrompt,
		panels: a.panels,
	})

	// Print the resume prompt to scrollback.
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	promptPrintCmd := tea.Println(wrapText("> [resume] "+userPrompt, max(a.width, 40), 0, promptStyle))

	params := a.captureRunParams()
	ads := rerunAdapters
	prog := a.prog
	histories := a.store.Histories()
	mcpDispatchers := a.mcpDispatchers

	baseCtx, cancelFn := context.WithCancel(context.Background())
	a.cancelRun = cancelFn
	if a.debugLog {
		baseCtx = adapters.WithDebugRequests(baseCtx)
	}

	var resumeBatchCmds []tea.Cmd
	if flushCmd != nil {
		resumeBatchCmds = append(resumeBatchCmds, flushCmd)
	}
	resumeBatchCmds = append(resumeBatchCmds, promptPrintCmd, panelTick(), func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		if params.contextStrategy != "manual" && params.contextStrategy != "off" {
			compactCtx := prompt.WithSummarizationPrompt(baseCtx, params.sumPrompt)
			for _, ad := range ads {
				if runner.ShouldAutoCompact(effectiveHistories, ad.ID(), params.compactThreshold) {
					prog.Send(agentEventMsg{modelID: ad.ID(), event: models.AgentEvent{
						Type: models.EventText, Data: "[auto-compacting history…]",
					}})
					effectiveHistories = runner.CompactHistories(
						compactCtx, []models.ModelAdapter{ad},
						effectiveHistories, func(id string, e models.AgentEvent) {
							prog.Send(agentEventMsg{modelID: id, event: e})
						},
					)
					compacted = effectiveHistories
				}
			}
		}
		runCtx := wireRunContext(baseCtx, params, mcpDispatchers)
		collector := output.NewCollector()
		completedCount := len(completedResponses)
		responses := runner.RunAll(
			runCtx, ads, effectiveHistories, userPrompt,
			collector.WrapOnEvent(func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			}),
			func(idx int, resp models.ModelResponse) {
				prog.Send(modelDoneMsg{idx: completedCount + idx, response: resp})
			},
			verbose,
		)

		// Save checkpoint if interrupted.
		if baseCtx.Err() != nil {
			// Merge completed + new for checkpoint so completed models stay preserved.
			allResp := slices.Concat(completedResponses, responses)
			allIDs := make([]string, len(allResp))
			for i, r := range allResp {
				allIDs[i] = r.ModelID
			}
			if cp := checkpoint.Build(userPrompt, allIDs, allResp, verbose); cp != nil {
				if err := checkpoint.Save(params.checkpointPath, *cp); err != nil {
					log.Printf("warning: failed to save checkpoint: %v", err)
				}
			}
		}

		// Merge completed responses (from checkpoint) with fresh re-run responses.
		allResponses := slices.Concat(completedResponses, responses)

		toolNames := make([]string, len(params.activeDefs))
		for i, d := range params.activeDefs {
			toolNames[i] = d.Name
		}

		return runCompleteMsg{responses: allResponses, compactedHistories: compacted, collector: collector, toolNames: toolNames}
	})
	return a, tea.Batch(resumeBatchCmds...)
}

func (a App) handlePublishCommand() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if !a.apiClient.IsLoggedIn() {
		return a.withMessage("Not logged in. Run: errata login")
	}
	rec := a.store.ActiveRecipe()
	if rec == nil {
		return a.withMessage("No recipe to publish.")
	}

	markdown := rec.MarshalMarkdown()
	client := a.apiClient
	app, printCmd := a.withMessage("Publishing…")
	return app, tea.Batch(printCmd, func() tea.Msg {
		entry, err := client.CreateRecipe(markdown)
		if err != nil {
			return publishCompleteMsg{err: err}
		}
		return publishCompleteMsg{ref: entry.Ref()}
	})
}

func (a App) handlePullCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if args == "" {
		return a.withMessage("Usage: /pull <author/slug>")
	}

	client := a.apiClient
	app, printCmd := a.withMessage("Pulling " + args + "…")
	return app, tea.Batch(printCmd, func() tea.Msg {
		raw, err := client.GetRecipeRaw(args)
		if err != nil {
			return pullCompleteMsg{err: err}
		}
		return pullCompleteMsg{raw: raw, ref: args}
	})
}

func (a App) handlePullComplete(raw, ref string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	rec, err := recipe.ParseContent([]byte(raw))
	if err != nil {
		return a.withMessage(fmt.Sprintf("Pull failed (invalid recipe): %v", err))
	}

	// Set as session recipe.
	a.store.SetSessionRecipe(cloneRecipe(rec))
	a.applySessionRecipe()

	// Save to data/recipes/ for future -r use.
	slug := api.SlugFromRef(ref)
	dir := a.store.RecipesDir()
	if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
		return a.withMessage(fmt.Sprintf("Pulled recipe loaded, but could not save to disk: %v", mkErr))
	}
	dest := paths.NextAvailable(dir, slug+".md")
	if writeErr := os.WriteFile(dest, []byte(raw), 0o600); writeErr != nil {
		return a.withMessage(fmt.Sprintf("Recipe loaded but could not save to %s: %v", dest, writeErr))
	}

	name := rec.Name
	if name == "" {
		name = ref
	}
	return a.withMessage(fmt.Sprintf("Pulled %q — loaded as session recipe. Saved to %s", name, dest))
}

func (a App) handlePrivacyCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if args == "" {
		s := api.LoadPrivacy()
		return a.withMessage(fmt.Sprintf("Upload privacy mode: %s", s.Mode))
	}
	mode := api.PrivacyMode(args)
	if mode != api.PrivacyMetadata && mode != api.PrivacyFull {
		return a.withMessage(fmt.Sprintf("Invalid mode %q — use \"metadata\" or \"full\".", args))
	}
	if err := api.SavePrivacy(api.PrivacySettings{Mode: mode}); err != nil {
		return a.withMessage(fmt.Sprintf("Could not save privacy setting: %v", err))
	}
	return a.withMessage(fmt.Sprintf("Upload privacy mode set to: %s", mode))
}

func (a App) handleSyncCommand() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if !a.apiClient.IsLoggedIn() {
		return a.withMessage("Not logged in. Run: errata login")
	}

	client := a.apiClient
	sessionsDir := a.store.SessionsDir()
	nameLookup := a.store.RecipeNameLookup()
	store := a.store
	privacy := api.LoadPrivacy()

	app, printCmd := a.withMessage("Syncing…")
	return app, tea.Batch(printCmd, func() tea.Msg {
		since := api.LoadLastSync()
		sessions := session.CollectForUpload(sessionsDir, since, nameLookup)
		if len(sessions) == 0 {
			return syncCompleteMsg{accepted: 0}
		}
		configs := store.ConfigSnapshots(session.CollectConfigHashes(sessions))
		payload := api.PreferenceUpload{Configs: configs, Sessions: sessions}
		if privacy.Mode == api.PrivacyFull {
			sessionIDs := make([]string, len(sessions))
			for i, s := range sessions {
				sessionIDs[i] = s.ID
			}
			payload.Content = session.CollectContentForUpload(sessionsDir, sessionIDs)
		}
		accepted, err := client.UploadPreferences(payload)
		if err != nil {
			return syncCompleteMsg{err: err}
		}
		_ = api.SaveLastSync(time.Now())
		return syncCompleteMsg{accepted: accepted}
	})
}

func (a App) handleConfigCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if a.store.SessionRecipe() == nil {
		a.store.SetSessionRecipe(cloneRecipe(a.store.BaseRecipe()))
	}
	sessRec := a.store.SessionRecipe()
	a.configSections = buildConfigSections(sessRec, a.adapters, a.disabledTools)
	a.configOverlayActive = true
	a.configSelectedIdx = 0
	a.configExpandedIdx = -1

	if args != "" {
		lowerArgs := strings.ToLower(args)
		if lowerArgs == "reset" {
			a.store.SetSessionRecipe(cloneRecipe(a.store.BaseRecipe()))
			a.applySessionRecipe()
			a.configOverlayActive = false
			return a.withMessage("Configuration reset to recipe defaults.")
		}
		for i, sec := range a.configSections {
			if strings.EqualFold(sec.Name, lowerArgs) {
				a.configSelectedIdx = i
				a.configExpandedIdx = i
				// Populate the editor state for the expanded section.
				switch sec.Kind {
				case "list":
					switch sec.Name {
					case "models":
						a.configListFilter = ""
						a.configListItems = buildModelsList(a.activeAdapters, a.adapters, a.providerModels, "")
					case "tools":
						var allowlist []string
						if sessRec.Tools != nil {
							allowlist = sessRec.Tools.Allowlist
						}
						a.configListItems = buildToolsList(allowlist, a.disabledTools)
					case "mcp-servers":
						var items []listItem
						for _, s := range sessRec.MCPServers {
							items = append(items, listItem{Label: s.Name + ": " + s.Command, Active: true})
						}
						a.configListItems = items
					}
					a.configListCursor = 0
				case "scalar":
					a.configScalarFields = buildScalarFields(sec.Name, sessRec)
					a.configScalarCursor = 0
					a.configEditBuf = ""
				case "text":
					var content string
					switch sec.Name {
					case "system-prompt":
						content = sessRec.SystemPrompt
					case "context-summarization":
						content = sessRec.Context.SummarizationPrompt
					}
					a.configTextArea.SetValue(content)
					a.configTextArea.Focus()
					a.configTextEditing = true
				}
				break
			}
		}
	}
	return a, nil
}

func helpText() string {
	var sb strings.Builder
	sb.WriteString("Commands:")
	for _, c := range commands.All {
		fmt.Fprintf(&sb, "\n  %-20s%s", c.Name, c.Desc)
	}
	return sb.String()
}

// ── save/load/export handlers ────────────────────────────────────────────────

func (a App) handleSaveCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	rec := a.store.ActiveRecipe()
	if rec == nil {
		return a.withMessage("No recipe to save.")
	}

	var path string
	if args != "" {
		path = args
	} else {
		path = nextAvailablePath("recipe.md")
	}

	md := rec.MarshalMarkdown()
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		return a.withMessage(fmt.Sprintf("Save failed: %v", err))
	}
	return a.withMessage(fmt.Sprintf("Recipe saved to %s", path))
}

func (a App) handleLoadCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if args == "" {
		return a.withMessage("Usage: /load <path>")
	}

	rec, err := recipe.Parse(args)
	if err != nil {
		return a.withMessage(fmt.Sprintf("Load failed: %v", err))
	}

	a.store.SetSessionRecipe(cloneRecipe(rec))
	a.applySessionRecipe()

	name := rec.Name
	if name == "" {
		name = args
	}
	return a.withMessage(fmt.Sprintf("Loaded recipe %q (%d models, %d tools)",
		name,
		len(rec.Models),
		func() int {
			if rec.Tools == nil {
				return 0
			}
			return len(rec.Tools.Allowlist)
		}(),
	))
}

func (a App) handleExportCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	meta := a.store.Metadata()
	content := a.store.Content()
	if len(content.Runs) == 0 {
		return a.withMessage("No run output to export. Run a prompt first.")
	}

	dir := "."
	if args != "" {
		dir = args
	}

	// Build per-run reports from metadata + content.
	var reports []*output.Report
	for i, rc := range content.Runs {
		var modelResults []output.ModelResult
		for _, m := range rc.Models {
			modelResults = append(modelResults, output.ModelResult{
				ModelID:        m.ModelID,
				Text:           m.Text,
				StopReason:     m.StopReason,
				Steps:          m.Steps,
				ProposedWrites: m.ProposedWrites,
				Events:         m.Events,
			})
		}
		// Enrich from metadata if available.
		var rs *session.RunSummary
		if i < len(meta.Runs) {
			rs = &meta.Runs[i]
			for j, mr := range modelResults {
				if lat, ok := rs.LatenciesMS[mr.ModelID]; ok {
					modelResults[j].LatencyMS = lat
				}
				if cost, ok := rs.CostsUSD[mr.ModelID]; ok {
					modelResults[j].CostUSD = cost
				}
				if tok, ok := rs.InputTokens[mr.ModelID]; ok {
					modelResults[j].InputTokens = tok
				}
				if tok, ok := rs.OutputTokens[mr.ModelID]; ok {
					modelResults[j].OutputTokens = tok
				}
			}
		}
		r := &output.Report{
			SessionID: a.store.SessionID(),
			Prompt:    rc.Prompt,
			Models:    modelResults,
		}
		if rs != nil {
			r.Timestamp = rs.Timestamp
			if rs.Selected != "" {
				r.Selection = &output.SelectionOutcome{
					SelectedModel: rs.Selected,
					AppliedFiles:  rs.AppliedFiles,
					Rating:        rs.Rating,
				}
			}
		}
		reports = append(reports, r)
	}

	sessionReport := output.BuildSessionReport(a.store.SessionID(), reports)
	path, err := output.SaveSession(dir, sessionReport)
	if err != nil {
		return a.withMessage(fmt.Sprintf("Export failed: %v", err))
	}
	return a.withMessage(fmt.Sprintf("Output exported to %s (%d turns)", path, len(reports)))
}

// maxSaveSuffix is the highest numeric suffix nextAvailablePath will try
// before giving up and falling back to overwriting the base path.
const maxSaveSuffix = 100

// nextAvailablePath returns base if it doesn't exist, otherwise tries
// base_1.ext, base_2.ext, … up to maxSaveSuffix. Falls back to base if
// all slots are taken.
func nextAvailablePath(base string) string {
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i <= maxSaveSuffix; i++ {
		candidate := fmt.Sprintf("%s_%d%s", stem, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return base
}
