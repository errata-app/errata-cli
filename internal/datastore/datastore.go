// Package datastore provides a unified data layer for session-scoped persistence.
// It composes existing data packages (history, prompthistory, runner) and serves
// as the authoritative source for conversation histories, prompt history, session
// state, rewind stack, checkpoint, and cost tracking within a session.
package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/session"
	"github.com/suarezc/errata/internal/tools"
)

// RewindEntry captures enough state to undo one run.
type RewindEntry struct {
	FileSnapshots  []tools.FileSnapshot // nil if no writes were applied
	HistoryLengths map[string]int       // per-adapter history len BEFORE AppendHistory
	FeedIndex      int                  // index into the UI feed for annotation
	Prompt         string               // original prompt (for RecordRewind)
}

// RewindResult is returned by Rewind with information the UI needs to update display.
type RewindResult struct {
	FileMsg   string // warning from file restore, or ""
	FeedIndex int    // index into the UI feed for annotation
	Note      string // "[rewound]" or "[rewound] Applied: ..."
}

// Store is the single source of truth for persisted and session-scoped data.
// It owns the in-memory state and persists on mutation.
type Store struct {
	histPath       string
	promptHistPath string

	// Per-model conversation history; keyed by adapter ID.
	histories map[string][]models.ConversationTurn

	// Prompt history (newest-first in memory, oldest-first on disk).
	promptHist []string

	// Session persistence paths and state.
	checkpointPath    string
	feedPath          string
	sessionMetaPath   string
	sessionRecipePath string
	sessionID         string
	prefPath          string
	sessionMeta       session.Meta
	sessionFeed       []session.FeedEntry

	// Rewind stack — each entry captures state for one undo step.
	rewindStack []RewindEntry

	// Cost tracking.
	costPerModel map[string]float64
	totalCost    float64
}

// Options configures a new Store.
type Options struct {
	HistoryPath    string // path to session history.json
	PromptHistPath string // path to global prompt_history.jsonl
	SessionPaths   session.Paths
	SessionID      string
	PrefPath       string
	Meta           session.Meta
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
		histPath:          opts.HistoryPath,
		promptHistPath:    opts.PromptHistPath,
		histories:         h,
		promptHist:        ph,
		checkpointPath:    opts.SessionPaths.CheckpointPath,
		feedPath:          opts.SessionPaths.FeedPath,
		sessionMetaPath:   opts.SessionPaths.MetaPath,
		sessionRecipePath: opts.SessionPaths.RecipePath,
		sessionID:         opts.SessionID,
		prefPath:          opts.PrefPath,
		sessionMeta:       opts.Meta,
		costPerModel:      make(map[string]float64),
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

// ── Session State ───────────────────────────────────────────────────────────

// SessionMeta returns the current session metadata.
func (s *Store) SessionMeta() session.Meta { return s.sessionMeta }

// SessionFeed returns the session feed entries (for resume replay).
func (s *Store) SessionFeed() []session.FeedEntry { return s.sessionFeed }

// SetSessionFeed replaces the session feed (used on resume).
func (s *Store) SetSessionFeed(entries []session.FeedEntry) {
	s.sessionFeed = entries
}

// SaveInitialMeta persists the initial session metadata to disk.
func (s *Store) SaveInitialMeta() {
	if err := session.SaveMeta(s.sessionMetaPath, s.sessionMeta); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
	}
}

// PersistRunState updates session metadata, appends a feed entry, and persists
// the session recipe. Called from the run-complete handler.
func (s *Store) PersistRunState(lastPrompt string, responses []models.ModelResponse, rec *recipe.Recipe) {
	now := time.Now()
	s.sessionMeta.LastActiveAt = now
	s.sessionMeta.PromptCount++
	if s.sessionMeta.FirstPrompt == "" {
		s.sessionMeta.FirstPrompt = truncateStr(lastPrompt, 120)
	}
	s.sessionMeta.LastPrompt = truncateStr(lastPrompt, 120)

	if err := session.SaveMeta(s.sessionMetaPath, s.sessionMeta); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
	}

	entry := BuildFeedEntry(lastPrompt, responses)
	s.sessionFeed = append(s.sessionFeed, entry)
	if err := session.SaveFeed(s.feedPath, s.sessionFeed); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session feed: %v\n", err)
	}

	s.persistSessionRecipe(rec)
}

// UpdateLastFeedNote updates the note on the most recent session feed entry
// and re-saves feed.json.
func (s *Store) UpdateLastFeedNote(note string) {
	if len(s.sessionFeed) == 0 {
		return
	}
	s.sessionFeed[len(s.sessionFeed)-1].Note = note
	if err := session.SaveFeed(s.feedPath, s.sessionFeed); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session feed: %v\n", err)
	}
}

// persistSessionRecipe writes the recipe to the per-session recipe.md.
func (s *Store) persistSessionRecipe(rec *recipe.Recipe) {
	if s.sessionRecipePath == "" || rec == nil {
		return
	}
	md := rec.MarshalMarkdown()
	_ = os.MkdirAll(filepath.Dir(s.sessionRecipePath), 0o750)
	_ = os.WriteFile(s.sessionRecipePath, []byte(md), 0o600)
}

// BuildFeedEntry creates a session.FeedEntry from a prompt and responses.
func BuildFeedEntry(prompt string, responses []models.ModelResponse) session.FeedEntry {
	entry := session.FeedEntry{Kind: "run", Prompt: prompt}
	for _, resp := range responses {
		me := session.ModelEntry{ID: resp.ModelID, Text: truncateStr(resp.Text, 500)}
		for _, fw := range resp.ProposedWrites {
			me.ProposedFiles = append(me.ProposedFiles, fw.Path)
		}
		entry.Models = append(entry.Models, me)
	}
	return entry
}

// truncateStr limits s to maxLen runes.
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

// ── Rewind ──────────────────────────────────────────────────────────────────

// PushRewindEntry pushes a new rewind entry onto the stack.
func (s *Store) PushRewindEntry(entry RewindEntry) {
	s.rewindStack = append(s.rewindStack, entry)
}

// PushFileSnapshots stores file snapshots on the top rewind entry (called
// after applySelection writes files).
func (s *Store) PushFileSnapshots(snaps []tools.FileSnapshot) {
	if len(s.rewindStack) > 0 && snaps != nil {
		s.rewindStack[len(s.rewindStack)-1].FileSnapshots = snaps
	}
}

// CanRewind returns true if the rewind stack is non-empty.
func (s *Store) CanRewind() bool { return len(s.rewindStack) > 0 }

// ClearRewindStack empties the rewind stack (called on /clear, /wipe, /compact).
func (s *Store) ClearRewindStack() { s.rewindStack = nil }

// Rewind pops the top rewind entry, restores files, truncates histories,
// records a rewind preference, and annotates the session feed.
// Returns a RewindResult for the UI to update display.
func (s *Store) Rewind() (RewindResult, error) {
	if len(s.rewindStack) == 0 {
		return RewindResult{}, fmt.Errorf("nothing to rewind")
	}

	// Pop entry.
	entry := s.rewindStack[len(s.rewindStack)-1]
	s.rewindStack = s.rewindStack[:len(s.rewindStack)-1]

	// Restore files.
	var fileMsg string
	if len(entry.FileSnapshots) > 0 {
		if err := tools.RestoreSnapshots(entry.FileSnapshots); err != nil {
			fmt.Fprintf(os.Stderr, "warning: rewind file restore error: %v\n", err)
			fileMsg = fmt.Sprintf(" (file restore warning: %v)", err)
		}
	}

	// Truncate conversation histories to pre-run lengths.
	s.TruncateHistories(entry.HistoryLengths)

	// Record rewind marker in preferences.
	if err := preferences.RecordRewind(s.prefPath, entry.Prompt, s.sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to record rewind preference: %v\n", err)
	}

	// Build note and annotate session feed.
	var note string
	if entry.FeedIndex >= 0 && entry.FeedIndex < len(s.sessionFeed) {
		existing := s.sessionFeed[entry.FeedIndex].Note
		if existing != "" {
			note = "[rewound] " + existing
		} else {
			note = "[rewound]"
		}
		s.sessionFeed[entry.FeedIndex].Note = note
		if err := session.SaveFeed(s.feedPath, s.sessionFeed); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save session feed: %v\n", err)
		}
	}

	return RewindResult{
		FileMsg:   fileMsg,
		FeedIndex: entry.FeedIndex,
		Note:      note,
	}, nil
}

// ── Checkpoint ──────────────────────────────────────────────────────────────

// CheckpointPath returns the path used for checkpoint files.
func (s *Store) CheckpointPath() string { return s.checkpointPath }

// LoadCheckpoint loads the checkpoint from disk. Returns (nil, nil) if not found.
func (s *Store) LoadCheckpoint() (*checkpoint.Checkpoint, error) {
	return checkpoint.Load(s.checkpointPath)
}

// ClearCheckpoint removes the checkpoint file.
func (s *Store) ClearCheckpoint() {
	_ = os.Remove(s.checkpointPath)
}

// ── Cost Tracking ───────────────────────────────────────────────────────────

// AccumulateCost adds cost for a model run.
func (s *Store) AccumulateCost(modelID string, cost float64) {
	s.costPerModel[modelID] += cost
	s.totalCost += cost
}

// TotalCost returns the cumulative cost across all runs this session.
func (s *Store) TotalCost() float64 { return s.totalCost }

// CostPerModel returns the per-model cumulative cost this session.
func (s *Store) CostPerModel() map[string]float64 { return s.costPerModel }

// PrefPath returns the preferences file path.
func (s *Store) PrefPath() string { return s.prefPath }

// SessionID returns the session ID.
func (s *Store) SessionID() string { return s.sessionID }

// FeedPath returns the session feed file path (used by Run for resume loading).
func (s *Store) FeedPath() string { return s.feedPath }

// SessionMetaPath returns the path to session meta.json (used by tests).
func (s *Store) SessionMetaPath() string { return s.sessionMetaPath }

// SessionRecipePath returns the path to session recipe.md (used by tests).
func (s *Store) SessionRecipePath() string { return s.sessionRecipePath }

// RewindStackLen returns the number of entries in the rewind stack (used by tests).
func (s *Store) RewindStackLen() int { return len(s.rewindStack) }
