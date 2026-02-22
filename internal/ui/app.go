// Package ui implements the bubbletea TUI for Errata.
package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	responses []models.ModelResponse
}

// ---- app modes ----

type mode int

const (
	modeIdle      mode = iota
	modeRunning        // agents running, panels live
	modeSelecting      // diff shown, awaiting selection
)

// ---- model ----

// App is the bubbletea model.
type App struct {
	adapters  []models.ModelAdapter
	prefPath  string
	sessionID string

	mode    mode
	verbose bool
	width   int
	height  int

	// idle
	input   textarea.Model
	history []string
	histIdx int

	// running
	panels    []*panelState
	panelIdx  map[string]int
	prog      *tea.Program

	// selecting
	responses  []models.ModelResponse
	selection  string // user's typed selection
	lastPrompt string
}

// New creates the App model.
func New(adapters []models.ModelAdapter, prefPath, sessionID string) *App {
	ta := textarea.New()
	ta.Placeholder = "Enter a prompt…"
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.CharLimit = 0
	ta.ShowLineNumbers = false

	return &App{
		adapters:  adapters,
		prefPath:  prefPath,
		sessionID: sessionID,
		input:     ta,
		panelIdx:  make(map[string]int),
	}
}

// SetProgram wires up the tea.Program reference so goroutines can send messages.
func (a *App) SetProgram(p *tea.Program) { a.prog = p }

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
		return a, nil

	case tea.KeyMsg:
		switch a.mode {
		case modeIdle:
			return a.handleIdleKey(msg)
		case modeSelecting:
			return a.handleSelectKey(msg)
		}

	case agentEventMsg:
		if idx, ok := a.panelIdx[msg.modelID]; ok {
			a.panels[idx].addEvent(msg.event)
		}
		return a, nil

	case runCompleteMsg:
		// mark all panels done
		for _, resp := range msg.responses {
			if idx, ok := a.panelIdx[resp.ModelID]; ok {
				a.panels[idx].done = true
				a.panels[idx].latencyMS = resp.LatencyMS
			}
		}
		a.responses = msg.responses
		a.mode = modeSelecting
		a.selection = ""
		return a, nil
	}

	// pass key events to textarea in idle mode
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

	case tea.KeyEnter:
		if msg.Alt {
			// alt-enter = newline inside textarea
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
	// slash commands
	switch strings.ToLower(strings.TrimSpace(prompt)) {
	case "/exit", "/quit":
		return a, tea.Quit
	case "/verbose":
		a.verbose = !a.verbose
		state := "off"
		if a.verbose {
			state = "on"
		}
		a.history = append(a.history, fmt.Sprintf("Verbose mode %s.", state))
		return a, nil
	case "/models":
		var ids []string
		for _, ad := range a.adapters {
			ids = append(ids, ad.ID())
		}
		a.history = append(a.history, "Models: "+strings.Join(ids, ", "))
		return a, nil
	case "/help":
		a.history = append(a.history, helpText())
		return a, nil
	}

	// launch agents
	a.lastPrompt = prompt
	a.mode = modeRunning
	a.panels = nil
	a.panelIdx = make(map[string]int)
	for i, ad := range a.adapters {
		a.panels = append(a.panels, newPanelState(ad.ID(), i))
		a.panelIdx[ad.ID()] = i
	}

	adapters := a.adapters
	verbose := a.verbose
	prog := a.prog

	return a, func() tea.Msg {
		responses := runner.RunAll(
			context.Background(),
			adapters,
			prompt,
			func(modelID string, event models.AgentEvent) {
				prog.Send(agentEventMsg{modelID: modelID, event: event})
			},
			verbose,
		)
		return runCompleteMsg{responses: responses}
	}
}

func (a App) handleSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD, tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyEnter:
		choice := strings.TrimSpace(a.selection)
		a.selection = ""
		return a.applySelection(choice)

	case tea.KeyBackspace, tea.KeyDelete:
		if len(a.selection) > 0 {
			a.selection = a.selection[:len(a.selection)-1]
		}

	case tea.KeyRunes:
		a.selection += string(msg.Runes)
	}
	return a, nil
}

func (a App) applySelection(choice string) (tea.Model, tea.Cmd) {
	if strings.EqualFold(choice, "s") {
		a.history = append(a.history, "Skipped.")
		a.responses = nil
		a.mode = modeIdle
		return a, nil
	}

	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(a.responses) {
		a.history = append(a.history, fmt.Sprintf("Invalid choice %q — type a number or 's'.", choice))
		return a, nil
	}

	selected := a.responses[n-1]
	if !selected.OK() {
		a.history = append(a.history, fmt.Sprintf("Model %s had an error — pick another.", selected.ModelID))
		return a, nil
	}

	if len(selected.ProposedWrites) == 0 {
		a.history = append(a.history, fmt.Sprintf("Model %s proposed no file writes.", selected.ModelID))
	} else {
		if err := tools.ApplyWrites(selected.ProposedWrites); err != nil {
			a.history = append(a.history, fmt.Sprintf("Error applying writes: %v", err))
		} else {
			var paths []string
			for _, fw := range selected.ProposedWrites {
				paths = append(paths, fw.Path)
			}
			a.history = append(a.history, fmt.Sprintf("Applied: %s", strings.Join(paths, ", ")))
		}
	}

	if err := preferences.Record(a.prefPath, a.lastPrompt, selected.ModelID, a.sessionID, a.responses); err != nil {
		a.history = append(a.history, fmt.Sprintf("Warning: could not save preference: %v", err))
	}

	a.responses = nil
	a.mode = modeIdle
	return a, nil
}

// ---- view ----

func (a App) View() string {
	var sb strings.Builder

	// header
	headerStyle := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color("#00AFAF"))
	sb.WriteString(headerStyle.Render("  Errata  A/B testing tool for agentic AI models"))
	sb.WriteString("\n\n")

	switch a.mode {
	case modeRunning:
		sb.WriteString(renderPanelRow(a.panels, a.width))
		sb.WriteByte('\n')

	case modeSelecting:
		sb.WriteString(RenderDiffs(a.responses))
		sb.WriteString(RenderSelectionMenu(a.responses))
		sb.WriteString("\nchoice> ")
		sb.WriteString(a.selection)

	case modeIdle:
		// show recent history (last 10 entries)
		start := 0
		if len(a.history) > 10 {
			start = len(a.history) - 10
		}
		for _, h := range a.history[start:] {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(h))
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
		sb.WriteString(a.input.View())
	}

	return sb.String()
}

func helpText() string {
	return `Commands:
  /help     Show this message
  /verbose  Toggle verbose mode
  /models   List active models
  /exit     Exit`
}

// Run starts the bubbletea program and blocks until exit.
func Run(adapters []models.ModelAdapter, prefPath, sessionID string, warnings []string) error {
	app := New(adapters, prefPath, sessionID)

	p := tea.NewProgram(app, tea.WithAltScreen())
	app.SetProgram(p)

	// print warnings to stderr before TUI starts
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	_, err := p.Run()
	return err
}
