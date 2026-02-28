package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/suarezc/errata/internal/commands"
)

func (a App) handleIdleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	// Config overlay captures all keystrokes.
	if a.configOverlayActive {
		return a.handleConfigKey(msg)
	}
	// Search mode captures all keystrokes.
	if a.searchActive {
		return a.handleSearchKey(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlD, tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyCtrlO:
		return a.toggleExpandLastRun()

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
			// Try argument completion first (e.g. /model gpt<TAB>).
			if completed, ok := a.tryArgComplete(val); ok {
				a.input.SetValue(completed)
				a.input.CursorEnd()
				return a, nil
			}
			// Fall back to command-name completion.
			prefix := strings.ToLower(strings.SplitN(val, " ", 2)[0])
			for _, c := range commands.All {
				if strings.HasPrefix(c.Name, prefix) {
					a.input.SetValue(c.Name + " ")
					a.input.CursorEnd()
					return a, nil
				}
			}
		}
		// Try @mention completion (e.g. @gpt<TAB>).
		if completed, ok := a.tryMentionComplete(val); ok {
			a.input.SetValue(completed)
			a.input.CursorEnd()
			return a, nil
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
func (a App) historyBack() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
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
func (a App) historyForward() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
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
func (a App) searchResults() []string { //nolint:gocritic // called from bubbletea value-receiver methods
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
func (a App) currentSearchResult() string { //nolint:gocritic // called from bubbletea value-receiver methods
	r := a.searchResults()
	if len(r) == 0 || a.searchResultIdx >= len(r) {
		return ""
	}
	return r[a.searchResultIdx]
}

// toggleExpandLastRun finds the most recent completed run and toggles its panels
// between expanded and collapsed views.
func (a App) toggleExpandLastRun() (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	for i := len(a.feed) - 1; i >= 0; i-- {
		item := &a.feed[i]
		if item.kind != "run" || len(item.panels) == 0 {
			continue
		}
		allDone := true
		for _, p := range item.panels {
			if !p.done {
				allDone = false
				break
			}
		}
		if !allDone {
			continue
		}
		newState := !item.panels[0].expanded
		for _, p := range item.panels {
			p.expanded = newState
		}
		return a.withFeedRebuilt(false), nil
	}
	return a, nil
}

// handleSearchKey processes keypresses while Ctrl-R search is active.
func (a App) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
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
