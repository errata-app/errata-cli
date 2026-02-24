// Package ui implements the bubbletea TUI for Errata.
package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/internal/runner"
)

// ---- message types ----

type agentEventMsg struct {
	modelID string
	event   models.AgentEvent
}

type runCompleteMsg struct {
	responses          []models.ModelResponse
	compactedHistories map[string][]models.ConversationTurn // non-nil if auto-compact ran
}

type compactCompleteMsg struct {
	histories map[string][]models.ConversationTurn
}

type listModelsResultMsg struct {
	results []adapters.ProviderModels
}

// ---- app modes ----

type mode int

const (
	modeIdle      mode = iota
	modeRunning        // agents running, panels live
	modeSelecting      // diff shown in feed, awaiting selection
)

// ---- feed item ----

// feedItem is one persistent entry in the conversation feed.
type feedItem struct {
	kind      string                 // "message" | "run"
	text      string                 // for "message" items
	prompt    string                 // for "run" items
	panels    []*panelState          // live during run (pointer-shared with a.panels), frozen after
	responses []models.ModelResponse // set on run complete; drives RenderDiffs inline
	note      string                 // outcome: "Applied: foo.go" / "Skipped." / error
}

// ---- model ----

// App is the bubbletea model.
type App struct {
	adapters       []models.ModelAdapter
	activeAdapters []models.ModelAdapter // nil = use all adapters
	prefPath       string
	sessionID      string
	cfg            config.Config

	mode    mode
	verbose bool
	width   int
	height  int

	// input
	input textarea.Model

	// persistent conversation feed
	feed   []feedItem
	feedVP viewport.Model

	// current run
	panels   []*panelState
	panelIdx map[string]int
	prog     *tea.Program

	// selecting
	responses    []models.ModelResponse
	selection    string
	selectionErr string
	lastPrompt   string

	// per-model conversation history; keyed by adapter ID
	conversationHistories map[string][]models.ConversationTurn
	histPath              string

	// prompt history (Up-arrow cycling and Ctrl-R search)
	promptHistory   []string // newest-first; loaded from disk + this session
	historyIdx      int      // -1 = not navigating; 0..N-1 = position in promptHistory
	historyInputBuf string   // typed text saved when navigation starts
	promptHistPath  string

	// ctrl-r reverse search
	searchActive    bool
	searchQuery     string
	searchResultIdx int

	// cumulative cost across all runs this session
	totalCostUSD        float64
	sessionCostPerModel map[string]float64 // per-model cumulative cost this session
}

// New creates the App model.
func New(adapters []models.ModelAdapter, prefPath, histPath, promptHistPath, sessionID string, cfg config.Config) *App {
	ta := textarea.New()
	ta.Placeholder = "Enter a prompt…"
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.CharLimit = 0
	ta.ShowLineNumbers = false

	h, err := history.Load(histPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load history: %v\n", err)
	}

	ph, err := prompthistory.Load(promptHistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load prompt history: %v\n", err)
	}

	return &App{
		adapters:              adapters,
		prefPath:              prefPath,
		histPath:              histPath,
		promptHistPath:        promptHistPath,
		promptHistory:         ph,
		historyIdx:            -1,
		sessionID:             sessionID,
		input:                 ta,
		feedVP:                viewport.New(80, 20),
		panelIdx:              make(map[string]int),
		conversationHistories: h,
		sessionCostPerModel:   make(map[string]float64),
		cfg:                   cfg,
	}
}

// SetProgram wires up the tea.Program reference so goroutines can send messages.
func (a *App) SetProgram(p *tea.Program) { a.prog = p }

// feedVPHeight returns the number of lines the feed viewport should occupy.
func (a App) feedVPHeight() int {
	const headerLines = 2 // header text + blank line
	const sepLine = 1     // blank line between viewport and footer
	var footerLines int
	switch a.mode {
	case modeIdle:
		footerLines = 3 // textarea SetHeight(3)
		if a.searchActive {
			footerLines++ // search bar line
		}
	case modeRunning:
		footerLines = 1 // "  running…"
	case modeSelecting:
		// "Select a response to apply:\n" + N entries + "  s  Skip\n" + "\nchoice> sel"
		footerLines = len(a.responses) + 4
		if footerLines < 4 {
			footerLines = 4
		}
	}
	h := a.height - headerLines - sepLine - footerLines
	if h < 3 {
		h = 3
	}
	return h
}

// renderFeedContent builds the viewport content string from all feed items.
func (a App) renderFeedContent() string {
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	noteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))

	var sb strings.Builder
	for _, item := range a.feed {
		switch item.kind {
		case "message":
			for _, line := range strings.Split(item.text, "\n") {
				sb.WriteString(msgStyle.Render("  " + line))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
		case "run":
			sb.WriteString(promptStyle.Render("> " + item.prompt))
			sb.WriteByte('\n')
			if len(item.panels) > 0 {
				sb.WriteString(renderPanelRow(item.panels, a.width))
				sb.WriteByte('\n')
			}
			if item.responses != nil {
				d := RenderDiffs(item.responses)
				if d != "" {
					sb.WriteString(d)
				}
			}
			if item.note != "" {
				sb.WriteString(noteStyle.Render("  " + item.note))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// withFeedRebuilt resizes and refreshes the feed viewport. Returns updated App.
func (a App) withFeedRebuilt(gotoBottom bool) App {
	a.feedVP.Width = a.width
	a.feedVP.Height = a.feedVPHeight()
	a.feedVP.SetContent(a.renderFeedContent())
	if gotoBottom {
		a.feedVP.GotoBottom()
	}
	return a
}

// withMessage appends a system message to the feed and rebuilds the viewport.
func (a App) withMessage(text string) App {
	a.feed = append(a.feed, feedItem{kind: "message", text: text})
	return a.withFeedRebuilt(true)
}

func (a App) Init() tea.Cmd {
	return textarea.Blink
}

// ---- update ----

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.input.SetWidth(msg.Width - 4)
		atBottom := a.feedVP.AtBottom()
		return a.withFeedRebuilt(atBottom), nil

	case tea.KeyMsg:
		switch a.mode {
		case modeIdle:
			return a.handleIdleKey(msg)
		case modeSelecting:
			return a.handleSelectKey(msg)
		}
		// modeRunning: ignore all key input
		return a, nil

	case agentEventMsg:
		if idx, ok := a.panelIdx[msg.modelID]; ok {
			a.panels[idx].addEvent(msg.event)
		}
		a.feedVP.Width = a.width
		a.feedVP.Height = a.feedVPHeight()
		a.feedVP.SetContent(a.renderFeedContent())
		a.feedVP.GotoBottom()
		return a, nil

	case listModelsResultMsg:
		return a.withMessage(formatAvailableModels(msg.results)), nil

	case compactCompleteMsg:
		a.conversationHistories = msg.histories
		if err := history.Save(a.histPath, a.conversationHistories); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
		}
		return a.withMessage("History compacted."), nil

	case runCompleteMsg:
		// Mark panels done. runner.RunAll preserves adapter order, so results[i] == panels[i].
		for i, resp := range msg.responses {
			if i >= len(a.panels) {
				break
			}
			p := a.panels[i]
			p.done = true
			p.latencyMS = resp.LatencyMS
			p.inputTokens = resp.InputTokens
			p.outputTokens = resp.OutputTokens
			p.costUSD = resp.CostUSD
			a.totalCostUSD += resp.CostUSD
			a.sessionCostPerModel[resp.ModelID] += resp.CostUSD
			if resp.Error != "" {
				p.errMsg = resp.Error
				if runner.IsContextOverflowError(resp.Error) {
					p.errMsg = "context limit reached — use /clear or /compact to reset"
				}
			}
		}

		// Accumulate conversation history for each adapter that returned text.
		panelIDs := make([]string, len(a.panels))
		for i, p := range a.panels {
			panelIDs[i] = p.modelID
		}
		if msg.compactedHistories != nil {
			a.conversationHistories = msg.compactedHistories
		}
		a.conversationHistories = runner.AppendHistory(a.conversationHistories, panelIDs, msg.responses, a.lastPrompt)
		if err := history.Save(a.histPath, a.conversationHistories); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
		}

		// Sort by latency ascending (fastest first) for display. Must happen after
		// panel-stat assignment and AppendHistory, which rely on the original index order.
		sort.SliceStable(msg.responses, func(i, j int) bool {
			return msg.responses[i].LatencyMS < msg.responses[j].LatencyMS
		})

		if len(a.feed) > 0 {
			a.feed[len(a.feed)-1].responses = msg.responses
		}

		hasWrites := false
		for _, resp := range msg.responses {
			if len(resp.ProposedWrites) > 0 {
				hasWrites = true
				break
			}
		}

		if !hasWrites {
			a.mode = modeIdle
			return a.withFeedRebuilt(true), nil
		}

		a.responses = msg.responses
		a.mode = modeSelecting
		a.selection = ""
		a.selectionErr = ""
		return a.withFeedRebuilt(true), nil
	}

	// Pass remaining events to textarea in idle mode.
	if a.mode == modeIdle {
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		return a, cmd
	}
	return a, nil
}

// ---- view ----

func (a App) View() string {
	var sb strings.Builder

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	sb.WriteString(headerStyle.Render("  Errata  A/B testing tool for agentic AI models"))
	sb.WriteString("\n\n")

	sb.WriteString(a.feedVP.View())
	sb.WriteByte('\n')

	switch a.mode {
	case modeSelecting:
		if !a.feedVP.AtBottom() {
			hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).
				Render(fmt.Sprintf("  ↑↓/pgup/pgdn  %.0f%%", a.feedVP.ScrollPercent()*100))
			sb.WriteString(hint)
			sb.WriteByte('\n')
		}
		sb.WriteString(RenderSelectionMenu(a.responses))
		if a.selectionErr != "" {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000")).
				Render("  " + a.selectionErr))
			sb.WriteByte('\n')
		}
		sb.WriteString("\nchoice> ")
		sb.WriteString(a.selection)

	case modeRunning:
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).
			Render("  running…"))

	case modeIdle:
		if !a.feedVP.AtBottom() {
			hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).
				Render(fmt.Sprintf("  ↑/pgup/pgdn  %.0f%%", a.feedVP.ScrollPercent()*100))
			sb.WriteString(hint)
			sb.WriteByte('\n')
		}
		if a.searchActive {
			query := a.searchQuery
			result := a.currentSearchResult()
			preview := "(no match)"
			if result != "" {
				preview = result
				if runes := []rune(preview); len(runes) > 60 {
					preview = string(runes[:60]) + "…"
				}
			}
			searchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF"))
			sb.WriteString(searchStyle.Render(
				fmt.Sprintf("  (reverse-i-search: %q): %s", query, preview)))
			sb.WriteByte('\n')
		}
		sb.WriteString(a.input.View())
		if val := a.input.Value(); len(val) > 0 && val[0] == '/' {
			prefix := strings.ToLower(strings.SplitN(val, " ", 2)[0])
			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF"))
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
			for _, c := range commands.All {
				if strings.HasPrefix(c.Name, prefix) {
					sb.WriteByte('\n')
					sb.WriteString(nameStyle.Render(fmt.Sprintf("  %-12s", c.Name)))
					sb.WriteString(descStyle.Render("  " + c.Desc))
				}
			}
		}
	}

	return sb.String()
}

// Run starts the bubbletea program and blocks until exit.
func Run(adapters []models.ModelAdapter, prefPath, histPath, promptHistPath, sessionID string, cfg config.Config, warnings []string) error {
	app := New(adapters, prefPath, histPath, promptHistPath, sessionID, cfg)

	p := tea.NewProgram(app, tea.WithAltScreen())
	app.SetProgram(p)

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	_, err := p.Run()
	return err
}
