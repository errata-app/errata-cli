package ui

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/errata-app/errata-cli/internal/datastore"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/tools"
)

// handleRatingKey handles y/n/s input in modeRating (single-model response).
// y = thumbs up (records a preference win), n = thumbs down (skipped, no record), s = skip.
func (a App) handleRatingKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	setNote := func(note string) {
		if len(a.feed) > 0 {
			a.feed[len(a.feed)-1].note = note
		}
		a.store.UpdateLastRunNote(note)
	}

	// Ctrl combos.
	if msg.Mod.Contains(tea.ModCtrl) {
		switch msg.Code {
		case 'd', 'c':
			return a, tea.Quit
		}
	}

	if len(msg.Text) > 0 {
		switch strings.ToLower(msg.Text) {
		case "y":
			// Find the single OK response and record it as the winner.
			for _, resp := range a.responses {
				if resp.OK() {
					a.store.RecordSelection(datastore.SelectionParams{
						Prompt:          a.lastPrompt,
						SelectedModelID: resp.ModelID,
						Responses:       a.responses,
						Rating:          "good",
					})
					setNote(fmt.Sprintf("Rated good: %s", resp.ModelID))
					break
				}
			}
			a.responses = nil
			a.mode = modeIdle
			return a, nil

		case "n":
			for _, resp := range a.responses {
				if resp.OK() {
					a.store.RecordSelection(datastore.SelectionParams{
						Prompt:          a.lastPrompt,
						SelectedModelID: resp.ModelID,
						Responses:       a.responses,
						Rating:          "bad",
					})
					setNote(fmt.Sprintf("Rated bad: %s", resp.ModelID))
					break
				}
			}
			a.responses = nil
			a.mode = modeIdle
			return a, nil

		case "s":
			setNote("Skipped.")
	
			a.responses = nil
			a.mode = modeIdle
			return a, nil
		}
	}
	return a, nil
}

func (a App) handleSelectKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	// Ctrl combos.
	if msg.Mod.Contains(tea.ModCtrl) {
		switch msg.Code {
		case 'd', 'c':
			return a, tea.Quit
		}
	}

	switch msg.Code {

	case tea.KeyEnter:
		choice := strings.TrimSpace(a.selection)
		a.selection = ""
		return a.applySelection(choice)

	case tea.KeyBackspace, tea.KeyDelete:
		if len(a.selection) > 0 {
			a.selection = a.selection[:len(a.selection)-1]
		}
		a.selectionErr = ""

	default:
		if len(msg.Text) > 0 {
			a.selection += msg.Text
			a.selectionErr = ""
		}
	}
	return a, nil
}

func (a App) applySelection(choice string) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	setNote := func(note string) {
		if len(a.feed) > 0 {
			a.feed[len(a.feed)-1].note = note
		}
		a.store.UpdateLastRunNote(note)
	}

	if strings.EqualFold(choice, "s") {
		setNote("Skipped.")

		a.responses = nil
		a.mode = modeIdle
		return a, nil
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

	var appliedPaths []string
	if len(selected.ProposedWrites) == 0 {
		setNote(fmt.Sprintf("Voted for: %s", selected.ModelID))
	} else {
		// Snapshot files before overwriting for /rewind support.
		snaps, snapErr := tools.SnapshotFiles(selected.ProposedWrites)
		if snapErr != nil {
			log.Printf("warning: could not snapshot files for rewind: %v", snapErr)
		}

		if err := tools.ApplyWrites(selected.ProposedWrites); err != nil {
			setNote(fmt.Sprintf("Error applying writes: %v", err))
		} else {
			for _, fw := range selected.ProposedWrites {
				appliedPaths = append(appliedPaths, fw.Path)
			}
			setNote(fmt.Sprintf("Applied: %s", strings.Join(appliedPaths, ", ")))
			a.store.PushFileSnapshots(snaps)
		}
	}

	a.store.RecordSelection(datastore.SelectionParams{
		Prompt:          a.lastPrompt,
		SelectedModelID: selected.ModelID,
		Responses:       a.responses,
		AppliedFiles:    appliedPaths,
	})

	a.responses = nil
	a.mode = modeIdle
	return a, nil
}
