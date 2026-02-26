// Package ui implements the bubbletea TUI for Errata.
package ui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/reminders"
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
	report             *output.Report                       // output report for selection recording
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
	activeAdapters []models.ModelAdapter // nil = use all adapters
	disabledTools  map[string]bool       // tools excluded from runs; nil = all enabled
	prefPath       string
	toolStatePath  string // path to .errata_tools persistence file; "" = no persistence
	sessionID      string
	cfg            config.Config

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
	feed   []feedItem
	feedVP viewport.Model

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

	// cumulative cost across all runs this session
	totalCostUSD        float64
	sessionCostPerModel map[string]float64 // per-model cumulative cost this session

	// seed for reproducible model sampling; nil = not set
	seed *int64

	// recipe holds the full recipe configuration; used for output reports.
	recipe *recipe.Recipe

	// reminderState tracks conditional mid-conversation injection state.
	// nil when no reminders are configured in the recipe.
	reminderState *reminders.State

	// lastReport is the most recent output report; used by selection/rating
	// handlers to call RecordSelection after the user picks a winner.
	lastReport *output.Report

	// config overlay state
	configOverlayActive bool
	configSections      []configSection
	configSelectedIdx   int
	configExpandedIdx   int // -1 = none expanded
	configListItems     []listItem
	configListCursor    int
	configScalarFields  []scalarField
	configScalarCursor  int
	configEditBuf       string
	configTextArea      textarea.Model // textarea for text section editing
	configTextEditing   bool           // true when textarea is active in a text section
	sessionRecipe       *recipe.Recipe // working copy; nil until first /config or /set
	recipeModified      bool
}

// New creates the App model.
func New(adapters []models.ModelAdapter, prefPath, histPath, promptHistPath, sessionID, toolStatePath string, cfg config.Config, mcpDefs []tools.ToolDef, mcpDispatchers map[string]tools.MCPDispatcher, rec *recipe.Recipe) *App {
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

	disabled, err := tools.LoadDisabledTools(toolStatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load tool state: %v\n", err)
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
		histPath:              histPath,
		promptHistPath:        promptHistPath,
		toolStatePath:         toolStatePath,
		promptHistory:         ph,
		historyIdx:            -1,
		sessionID:             sessionID,
		input:                 ta,
		configTextArea:        cta,
		feedVP:                viewport.New(80, 20),
		panelIdx:              make(map[string]int),
		conversationHistories: h,
		disabledTools:         disabled,
		sessionCostPerModel:   make(map[string]float64),
		cfg:                   cfg,
		mcpDefs:               mcpDefs,
		mcpDispatchers:        mcpDispatchers,
		seed:                  cfg.Seed,
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
		if len(rec.SystemReminders) > 0 {
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

// feedVPHeight returns the number of lines the feed viewport should occupy.
func (a *App) feedVPHeight() int {
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
		footerLines = max(len(a.responses)+4, 4)
	case modeRating:
		footerLines = 2 // rating prompt line + blank
	}
	h := max(a.height-headerLines-sepLine-footerLines, 3)
	return h
}

// renderFeedContent builds the viewport content string from all feed items.
func (a *App) renderFeedContent() string {
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	msgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	noteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))

	var sb strings.Builder
	for _, item := range a.feed {
		switch item.kind {
		case "message":
			for line := range strings.SplitSeq(item.text, "\n") {
				sb.WriteString(msgStyle.Render("  " + line))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
		case "run":
			sb.WriteString(promptStyle.Render("> " + item.prompt))
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
				sb.WriteString(noteStyle.Render("  " + item.note))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// withFeedRebuilt resizes and refreshes the feed viewport. Returns updated App.
func (a *App) withFeedRebuilt(gotoBottom bool) App {
	a.feedVP.Width = a.width
	a.feedVP.Height = a.feedVPHeight()
	a.feedVP.SetContent(a.renderFeedContent())
	if gotoBottom {
		a.feedVP.GotoBottom()
	}
	return *a
}

// withMessage appends a system message to the feed and rebuilds the viewport.
func (a *App) withMessage(text string) App {
	a.feed = append(a.feed, feedItem{kind: "message", text: text})
	return a.withFeedRebuilt(true)
}

//nolint:gocritic // bubbletea requires value receiver for tea.Model interface
func (a App) Init() tea.Cmd {
	return textarea.Blink
}

// ---- update ----

//nolint:gocritic // bubbletea requires value receiver for tea.Model interface
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.input.SetWidth(msg.Width - 4)
		atBottom := a.feedVP.AtBottom()
		return a.withFeedRebuilt(atBottom), nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress &&
			(msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown) {
			var cmd tea.Cmd
			a.feedVP, cmd = a.feedVP.Update(msg)
			return a, cmd
		}

	case tea.KeyMsg:
		switch a.mode {
		case modeIdle:
			return a.handleIdleKey(msg)
		case modeRunning:
			if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC {
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
		a.feedVP.Width = a.width
		a.feedVP.Height = a.feedVPHeight()
		a.feedVP.SetContent(a.renderFeedContent())
		a.feedVP.GotoBottom()
		return a, nil

	case listModelsResultMsg:
		var activeSet map[string]bool
		if a.activeAdapters != nil {
			activeSet = make(map[string]bool, len(a.activeAdapters))
			for _, ad := range a.activeAdapters {
				activeSet[ad.ID()] = true
			}
		}
		return a.withMessage(formatAvailableModels(msg.results, activeSet)), nil

	case compactCompleteMsg:
		a.conversationHistories = msg.histories
		if err := history.Save(a.histPath, a.conversationHistories); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
		}
		return a.withMessage("History compacted."), nil

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
			p.cacheReadTokens = resp.CacheReadTokens
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

		// If any models were interrupted, show a message and return to idle.
		if runner.HasInterrupted(msg.responses) {
			var names []string
			for _, r := range msg.responses {
				if r.Interrupted {
					names = append(names, r.ModelID)
				}
			}
			if len(a.feed) > 0 {
				a.feed[len(a.feed)-1].note = fmt.Sprintf(
					"Interrupted (%s). /resume to continue.",
					strings.Join(names, ", "),
				)
			}
			a.mode = modeIdle
			return a.withFeedRebuilt(true), nil
		}

		// Successful completion — clear any stale checkpoint.
		_ = checkpoint.Clear(checkpoint.DefaultPath)

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
				return a.withFeedRebuilt(true), nil
			}
			if okWithText == 1 {
				// Single usable response — offer thumbs-up/down rating.
				a.responses = msg.responses
				a.mode = modeRating
				return a.withFeedRebuilt(true), nil
			}
			// okWithText >= 2: fall through to modeSelecting for text voting.
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

func (a App) View() string { //nolint:gocritic // bubbletea requires value receiver for tea.Model interface
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
				Render(fmt.Sprintf("  scroll/↑↓/pgup/pgdn  %.0f%%", a.feedVP.ScrollPercent()*100))
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

	case modeRunning:
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).
			Render("  running… (ESC to cancel)"))

	case modeIdle:
		if a.configOverlayActive {
			sb.WriteString(renderConfigOverlay(
				a.configSections, a.configSelectedIdx, a.configExpandedIdx,
				a.recipeModified, a.width,
				a.configListItems, a.configListCursor,
				a.configScalarFields, a.configScalarCursor,
				a.configEditBuf,
				a.configTextEditing, a.configTextArea.View(),
			))
			break
		}
		if a.recipeModified {
			modStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))
			sb.WriteString(modStyle.Render("  [modified]"))
			sb.WriteByte('\n')
		}
		if a.activeAdapters != nil {
			subIDs := make([]string, 0, len(a.activeAdapters))
			for _, ad := range a.activeAdapters {
				subIDs = append(subIDs, ad.ID())
			}
			filterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAF00"))
			sb.WriteString(filterStyle.Render("  [subset: " + strings.Join(subIDs, ", ") + "]"))
			sb.WriteByte('\n')
		}
		if !a.feedVP.AtBottom() {
			hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444")).
				Render(fmt.Sprintf("  scroll/pgup/pgdn  %.0f%%", a.feedVP.ScrollPercent()*100))
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
		if val := a.input.Value(); len(val) > 0 {
			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF"))
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

			if val[0] == '/' {
				lower := strings.ToLower(val)
				switch {
				case strings.HasPrefix(lower, "/model "):
					partial := lastWord(val[len("/model "):])
					a.renderModelHints(&sb, partial, nameStyle)

				case strings.HasPrefix(lower, "/subset "):
					partial := lastWord(val[len("/subset "):])
					a.renderModelHints(&sb, partial, nameStyle)

				case strings.HasPrefix(lower, "/tools on "):
					partial := lastWord(val[len("/tools on "):])
					a.renderToolHints(&sb, partial, nameStyle, descStyle)

				case strings.HasPrefix(lower, "/tools off "):
					partial := lastWord(val[len("/tools off "):])
					a.renderToolHints(&sb, partial, nameStyle, descStyle)

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

				case strings.HasPrefix(lower, "/set "):
					partial := lastWord(val[len("/set "):])
					lp := strings.ToLower(partial)
					hw := newHintWriter(&sb, descStyle)
					for _, path := range configPathCandidates() {
						if strings.HasPrefix(path, lp) {
							hw.add(nameStyle.Render("  " + path))
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

	return sb.String()
}

// Run starts the bubbletea program and blocks until exit.
func Run(adapters []models.ModelAdapter, prefPath, histPath, promptHistPath, sessionID string, cfg config.Config, warnings []string, mcpDefs []tools.ToolDef, mcpDispatchers map[string]tools.MCPDispatcher, rec *recipe.Recipe) error {
	app := New(adapters, prefPath, histPath, promptHistPath, sessionID, ".errata_tools", cfg, mcpDefs, mcpDispatchers, rec)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	app.SetProgram(p)

	// Handle SIGTERM for graceful shutdown. Bubbletea already handles SIGINT
	// (Ctrl-C) by sending tea.KeyCtrlC through the event loop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		<-sigCh
		p.Quit()
	}()

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	_, err := p.Run()
	return err
}
