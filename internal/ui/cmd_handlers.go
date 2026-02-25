package ui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/tools"
)

func (a App) handlePrompt(prompt string) (tea.Model, tea.Cmd) {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)

	if lower == "/model" || strings.HasPrefix(lower, "/model ") {
		return a.handleModelCommand(strings.TrimSpace(trimmed[len("/model"):]))
	}
	if lower == "/tools" || strings.HasPrefix(lower, "/tools ") {
		return a.handleToolsCommand(strings.TrimSpace(trimmed[len("/tools"):]))
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
	case "/compact":
		return a.handleCompactCmd()
	case "/stats":
		return a.handleStatsCmd()
	case "/totalcost":
		return a.withMessage(fmt.Sprintf("Total session cost: $%.4f", a.totalCostUSD)), nil
	case "/help":
		return a.withMessage(helpText()), nil
	}
	return a.launchRun(trimmed)
}

func (a App) handleVerboseCmd() (tea.Model, tea.Cmd) {
	a.verbose = !a.verbose
	state := "off"
	if a.verbose {
		state = "on"
	}
	return a.withMessage(fmt.Sprintf("Verbose mode %s.", state)), nil
}

func (a App) handleModelsListCmd() (tea.Model, tea.Cmd) {
	active := a.activeAdapters
	if active == nil {
		active = a.adapters
	}
	var ids []string
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

func (a App) handleClearCmd() (tea.Model, tea.Cmd) {
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

func (a App) handleCompactCmd() (tea.Model, tea.Cmd) {
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

func (a App) handleStatsCmd() (tea.Model, tea.Cmd) {
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
			sb.WriteString(fmt.Sprintf("    %s: %s  %.1f%% win  avg %dms%s  (%d runs)\n",
				r.id,
				signals,
				r.s.WinRate,
				int64(r.s.AvgLatencyMS),
				cost,
				r.s.Participations,
			))
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
			sb.WriteString(fmt.Sprintf("    %s: $%.4f\n", id, a.sessionCostPerModel[id]))
		}
		sb.WriteString(fmt.Sprintf("  Total: $%.4f\n", a.totalCostUSD))
	}
	return a.withMessage(strings.TrimRight(sb.String(), "\n")), nil
}

func (a App) launchRun(trimmed string) (tea.Model, tea.Cmd) {
	toRun := a.adapters
	if a.activeAdapters != nil {
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
	activeDefs := tools.ActiveDefinitions(a.disabledTools)
	activeDefs = append(activeDefs, a.mcpDefs...)
	mcpDispatchers := a.mcpDispatchers

	return a, func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		for _, ad := range ads {
			if runner.ShouldAutoCompact(effectiveHistories, ad.ID()) {
				prog.Send(agentEventMsg{modelID: ad.ID(), event: models.AgentEvent{
					Type: "text", Data: "[auto-compacting history…]",
				}})
				effectiveHistories = runner.CompactHistories(
					context.Background(), []models.ModelAdapter{ad},
					effectiveHistories, func(id string, e models.AgentEvent) {
						prog.Send(agentEventMsg{modelID: id, event: e})
					},
				)
				compacted = effectiveHistories
			}
		}
		runCtx := tools.WithActiveTools(context.Background(), activeDefs)
		runCtx = tools.WithMCPDispatchers(runCtx, mcpDispatchers)
		responses := runner.RunAll(
			runCtx, ads, effectiveHistories, trimmed,
			func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			},
			verbose,
		)
		return runCompleteMsg{responses: responses, compactedHistories: compacted}
	}
}

func (a App) handleToolsCommand(args string) (tea.Model, tea.Cmd) {
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
		return a.withMessage("Tools:\n" + strings.Join(lines, "\n")), nil
	}

	// /tools on <name...> or /tools off <name...>
	parts := strings.Fields(lower)
	if len(parts) < 2 || (parts[0] != "on" && parts[0] != "off") {
		return a.withMessage("Usage: /tools  |  /tools off <name...>  |  /tools on <name...>  |  /tools reset"), nil
	}

	action, names := parts[0], parts[1:]
	// Validate all names first.
	validNames := make(map[string]bool, len(tools.Definitions))
	for _, d := range tools.Definitions {
		validNames[d.Name] = true
	}
	for _, n := range names {
		if !validNames[n] {
			var all []string
			for _, d := range tools.Definitions {
				all = append(all, d.Name)
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

func (a App) handleModelCommand(args string) (tea.Model, tea.Cmd) {
	if args == "" {
		a.activeAdapters = nil
		var ids []string
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
func fmtPrice(v float64) string {
	if v >= 1 {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%g", v)
}

// formatAvailableModels formats a ListAvailableModels result for display.
func formatAvailableModels(results []adapters.ProviderModels) string {
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
			qid := pricing.ProviderQualifiedID(r.Provider, id)
			if in, out, ok := pricing.PricingFor(qid); ok {
				fmt.Fprintf(&sb, "\n  %s  (%s in / %s out /1M)", id, fmtPrice(in), fmtPrice(out))
			} else {
				fmt.Fprintf(&sb, "\n  %s", id)
			}
		}
		if n > cap {
			fmt.Fprintf(&sb, "\n  … and %d more", n-cap)
		}
	}
	return sb.String()
}
