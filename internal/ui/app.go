// Package ui implements the bubbletea TUI for Errata.
package ui

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

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/recipestore"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/reminders"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/session"
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
	report             *output.Report                       // output report for selection recording
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

// ---- rewind ----

// rewindEntry captures enough state to undo one run.
type rewindEntry struct {
	fileSnapshots  []tools.FileSnapshot // nil if no writes were applied
	historyLengths map[string]int       // per-adapter history len BEFORE AppendHistory
	feedIndex      int                  // index into a.feed for annotation
	prompt         string               // original prompt (for RecordRewind)
}

// ---- model ----

// App is the bubbletea model.
type App struct {
	adapters       []models.ModelAdapter
	activeAdapters []models.ModelAdapter // nil = use all adapters
	disabledTools  map[string]bool       // tools excluded from runs; nil = all enabled
	prefPath       string
	sessionID      string
	cfg            config.Config
	recipeStore    *recipestore.Store // content-addressed config snapshot store

	// MCP tool definitions and dispatchers (nil if no MCP servers configured)
	mcpDefs        []tools.ToolDef
	mcpDispatchers map[string]tools.MCPDispatcher

	// Recipe-derived settings (nil/empty = unrestricted)
	toolAllowlist     []string // nil = all tools; from recipe ## Tools allowlist
	bashPrefixes      []string // nil = unrestricted bash; from recipe ## Tools bash(...)
	contextStrategy   string   // "auto_compact" | "manual" | "off"
	sandboxFilesystem string   // "" | "project_only" | "read_only"
	sandboxNetwork    string   // "" | "full" | "none"
	projectRoot       string   // absolute path from recipe Metadata.ProjectRoot; "" = cwd

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

	// double-ESC to clear prompt
	lastEscTime    time.Time // timestamp of last ESC press in idle mode
	escHintVisible bool      // true while "ESC again to clear" hint is shown

	// multi-line paste badge (like Claude Code's "[pasted N lines]")
	pastedText      string // full text from a bracketed paste
	pastedLineCount int    // line count for badge display

	// cumulative cost across all runs this session
	totalCostUSD        float64
	sessionCostPerModel map[string]float64 // per-model cumulative cost this session

	// seed for reproducible model sampling; nil = not set
	seed *int64

	// availableModels is the full list of model IDs available from enabled
	// providers (fetched at startup). Used by @mention autocomplete.
	availableModels []string

	// recipe holds the full recipe configuration; used for output reports.
	recipe *recipe.Recipe

	// reminderState tracks conditional mid-conversation injection state.
	// nil when no reminders are configured in the recipe.
	reminderState *reminders.State

	// lastReport is the most recent output report; used by selection/rating
	// handlers to call RecordSelection after the user picks a winner.
	lastReport *output.Report

	// rewind stack — each entry captures state for one undo step
	rewindStack []rewindEntry

	// per-session persistence paths and metadata
	checkpointPath    string        // per-session checkpoint.json
	feedPath          string        // per-session feed.json
	sessionMetaPath   string        // per-session meta.json
	sessionRecipePath string        // per-session recipe.md
	sessionMeta       session.Meta  // in-memory metadata, updated after each run
	sessionFeed       []session.FeedEntry // serialized feed for persistence

	// debugLog is true when --debug-log is active; enables raw API request logging.
	debugLog bool

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
	configTextArea      textarea.Model // textarea for text section editing
	configTextEditing   bool           // true when textarea is active in a text section
	sessionRecipe       *recipe.Recipe // working copy; nil until first /config or /set
	recipeModified      bool
}

// New creates the App model.
func New(adapters []models.ModelAdapter, prefPath, promptHistPath, sessionID string, cfg config.Config, mcpDefs []tools.ToolDef, mcpDispatchers map[string]tools.MCPDispatcher, rec *recipe.Recipe, sp session.Paths, meta session.Meta, availableModels []string, cs *recipestore.Store, debugLog bool) *App {
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

	h, err := history.Load(sp.HistoryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load history: %v\n", err)
	}

	ph, err := prompthistory.Load(promptHistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load prompt history: %v\n", err)
	}

	cta := textarea.New()
	cta.Placeholder = "Enter text…"
	cta.SetHeight(8)
	cta.SetWidth(80)
	cta.CharLimit = 0
	cta.ShowLineNumbers = false

	app := &App{
		adapters:              adapters,
		prefPath:              prefPath,
		histPath:              sp.HistoryPath,
		promptHistPath:        promptHistPath,
		promptHistory:         ph,
		historyIdx:            -1,
		sessionID:             sessionID,
		input:                 ta,
		configTextArea:        cta,
		panelIdx:              make(map[string]int),
		conversationHistories: h,
		sessionCostPerModel:   make(map[string]float64),
		cfg:                   cfg,
		recipeStore:           cs,
		mcpDefs:               mcpDefs,
		mcpDispatchers:        mcpDispatchers,
		availableModels:       availableModels,
		seed:                  cfg.Seed,
		debugLog:              debugLog,
		checkpointPath:        sp.CheckpointPath,
		feedPath:              sp.FeedPath,
		sessionMetaPath:       sp.MetaPath,
		sessionRecipePath:     sp.RecipePath,
		sessionMeta:           meta,
	}
	app.recipe = rec
	if rec != nil {
		if rec.Tools != nil {
			app.toolAllowlist = rec.Tools.Allowlist
			app.bashPrefixes = rec.Tools.BashPrefixes
		}
		app.contextStrategy = rec.Context.Strategy
		app.sandboxFilesystem = rec.Sandbox.Filesystem
		app.sandboxNetwork = rec.Sandbox.Network
		app.projectRoot = rec.Metadata.ProjectRoot
		if tools.RemindersEnabled && len(rec.SystemReminders) > 0 {
			var rs []reminders.Reminder
			for _, cfg := range rec.SystemReminders {
				tr, err := reminders.ParseTrigger(cfg.Trigger)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: skipping reminder %q: %v\n", cfg.Name, err)
					continue
				}
				rs = append(rs, reminders.Reminder{Name: cfg.Name, Trigger: tr, Content: cfg.Content})
			}
			if len(rs) > 0 {
				app.reminderState = reminders.NewState(rs)
			}
		}
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

// withMessage appends a system message to the feed and prints it to terminal
// scrollback via tea.Println.
func (a *App) withMessage(text string) (App, tea.Cmd) {
	a.feed = append(a.feed, feedItem{kind: "message", text: text})
	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	w := max(a.width, 40)
	var sb strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		sb.WriteString(wrapText(line, w, 2, msgStyle))
		sb.WriteByte('\n')
	}
	return *a, tea.Println(strings.TrimRight(sb.String(), "\n"))
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

	case escHintMsg:
		a.escHintVisible = false
		return a, nil

	case welcomeMsg:
		return a.withMessage(
			"Welcome to Errata! No API keys are configured.\n" +
				"Set API keys in .env and restart, or use /config to view settings.",
		)

	case compactCompleteMsg:
		a.conversationHistories = msg.histories
		a.rewindStack = nil // compaction invalidates stored history lengths
		if err := history.Save(a.histPath, a.conversationHistories); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
		}
		return a.withMessage("History compacted.")

	case runCompleteMsg:
		a.cancelRun = nil
		a.lastReport = msg.report

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
			a.conversationHistories = msg.compactedHistories
		}

		// Capture pre-history lengths for rewind before AppendHistory mutates them.
		preHistLengths := make(map[string]int, len(panelIDs))
		for _, id := range panelIDs {
			preHistLengths[id] = len(a.conversationHistories[id])
		}

		a.conversationHistories = runner.AppendHistory(a.conversationHistories, panelIDs, msg.responses, a.lastPrompt)
		if err := history.Save(a.histPath, a.conversationHistories); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
		}

		// Persist session metadata and feed.
		a.persistSessionState(msg.responses)

		// Push rewind entry (fileSnapshots populated later by applySelection if writes happen).
		a.rewindStack = append(a.rewindStack, rewindEntry{
			historyLengths: preHistLengths,
			feedIndex:      len(a.feed) - 1,
			prompt:         a.lastPrompt,
		})

		// Sort by latency ascending (fastest first) for display. Must happen after
		// panel-stat assignment and AppendHistory, which rely on the original index order.
		sort.SliceStable(msg.responses, func(i, j int) bool {
			return msg.responses[i].LatencyMS < msg.responses[j].LatencyMS
		})

		if len(a.feed) > 0 {
			a.feed[len(a.feed)-1].responses = msg.responses
		}

		// Build scrollback output: panel summaries + diffs.
		var out strings.Builder
		out.WriteString(renderInlinePanels(a.panels, a.width))
		if d := RenderDiffs(msg.responses, a.width); d != "" {
			out.WriteString(d)
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
			noteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))
			out.WriteString(wrapText(note, a.width, 2, noteStyle))
			out.WriteByte('\n')
			a.mode = modeIdle
			return a, tea.Println(strings.TrimRight(out.String(), "\n"))
		}

		// Successful completion — clear any stale checkpoint.
		clearCheckpoint(a.checkpointPath)

		hasWrites := false
		for _, resp := range msg.responses {
			if len(resp.ProposedWrites) > 0 {
				hasWrites = true
				break
			}
		}

		printCmd := tea.Cmd(nil)
		if s := strings.TrimRight(out.String(), "\n"); s != "" {
			printCmd = tea.Println(s)
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
				return a, printCmd
			}
			if okWithText == 1 {
				// Single usable response — offer thumbs-up/down rating.
				a.responses = msg.responses
				a.mode = modeRating
				return a, printCmd
			}
			// okWithText >= 2: fall through to modeSelecting for text voting.
		}

		a.responses = msg.responses
		a.mode = modeSelecting
		a.selection = ""
		a.selectionErr = ""
		return a, printCmd
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
		sb.WriteString(RenderSelectionMenu(a.responses))
		if a.selectionErr != "" {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000")).
				Render("  " + a.selectionErr))
			sb.WriteByte('\n')
		}
		sb.WriteString("\nchoice> ")
		sb.WriteString(a.selection)

	case modeRating:
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
		if a.configOverlayActive {
			overlayHeight := max(a.height-1, 5)
			sb.WriteString(renderConfigOverlay(
				a.configSections, a.configSelectedIdx, a.configExpandedIdx,
				a.recipeModified, a.width, overlayHeight,
				a.configListItems, a.configListCursor, a.configListOffset,
				a.configScalarFields, a.configScalarCursor,
				a.configEditBuf,
				a.configTextEditing, a.configTextArea.View(),
			))
			v := tea.NewView(sb.String())
			v.AltScreen = true
			return v
		}
		if a.recipeModified {
			modStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))
			sb.WriteString(modStyle.Render("  [modified]"))
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

				case strings.HasPrefix(lower, "/export "):
					partial := lastWord(val[len("/export "):])
					lp := strings.ToLower(partial)
					for _, sub := range []string{"recipe", "output"} {
						if strings.HasPrefix(sub, lp) {
							sb.WriteByte('\n')
							sb.WriteString(nameStyle.Render("  " + sub))
						}
					}

				case strings.HasPrefix(lower, "/import "):
					partial := lastWord(val[len("/import "):])
					lp := strings.ToLower(partial)
					for _, sub := range []string{"recipe"} {
						if strings.HasPrefix(sub, lp) {
							sb.WriteByte('\n')
							sb.WriteString(nameStyle.Render("  " + sub))
						}
					}

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
func Run(adapters []models.ModelAdapter, prefPath, promptHistPath, sessionID string, cfg config.Config, warnings []string, mcpDefs []tools.ToolDef, mcpDispatchers map[string]tools.MCPDispatcher, rec *recipe.Recipe, sp session.Paths, meta session.Meta, resuming bool, availableModels []string, cs *recipestore.Store, debugLog bool) error {
	app := New(adapters, prefPath, promptHistPath, sessionID, cfg, mcpDefs, mcpDispatchers, rec, sp, meta, availableModels, cs, debugLog)

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

	// On resume: replay saved feed as visual history.
	if resuming {
		feedEntries, err := session.LoadFeed(sp.FeedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load feed: %v\n", err)
		}
		app.sessionFeed = feedEntries
		app.feed = append(app.feed, feedItem{kind: "message", text: fmt.Sprintf("Resumed session %s", sessionID)})
		app.feed = append(app.feed, replayFeed(feedEntries)...)
	}

	for _, w := range warnings {
		app.feed = append(app.feed, feedItem{kind: "message", text: "warning: " + w})
	}

	// Write initial session metadata.
	if err := session.SaveMeta(sp.MetaPath, meta); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
	}

	_, err := p.Run()
	return err
}

// persistSessionState updates and saves session metadata, feed, and recipe
// after a run completes. Called from the runCompleteMsg handler.
func (a *App) persistSessionState(responses []models.ModelResponse) {
	now := time.Now()
	a.sessionMeta.LastActiveAt = now
	a.sessionMeta.PromptCount++
	if a.sessionMeta.FirstPrompt == "" {
		a.sessionMeta.FirstPrompt = truncateStr(a.lastPrompt, 120)
	}
	a.sessionMeta.LastPrompt = truncateStr(a.lastPrompt, 120)

	if err := session.SaveMeta(a.sessionMetaPath, a.sessionMeta); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
	}

	// Build and append feed entry.
	entry := buildFeedEntry(a.lastPrompt, responses)
	a.sessionFeed = append(a.sessionFeed, entry)
	if err := session.SaveFeed(a.feedPath, a.sessionFeed); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session feed: %v\n", err)
	}

	// Persist session recipe (captures the exact config used for this run).
	a.persistSessionRecipe()
}

// persistSessionRecipe writes the current session recipe to the per-session
// recipe.md. This is the single save point for config changes — called after
// every run completion.
func (a *App) persistSessionRecipe() {
	if a.sessionRecipePath == "" {
		return
	}
	rec := a.sessionRecipe
	if rec == nil {
		rec = a.recipe
	}
	if rec == nil {
		return
	}
	md := rec.MarshalMarkdown()
	_ = os.MkdirAll(filepath.Dir(a.sessionRecipePath), 0o750)
	_ = os.WriteFile(a.sessionRecipePath, []byte(md), 0o600)
}

// updateLastFeedNote updates the note on the most recent session feed entry
// and re-saves feed.json. Called after selection/rating.
func (a *App) updateLastFeedNote(note string) {
	if len(a.sessionFeed) == 0 {
		return
	}
	a.sessionFeed[len(a.sessionFeed)-1].Note = note
	if err := session.SaveFeed(a.feedPath, a.sessionFeed); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session feed: %v\n", err)
	}
}

// clearCheckpoint removes the checkpoint file at the given path. Used instead
// of checkpoint.Clear to avoid importing the checkpoint package for a single call.
func clearCheckpoint(path string) {
	_ = os.Remove(path)
}

// activeModelIDs returns the IDs of the adapters that will run on the next prompt.
func (a App) activeModelIDs() []string { //nolint:gocritic // called from bubbletea value-receiver methods
	adapters := a.adapters
	if a.activeAdapters != nil {
		adapters = a.activeAdapters
	}
	ids := make([]string, len(adapters))
	for i, ad := range adapters {
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

// buildFeedEntry creates a session.FeedEntry from a run's prompt and responses.
func buildFeedEntry(prompt string, responses []models.ModelResponse) session.FeedEntry {
	modelEntries := make([]session.ModelEntry, 0, len(responses))
	for _, r := range responses {
		files := make([]string, 0, len(r.ProposedWrites))
		for _, fw := range r.ProposedWrites {
			files = append(files, fw.Path)
		}
		modelEntries = append(modelEntries, session.ModelEntry{
			ID:            r.ModelID,
			Text:          truncateStr(r.Text, 500),
			ProposedFiles: files,
		})
	}
	return session.FeedEntry{
		Kind:   "run",
		Prompt: prompt,
		Models: modelEntries,
	}
}

// replayFeed converts saved feed entries into feedItem structs for display.
func replayFeed(entries []session.FeedEntry) []feedItem {
	items := make([]feedItem, 0, len(entries))
	for _, e := range entries {
		switch e.Kind {
		case "message":
			items = append(items, feedItem{kind: "message", text: e.Text})
		case "run":
			// Build a summary: prompt + per-model text snippets.
			var sb strings.Builder
			for _, m := range e.Models {
				preview := truncateStr(m.Text, 200)
				preview = strings.ReplaceAll(preview, "\n", " ")
				fmt.Fprintf(&sb, "  %s: %s", m.ID, preview)
				if len(m.ProposedFiles) > 0 {
					fmt.Fprintf(&sb, " [%s]", strings.Join(m.ProposedFiles, ", "))
				}
				sb.WriteByte('\n')
			}
			items = append(items, feedItem{
				kind: "run",
				prompt: e.Prompt,
				note:   e.Note,
				panels: replayPanels(e.Models),
			})
		}
	}
	return items
}

// replayPanels creates frozen panel states from saved model entries for
// display during session resume. The panels are marked done with minimal info.
func replayPanels(entries []session.ModelEntry) []*panelState {
	panels := make([]*panelState, len(entries))
	for i, m := range entries {
		ps := newPanelState(m.ID, i)
		ps.done = true
		if len(m.Text) > 0 {
			ps.addEvent(models.AgentEvent{Type: models.EventText, Data: truncateStr(m.Text, 200)})
		}
		panels[i] = ps
	}
	return panels
}
