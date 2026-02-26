package ui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/sandbox"
	"github.com/suarezc/errata/internal/subagent"
	"github.com/suarezc/errata/internal/tools"
)

func (a App) handlePrompt(prompt string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea value-receiver pattern
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)

	if lower == "/model" || strings.HasPrefix(lower, "/model ") {
		return a.handleModelCommand(strings.TrimSpace(trimmed[len("/model"):]))
	}
	if lower == "/tools" || strings.HasPrefix(lower, "/tools ") {
		return a.handleToolsCommand(strings.TrimSpace(trimmed[len("/tools"):]))
	}
	if lower == "/seed" || strings.HasPrefix(lower, "/seed ") {
		return a.handleSeedCommand(strings.TrimSpace(trimmed[len("/seed"):]))
	}
	if lower == "/subset" || strings.HasPrefix(lower, "/subset ") {
		return a.handleSubsetCommand(strings.TrimSpace(trimmed[len("/subset"):]))
	}
	if lower == "/all" {
		return a.handleAllCommand()
	}
	if lower == "/config" || strings.HasPrefix(lower, "/config ") {
		return a.handleConfigCommand(strings.TrimSpace(trimmed[len("/config"):]))
	}
	if lower == "/set" || strings.HasPrefix(lower, "/set ") {
		return a.handleSetCommand(strings.TrimSpace(trimmed[len("/set"):]))
	}
	if lower == "/remind" || strings.HasPrefix(lower, "/remind ") {
		return a.handleRemindCommand(strings.TrimSpace(trimmed[len("/remind"):]))
	}
	if lower == "/export" || strings.HasPrefix(lower, "/export ") {
		return a.handleExportCommand(strings.TrimSpace(trimmed[len("/export"):]))
	}
	if lower == "/import" || strings.HasPrefix(lower, "/import ") {
		return a.handleImportCommand(strings.TrimSpace(trimmed[len("/import"):]))
	}
	switch lower {
	case "/exit", "/quit":
		return a, tea.Quit
	case "/verbose":
		return a.handleVerboseCmd()
	case "/models":
		return a.handleModelsListCmd()
	case "/clear":
		return a.handleClearCmd()
	case "/wipe":
		return a.handleWipeCmd()
	case "/compact":
		return a.handleCompactCmd()
	case "/resume":
		return a.handleResumeCmd()
	case "/stats":
		return a.handleStatsCmd()
	case "/totalcost":
		return a.withMessage(fmt.Sprintf("Total session cost: $%.4f", a.totalCostUSD)), nil
	case "/help":
		return a.withMessage(helpText()), nil
	}
	// Parse @mentions for transient per-message model targeting.
	mention := ParseMentions(trimmed, a.modelIDCandidates())
	if len(mention.Errors) > 0 {
		return a.withMessage(fmt.Sprintf("No model matching %q in current recipe.", mention.Errors[0])), nil
	}
	if len(mention.ModelIDs) > 0 {
		if mention.Prompt == "" {
			return a.withMessage("No prompt text after @mention(s)."), nil
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
				newAd, err := adapters.NewAdapter(id, a.cfg)
				if err != nil {
					return a.withMessage(fmt.Sprintf("Cannot create adapter for %q: %v", id, err)), nil
				}
				found = newAd
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
	return a.withMessage(fmt.Sprintf("Verbose mode %s.", state)), nil
}

func (a App) handleModelsListCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	active := a.activeAdapters
	if active == nil {
		active = a.adapters
	}
	ids := make([]string, 0, len(active))
	for _, ad := range active {
		ids = append(ids, ad.ID())
	}
	suffix := ""
	if a.activeAdapters != nil {
		suffix = " (filtered)"
	}
	cfg := a.cfg
	updated := a.withMessage("Active" + suffix + ": " + strings.Join(ids, ", ") + "\nFetching available models…")
	return updated, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return listModelsResultMsg{results: adapters.ListAvailableModels(ctx, cfg)}
	}
}

func (a App) handleClearCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	a.feed = nil
	a.feedVP.Width = a.width
	a.feedVP.Height = a.feedVPHeight()
	a.feedVP.SetContent("")
	return a, nil
}

func (a App) handleWipeCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	a.feed = nil
	a.conversationHistories = nil
	if err := history.Clear(a.histPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not clear history: %v\n", err)
	}
	a.feedVP.Width = a.width
	a.feedVP.Height = a.feedVPHeight()
	a.feedVP.SetContent("")
	return a, nil
}

func (a App) handleCompactCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	toCompact := a.adapters
	if a.activeAdapters != nil {
		toCompact = a.activeAdapters
	}
	histories := a.conversationHistories
	prog := a.prog
	return a.withMessage("Compacting conversation history…"), func() tea.Msg {
		updated := runner.CompactHistories(
			context.Background(), toCompact, histories,
			func(modelID string, e models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: e})
			},
		)
		return compactCompleteMsg{histories: updated}
	}
}

func (a App) handleStatsCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	stats := preferences.SummarizeDetailed(a.prefPath)
	var sb strings.Builder
	sb.WriteString("Stats:\n")
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
	if len(a.sessionCostPerModel) > 0 {
		sb.WriteString("  Session cost:\n")
		ids := make([]string, 0, len(a.sessionCostPerModel))
		for id := range a.sessionCostPerModel {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return a.sessionCostPerModel[ids[i]] > a.sessionCostPerModel[ids[j]]
		})
		for _, id := range ids {
			fmt.Fprintf(&sb, "    %s: $%.4f\n", id, a.sessionCostPerModel[id])
		}
		fmt.Fprintf(&sb, "  Total: $%.4f\n", a.totalCostUSD)
	}
	return a.withMessage(strings.TrimRight(sb.String(), "\n")), nil
}

func (a App) launchRun(trimmed string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	return a.launchRunTargeted(trimmed, nil)
}

func (a App) launchRunTargeted(trimmed string, mentionTargets []models.ModelAdapter) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	toRun := a.adapters
	if mentionTargets != nil {
		toRun = mentionTargets
	} else if a.activeAdapters != nil {
		toRun = a.activeAdapters
	}

	// Record in prompt history (deduplicate consecutive identical entries).
	a.historyIdx = -1
	a.historyInputBuf = ""
	if len(a.promptHistory) == 0 || a.promptHistory[0] != trimmed {
		a.promptHistory = append([]string{trimmed}, a.promptHistory...)
		if err := prompthistory.Append(a.promptHistPath, trimmed); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save prompt history: %v\n", err)
		}
	}

	a.lastPrompt = trimmed
	a.mode = modeRunning
	a.panels = nil
	a.panelIdx = make(map[string]int)
	for i, ad := range toRun {
		ps := newPanelState(ad.ID(), i)
		ps.histTokens = runner.EstimateHistoryTokens(a.conversationHistories[ad.ID()])
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
	a = a.withFeedRebuilt(true)

	ads := toRun
	verbose := a.verbose
	prog := a.prog
	histories := a.conversationHistories // read-only in goroutine; written only by main loop
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
	mcpDispatchers := a.mcpDispatchers
	bashPrefixes := a.bashPrefixes
	contextStrategy := a.contextStrategy
	sandboxFilesystem := a.sandboxFilesystem
	sandboxNetwork := a.sandboxNetwork
	projectRoot := a.projectRoot
	cfg := a.cfg
	seed := a.seed
	sessionID := a.sessionID
	rec := a.recipe

	baseCtx, cancelFn := context.WithCancel(context.Background())
	a.cancelRun = cancelFn

	return a, func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		// Skip auto-compact when context strategy is "manual" or "off".
		if contextStrategy != "manual" && contextStrategy != "off" {
			for _, ad := range ads {
				if runner.ShouldAutoCompact(effectiveHistories, ad.ID(), cfg.CompactThreshold) {
					prog.Send(agentEventMsg{modelID: ad.ID(), event: models.AgentEvent{
						Type: "text", Data: "[auto-compacting history…]",
					}})
					effectiveHistories = runner.CompactHistories(
						baseCtx, []models.ModelAdapter{ad},
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
			CheckpointPath:   checkpoint.DefaultPath,
		})
		runCtx = tools.WithSubagentDispatcher(runCtx, subagent.NewDispatcher(
			ads, cfg, mcpDispatchers,
			func(modelID string, e models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: e})
			},
		))
		runCtx = tools.WithSubagentDepth(runCtx, 0)
		if seed != nil {
			runCtx = tools.WithSeed(runCtx, *seed)
		}
		collector := output.NewCollector()
		responses := runner.RunAll(
			runCtx, ads, effectiveHistories, trimmed,
			collector.WrapOnEvent(func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			}),
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
				_ = checkpoint.Save(checkpoint.DefaultPath, *cp)
			}
		}

		toolNames := make([]string, len(activeDefs))
		for i, d := range activeDefs {
			toolNames[i] = d.Name
		}
		report := output.BuildReport(sessionID, rec, trimmed, responses, collector, toolNames)
		if _, err := output.Save(output.DefaultDir, report); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save output report: %v\n", err)
		}

		return runCompleteMsg{responses: responses, compactedHistories: compacted, report: report}
	}
}

func (a App) handleResumeCmd() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	cp, err := checkpoint.Load(checkpoint.DefaultPath)
	if err != nil {
		return a.withMessage(fmt.Sprintf("Error loading checkpoint: %v", err)), nil
	}
	if cp == nil {
		return a.withMessage("No interrupted run to resume."), nil
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
		_ = checkpoint.Clear(checkpoint.DefaultPath)
		return a.withMessage("All models from the last run completed. No resume needed."), nil
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
				return a.withMessage(fmt.Sprintf("Cannot create adapter for %q: %v", id, err)), nil
			}
			found = newAd
		}
		rerunAdapters = append(rerunAdapters, found)
	}

	_ = checkpoint.Clear(checkpoint.DefaultPath)
	return a.launchResumeRun(cp.Prompt, rerunAdapters, completedResponses, cp.Verbose)
}

func (a App) launchResumeRun(prompt string, rerunAdapters []models.ModelAdapter, completedResponses []models.ModelResponse, verbose bool) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	a.lastPrompt = prompt
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
		ps.histTokens = runner.EstimateHistoryTokens(a.conversationHistories[ad.ID()])
		a.panels = append(a.panels, ps)
		a.panelIdx[ad.ID()] = idx
	}

	a.feed = append(a.feed, feedItem{
		kind:   "run",
		prompt: "[resume] " + prompt,
		panels: a.panels,
	})
	a = a.withFeedRebuilt(true)

	ads := rerunAdapters
	prog := a.prog
	histories := a.conversationHistories
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
	mcpDispatchers := a.mcpDispatchers
	bashPrefixes := a.bashPrefixes
	contextStrategy := a.contextStrategy
	sandboxFilesystem := a.sandboxFilesystem
	sandboxNetwork := a.sandboxNetwork
	projectRoot := a.projectRoot
	cfg := a.cfg
	seed := a.seed
	sessionID := a.sessionID
	rec := a.recipe

	baseCtx, cancelFn := context.WithCancel(context.Background())
	a.cancelRun = cancelFn

	return a, func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		if contextStrategy != "manual" && contextStrategy != "off" {
			for _, ad := range ads {
				if runner.ShouldAutoCompact(effectiveHistories, ad.ID(), cfg.CompactThreshold) {
					prog.Send(agentEventMsg{modelID: ad.ID(), event: models.AgentEvent{
						Type: "text", Data: "[auto-compacting history…]",
					}})
					effectiveHistories = runner.CompactHistories(
						baseCtx, []models.ModelAdapter{ad},
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
			CheckpointPath:   checkpoint.DefaultPath,
		})
		runCtx = tools.WithSubagentDispatcher(runCtx, subagent.NewDispatcher(
			ads, cfg, mcpDispatchers,
			func(modelID string, e models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: e})
			},
		))
		runCtx = tools.WithSubagentDepth(runCtx, 0)
		if seed != nil {
			runCtx = tools.WithSeed(runCtx, *seed)
		}
		collector := output.NewCollector()
		responses := runner.RunAll(
			runCtx, ads, effectiveHistories, prompt,
			collector.WrapOnEvent(func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			}),
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
			if cp := checkpoint.Build(prompt, allIDs, allResp, verbose); cp != nil {
				_ = checkpoint.Save(checkpoint.DefaultPath, *cp)
			}
		}

		// Merge completed responses (from checkpoint) with fresh re-run responses.
		allResponses := slices.Concat(completedResponses, responses)

		toolNames := make([]string, len(activeDefs))
		for i, d := range activeDefs {
			toolNames[i] = d.Name
		}
		report := output.BuildReport(sessionID, rec, prompt, allResponses, collector, toolNames)
		if _, err := output.Save(output.DefaultDir, report); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save output report: %v\n", err)
		}

		return runCompleteMsg{responses: allResponses, compactedHistories: compacted, report: report}
	}
}

func (a App) handleToolsCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	lower := strings.ToLower(strings.TrimSpace(args))

	// /tools reset — re-enable all tools
	if lower == "reset" {
		a.disabledTools = nil
		if err := tools.SaveDisabledTools(a.toolStatePath, nil); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save tool state: %v\n", err)
		}
		return a.withMessage("All tools enabled."), nil
	}

	// /tools (bare) — list current state
	if lower == "" {
		var lines []string
		for _, d := range tools.Definitions {
			state := "on "
			if a.disabledTools[d.Name] {
				state = "off"
			}
			lines = append(lines, fmt.Sprintf("  [%s] %s", state, d.Name))
		}
		for _, d := range a.mcpDefs {
			state := "on "
			if a.disabledTools[d.Name] {
				state = "off"
			}
			lines = append(lines, fmt.Sprintf("  [%s] %s  (mcp)", state, d.Name))
		}
		return a.withMessage("Tools:\n" + strings.Join(lines, "\n")), nil
	}

	// /tools on <name...> or /tools off <name...>
	parts := strings.Fields(lower)
	if len(parts) < 2 || (parts[0] != "on" && parts[0] != "off") {
		return a.withMessage("Usage: /tools  |  /tools off <name...>  |  /tools on <name...>  |  /tools reset"), nil
	}

	action, names := parts[0], parts[1:]
	// Validate all names against both built-in and MCP tools.
	validNames := make(map[string]bool, len(tools.Definitions)+len(a.mcpDefs))
	for _, d := range tools.Definitions {
		validNames[d.Name] = true
	}
	for _, d := range a.mcpDefs {
		validNames[d.Name] = true
	}
	for _, n := range names {
		if !validNames[n] {
			var all []string
			for _, d := range tools.Definitions {
				all = append(all, d.Name)
			}
			for _, d := range a.mcpDefs {
				all = append(all, d.Name+" (mcp)")
			}
			return a.withMessage(fmt.Sprintf("Unknown tool %q. Available: %s", n, strings.Join(all, ", "))), nil
		}
	}

	if a.disabledTools == nil {
		a.disabledTools = make(map[string]bool)
	}
	for _, n := range names {
		if action == "off" {
			a.disabledTools[n] = true
		} else {
			delete(a.disabledTools, n)
		}
	}
	if err := tools.SaveDisabledTools(a.toolStatePath, a.disabledTools); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save tool state: %v\n", err)
	}
	// Return the updated list.
	return a.handleToolsCommand("")
}

func (a App) handleModelCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if args == "" {
		a.activeAdapters = nil
		ids := make([]string, 0, len(a.adapters))
		for _, ad := range a.adapters {
			ids = append(ids, ad.ID())
		}
		return a.withMessage("Active models: all — " + strings.Join(ids, ", ")), nil
	}

	requested := strings.Fields(args)
	var selected []models.ModelAdapter
	for _, id := range requested {
		var found models.ModelAdapter
		for _, ad := range a.adapters {
			if ad.ID() == id {
				found = ad
				break
			}
		}
		if found == nil {
			newAdapter, err := adapters.NewAdapter(id, a.cfg)
			if err != nil {
				var available []string
				for _, ad := range a.adapters {
					available = append(available, ad.ID())
				}
				return a.withMessage(fmt.Sprintf(
					"Unknown model %q. Available: %s", id, strings.Join(available, ", "),
				)), nil
			}
			found = newAdapter
		}
		selected = append(selected, found)
	}

	a.activeAdapters = selected
	var ids []string
	for _, ad := range selected {
		ids = append(ids, ad.ID())
	}
	return a.withMessage("Active models: " + strings.Join(ids, ", ")), nil
}

func (a App) handleConfigCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if a.sessionRecipe == nil {
		a.sessionRecipe = cloneRecipe(a.recipe)
	}
	a.configSections = buildConfigSections(a.sessionRecipe, a.adapters, a.disabledTools)
	a.configOverlayActive = true
	a.configSelectedIdx = 0
	a.configExpandedIdx = -1

	if args != "" {
		lowerArgs := strings.ToLower(args)
		if lowerArgs == "reset" {
			a.sessionRecipe = cloneRecipe(a.recipe)
			a.recipeModified = false
			a.applySessionRecipe()
			a.configOverlayActive = false
			return a.withMessage("Configuration reset to recipe defaults."), nil
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
						a.configListItems = buildModelsList(a.sessionRecipe, a.adapters, a.activeAdapters)
					case "tools":
						a.configListItems = buildToolsList(a.disabledTools)
					case "mcp-servers":
						var items []listItem
						for _, s := range a.sessionRecipe.MCPServers {
							items = append(items, listItem{Label: s.Name + ": " + s.Command, Active: true})
						}
						a.configListItems = items
					}
					a.configListCursor = 0
				case "scalar":
					a.configScalarFields = buildScalarFields(sec.Name, a.sessionRecipe)
					a.configScalarCursor = 0
					a.configEditBuf = ""
				case "text":
					var content string
					switch sec.Name {
					case "system-prompt":
						content = a.sessionRecipe.SystemPrompt
					case "context-summarization":
						content = a.sessionRecipe.SummarizationPrompt
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

func (a App) handleSetCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if args == "" {
		return a.withMessage("Usage: /set <path> [value]"), nil
	}
	if a.sessionRecipe == nil {
		a.sessionRecipe = cloneRecipe(a.recipe)
	}

	parts := strings.SplitN(args, " ", 2)
	path := parts[0]
	if len(parts) == 1 {
		// Query mode: show current value.
		v := getConfigValue(a.sessionRecipe, path)
		return a.withMessage(fmt.Sprintf("%s = %s", path, v)), nil
	}

	value := parts[1]
	err := setConfigValue(a.sessionRecipe, path, value)
	if err != nil {
		return a.withMessage(fmt.Sprintf("Error: %v", err)), nil
	}
	a.recipeModified = true
	a.applySessionRecipe()
	return a.withMessage(fmt.Sprintf("Set %s = %s", path, value)), nil
}

func (a App) handleRemindCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if a.reminderState == nil {
		return a.withMessage("No reminders configured in recipe."), nil
	}
	if args == "" {
		// Bare /remind: list available reminders.
		names := a.reminderState.ReminderNames()
		if len(names) == 0 {
			return a.withMessage("No reminders configured."), nil
		}
		return a.withMessage("Available reminders:\n  " + strings.Join(names, "\n  ")), nil
	}
	// Fire a specific reminder by name.
	r, ok := a.reminderState.FireManual(args)
	if !ok {
		return a.withMessage(fmt.Sprintf("Reminder %q not found. Use /remind to list available.", args)), nil
	}
	return a.withMessage(fmt.Sprintf("[reminder: %s fired]\n%s", r.Name, r.Content)), nil
}

func (a App) handleSeedCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if args == "" {
		a.seed = nil
		return a.withMessage("Seed cleared."), nil
	}
	n, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		return a.withMessage(fmt.Sprintf("Invalid seed %q — must be an integer.", args)), nil
	}
	a.seed = &n
	return a.withMessage(fmt.Sprintf("Seed set to %d.", n)), nil
}

func (a App) handleSubsetCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if args == "" {
		// Bare /subset: show current targeting state.
		if a.activeAdapters == nil {
			return a.withMessage("Model targeting: all models active"), nil
		}
		var ids []string
		for _, ad := range a.activeAdapters {
			ids = append(ids, ad.ID())
		}
		return a.withMessage("Model targeting: " + strings.Join(ids, ", ")), nil
	}
	// With args: same validation and resolution as /model.
	return a.handleModelCommand(args)
}

func (a App) handleAllCommand() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	return a.handleModelCommand("")
}

func helpText() string {
	var sb strings.Builder
	sb.WriteString("Commands:")
	for _, c := range commands.All {
		name := c.Name
		if c.Name == "/model" {
			name = "/model [id...]"
		}
		fmt.Fprintf(&sb, "\n  %-20s%s", name, c.Desc)
	}
	return sb.String()
}

// fmtPrice formats a per-million-token USD price compactly.
// Always shows at least 2 decimal places ($0.80, not $0.8) and extends
// precision for sub-cent values ($0.075).
func fmtPrice(v float64) string {
	if v >= 1 {
		return fmt.Sprintf("$%.2f", v)
	}
	// Sub-dollar: use 4 decimal places, then trim trailing zeros
	// keeping at least 2 decimal places for consistency.
	s := fmt.Sprintf("%.4f", v)
	dot := strings.Index(s, ".")
	for len(s) > dot+3 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	return "$" + s
}

// formatAvailableModels formats a ListAvailableModels result for display.
// If activeSet is non-nil, models in the set are marked with *.
func formatAvailableModels(results []adapters.ProviderModels, activeSet map[string]bool) string {
	if len(results) == 0 {
		return "No provider API keys configured."
	}
	var sb strings.Builder
	sb.WriteString("Available models:")
	for _, r := range results {
		sb.WriteString("\n\n")
		if r.Err != nil {
			fmt.Fprintf(&sb, "%s — error: %v", r.Provider, r.Err)
			continue
		}
		n := len(r.Models)
		var header string
		if r.TotalCount > n {
			header = fmt.Sprintf("%s (%d of %d, chat only)", r.Provider, n, r.TotalCount)
		} else {
			header = fmt.Sprintf("%s (%d)", r.Provider, n)
		}
		sb.WriteString(header + ":")
		cap := adapters.ModelListCap
		shown := r.Models
		if n > cap {
			shown = r.Models[:cap]
		}
		for _, id := range shown {
			marker := ""
			if activeSet != nil && activeSet[id] {
				marker = " *"
			}
			qid := pricing.ProviderQualifiedID(r.Provider, id)
			if in, out, ok := pricing.PricingFor(qid); ok {
				fmt.Fprintf(&sb, "\n  %s%s  (%s in / %s out /1M)", id, marker, fmtPrice(in), fmtPrice(out))
			} else {
				fmt.Fprintf(&sb, "\n  %s%s", id, marker)
			}
		}
		if n > cap {
			fmt.Fprintf(&sb, "\n  … and %d more", n-cap)
		}
	}
	return sb.String()
}

// ── export/import handlers ──────────────────────────────────────────────────

func (a App) handleExportCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	parts := strings.SplitN(args, " ", 2)
	sub := strings.ToLower(parts[0])

	switch sub {
	case "recipe":
		return a.handleExportRecipe(parts)
	case "output":
		return a.handleExportOutput(parts)
	default:
		return a.withMessage("Usage: /export recipe [path] | /export output [path]"), nil
	}
}

func (a App) handleExportRecipe(parts []string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	rec := a.sessionRecipe
	if rec == nil {
		rec = a.recipe
	}
	if rec == nil {
		return a.withMessage("No recipe to export."), nil
	}

	path := "recipe_export.md"
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		path = strings.TrimSpace(parts[1])
	}

	md := rec.MarshalMarkdown()
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		return a.withMessage(fmt.Sprintf("Export failed: %v", err)), nil
	}
	return a.withMessage(fmt.Sprintf("Recipe exported to %s", path)), nil
}

func (a App) handleExportOutput(parts []string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if a.lastReport == nil {
		return a.withMessage("No run output to export. Run a prompt first."), nil
	}

	dir := output.DefaultDir
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		dir = strings.TrimSpace(parts[1])
	}

	path, err := output.Save(dir, a.lastReport)
	if err != nil {
		return a.withMessage(fmt.Sprintf("Export failed: %v", err)), nil
	}
	return a.withMessage(fmt.Sprintf("Output exported to %s", path)), nil
}

func (a App) handleImportCommand(args string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	parts := strings.SplitN(args, " ", 2)
	sub := strings.ToLower(parts[0])

	switch sub {
	case "recipe":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			return a.withMessage("Usage: /import recipe <path>"), nil
		}
		return a.handleImportRecipe(strings.TrimSpace(parts[1]))
	default:
		return a.withMessage("Usage: /import recipe <path>"), nil
	}
}

func (a App) handleImportRecipe(path string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	rec, err := recipe.Parse(path)
	if err != nil {
		return a.withMessage(fmt.Sprintf("Import failed: %v", err)), nil
	}

	a.sessionRecipe = cloneRecipe(rec)
	a.recipeModified = true
	a.applySessionRecipe()

	name := rec.Name
	if name == "" {
		name = path
	}
	return a.withMessage(fmt.Sprintf("Imported recipe %q (%d models, %d tools)",
		name,
		len(rec.Models),
		func() int {
			if rec.Tools == nil {
				return 0
			}
			return len(rec.Tools.Allowlist)
		}(),
	)), nil
}
