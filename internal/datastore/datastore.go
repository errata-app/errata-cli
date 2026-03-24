// Package datastore provides a unified data layer for session-scoped persistence.
// It composes existing data packages and serves as the authoritative source for
// conversation histories, prompt history, session state, rewind stack, checkpoint,
// and cost tracking within a session.
package datastore

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/errata-app/errata-cli/internal/checkpoint"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
	"github.com/errata-app/errata-cli/internal/prompthistory"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/pkg/recipestore"
	"github.com/errata-app/errata-cli/internal/runner"
	"github.com/errata-app/errata-cli/internal/session"
	"github.com/errata-app/errata-cli/internal/tools"
)

// RewindEntry captures enough state to undo one run.
type RewindEntry struct {
	FileSnapshots  []tools.FileSnapshot // nil if no writes were applied
	HistoryLengths map[string]int       // per-adapter history len BEFORE AppendHistory
	FeedIndex      int                  // index into the UI feed for annotation
	Prompt         string               // original prompt (for rewind marker)
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
	promptHistPath string

	// Per-model conversation history; keyed by adapter ID.
	histories map[string][]models.ConversationTurn

	// Prompt history (newest-first in memory, oldest-first on disk).
	promptHist []string

	// Session persistence paths and state.
	metadataPath      string
	contentPath       string
	checkpointPath    string
	sessionRecipePath string
	sessionID         string
	metadata          session.SessionMetadata
	content           session.SessionContent

	// Rewind stack — each entry captures state for one undo step.
	rewindStack []RewindEntry

	// Cost tracking.
	costPerModel map[string]float64
	totalCost    float64

	// Recipe state.
	baseRecipe    *recipe.Recipe // immutable base recipe from startup
	sessionRecipe *recipe.Recipe // working copy; nil until first /config or /load

	// Recipe store for content-addressed config snapshots.
	recipeStore *recipestore.Store

	// Active tool names from last run (for recipe snapshot).
	lastActiveTools []string
}

// Options configures a new Store.
type Options struct {
	PromptHistPath string // path to global prompt_history.jsonl
	SessionPaths   session.Paths
	SessionID      string
	Meta           session.SessionMetadata
	RecipeStore    *recipestore.Store
	Recipe         *recipe.Recipe // base recipe loaded at startup
}

// New creates a Store, loading existing data from disk.
// Missing files are not errors — the store starts empty.
func New(opts Options) (*Store, error) {
	// Load content (includes histories).
	c, err := session.LoadContent(opts.SessionPaths.ContentPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load session content: %v\n", err)
	}
	if c == nil {
		c = &session.SessionContent{}
	}

	ph, err := prompthistory.Load(opts.PromptHistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load prompt history: %v\n", err)
	}

	return &Store{
		promptHistPath:    opts.PromptHistPath,
		histories:         c.Histories,
		promptHist:        ph,
		metadataPath:      opts.SessionPaths.MetadataPath,
		contentPath:       opts.SessionPaths.ContentPath,
		checkpointPath:    opts.SessionPaths.CheckpointPath,
		sessionRecipePath: opts.SessionPaths.RecipePath,
		sessionID:         opts.SessionID,
		metadata:          opts.Meta,
		content:           *c,
		costPerModel:      make(map[string]float64),
		baseRecipe:        opts.Recipe,
		recipeStore:       opts.RecipeStore,
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
	s.saveContent()
	return preLengths
}

// SetHistories replaces the histories wholesale (used by compaction) and persists.
func (s *Store) SetHistories(h map[string][]models.ConversationTurn) {
	s.histories = h
	s.saveContent()
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
	s.saveContent()
}

// ClearHistories wipes all conversation history and persists.
func (s *Store) ClearHistories() {
	s.histories = nil
	s.content.Histories = nil
	s.saveContent()
}

// saveContent persists the current content (histories + runs) to disk.
func (s *Store) saveContent() {
	s.content.Histories = s.histories
	if err := session.SaveContent(s.contentPath, s.content); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session content: %v\n", err)
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

// Metadata returns the current session metadata.
func (s *Store) Metadata() session.SessionMetadata { return s.metadata }

// Content returns the current session content.
func (s *Store) Content() session.SessionContent { return s.content }

// SaveInitialMeta persists the initial session metadata to disk.
func (s *Store) SaveInitialMeta() {
	if err := session.SaveMetadata(s.metadataPath, s.metadata); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
	}
}

// PersistRunState updates session metadata with a RunSummary, appends a
// RunContent to content, and persists both files plus the session recipe.
func (s *Store) PersistRunState(
	lastPrompt string,
	responses []models.ModelResponse,
	collector *output.Collector,
	toolNames []string,
) {
	rec := s.ActiveRecipe()
	now := time.Now()

	s.lastActiveTools = toolNames

	// Update metadata header fields.
	s.metadata.LastActiveAt = now
	s.metadata.PromptCount++
	if s.metadata.FirstPrompt == "" {
		s.metadata.FirstPrompt = truncateStr(lastPrompt, 120)
	}
	s.metadata.LastPrompt = truncateStr(lastPrompt, 120)

	// Build RunSummary.
	hash := sha256.Sum256([]byte(lastPrompt))
	preview := lastPrompt
	if len(preview) > 120 {
		preview = preview[:120]
	}

	modelIDs := make([]string, len(responses))
	latencies := make(map[string]int64, len(responses))
	costs := make(map[string]float64, len(responses))
	inputTokens := make(map[string]int64, len(responses))
	outputTokens := make(map[string]int64, len(responses))
	toolCallsMap := make(map[string]map[string]int, len(responses))
	proposedWritesCount := make(map[string]int, len(responses))
	for i, r := range responses {
		modelIDs[i] = r.ModelID
		latencies[r.ModelID] = r.LatencyMS
		costs[r.ModelID] = r.CostUSD
		inputTokens[r.ModelID] = r.InputTokens
		outputTokens[r.ModelID] = r.OutputTokens
		toolCallsMap[r.ModelID] = r.ToolCalls
		proposedWritesCount[r.ModelID] = len(r.ProposedWrites)
	}

	configHash := s.RecipeHash()

	rs := session.RunSummary{
		Timestamp:           now,
		PromptHash:          fmt.Sprintf("ph_%x", hash),
		PromptPreview:       preview,
		Models:              modelIDs,
		LatenciesMS:         latencies,
		CostsUSD:            costs,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		ToolCalls:           toolCallsMap,
		ProposedWritesCount: proposedWritesCount,
		ConfigHash:          configHash,
	}
	s.metadata.Runs = append(s.metadata.Runs, rs)

	if err := session.SaveMetadata(s.metadataPath, s.metadata); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
	}

	// Build RunContent.
	rc := session.RunContent{Prompt: lastPrompt}
	for _, resp := range responses {
		var writes []output.WriteEntry
		for _, fw := range resp.ProposedWrites {
			writes = append(writes, output.WriteEntry{Path: fw.Path, Content: fw.Content, Delete: fw.Delete})
		}

		var events []output.EventEntry
		if collector != nil {
			events = collector.Events(resp.ModelID)
		}
		if events == nil {
			events = []output.EventEntry{}
		}

		rc.Models = append(rc.Models, session.ModelRunContent{
			ModelID:         resp.ModelID,
			Text:            resp.Text,
			ProposedWrites:  writes,
			Events:          events,
			StopReason:      string(resp.StopReason),
			Steps:           resp.Steps,
			ReasoningTokens: resp.ReasoningTokens,
		})
	}
	s.content.Runs = append(s.content.Runs, rc)
	s.saveContent()

	s.persistSessionRecipe(rec)
}

// UpdateLastRunNote updates the note on the most recent RunSummary in metadata
// and re-saves session_metadata.json.
func (s *Store) UpdateLastRunNote(note string) {
	if len(s.metadata.Runs) == 0 {
		return
	}
	s.metadata.Runs[len(s.metadata.Runs)-1].Note = note
	if err := session.SaveMetadata(s.metadataPath, s.metadata); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
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
// records a rewind marker in metadata, and annotates the run note.
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

	// Append rewind marker to metadata runs.
	hash := sha256.Sum256([]byte(entry.Prompt))
	s.metadata.Runs = append(s.metadata.Runs, session.RunSummary{
		Timestamp:  time.Now(),
		PromptHash: fmt.Sprintf("ph_%x", hash),
		Type:       "rewind",
	})

	// Build note and annotate the original run in metadata.
	var note string
	if entry.FeedIndex >= 0 && entry.FeedIndex < len(s.metadata.Runs) {
		existing := s.metadata.Runs[entry.FeedIndex].Note
		if existing != "" {
			note = "[rewound] " + existing
		} else {
			note = "[rewound]"
		}
		s.metadata.Runs[entry.FeedIndex].Note = note
	}

	if err := session.SaveMetadata(s.metadataPath, s.metadata); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session metadata: %v\n", err)
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

// SessionID returns the session ID.
func (s *Store) SessionID() string { return s.sessionID }

// MetadataPath returns the path to session_metadata.json.
func (s *Store) MetadataPath() string { return s.metadataPath }

// ContentPath returns the path to session_content.json.
func (s *Store) ContentPath() string { return s.contentPath }

// SessionRecipePath returns the path to session recipe.md (used by tests).
func (s *Store) SessionRecipePath() string { return s.sessionRecipePath }

// RewindStackLen returns the number of entries in the rewind stack (used by tests).
func (s *Store) RewindStackLen() int { return len(s.rewindStack) }

// SessionsDir returns the base sessions directory (data/sessions/).
func (s *Store) SessionsDir() string {
	// metadataPath is data/sessions/{id}/session_metadata.json
	return filepath.Dir(filepath.Dir(s.metadataPath))
}

// RecipesDir returns the recipes directory (data/recipes/).
func (s *Store) RecipesDir() string {
	return filepath.Join(filepath.Dir(s.SessionsDir()), "recipes")
}

// RecipeNameLookup returns a function that resolves a config hash to a recipe name.
func (s *Store) RecipeNameLookup() func(string) string {
	return func(hash string) string {
		if s.recipeStore == nil {
			return ""
		}
		if snap := s.recipeStore.Get(hash); snap != nil {
			return snap.Name
		}
		return ""
	}
}

// RecipeStore returns the recipe store (for /stats filter).
func (s *Store) RecipeStore() *recipestore.Store { return s.recipeStore }

// LastActiveTools returns the tool names from the last run.
func (s *Store) LastActiveTools() []string { return s.lastActiveTools }

// SetLastActiveTools sets the last active tool names (used after run complete).
func (s *Store) SetLastActiveTools(names []string) { s.lastActiveTools = names }

// ── Recipe State ────────────────────────────────────────────────────────────

// BaseRecipe returns the immutable base recipe from startup.
func (s *Store) BaseRecipe() *recipe.Recipe { return s.baseRecipe }

// SessionRecipe returns the working copy of the recipe (nil if unmodified).
func (s *Store) SessionRecipe() *recipe.Recipe { return s.sessionRecipe }

// SetSessionRecipe sets the working copy of the recipe.
func (s *Store) SetSessionRecipe(r *recipe.Recipe) { s.sessionRecipe = r }

// ActiveRecipe returns the session recipe if set, otherwise the base recipe.
func (s *Store) ActiveRecipe() *recipe.Recipe {
	if s.sessionRecipe != nil {
		return s.sessionRecipe
	}
	return s.baseRecipe
}

// ── Recipe Snapshot ─────────────────────────────────────────────────────────

// BuildRecipeSnapshot creates a RecipeSnapshot from the active recipe and
// the last active tools.
func (s *Store) BuildRecipeSnapshot() *recipestore.RecipeSnapshot {
	rec := s.ActiveRecipe()
	snap := &recipestore.RecipeSnapshot{Name: "default"}
	if rec != nil {
		snap.Version = rec.Version
		snap.Name = rec.Name
		if snap.Name == "" {
			snap.Name = "default"
		}
		snap.SystemPrompt = rec.SystemPrompt
		if rec.Tools != nil {
			snap.ToolGuidance = rec.Tools.Guidance
		}
		snap.ToolDescriptions = rec.ToolDescriptions
		snap.SummarizationPrompt = rec.SummarizationPrompt

		if rec.Tools != nil {
			snap.BashPrefixes = rec.Tools.BashPrefixes
		}

		if rec.Constraints.MaxSteps > 0 || rec.Constraints.Timeout > 0 {
			snap.Constraints = &recipestore.ConstraintsConfig{
				MaxSteps: rec.Constraints.MaxSteps,
			}
			if rec.Constraints.Timeout > 0 {
				snap.Constraints.Timeout = rec.Constraints.Timeout.String()
			}
		}

		if rec.Context.MaxHistoryTurns > 0 || rec.Context.Strategy != "" ||
			rec.Context.TaskMode != "" {
			snap.Context = &recipestore.ContextConfig{
				MaxHistoryTurns: rec.Context.MaxHistoryTurns,
				Strategy:        rec.Context.Strategy,
				TaskMode:        rec.Context.TaskMode,
			}
		}

		if len(rec.OutputProcessing) > 0 {
			snap.OutputProcessing = make(map[string]recipestore.OutputRuleConfig, len(rec.OutputProcessing))
			for name, rule := range rec.OutputProcessing {
				snap.OutputProcessing[name] = recipestore.OutputRuleConfig{
					MaxLines:          rule.MaxLines,
					MaxTokens:         rule.MaxTokens,
					Truncation:        rule.Truncation,
					TruncationMessage: rule.TruncationMessage,
				}
			}
		}

		if len(rec.ModelProfiles) > 0 {
			snap.ModelProfiles = make(map[string]recipestore.ModelProfileConfig, len(rec.ModelProfiles))
			for name, p := range rec.ModelProfiles {
				snap.ModelProfiles[name] = recipestore.ModelProfileConfig{
					ContextBudget: p.ContextBudget,
				}
			}
		}
	}

	// Populate tools from the last run's active tool list.
	snap.Tools = s.lastActiveTools

	return snap
}

// RecipeHash computes a content-addressed hash for the active recipe snapshot.
// Returns "" if no recipe store is configured.
func (s *Store) RecipeHash() string {
	if s.recipeStore == nil {
		return ""
	}
	snap := s.BuildRecipeSnapshot()
	return s.recipeStore.Put(snap)
}

// ── Selection / Rating Recording ────────────────────────────────────────────

// SelectionParams captures the information needed to record a selection or rating.
type SelectionParams struct {
	Prompt          string
	SelectedModelID string
	Responses       []models.ModelResponse
	AppliedFiles    []string // nil for text-only votes / ratings
	Rating          string   // "" for selection, "good" or "bad" for rating
}

// RecordSelection updates the last RunSummary in metadata with the selection outcome.
func (s *Store) RecordSelection(p SelectionParams) {
	if len(s.metadata.Runs) == 0 {
		return
	}
	last := &s.metadata.Runs[len(s.metadata.Runs)-1]
	last.Selected = p.SelectedModelID
	last.Rating = p.Rating
	if len(p.AppliedFiles) > 0 {
		last.AppliedFiles = p.AppliedFiles
	}

	if err := session.SaveMetadata(s.metadataPath, s.metadata); err != nil {
		log.Printf("warning: could not save session metadata: %v", err)
	}
}
