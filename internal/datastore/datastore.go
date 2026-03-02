// Package datastore provides a unified data layer for session-scoped persistence.
// It composes existing data packages (history, prompthistory, runner) and serves
// as the authoritative source for conversation histories and prompt history within
// a session, consolidating scattered I/O that was previously spread across ui/*.go.
package datastore

import (
	"fmt"
	"os"

	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/internal/runner"
)

// Store is the single source of truth for persisted and session-scoped data.
// It owns the in-memory state and persists on mutation.
type Store struct {
	histPath       string
	promptHistPath string

	// Per-model conversation history; keyed by adapter ID.
	histories map[string][]models.ConversationTurn

	// Prompt history (newest-first in memory, oldest-first on disk).
	promptHist []string
}

// Options configures a new Store.
type Options struct {
	HistoryPath    string // path to session history.json
	PromptHistPath string // path to global prompt_history.jsonl
}

// New creates a Store, loading existing data from disk.
// Missing files are not errors — the store starts empty.
func New(opts Options) (*Store, error) {
	h, err := history.Load(opts.HistoryPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load history: %v\n", err)
	}

	ph, err := prompthistory.Load(opts.PromptHistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load prompt history: %v\n", err)
	}

	return &Store{
		histPath:       opts.HistoryPath,
		promptHistPath: opts.PromptHistPath,
		histories:      h,
		promptHist:     ph,
	}, nil
}

// ── History ─────────────────────────────────────────────────────────────────

// Histories returns the current conversation histories map.
// The caller must not mutate the returned map.
func (s *Store) Histories() map[string][]models.ConversationTurn {
	return s.histories
}

// AppendHistories appends conversation turns for a completed run and persists
// to disk. Returns the pre-append lengths (for rewind support).
func (s *Store) AppendHistories(
	panelIDs []string,
	responses []models.ModelResponse,
	userPrompt string,
) map[string]int {
	preLengths := make(map[string]int, len(panelIDs))
	for _, id := range panelIDs {
		preLengths[id] = len(s.histories[id])
	}
	s.histories = runner.AppendHistory(s.histories, panelIDs, responses, userPrompt)
	s.saveHistories()
	return preLengths
}

// SetHistories replaces the histories wholesale (used by compaction) and persists.
func (s *Store) SetHistories(h map[string][]models.ConversationTurn) {
	s.histories = h
	s.saveHistories()
}

// TruncateHistories restores per-adapter history to the given pre-run lengths
// (used by rewind) and persists.
func (s *Store) TruncateHistories(lengths map[string]int) {
	for adapterID, prevLen := range lengths {
		h := s.histories[adapterID]
		if prevLen < len(h) {
			s.histories[adapterID] = h[:prevLen]
		}
		if len(s.histories[adapterID]) == 0 {
			delete(s.histories, adapterID)
		}
	}
	s.saveHistories()
}

// ClearHistories wipes all conversation history and removes the file from disk.
func (s *Store) ClearHistories() {
	s.histories = nil
	if err := history.Clear(s.histPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not clear history: %v\n", err)
	}
}

// saveHistories persists the current histories to disk.
func (s *Store) saveHistories() {
	if err := history.Save(s.histPath, s.histories); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
	}
}

// ── Prompt History ──────────────────────────────────────────────────────────

// PromptHistory returns the prompt history (newest-first).
// The caller must not mutate the returned slice.
func (s *Store) PromptHistory() []string {
	return s.promptHist
}

// RecordPrompt adds a prompt to history if it differs from the most recent entry,
// persists to disk, and returns whether the prompt was actually added.
func (s *Store) RecordPrompt(prompt string) bool {
	if len(s.promptHist) > 0 && s.promptHist[0] == prompt {
		return false
	}
	s.promptHist = append([]string{prompt}, s.promptHist...)
	if err := prompthistory.Append(s.promptHistPath, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save prompt history: %v\n", err)
	}
	return true
}
