// Package ui implements the bubbletea TUI for Errata.
package ui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
	"github.com/errata-app/errata-cli/internal/adapters"
	"github.com/errata-app/errata-cli/internal/api"
	"github.com/errata-app/errata-cli/internal/commands"
	"github.com/errata-app/errata-cli/internal/config"
	"github.com/errata-app/errata-cli/internal/datastore"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
	"github.com/errata-app/errata-cli/internal/runner"
	"github.com/errata-app/errata-cli/internal/session"
	"github.com/errata-app/errata-cli/internal/tools"
)

// ---- message types ----

type agentEventMsg struct {
	modelID string
	event   models.AgentEvent
}

type runCompleteMsg struct {
	responses          []models.ModelResponse
	compactedHistories map[string][]models.ConversationTurn // non-nil if auto-compact ran
	collector          *output.Collector                    // collected per-model events
	toolNames          []string                             // active tool names during run (for recipe hash)
}

type modelDoneMsg struct {
	idx      int
	response models.ModelResponse
}

type compactCompleteMsg struct {
	histories map[string][]models.ConversationTurn
}

type welcomeMsg struct{}

type escHintMsg struct{} // fired after 300ms to dismiss "ESC again to clear" hint

type panelTickMsg struct{} // periodic re-render during modeRunning for elapsed time

type publishCompleteMsg struct {
	ref string
	err error
}

type pullCompleteMsg struct {
	raw string
	ref string
	err error
}

type syncCompleteMsg struct {
	accepted int
	err      error
}

// panelTick returns a tea.Cmd that fires a panelTickMsg after 1 second.
func panelTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return panelTickMsg{} })
}

// ---- app modes ----

type mode int

const (
	modeIdle      mode = iota
	modeRunning        // agents running, panels live
	modeSelecting      // diff shown in feed, awaiting selection
	modeRating         // single-model text response, awaiting thumbs-up/down
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
	activeAdapters []models.ModelAdapter // always explicit; initialised to adapters in New()
	disabledTools  map[string]bool       // tools excluded from runs; nil = all enabled
	cfg config.Config

	// providerModels is the full per-provider model catalogue fetched at startup.
	// Used by /config models to show activatable models from all connected providers.
	providerModels []adapters.ProviderModels

	// configListFilter is the live type-to-filter text for the models list section.
	configListFilter string

	// MCP tool definitions and dispatchers (nil if no MCP servers configured)
	mcpDefs        []tools.ToolDef
	mcpDispatchers map[string]tools.MCPDispatcher

	mode    mode
	verbose bool
	width   int
	height  int

	// input
	input textarea.Model

	// persistent conversation feed
	feed []feedItem

	// current run
	panels    []*panelState
	panelIdx  map[string]int
	prog      *tea.Program
	cancelRun context.CancelFunc // cancels running agents; nil when idle

	// selecting
	responses    []models.ModelResponse
	selection    string
	selectionErr string
	lastPrompt   string

	// store owns conversation histories and prompt history (persisted on mutation).
	store *datastore.Store

	// prompt history navigation state (UI-only; the actual data lives in store)
	historyIdx      int    // -1 = not navigating; 0..N-1 = position in store.PromptHistory()
	historyInputBuf string // typed text saved when navigation starts

	// ctrl-r reverse search
	searchActive    bool
	searchQuery     string
	searchResultIdx int

	// double-ESC to clear prompt
	lastEscTime    time.Time // timestamp of last ESC press in idle mode
	escHintVisible bool      // true while "ESC again to clear" hint is shown

	// multi-line paste badge (like Claude Code's "[pasted N lines]")
	pastedText      string // full text from a bracketed paste
	pastedLineCount int    // line count for badge display


	// apiClient is the errata.app API client, injected for testability.
	apiClient *api.Client

	// debugLog is true when --debug-log is active; enables raw API request logging.
	debugLog bool

	// lastRunInView: when true, the last completed run's panels are rendered
	// in View() instead of being pushed to scrollback. Flushed to scrollback
	// when the next run starts, /clear, /wipe, or withMessage is called.
	lastRunInView bool

	// hint line tracking (for feed viewport height budget)
	lastHintLines int

	// config overlay state
	configOverlayActive bool
	configSections      []configSection
	configSelectedIdx   int
	configExpandedIdx   int // -1 = none expanded
	configListItems     []listItem
	configListCursor    int
	configListOffset    int // first visible item index in windowed list
	configScalarFields  []scalarField
	configScalarCursor  int
	configEditBuf       string
	configTextArea    textarea.Model // textarea for text section editing
	configTextEditing bool           // true when textarea is active in a text section
}

// New creates the App model.
func New(adapterList []models.ModelAdapter, cfg config.Config, mcpDefs []tools.ToolDef, mcpDispatchers map[string]tools.MCPDispatcher, providerModels []adapters.ProviderModels, debugLog bool, store *datastore.Store) *App {
	ta := textarea.New()
	ta.Placeholder = "Enter a prompt…"
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(1)
	ta.MaxHeight = 8
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	// Extend InsertNewline binding so Shift+Enter and Alt+Enter insert
	// newlines via the textarea's native splitLine path. Without this,
	// the textarea only matches bare "enter".
	ta.KeyMap.InsertNewline.SetKeys(append(
		ta.KeyMap.InsertNewline.Keys(), "shift+enter", "alt+enter",
	)...)

	cta := textarea.New()
	cta.Placeholder = "Enter text…"
	cta.SetHeight(8)
	cta.SetWidth(80)
	cta.CharLimit = 0
	cta.ShowLineNumbers = false

	app := &App{
		adapters:       adapterList,
		activeAdapters: slices.Clone(adapterList),
		historyIdx:     -1,
		input:          ta,
		configTextArea: cta,
		panelIdx:       make(map[string]int),
		cfg:     cfg,
		mcpDefs: mcpDefs,
		mcpDispatchers: mcpDispatchers,
		providerModels: providerModels,
		apiClient:      api.NewClient(),
		debugLog:       debugLog,
		store:          store,
	}
	return app
}

// SetProgram wires up the tea.Program reference so goroutines can send messages.
func (a *App) SetProgram(p *tea.Program) { a.prog = p }

// inputLines computes the visual line count of the textarea (accounting for
// soft-wrap at the textarea width), capped at MaxHeight.
func (a *App) inputLines() int {
	val := a.input.Value()
	if val == "" {
		return 1
	}
	w := a.input.Width()
	if w <= 0 {
		w = 1
	}
	lines := 0
	for line := range strings.SplitSeq(val, "\n") {
		r := uniseg.StringWidth(line)
		if r == 0 {
			lines++
		} else {
			lines += (r + w - 1) / w
		}
	}
	return max(1, min(lines, a.input.MaxHeight))
}

// resizeInput updates the textarea height to match its content and recomputes
// hint lines for the View layout.
func (a *App) resizeInput() {
	h := a.inputLines()
	if h != a.input.Height() {
		a.input.SetHeight(h)
	}
	a.lastHintLines = a.computeHintLines()
}

// renderInitialFeed builds a printable string from all current feed items.
// Used by Init to print startup content (warnings, resume messages) to scrollback.
func (a *App) renderInitialFeed() string {
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	noteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))

	var sb strings.Builder
	for _, item := range a.feed {
		switch item.kind {
		case "message":
			for line := range strings.SplitSeq(item.text, "\n") {
				sb.WriteString(wrapText(line, a.width, 2, msgStyle))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
		case "run":
			sb.WriteString(wrapText("> "+item.prompt, a.width, 0, promptStyle))
			sb.WriteByte('\n')
			if len(item.panels) > 0 {
				sb.WriteString(renderInlinePanels(item.panels, a.width))
				sb.WriteByte('\n')
			}
			if item.responses != nil {
				d := RenderDiffs(item.responses, a.width)
				if d != "" {
					sb.WriteString(d)
				}
			}
			if item.note != "" {
				sb.WriteString(wrapText(item.note, a.width, 2, noteStyle))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// renderLastRunView builds the display string for the last completed run's
// panels + note, suitable for rendering in View(). Diffs are NOT included
// here — they are pushed to scrollback in runCompleteMsg so that long diff
// output doesn't crowd out panels in the live area.
// Returns "" if !a.lastRunInView or no run feed item exists.
func (a App) renderLastRunView() string { //nolint:gocritic // called from bubbletea value-receiver methods
	if !a.lastRunInView {
		return ""
	}
	for i := len(a.feed) - 1; i >= 0; i-- {
		item := a.feed[i]
		if item.kind != "run" {
			continue
		}
		noteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))
		var sb strings.Builder
		if len(item.panels) > 0 {
			sb.WriteString(renderInlinePanels(item.panels, a.width))
		}
		if item.note != "" {
			sb.WriteString(wrapText(item.note, a.width, 2, noteStyle))
			sb.WriteByte('\n')
		}
		return sb.String()
	}
	return ""
}

// flushLastRunToScrollback pushes the last run's panels + note to terminal
// scrollback and clears the lastRunInView flag. Returns the updated App and a
// tea.Println cmd (or nil if nothing to flush). Diffs are already in scrollback
// (pushed by runCompleteMsg), so they are not included here.
func (a App) flushLastRunToScrollback() (App, tea.Cmd) { //nolint:gocritic // bubbletea value-receiver pattern
	if !a.lastRunInView {
		return a, nil
	}
	content := a.renderLastRunView()
	a.lastRunInView = false
	if content == "" {
		return a, nil
	}
	return a, tea.Println(strings.TrimRight(content, "\n"))
}

// withMessage appends a system message to the feed and prints it to terminal
// scrollback via tea.Println.
func (a *App) withMessage(text string) (App, tea.Cmd) {
	flushed, flushCmd := a.flushLastRunToScrollback()
	*a = flushed
	a.feed = append(a.feed, feedItem{kind: "message", text: text})
	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	w := max(a.width, 40)
	var sb strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		sb.WriteString(wrapText(line, w, 2, msgStyle))
		sb.WriteByte('\n')
	}
	printCmd := tea.Println(strings.TrimRight(sb.String(), "\n"))
	if flushCmd != nil {
		return *a, tea.Batch(flushCmd, printCmd)
	}
	return *a, printCmd
}

//nolint:gocritic // bubbletea requires value receiver for tea.Model interface
func (a App) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink}

	// Print header to scrollback.
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	modelLineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	header := headerStyle.Render("  Errata  A/B testing tool for agentic AI models") + "\n" +
		wrapText(strings.Join(a.activeModelIDs(), " · "), max(a.width, 40), 2, modelLineStyle)
	cmds = append(cmds, tea.Println(header))

	// Print initial feed items (warnings, resume messages).
	if len(a.feed) > 0 {
		if content := a.renderInitialFeed(); content != "" {
			cmds = append(cmds, tea.Println(strings.TrimRight(content, "\n")))
		}
	}

	if len(a.adapters) == 0 {
		cmds = append(cmds, func() tea.Msg { return welcomeMsg{} })
	}
	return tea.Batch(cmds...)
}

// ---- update ----

//nolint:gocritic // bubbletea requires value receiver for tea.Model interface
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.input.SetWidth(msg.Width - 4)
		a.resizeInput()
		return a, nil

	case tea.PasteMsg:
		if a.mode == modeIdle {
			return a.handlePaste(msg.Content)
		}
		return a, nil

	case tea.KeyPressMsg:
		switch a.mode {
		case modeIdle:
			return a.handleIdleKey(msg)
		case modeRunning:
			if msg.Code == tea.KeyEscape || (msg.Code == 'c' && msg.Mod.Contains(tea.ModCtrl)) {
				if a.cancelRun != nil {
					a.cancelRun()
					a.cancelRun = nil
				}
				return a, nil
			}
			return a, nil
		case modeSelecting:
			return a.handleSelectKey(msg)
		case modeRating:
			return a.handleRatingKey(msg)
		}
		return a, nil

	case agentEventMsg:
		if idx, ok := a.panelIdx[msg.modelID]; ok {
			a.panels[idx].addEvent(msg.event)
		}
		return a, nil

	case modelDoneMsg:
		if msg.idx < len(a.panels) {
			p := a.panels[msg.idx]
			p.done = true
			p.latencyMS = msg.response.LatencyMS
			p.inputTokens = msg.response.InputTokens
			p.outputTokens = msg.response.OutputTokens
			p.reasoningTokens = msg.response.ReasoningTokens
			p.costUSD = msg.response.CostUSD
			if msg.response.Interrupted {
				p.errMsg = "interrupted"
			} else if msg.response.Error != "" {
				p.errMsg = msg.response.Error
				if runner.IsContextOverflowError(msg.response.Error) {
					p.errMsg = "context limit reached — use /wipe or /compact to reset"
				}
			}
		}
		return a, nil

	case panelTickMsg:
		if a.mode == modeRunning {
			return a, panelTick()
		}
		return a, nil

	case escHintMsg:
		a.escHintVisible = false
		return a, nil

	case welcomeMsg:
		return a.withMessage(
			"Welcome to Errata! No models are active. Use /config to add models.",
		)

	case publishCompleteMsg:
		if msg.err != nil {
			return a.withMessage(fmt.Sprintf("Publish failed: %v", msg.err))
		}
		return a.withMessage(fmt.Sprintf("Published: %s", msg.ref))

	case pullCompleteMsg:
		if msg.err != nil {
			return a.withMessage(fmt.Sprintf("Pull failed: %v", msg.err))
		}
		return a.handlePullComplete(msg.raw, msg.ref)

	case syncCompleteMsg:
		if msg.err != nil {
			return a.withMessage(fmt.Sprintf("Sync failed: %v", msg.err))
		}
		return a.withMessage(fmt.Sprintf("Synced: %d runs uploaded.", msg.accepted))

	case compactCompleteMsg:
		a.store.SetHistories(msg.histories)
		a.store.ClearRewindStack() // compaction invalidates stored history lengths
		return a.withMessage("History compacted.")

	case runCompleteMsg:
		a.cancelRun = nil

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
			p.reasoningTokens = resp.ReasoningTokens
			p.costUSD = resp.CostUSD
			a.store.AccumulateCost(resp.ModelID, resp.CostUSD)
			if resp.Interrupted {
				p.errMsg = "interrupted"
			} else if resp.Error != "" {
				p.errMsg = resp.Error
				if runner.IsContextOverflowError(resp.Error) {
					p.errMsg = "context limit reached — use /wipe or /compact to reset"
				}
			}
		}

		// Accumulate conversation history for each adapter that returned text.
		panelIDs := make([]string, len(a.panels))
		for i, p := range a.panels {
			panelIDs[i] = p.modelID
		}
		if msg.compactedHistories != nil {
			a.store.SetHistories(msg.compactedHistories)
		}

		// Append histories (captures pre-lengths internally for rewind).
		preHistLengths := a.store.AppendHistories(panelIDs, msg.responses, a.lastPrompt)

		// Persist session metadata and content.
		a.store.PersistRunState(a.lastPrompt, msg.responses, msg.collector, msg.toolNames)

		// Push rewind entry (fileSnapshots populated later by applySelection if writes happen).
		a.store.PushRewindEntry(datastore.RewindEntry{
			HistoryLengths: preHistLengths,
			FeedIndex:      len(a.feed) - 1,
			Prompt:         a.lastPrompt,
		})

		// Sort by latency ascending (fastest first) for display. Must happen after
		// panel-stat assignment and AppendHistory, which rely on the original index order.
		sort.SliceStable(msg.responses, func(i, j int) bool {
			return msg.responses[i].LatencyMS < msg.responses[j].LatencyMS
		})

		if len(a.feed) > 0 {
			a.feed[len(a.feed)-1].responses = msg.responses
		}

		// Panels stay in View() for live re-rendering; diffs go to
		// scrollback immediately (they're static and can be very long).
		a.lastRunInView = true
		var diffCmd tea.Cmd
		if d := RenderDiffs(msg.responses, a.width); d != "" {
			diffCmd = tea.Println(strings.TrimRight(d, "\n"))
		}

		// If any models were interrupted, show a message and return to idle.
		if runner.HasInterrupted(msg.responses) {
			var names []string
			for _, r := range msg.responses {
				if r.Interrupted {
					names = append(names, r.ModelID)
				}
			}
			note := fmt.Sprintf(
				"Interrupted (%s). /resume to continue.",
				strings.Join(names, ", "),
			)
			if len(a.feed) > 0 {
				a.feed[len(a.feed)-1].note = note
			}
			a.mode = modeIdle
			return a, diffCmd
		}

		// Successful completion — clear any stale checkpoint.
		a.store.ClearCheckpoint()

		hasWrites := false
		for _, resp := range msg.responses {
			if len(resp.ProposedWrites) > 0 {
				hasWrites = true
				break
			}
		}

		if !hasWrites {
			okWithText := 0
			for _, resp := range msg.responses {
				if resp.OK() && resp.Text != "" {
					okWithText++
				}
			}
			if okWithText == 0 {
				a.mode = modeIdle
				return a, diffCmd
			}
			if okWithText == 1 {
				// Single usable response — offer thumbs-up/down rating.
				a.responses = msg.responses
				a.mode = modeRating
				return a, diffCmd
			}
			// okWithText >= 2: fall through to modeSelecting for text voting.
		}

		a.responses = msg.responses
		a.mode = modeSelecting
		a.selection = ""
		a.selectionErr = ""
		return a, diffCmd
	}

	// Pass remaining events to textarea in idle mode.
	if a.mode == modeIdle {
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		a.resizeInput()
		return a, cmd
	}
	return a, nil
}

// ---- view ----

func (a App) View() tea.View { //nolint:gocritic // bubbletea requires value receiver for tea.Model interface
	var sb strings.Builder

	switch a.mode {
	case modeRunning:
		sb.WriteString(renderInlinePanels(a.panels, a.width))
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).
			Render("  running… (ESC to cancel)"))

	case modeSelecting:
		sb.WriteString(a.renderLastRunView())
		sb.WriteString(RenderSelectionMenu(a.responses))
		if a.selectionErr != "" {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000")).
				Render("  " + a.selectionErr))
			sb.WriteByte('\n')
		}
		sb.WriteString("\nchoice> ")
		sb.WriteString(a.selection)

	case modeRating:
		sb.WriteString(a.renderLastRunView())
		ratingStyle := lipgloss.NewStyle().Bold(true)
		ratingModelName := "this"
		for _, resp := range a.responses {
			if resp.OK() {
				ratingModelName = resp.ModelID
				break
			}
		}
		sb.WriteString(ratingStyle.Render(fmt.Sprintf("  Rate %s's response:", ratingModelName)))
		sb.WriteString("  y = good  n = bad  s = skip\n")

	case modeIdle:
		sb.WriteString(a.renderLastRunView())
		if a.configOverlayActive {
			overlayHeight := max(a.height-1, 5)
			sb.WriteString(renderConfigOverlay(
				a.configSections, a.configSelectedIdx, a.configExpandedIdx,
				a.width, overlayHeight,
				a.configListItems, a.configListCursor, a.configListOffset,
				a.configScalarFields, a.configScalarCursor,
				a.configEditBuf,
				a.configTextEditing, a.configTextArea.View(),
				a.configListFilter,
			))
			v := tea.NewView(sb.String())
			v.AltScreen = true
			return v
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
		if a.pastedText != "" {
			pasteStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888888")).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#555555")).
				Padding(0, 1)
			sb.WriteString(pasteStyle.Render(fmt.Sprintf("pasted %d lines", a.pastedLineCount)))
			sb.WriteByte('\n')
		}
		if a.escHintVisible {
			hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true)
			sb.WriteString(hintStyle.Render("  ESC again to clear"))
			sb.WriteByte('\n')
		}
		sb.WriteString(a.input.View())
		if val := a.input.Value(); len(val) > 0 {
			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF"))
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

			if val[0] == '/' {
				lower := strings.ToLower(val)
				switch {
				case strings.HasPrefix(lower, "/config "):
					partial := lastWord(val[len("/config "):])
					lp := strings.ToLower(partial)
					hw := newHintWriter(&sb, descStyle)
					for _, name := range interactiveSections {
						if strings.HasPrefix(name, lp) {
							hw.add(nameStyle.Render("  " + name))
						}
					}
					hw.flush()

				default:
					prefix := strings.ToLower(strings.SplitN(val, " ", 2)[0])
					hw := newHintWriter(&sb, descStyle)
					for _, c := range commands.All {
						if strings.HasPrefix(c.Name, prefix) {
							hw.add(nameStyle.Render(fmt.Sprintf("  %-12s", c.Name)) + descStyle.Render("  "+c.Desc))
						}
					}
					hw.flush()
				}
			} else {
				// @mention hints: if the last word starts with @, show matching models.
				lw := lastWord(val)
				if strings.HasPrefix(lw, "@") && len(lw) >= 2 {
					partial := strings.ToLower(lw[1:])
					hw := newHintWriter(&sb, descStyle)
					for _, id := range a.modelIDCandidates() {
						if strings.HasPrefix(strings.ToLower(id), partial) {
							hw.add(nameStyle.Render("  @" + id))
						}
					}
					hw.flush()
				}
			}
		}
	}

	v := tea.NewView(sb.String())
	v.AltScreen = false
	return v
}

// Run starts the bubbletea program and blocks until exit.
func Run(adapterList []models.ModelAdapter, cfg config.Config, warnings []string, mcpDefs []tools.ToolDef, mcpDispatchers map[string]tools.MCPDispatcher, resuming bool, providerModels []adapters.ProviderModels, debugLog bool, store *datastore.Store) error {
	app := New(adapterList, cfg, mcpDefs, mcpDispatchers, providerModels, debugLog, store)

	p := tea.NewProgram(app)
	app.SetProgram(p)

	// Handle SIGTERM for graceful shutdown. Bubbletea already handles SIGINT
	// (Ctrl-C) by sending tea.KeyCtrlC through the event loop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		p.Quit()
	}()

	// On resume: replay saved metadata runs as visual history.
	if resuming {
		meta := store.Metadata()
		content := store.Content()
		app.feed = append(app.feed, feedItem{kind: "message", text: fmt.Sprintf("Resumed session %s", store.SessionID())})
		app.feed = append(app.feed, replayFromMetadata(meta.Runs, content.Runs)...)
	}

	for _, w := range warnings {
		app.feed = append(app.feed, feedItem{kind: "message", text: "warning: " + w})
	}

	// Write initial session metadata.
	store.SaveInitialMeta()

	_, err := p.Run()
	return err
}


// activeModelIDs returns the IDs of the adapters that will run on the next prompt.
func (a App) activeModelIDs() []string { //nolint:gocritic // called from bubbletea value-receiver methods
	ids := make([]string, len(a.activeAdapters))
	for i, ad := range a.activeAdapters {
		ids[i] = ad.ID()
	}
	return ids
}

// truncateStr truncates s to at most maxLen runes.
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}

// replayFromMetadata converts saved metadata runs + content runs into feedItems for display on resume.
func replayFromMetadata(runs []session.RunSummary, contentRuns []session.RunContent) []feedItem {
	items := make([]feedItem, 0, len(runs))
	for i, r := range runs {
		if r.Type == "rewind" {
			continue
		}
		// Build panels from content if available.
		var panels []*panelState
		if i < len(contentRuns) {
			panels = replayPanelsFromContent(contentRuns[i].Models)
		} else {
			// Fallback: build minimal panels from metadata model IDs.
			panels = replayPanelsFromIDs(r.Models)
		}
		items = append(items, feedItem{
			kind:   "run",
			prompt: r.PromptPreview,
			note:   r.Note,
			panels: panels,
		})
	}
	return items
}

// replayPanelsFromContent creates frozen panel states from saved content model entries.
func replayPanelsFromContent(entries []session.ModelRunContent) []*panelState {
	panels := make([]*panelState, len(entries))
	for i, m := range entries {
		ps := newPanelState(m.ModelID, i)
		ps.done = true
		if len(m.Text) > 0 {
			ps.addEvent(models.AgentEvent{Type: models.EventText, Data: truncateStr(m.Text, 200)})
		}
		panels[i] = ps
	}
	return panels
}

// replayPanelsFromIDs creates minimal frozen panels from model IDs only.
func replayPanelsFromIDs(modelIDs []string) []*panelState {
	panels := make([]*panelState, len(modelIDs))
	for i, id := range modelIDs {
		ps := newPanelState(id, i)
		ps.done = true
		panels[i] = ps
	}
	return panels
}
