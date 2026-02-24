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
)

func (a App) handlePrompt(prompt string) (tea.Model, tea.Cmd) {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)

	if lower == "/model" || strings.HasPrefix(lower, "/model ") {
		return a.handleModelCommand(strings.TrimSpace(trimmed[len("/model"):]))
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
	tally := preferences.Summarize(a.prefPath)
	var sb strings.Builder
	sb.WriteString("Stats:\n")
	if len(tally) == 0 {
		sb.WriteString("  No preference data yet.\n")
	} else {
		sb.WriteString("  Preference wins:\n")
		type kv struct {
			id   string
			wins int
		}
		kvs := make([]kv, 0, len(tally))
		for id, wins := range tally {
			kvs = append(kvs, kv{id, wins})
		}
		sort.Slice(kvs, func(i, j int) bool { return kvs[i].wins > kvs[j].wins })
		for _, e := range kvs {
			plural := "s"
			if e.wins == 1 {
				plural = ""
			}
			sb.WriteString(fmt.Sprintf("    %s: %d win%s\n", e.id, e.wins, plural))
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
		responses := runner.RunAll(
			context.Background(), ads, effectiveHistories, trimmed,
			func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			},
			verbose,
		)
		return runCompleteMsg{responses: responses, compactedHistories: compacted}
	}
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
