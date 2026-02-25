package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/tools"
)

// handleRatingKey handles y/n/s input in modeRating (single-model response).
// y = thumbs up (records a preference win), n = thumbs down (skipped, no record), s = skip.
func (a App) handleRatingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	setNote := func(note string) {
		if len(a.feed) > 0 {
			a.feed[len(a.feed)-1].note = note
		}
	}

	switch msg.Type {
	case tea.KeyCtrlD, tea.KeyCtrlC:
		return a, tea.Quit

	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
		var cmd tea.Cmd
		a.feedVP, cmd = a.feedVP.Update(msg)
		return a, cmd

	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "y", "Y":
			// Find the single OK response and record it as the winner.
			for _, resp := range a.responses {
				if resp.OK() {
					_ = preferences.Record(a.prefPath, a.lastPrompt, resp.ModelID, a.sessionID, a.responses)
					setNote(fmt.Sprintf("Rated good: %s", resp.ModelID))
					break
				}
			}
			a.responses = nil
			a.mode = modeIdle
			return a.withFeedRebuilt(true), nil

		case "n", "N":
			for _, resp := range a.responses {
				if resp.OK() {
					_ = preferences.RecordBad(a.prefPath, a.lastPrompt, resp.ModelID, a.sessionID, a.responses)
					setNote(fmt.Sprintf("Rated bad: %s", resp.ModelID))
					break
				}
			}
			a.responses = nil
			a.mode = modeIdle
			return a.withFeedRebuilt(true), nil

		case "s", "S":
			setNote("Skipped.")
			a.responses = nil
			a.mode = modeIdle
			return a.withFeedRebuilt(true), nil
		}
	}
	return a, nil
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
		setNote(fmt.Sprintf("Voted for: %s", selected.ModelID))
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
