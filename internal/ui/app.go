// Package ui implements the bubbletea TUI for Errata.
package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/tools"
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
	kind      string               // "message" | "run"
	text      string               // for "message" items
	prompt    string               // for "run" items
	panels    []*panelState        // live during run (pointer-shared with a.panels), frozen after
	responses []models.ModelResponse // set on run complete; drives RenderDiffs inline
	note      string               // outcome: "Applied: foo.go" / "Skipped." / error
}

// ---- model ----

// App is the bubbletea model.
type App struct {
	adapters       []models.ModelAdapter
	activeAdapters []models.ModelAdapter // nil = use all adapters
	prefPath       string
	sessionID      string

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
}

// New creates the App model.
func New(adapters []models.ModelAdapter, prefPath, histPath, sessionID string) *App {
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

	return &App{
		adapters:              adapters,
		prefPath:              prefPath,
		histPath:              histPath,
		sessionID:             sessionID,
		input:                 ta,
		feedVP:                viewport.New(80, 20),
		panelIdx:              make(map[string]int),
		conversationHistories: h,
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
			if resp.Error != "" {
				p.errMsg = resp.Error
				if runner.IsContextOverflowError(resp.Error) {
					p.errMsg = "context limit reached — use /clear or /compact to reset"
				}
			}
		}

		// Accumulate conversation history for each adapter that returned text.
		// Use panels[i].modelID (the configured adapter ID) as the key, not resp.ModelID,
		// to avoid mismatches from resolved version strings (e.g. Gemini).
		panelIDs := make([]string, len(a.panels))
		for i, p := range a.panels {
			panelIDs[i] = p.modelID
		}
		// If auto-compact ran, use the post-compact state as the base for AppendHistory.
		if msg.compactedHistories != nil {
			a.conversationHistories = msg.compactedHistories
		}
		a.conversationHistories = runner.AppendHistory(a.conversationHistories, panelIDs, msg.responses, a.lastPrompt)
		if err := history.Save(a.histPath, a.conversationHistories); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
		}

		// Store responses on the last feed item so renderFeedContent renders the diff.
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

func (a App) handleIdleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD, tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		var cmd tea.Cmd
		a.feedVP, cmd = a.feedVP.Update(msg)
		return a, cmd

	case tea.KeyEnter:
		if msg.Alt {
			break
		}
		prompt := strings.TrimSpace(a.input.Value())
		a.input.Reset()
		if prompt == "" {
			return a, nil
		}
		return a.handlePrompt(prompt)
	}

	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	return a, cmd
}

func (a App) handlePrompt(prompt string) (tea.Model, tea.Cmd) {
	trimmed := strings.TrimSpace(prompt)
	lower := strings.ToLower(trimmed)

	if lower == "/model" || strings.HasPrefix(lower, "/model ") {
		args := strings.TrimSpace(trimmed[len("/model"):])
		return a.handleModelCommand(args)
	}

	switch lower {
	case "/exit", "/quit":
		return a, tea.Quit
	case "/verbose":
		a.verbose = !a.verbose
		state := "off"
		if a.verbose {
			state = "on"
		}
		return a.withMessage(fmt.Sprintf("Verbose mode %s.", state)), nil
	case "/models":
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
		return a.withMessage("Models: " + strings.Join(ids, ", ") + suffix), nil
	case "/clear":
		a.feed = nil
		a.conversationHistories = nil
		if err := history.Clear(a.histPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not clear history: %v\n", err)
		}
		a.feedVP.Width = a.width
		a.feedVP.Height = a.feedVPHeight()
		a.feedVP.SetContent("")
		return a, nil
	case "/compact":
		toCompact := a.adapters
		if a.activeAdapters != nil {
			toCompact = a.activeAdapters
		}
		histories := a.conversationHistories
		prog := a.prog
		return a.withMessage("Compacting conversation history…"), func() tea.Msg {
			updated := runner.CompactHistories(
				context.Background(),
				toCompact,
				histories,
				func(modelID string, e models.AgentEvent) {
					prog.Send(agentEventMsg{modelID: modelID, event: e})
				},
			)
			return compactCompleteMsg{histories: updated}
		}
	case "/help":
		return a.withMessage(helpText()), nil
	}

	// Launch agents.
	toRun := a.adapters
	if a.activeAdapters != nil {
		toRun = a.activeAdapters
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

	adapters := toRun
	verbose := a.verbose
	prog := a.prog
	histories := a.conversationHistories // read-only in goroutine; written only by main loop

	return a, func() tea.Msg {
		effectiveHistories := histories
		var compacted map[string][]models.ConversationTurn
		for _, ad := range adapters {
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
			context.Background(),
			adapters,
			effectiveHistories,
			trimmed,
			func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			},
			verbose,
		)
		return runCompleteMsg{responses: responses, compactedHistories: compacted}
	}
}

func (a App) handleSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD, tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		var cmd tea.Cmd
		a.feedVP, cmd = a.feedVP.Update(msg)
		return a, cmd

	case tea.KeyEnter:
		choice := strings.TrimSpace(a.selection)
		a.selection = ""
		return a.applySelection(choice)

	case tea.KeyBackspace, tea.KeyDelete:
		if len(a.selection) > 0 {
			a.selection = a.selection[:len(a.selection)-1]
		}
		a.selectionErr = ""

	case tea.KeyRunes:
		a.selection += string(msg.Runes)
		a.selectionErr = ""
	}
	return a, nil
}

func (a App) applySelection(choice string) (tea.Model, tea.Cmd) {
	setNote := func(note string) {
		if len(a.feed) > 0 {
			a.feed[len(a.feed)-1].note = note
		}
	}

	if strings.EqualFold(choice, "s") {
		setNote("Skipped.")
		a.responses = nil
		a.mode = modeIdle
		return a.withFeedRebuilt(true), nil
	}

	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 {
		a.selectionErr = fmt.Sprintf("Invalid choice %q — type a number or 's'.", choice)
		return a, nil
	}

	// Find the n-th OK response (errors are listed but not numbered).
	selIdx := 0
	var selected models.ModelResponse
	found := false
	for _, resp := range a.responses {
		if !resp.OK() {
			continue
		}
		selIdx++
		if selIdx == n {
			selected = resp
			found = true
			break
		}
	}
	if !found {
		a.selectionErr = fmt.Sprintf("Invalid choice %d — type a valid number or 's'.", n)
		return a, nil
	}

	if len(selected.ProposedWrites) == 0 {
		setNote(fmt.Sprintf("Model %s proposed no file writes.", selected.ModelID))
	} else {
		if err := tools.ApplyWrites(selected.ProposedWrites); err != nil {
			setNote(fmt.Sprintf("Error applying writes: %v", err))
		} else {
			var paths []string
			for _, fw := range selected.ProposedWrites {
				paths = append(paths, fw.Path)
			}
			setNote(fmt.Sprintf("Applied: %s", strings.Join(paths, ", ")))
		}
	}

	_ = preferences.Record(a.prefPath, a.lastPrompt, selected.ModelID, a.sessionID, a.responses)

	a.responses = nil
	a.mode = modeIdle
	return a.withFeedRebuilt(true), nil
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
				Render(fmt.Sprintf("  ↑↓/pgup/pgdn  %.0f%%", a.feedVP.ScrollPercent()*100))
			sb.WriteString(hint)
			sb.WriteByte('\n')
		}
		sb.WriteString(a.input.View())
	}

	return sb.String()
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
			var available []string
			for _, ad := range a.adapters {
				available = append(available, ad.ID())
			}
			return a.withMessage(fmt.Sprintf(
				"Unknown model %q. Available: %s", id, strings.Join(available, ", "),
			)), nil
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
	return `Commands:
  /help              Show this message
  /clear             Clear display history and conversation memory
  /compact           Summarise conversation history to free up context
  /verbose           Toggle verbose mode
  /models            List active models
  /model [id...]     Restrict to model(s); bare /model resets to all
  /exit              Exit`
}

// Run starts the bubbletea program and blocks until exit.
func Run(adapters []models.ModelAdapter, prefPath, histPath, sessionID string, warnings []string) error {
	app := New(adapters, prefPath, histPath, sessionID)

	p := tea.NewProgram(app, tea.WithAltScreen())
	app.SetProgram(p)

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	_, err := p.Run()
	return err
}
