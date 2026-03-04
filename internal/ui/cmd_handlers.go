package ui

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/prompt"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/sandbox"
	"github.com/suarezc/errata/internal/subagent"
	"github.com/suarezc/errata/internal/tools"
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
	a.store.ClearRewindStack()
	a.pastedText = ""
	a.pastedLineCount = 0
	return a, nil
}

func (a App) handleWipeCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	a.feed = nil
	a.store.ClearRewindStack()
	a.pastedText = ""
	a.pastedLineCount = 0
	a.store.ClearHistories()
	a.store.ClearReportPaths()
	return a, nil
}

func (a App) handleCompactCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	toCompact := a.activeAdapters
	histories := a.store.Histories()
	prog := a.prog
	var compactSumPrompt string
	if compactRec := a.store.ActiveRecipe(); compactRec != nil {
		compactSumPrompt = compactRec.SummarizationPrompt
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
	// Filter stats by current config when a config store is available.
	var filter *preferences.StatsFilter
	var recipeName string
	if a.store.RecipeStore() != nil {
		snap := a.store.BuildRecipeSnapshot()
		h := a.store.RecipeStore().Put(snap)
		filter = &preferences.StatsFilter{ConfigHash: h}
		recipeName = snap.Name
	}
	stats := preferences.SummarizeDetailed(a.store.PrefPath(), filter)
	var sb strings.Builder
	if recipeName != "" {
		fmt.Fprintf(&sb, "Stats (recipe: %s):\n", recipeName)
	} else {
		sb.WriteString("Stats:\n")
	}
	if len(stats) == 0 {
		sb.WriteString("  No preference data yet.\n")
	} else {
		sb.WriteString("  Preference wins:\n")
		type row struct {
			id string
			s  preferences.ModelStats
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
		return a.withMessage("No models configured. Set API keys in .env and restart.")
	}

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

	ads := toRun
	verbose := a.verbose
	prog := a.prog
	histories := a.store.Histories() // read-only in goroutine; written only by main loop
	activeDefs := tools.DefinitionsAllowed(a.toolAllowlist, a.disabledTools)
	activeDefs = append(activeDefs, tools.FilterDefs(a.mcpDefs, a.disabledTools)...)
	// Apply sandbox restrictions from recipe.
	if a.sandboxFilesystem == "read_only" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WriteToolName: true,
			tools.EditToolName:  true,
		})
	}
	if a.sandboxNetwork == "none" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WebFetchToolName:  true,
			tools.WebSearchToolName: true,
		})
	}
	// Apply recipe-level tool description overrides (uniform for all models).
	var sumPrompt string
	var recSystemPrompt, recToolGuidance string
	if activeRec := a.store.ActiveRecipe(); activeRec != nil {
		activeDefs = tools.ApplyDescriptions(activeDefs, activeRec.ToolDescriptions)
		sumPrompt = activeRec.SummarizationPrompt
		recSystemPrompt = activeRec.SystemPrompt
		recToolGuidance = activeRec.ToolGuidance
	}
	mcpDispatchers := a.mcpDispatchers
	bashPrefixes := a.bashPrefixes
	contextStrategy := a.contextStrategy
	sandboxFilesystem := a.sandboxFilesystem
	sandboxNetwork := a.sandboxNetwork
	projectRoot := a.projectRoot
	cfg := a.cfg
	seed := a.seed
	sessionID := a.store.SessionID()
	rec := a.store.BaseRecipe()
	cpPath := a.store.CheckpointPath()

	baseCtx, cancelFn := context.WithCancel(context.Background())
	a.cancelRun = cancelFn
	if a.debugLog {
		baseCtx = adapters.WithDebugRequests(baseCtx)
	}

	return a, tea.Batch(promptPrintCmd, func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		// Skip auto-compact when context strategy is "manual" or "off".
		if contextStrategy != "manual" && contextStrategy != "off" {
			compactCtx := prompt.WithSummarizationPrompt(baseCtx, sumPrompt)
			for _, ad := range ads {
				if runner.ShouldAutoCompact(effectiveHistories, ad.ID(), cfg.CompactThreshold) {
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
		runCtx := tools.WithActiveTools(baseCtx, activeDefs)
		runCtx = tools.WithMCPDispatchers(runCtx, mcpDispatchers)
		runCtx = tools.WithBashPrefixes(runCtx, bashPrefixes)
		runCtx = sandbox.WithConfig(runCtx, sandbox.Config{
			Filesystem:  sandboxFilesystem,
			Network:     sandboxNetwork,
			ProjectRoot: projectRoot,
		})
		runCtx = runner.WithRunOptions(runCtx, runner.RunOptions{
			Timeout:          cfg.AgentTimeout,
			CompactThreshold: cfg.CompactThreshold,
			MaxHistoryTurns:  cfg.MaxHistoryTurns,
			MaxSteps:         cfg.MaxSteps,
			CheckpointPath:   cpPath,
		})
		runCtx = tools.WithSystemPromptExtra(runCtx, recSystemPrompt)
		runCtx = tools.WithToolGuidance(runCtx, recToolGuidance)
		if tools.SubagentEnabled {
			runCtx = tools.WithSubagentDispatcher(runCtx, subagent.NewDispatcher(
				ads, cfg, mcpDispatchers,
				func(modelID string, e models.AgentEvent) {
					prog.Send(agentEventMsg{modelID: modelID, event: e})
				},
			))
			runCtx = tools.WithSubagentDepth(runCtx, 0)
		}
		if seed != nil {
			runCtx = tools.WithSeed(runCtx, *seed)
		}
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
				if err := checkpoint.Save(cpPath, *cp); err != nil {
					log.Printf("warning: failed to save checkpoint: %v", err)
				}
			}
		}

		toolNames := make([]string, len(activeDefs))
		for i, d := range activeDefs {
			toolNames[i] = d.Name
		}
		report := output.BuildReport(sessionID, rec, trimmed, responses, collector, toolNames)
		reportPath, err := output.Save(a.store.OutputDir(), report)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save output report: %v\n", err)
		}

		return runCompleteMsg{responses: responses, compactedHistories: compacted, reportPath: reportPath, toolNames: toolNames}
	})
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

	ads := rerunAdapters
	prog := a.prog
	histories := a.store.Histories()
	activeDefs := tools.DefinitionsAllowed(a.toolAllowlist, a.disabledTools)
	activeDefs = append(activeDefs, tools.FilterDefs(a.mcpDefs, a.disabledTools)...)
	if a.sandboxFilesystem == "read_only" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WriteToolName: true,
			tools.EditToolName:  true,
		})
	}
	if a.sandboxNetwork == "none" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WebFetchToolName:  true,
			tools.WebSearchToolName: true,
		})
	}
	// Apply recipe-level tool description overrides (uniform for all models).
	var resumeSumPrompt string
	var resumeSystemPrompt, resumeToolGuidance string
	if resumeRec := a.store.ActiveRecipe(); resumeRec != nil {
		activeDefs = tools.ApplyDescriptions(activeDefs, resumeRec.ToolDescriptions)
		resumeSumPrompt = resumeRec.SummarizationPrompt
		resumeSystemPrompt = resumeRec.SystemPrompt
		resumeToolGuidance = resumeRec.ToolGuidance
	}
	mcpDispatchers := a.mcpDispatchers
	bashPrefixes := a.bashPrefixes
	contextStrategy := a.contextStrategy
	sandboxFilesystem := a.sandboxFilesystem
	sandboxNetwork := a.sandboxNetwork
	projectRoot := a.projectRoot
	cfg := a.cfg
	seed := a.seed
	sessionID := a.store.SessionID()
	rec := a.store.BaseRecipe()
	resumeCPPath := a.store.CheckpointPath()

	baseCtx, cancelFn := context.WithCancel(context.Background())
	a.cancelRun = cancelFn
	if a.debugLog {
		baseCtx = adapters.WithDebugRequests(baseCtx)
	}

	return a, tea.Batch(promptPrintCmd, func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		if contextStrategy != "manual" && contextStrategy != "off" {
			compactCtx := prompt.WithSummarizationPrompt(baseCtx, resumeSumPrompt)
			for _, ad := range ads {
				if runner.ShouldAutoCompact(effectiveHistories, ad.ID(), cfg.CompactThreshold) {
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
		runCtx := tools.WithActiveTools(baseCtx, activeDefs)
		runCtx = tools.WithMCPDispatchers(runCtx, mcpDispatchers)
		runCtx = tools.WithBashPrefixes(runCtx, bashPrefixes)
		runCtx = sandbox.WithConfig(runCtx, sandbox.Config{
			Filesystem:  sandboxFilesystem,
			Network:     sandboxNetwork,
			ProjectRoot: projectRoot,
		})
		runCtx = runner.WithRunOptions(runCtx, runner.RunOptions{
			Timeout:          cfg.AgentTimeout,
			CompactThreshold: cfg.CompactThreshold,
			MaxHistoryTurns:  cfg.MaxHistoryTurns,
			CheckpointPath:   resumeCPPath,
		})
		runCtx = tools.WithSystemPromptExtra(runCtx, resumeSystemPrompt)
		runCtx = tools.WithToolGuidance(runCtx, resumeToolGuidance)
		if tools.SubagentEnabled {
			runCtx = tools.WithSubagentDispatcher(runCtx, subagent.NewDispatcher(
				ads, cfg, mcpDispatchers,
				func(modelID string, e models.AgentEvent) {
					prog.Send(agentEventMsg{modelID: modelID, event: e})
				},
			))
			runCtx = tools.WithSubagentDepth(runCtx, 0)
		}
		if seed != nil {
			runCtx = tools.WithSeed(runCtx, *seed)
		}
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
				if err := checkpoint.Save(resumeCPPath, *cp); err != nil {
					log.Printf("warning: failed to save checkpoint: %v", err)
				}
			}
		}

		// Merge completed responses (from checkpoint) with fresh re-run responses.
		allResponses := slices.Concat(completedResponses, responses)

		toolNames := make([]string, len(activeDefs))
		for i, d := range activeDefs {
			toolNames[i] = d.Name
		}
		report := output.BuildReport(sessionID, rec, userPrompt, allResponses, collector, toolNames)
		reportPath, err := output.Save(a.store.OutputDir(), report)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save output report: %v\n", err)
		}

		return runCompleteMsg{responses: allResponses, compactedHistories: compacted, reportPath: reportPath, toolNames: toolNames}
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
						a.configListItems = buildToolsList(a.toolAllowlist, a.disabledTools)
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
						content = sessRec.SummarizationPrompt
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
	reports := a.store.LoadAllReports()
	if len(reports) == 0 {
		return a.withMessage("No run output to export. Run a prompt first.")
	}

	dir := a.store.OutputDir()
	if args != "" {
		dir = args
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
