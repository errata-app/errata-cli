// Package ui implements the bubbletea TUI for Errata.
package ui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/prompthistory"
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

		// Sort by latency ascending (fastest first) for display. Must happen after
		// panel-stat assignment and AppendHistory, which rely on the original index order.
		sort.SliceStable(msg.responses, func(i, j int) bool {
			return msg.responses[i].LatencyMS < msg.responses[j].LatencyMS
		})

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
	// Search mode captures all keystrokes.
	if a.searchActive {
		return a.handleSearchKey(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlD, tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyCtrlR:
		a.searchActive = true
		a.searchQuery = ""
		a.searchResultIdx = 0
		return a.withFeedRebuilt(false), nil

	case tea.KeyUp:
		if a.input.Line() == 0 {
			return a.historyBack()
		}
		// cursor on line > 0: fall through to textarea (cursor up in multiline)

	case tea.KeyDown:
		if a.historyIdx >= 0 {
			return a.historyForward()
		}
		var cmd tea.Cmd
		a.feedVP, cmd = a.feedVP.Update(msg)
		return a, cmd

	case tea.KeyPgUp, tea.KeyPgDown:
		var cmd tea.Cmd
		a.feedVP, cmd = a.feedVP.Update(msg)
		return a, cmd

	case tea.KeyEnter:
		if msg.Alt {
			break
		}
		prompt := strings.TrimSpace(a.input.Value())
		a.input.Reset()
		a.historyIdx = -1
		a.historyInputBuf = ""
		if prompt == "" {
			return a, nil
		}
		return a.handlePrompt(prompt)

	case tea.KeyTab:
		val := a.input.Value()
		if len(val) > 0 && val[0] == '/' {
			prefix := strings.ToLower(strings.SplitN(val, " ", 2)[0])
			for _, c := range commands.All {
				if strings.HasPrefix(c.Name, prefix) {
					a.input.SetValue(c.Name + " ")
					a.input.CursorEnd()
					return a, nil
				}
			}
		}
	}

	// For any other key: if currently navigating history, exit navigation
	// (user is editing — standard shell behaviour).
	if a.historyIdx >= 0 {
		a.historyIdx = -1
		a.historyInputBuf = ""
	}

	var cmd tea.Cmd
	a.input, cmd = a.input.Update(msg)
	return a, cmd
}

// historyBack moves one step backward (older) through prompt history.
func (a App) historyBack() (tea.Model, tea.Cmd) {
	if len(a.promptHistory) == 0 {
		return a, nil
	}
	if a.historyIdx == -1 {
		a.historyInputBuf = a.input.Value()
		a.historyIdx = 0
	} else if a.historyIdx < len(a.promptHistory)-1 {
		a.historyIdx++
	} else {
		return a, nil // already at oldest entry
	}
	a.input.SetValue(a.promptHistory[a.historyIdx])
	a.input.CursorEnd()
	return a, nil
}

// historyForward moves one step forward (newer) through prompt history,
// restoring the saved input buffer when the end is reached.
func (a App) historyForward() (tea.Model, tea.Cmd) {
	if a.historyIdx == -1 {
		return a, nil
	}
	if a.historyIdx == 0 {
		a.historyIdx = -1
		a.input.SetValue(a.historyInputBuf)
		a.input.CursorEnd()
		a.historyInputBuf = ""
	} else {
		a.historyIdx--
		a.input.SetValue(a.promptHistory[a.historyIdx])
		a.input.CursorEnd()
	}
	return a, nil
}

// searchResults returns prompts matching searchQuery, newest-first.
// An empty query returns the full history.
func (a App) searchResults() []string {
	if a.searchQuery == "" {
		return a.promptHistory
	}
	q := strings.ToLower(a.searchQuery)
	var out []string
	for _, p := range a.promptHistory {
		if strings.Contains(strings.ToLower(p), q) {
			out = append(out, p)
		}
	}
	return out
}

// currentSearchResult returns the entry at searchResultIdx, or "" if none.
func (a App) currentSearchResult() string {
	r := a.searchResults()
	if len(r) == 0 || a.searchResultIdx >= len(r) {
		return ""
	}
	return r[a.searchResultIdx]
}

// handleSearchKey processes keypresses while Ctrl-R search is active.
func (a App) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		a.searchActive = false
		a.searchQuery = ""
		a.searchResultIdx = 0
		return a.withFeedRebuilt(false), nil

	case tea.KeyCtrlR:
		// Cycle to next (older) match.
		results := a.searchResults()
		if a.searchResultIdx < len(results)-1 {
			a.searchResultIdx++
		}
		if r := a.currentSearchResult(); r != "" {
			a.input.SetValue(r)
			a.input.CursorEnd()
		}
		return a, nil

	case tea.KeyEnter:
		result := a.currentSearchResult()
		a.searchActive = false
		a.searchQuery = ""
		a.searchResultIdx = 0
		if result != "" {
			a.input.SetValue(result)
			a.input.CursorEnd()
		}
		return a.withFeedRebuilt(false), nil

	case tea.KeyBackspace:
		if len(a.searchQuery) > 0 {
			runes := []rune(a.searchQuery)
			a.searchQuery = string(runes[:len(runes)-1])
			a.searchResultIdx = 0
		}
		if r := a.currentSearchResult(); r != "" {
			a.input.SetValue(r)
			a.input.CursorEnd()
		}
		return a, nil

	case tea.KeyRunes:
		a.searchQuery += string(msg.Runes)
		a.searchResultIdx = 0
		if r := a.currentSearchResult(); r != "" {
			a.input.SetValue(r)
			a.input.CursorEnd()
		}
		return a, nil
	}
	return a, nil
}

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


// fmtPrice formats a per-million-token USD price compactly: "$15" for whole
// dollars, "$3.00" for fractional, "$0.075" for sub-cent values.
func fmtPrice(v float64) string {
	if v >= 1 {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%g", v)
}

// formatAvailableModels formats a ListAvailableModels result for display.
// Each provider lists up to ModelListCap models, one per line with pricing when
// known. When a provider has more, a "… and N more" notice is appended.
// When a provider filters its catalogue (OpenAI, Gemini), the header shows
// "N of M (chat only)" so users understand why the count is lower than expected.
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
