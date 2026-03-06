package datastore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/prompthistory"
	"github.com/suarezc/errata/pkg/recipe"
	"github.com/suarezc/errata/internal/session"
	"github.com/suarezc/errata/internal/tools"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	tmp := t.TempDir()
	s, err := New(Options{
		HistoryPath:    filepath.Join(tmp, "history.json"),
		PromptHistPath: filepath.Join(tmp, "prompt_history.jsonl"),
	})
	require.NoError(t, err)
	return s
}

// tempStoreWithSession creates a Store with full session paths configured.
func tempStoreWithSession(t *testing.T) *Store {
	t.Helper()
	tmp := t.TempDir()
	sessDir := filepath.Join(tmp, "session")
	require.NoError(t, os.MkdirAll(sessDir, 0o750))
	sp := session.Paths{
		Dir:            sessDir,
		HistoryPath:    filepath.Join(sessDir, "history.json"),
		CheckpointPath: filepath.Join(sessDir, "checkpoint.json"),
		MetaPath:       filepath.Join(sessDir, "meta.json"),
		FeedPath:       filepath.Join(sessDir, "feed.json"),
		RecipePath:     filepath.Join(sessDir, "recipe.md"),
	}
	s, err := New(Options{
		HistoryPath:    sp.HistoryPath,
		PromptHistPath: filepath.Join(tmp, "prompt_history.jsonl"),
		SessionPaths:   sp,
		SessionID:      "test-session",
		PrefPath:       filepath.Join(tmp, "pref.jsonl"),
		Meta:           session.Meta{ID: "test-session"},
	})
	require.NoError(t, err)
	return s
}

// ── Construction ────────────────────────────────────────────────────────────

func TestNew_EmptyPaths(t *testing.T) {
	s := tempStore(t)
	assert.Nil(t, s.Histories())
	assert.Nil(t, s.PromptHistory())
}

func TestNew_LoadsExistingHistory(t *testing.T) {
	tmp := t.TempDir()
	histPath := filepath.Join(tmp, "history.json")
	h := map[string][]models.ConversationTurn{
		"m1": {
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
	}
	require.NoError(t, history.Save(histPath, h))

	s, err := New(Options{
		HistoryPath:    histPath,
		PromptHistPath: filepath.Join(tmp, "prompt.jsonl"),
	})
	require.NoError(t, err)
	require.Len(t, s.Histories()["m1"], 2)
	assert.Equal(t, "hello", s.Histories()["m1"][0].Content)
}

func TestNew_LoadsExistingPromptHistory(t *testing.T) {
	tmp := t.TempDir()
	phPath := filepath.Join(tmp, "prompt.jsonl")
	require.NoError(t, prompthistory.Append(phPath, "first"))
	require.NoError(t, prompthistory.Append(phPath, "second"))

	s, err := New(Options{
		HistoryPath:    filepath.Join(tmp, "history.json"),
		PromptHistPath: phPath,
	})
	require.NoError(t, err)
	// Newest-first.
	require.Len(t, s.PromptHistory(), 2)
	assert.Equal(t, "second", s.PromptHistory()[0])
	assert.Equal(t, "first", s.PromptHistory()[1])
}

// ── History: AppendHistories ────────────────────────────────────────────────

func TestAppendHistories_AppendsAndPersists(t *testing.T) {
	s := tempStore(t)

	preLens := s.AppendHistories(
		[]string{"m1", "m2"},
		[]models.ModelResponse{
			{ModelID: "m1", Text: "answer1"},
			{ModelID: "m2", Text: "answer2"},
		},
		"question",
	)

	// Pre-lengths should be 0 for both.
	assert.Equal(t, 0, preLens["m1"])
	assert.Equal(t, 0, preLens["m2"])

	// In-memory state should have 2 turns per model.
	assert.Len(t, s.Histories()["m1"], 2)
	assert.Len(t, s.Histories()["m2"], 2)
	assert.Equal(t, "user", s.Histories()["m1"][0].Role)
	assert.Equal(t, "question", s.Histories()["m1"][0].Content)
	assert.Equal(t, "assistant", s.Histories()["m1"][1].Role)
	assert.Equal(t, "answer1", s.Histories()["m1"][1].Content)

	// Disk should match.
	loaded, err := history.Load(s.histPath)
	require.NoError(t, err)
	assert.Len(t, loaded["m1"], 2)
	assert.Len(t, loaded["m2"], 2)
}

func TestAppendHistories_PreLengthsCapturePrior(t *testing.T) {
	s := tempStore(t)

	// First append.
	s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "a1"}},
		"q1",
	)

	// Second append — preLengths should reflect the first append.
	preLens := s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "a2"}},
		"q2",
	)
	assert.Equal(t, 2, preLens["m1"])
	assert.Len(t, s.Histories()["m1"], 4) // 2+2
}

func TestAppendHistories_ErrorResponseSkipped(t *testing.T) {
	s := tempStore(t)

	s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Error: "something broke"}},
		"question",
	)

	// Error responses should not add history turns.
	h := s.Histories()
	if turns, ok := h["m1"]; ok {
		assert.Empty(t, turns)
	}
}

// ── History: SetHistories ───────────────────────────────────────────────────

func TestSetHistories_ReplacesAndPersists(t *testing.T) {
	s := tempStore(t)
	s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "old"}},
		"old q",
	)

	compacted := map[string][]models.ConversationTurn{
		"m1": {
			{Role: "user", Content: "[compacted]"},
			{Role: "assistant", Content: "summary"},
		},
	}
	s.SetHistories(compacted)

	assert.Len(t, s.Histories()["m1"], 2)
	assert.Equal(t, "[compacted]", s.Histories()["m1"][0].Content)

	loaded, err := history.Load(s.histPath)
	require.NoError(t, err)
	assert.Len(t, loaded["m1"], 2)
}

// ── History: TruncateHistories ──────────────────────────────────────────────

func TestTruncateHistories_RestoresLengths(t *testing.T) {
	s := tempStore(t)

	// Append two runs.
	s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "a1"}},
		"q1",
	)
	preLens := s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "a2"}},
		"q2",
	)
	require.Len(t, s.Histories()["m1"], 4)

	// Truncate back to pre-second-run lengths.
	s.TruncateHistories(preLens)
	assert.Len(t, s.Histories()["m1"], 2)

	// Disk should match.
	loaded, err := history.Load(s.histPath)
	require.NoError(t, err)
	assert.Len(t, loaded["m1"], 2)
}

func TestTruncateHistories_DeletesEmptyKeys(t *testing.T) {
	s := tempStore(t)

	preLens := s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "a"}},
		"q",
	)

	s.TruncateHistories(preLens)
	_, exists := s.Histories()["m1"]
	assert.False(t, exists, "empty history key should be deleted")
}

// ── History: ClearHistories ─────────────────────────────────────────────────

func TestClearHistories_WipesMemoryAndDisk(t *testing.T) {
	s := tempStore(t)
	s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "a"}},
		"q",
	)
	require.NotNil(t, s.Histories())

	s.ClearHistories()

	assert.Nil(t, s.Histories())
	_, err := os.Stat(s.histPath)
	assert.True(t, os.IsNotExist(err))
}

// ── Prompt History: RecordPrompt ────────────────────────────────────────────

func TestRecordPrompt_AppendsAndPersists(t *testing.T) {
	s := tempStore(t)

	added := s.RecordPrompt("first prompt")
	assert.True(t, added)
	require.Len(t, s.PromptHistory(), 1)
	assert.Equal(t, "first prompt", s.PromptHistory()[0])

	added = s.RecordPrompt("second prompt")
	assert.True(t, added)
	require.Len(t, s.PromptHistory(), 2)
	assert.Equal(t, "second prompt", s.PromptHistory()[0])
	assert.Equal(t, "first prompt", s.PromptHistory()[1])

	// Verify disk persistence.
	loaded, err := prompthistory.Load(s.promptHistPath)
	require.NoError(t, err)
	assert.Len(t, loaded, 2)
	assert.Equal(t, "second prompt", loaded[0])
}

func TestRecordPrompt_DeduplicatesConsecutive(t *testing.T) {
	s := tempStore(t)

	s.RecordPrompt("same")
	added := s.RecordPrompt("same")
	assert.False(t, added)
	assert.Len(t, s.PromptHistory(), 1)
}

func TestRecordPrompt_AllowsNonConsecutiveDuplicates(t *testing.T) {
	s := tempStore(t)

	s.RecordPrompt("A")
	s.RecordPrompt("B")
	added := s.RecordPrompt("A")
	assert.True(t, added)
	assert.Len(t, s.PromptHistory(), 3)
	assert.Equal(t, "A", s.PromptHistory()[0])
}

// ── Round-trip: construct → mutate → reconstruct ────────────────────────────

func TestRoundTrip_HistoryPersistsAcrossInstances(t *testing.T) {
	tmp := t.TempDir()
	opts := Options{
		HistoryPath:    filepath.Join(tmp, "history.json"),
		PromptHistPath: filepath.Join(tmp, "prompt.jsonl"),
	}

	// First instance: append history.
	s1, err := New(opts)
	require.NoError(t, err)
	s1.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "answer"}},
		"question",
	)

	// Second instance: should see the history.
	s2, err := New(opts)
	require.NoError(t, err)
	require.Len(t, s2.Histories()["m1"], 2)
	assert.Equal(t, "question", s2.Histories()["m1"][0].Content)
	assert.Equal(t, "answer", s2.Histories()["m1"][1].Content)
}

func TestRoundTrip_PromptHistoryPersistsAcrossInstances(t *testing.T) {
	tmp := t.TempDir()
	opts := Options{
		HistoryPath:    filepath.Join(tmp, "history.json"),
		PromptHistPath: filepath.Join(tmp, "prompt.jsonl"),
	}

	s1, err := New(opts)
	require.NoError(t, err)
	s1.RecordPrompt("p1")
	s1.RecordPrompt("p2")

	s2, err := New(opts)
	require.NoError(t, err)
	require.Len(t, s2.PromptHistory(), 2)
	assert.Equal(t, "p2", s2.PromptHistory()[0])
	assert.Equal(t, "p1", s2.PromptHistory()[1])
}

// ── Checkpoint ──────────────────────────────────────────────────────────────

func TestClearCheckpoint_RemovesFile(t *testing.T) {
	s := tempStoreWithSession(t)
	require.NoError(t, os.WriteFile(s.CheckpointPath(), []byte(`{}`), 0o600))

	s.ClearCheckpoint()

	_, err := os.Stat(s.CheckpointPath())
	assert.True(t, os.IsNotExist(err))
}

func TestClearCheckpoint_MissingFileNoPanic(t *testing.T) {
	s := tempStoreWithSession(t)
	assert.NotPanics(t, func() {
		s.ClearCheckpoint()
	})
}

// ── PersistRunState ─────────────────────────────────────────────────────────

func TestPersistRunState_UpdatesMetadata(t *testing.T) {
	s := tempStoreWithSession(t)
	before := time.Now().Truncate(time.Second)

	responses := []models.ModelResponse{{ModelID: "m1", Text: "done"}}
	s.PersistRunState("fix bug", responses)

	meta := s.SessionMeta()
	assert.Equal(t, 1, meta.PromptCount)
	assert.Equal(t, "fix bug", meta.FirstPrompt)
	assert.Equal(t, "fix bug", meta.LastPrompt)
	assert.WithinDuration(t, before, meta.LastActiveAt, 2*time.Second)
}

func TestPersistRunState_SecondCallPreservesFirst(t *testing.T) {
	s := tempStoreWithSession(t)

	s.PersistRunState("first prompt", []models.ModelResponse{{ModelID: "m1", Text: "r1"}})
	s.PersistRunState("second prompt", []models.ModelResponse{{ModelID: "m1", Text: "r2"}})

	meta := s.SessionMeta()
	assert.Equal(t, 2, meta.PromptCount)
	assert.Equal(t, "first prompt", meta.FirstPrompt)
	assert.Equal(t, "second prompt", meta.LastPrompt)
}

func TestPersistRunState_MetaSavedToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("disk test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	loaded, err := session.LoadMeta(s.SessionMetaPath())
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, 1, loaded.PromptCount)
	assert.Equal(t, "disk test", loaded.FirstPrompt)
}

func TestPersistRunState_FeedSavedToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("feed test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	entries, err := session.LoadFeed(s.FeedPath())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "run", entries[0].Kind)
}

func TestPersistRunState_FeedAccumulates(t *testing.T) {
	s := tempStoreWithSession(t)

	s.PersistRunState("prompt 1", []models.ModelResponse{{ModelID: "m1", Text: "r1"}})
	s.PersistRunState("prompt 2", []models.ModelResponse{{ModelID: "m1", Text: "r2"}})

	assert.Len(t, s.SessionFeed(), 2)

	entries, err := session.LoadFeed(s.FeedPath())
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestPersistRunState_LongPromptTruncated(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState(strings.Repeat("x", 200), []models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	meta := s.SessionMeta()
	assert.LessOrEqual(t, len([]rune(meta.FirstPrompt)), 120)
	assert.LessOrEqual(t, len([]rune(meta.LastPrompt)), 120)
}

// ── persistSessionRecipe (tested through PersistRunState) ───────────────────

func TestPersistRunState_RecipeWrittenToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.SetSessionRecipe(&recipe.Recipe{Name: "session-recipe"})
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	data, err := os.ReadFile(s.SessionRecipePath())
	require.NoError(t, err)
	assert.Contains(t, string(data), "session-recipe")
}

func TestPersistRunState_NilRecipeNoFile(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	_, err := os.Stat(s.SessionRecipePath())
	assert.True(t, os.IsNotExist(err))
}

// ── UpdateLastFeedNote ──────────────────────────────────────────────────────

func TestUpdateLastFeedNote_EmptyFeedNoop(t *testing.T) {
	s := tempStoreWithSession(t)
	assert.NotPanics(t, func() {
		s.UpdateLastFeedNote("should not panic")
	})
	_, err := os.Stat(s.FeedPath())
	assert.True(t, os.IsNotExist(err))
}

func TestUpdateLastFeedNote_UpdatesLastEntry(t *testing.T) {
	s := tempStoreWithSession(t)
	s.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "first"},
		{Kind: "run", Prompt: "second"},
	})

	s.UpdateLastFeedNote("Applied: foo.go")

	feed := s.SessionFeed()
	assert.Empty(t, feed[0].Note)
	assert.Equal(t, "Applied: foo.go", feed[1].Note)
}

func TestUpdateLastFeedNote_SavesToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "test"},
	})

	s.UpdateLastFeedNote("Skipped.")

	entries, err := session.LoadFeed(s.FeedPath())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "Skipped.", entries[0].Note)
}

func TestUpdateLastFeedNote_OverwritesPrevious(t *testing.T) {
	s := tempStoreWithSession(t)
	s.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "test"},
	})

	s.UpdateLastFeedNote("first note")
	s.UpdateLastFeedNote("second note")

	assert.Equal(t, "second note", s.SessionFeed()[0].Note)
}

// ── BuildFeedEntry ──────────────────────────────────────────────────────────

func TestBuildFeedEntry_BasicRun(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID: "m1",
			Text:    "fixed it",
			ProposedWrites: []tools.FileWrite{
				{Path: "foo.go", Content: "package foo"},
			},
		},
		{
			ModelID: "m2",
			Text:    "also fixed",
			ProposedWrites: []tools.FileWrite{
				{Path: "bar.go", Content: "package bar"},
			},
		},
	}
	entry := BuildFeedEntry("fix bug", responses)

	assert.Equal(t, "run", entry.Kind)
	assert.Equal(t, "fix bug", entry.Prompt)
	require.Len(t, entry.Models, 2)
	assert.Equal(t, "m1", entry.Models[0].ID)
	assert.Equal(t, "m2", entry.Models[1].ID)
	assert.Equal(t, []string{"foo.go"}, entry.Models[0].ProposedFiles)
	assert.Equal(t, []string{"bar.go"}, entry.Models[1].ProposedFiles)
}

func TestBuildFeedEntry_TextTruncatedAt500(t *testing.T) {
	longText := strings.Repeat("x", 600)
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: longText},
	}
	entry := BuildFeedEntry("test", responses)

	require.Len(t, entry.Models, 1)
	assert.Len(t, []rune(entry.Models[0].Text), 500)
}

// ── Cost Tracking ───────────────────────────────────────────────────────────

func TestAccumulateCost_Accumulates(t *testing.T) {
	s := tempStore(t)

	s.AccumulateCost("m1", 0.01)
	s.AccumulateCost("m2", 0.02)
	s.AccumulateCost("m1", 0.005)

	assert.InDelta(t, 0.035, s.TotalCost(), 0.001)
	assert.InDelta(t, 0.015, s.CostPerModel()["m1"], 0.001)
	assert.InDelta(t, 0.02, s.CostPerModel()["m2"], 0.001)
}

// ── Rewind ──────────────────────────────────────────────────────────────────

func TestRewind_EmptyStackReturnsError(t *testing.T) {
	s := tempStoreWithSession(t)
	_, err := s.Rewind()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to rewind")
}

func TestRewind_TruncatesHistories(t *testing.T) {
	s := tempStoreWithSession(t)

	// Append history, then push a rewind entry.
	preLens := s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "a"}},
		"q",
	)
	s.PushRewindEntry(RewindEntry{
		HistoryLengths: preLens,
		FeedIndex:      -1,
		Prompt:         "q",
	})

	require.Len(t, s.Histories()["m1"], 2)

	_, err := s.Rewind()
	require.NoError(t, err)

	assert.Empty(t, s.Histories()["m1"])
	assert.False(t, s.CanRewind())
}

func TestRewind_AnnotatesSessionFeed(t *testing.T) {
	s := tempStoreWithSession(t)
	s.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "fix bug", Note: "Applied: foo.go"},
	})
	s.PushRewindEntry(RewindEntry{
		FeedIndex:      0,
		Prompt:         "fix bug",
		HistoryLengths: map[string]int{},
	})

	result, err := s.Rewind()
	require.NoError(t, err)

	assert.Equal(t, "[rewound] Applied: foo.go", result.Note)
	assert.Equal(t, "[rewound] Applied: foo.go", s.SessionFeed()[0].Note)

	// Also saved to disk.
	entries, err := session.LoadFeed(s.FeedPath())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "[rewound] Applied: foo.go", entries[0].Note)
}

func TestRewind_EmptyNoteBecomes_Rewound(t *testing.T) {
	s := tempStoreWithSession(t)
	s.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "test"},
	})
	s.PushRewindEntry(RewindEntry{
		FeedIndex:      0,
		Prompt:         "test",
		HistoryLengths: map[string]int{},
	})

	result, err := s.Rewind()
	require.NoError(t, err)

	assert.Equal(t, "[rewound]", result.Note)
	assert.Equal(t, "[rewound]", s.SessionFeed()[0].Note)
}

// ── RewindStackLen ──────────────────────────────────────────────────────────

func TestRewindStackLen(t *testing.T) {
	s := tempStore(t)
	assert.Equal(t, 0, s.RewindStackLen())

	s.PushRewindEntry(RewindEntry{FeedIndex: 0, Prompt: "p1", HistoryLengths: map[string]int{}})
	assert.Equal(t, 1, s.RewindStackLen())

	s.PushRewindEntry(RewindEntry{FeedIndex: 1, Prompt: "p2", HistoryLengths: map[string]int{}})
	assert.Equal(t, 2, s.RewindStackLen())

	s.ClearRewindStack()
	assert.Equal(t, 0, s.RewindStackLen())
}

// ── BuildRecipeSnapshot ─────────────────────────────────────────────────────

func TestBuildRecipeSnapshot_DefaultRecipe(t *testing.T) {
	s := tempStore(t)
	snap := s.BuildRecipeSnapshot()
	assert.Equal(t, "default", snap.Name)
}

func TestBuildRecipeSnapshot_AllFields(t *testing.T) {
	s := tempStore(t)
	temp := 0.7
	sysRole := true
	s.baseRecipe = &recipe.Recipe{
		Version:      1,
		Name:         "test-recipe",
		SystemPrompt: "be helpful",
		ToolGuidance: "use tools wisely",
		Tools:        &recipe.ToolsConfig{Allowlist: []string{"bash"}, BashPrefixes: []string{"go test"}},
		ToolDescriptions: map[string]string{"bash": "run commands"},
		ModelParams:      recipe.ModelParamsConfig{Temperature: &temp},
		Constraints:      recipe.ConstraintsConfig{MaxSteps: 5, Timeout: 3 * time.Minute},
		Context:          recipe.ContextConfig{MaxHistoryTurns: 10, Strategy: "auto_compact", CompactThreshold: 0.8, TaskMode: "sequential"},
		SystemReminders:  []recipe.SystemReminderConfig{{Name: "warn", Trigger: "context_usage > 0.75", Content: "heads up"}},
		OutputProcessing: map[string]recipe.OutputRuleConfig{"bash": {MaxLines: 50, Truncation: "tail"}},
		ModelProfiles:    map[string]recipe.ModelProfileConfig{"claude": {ContextBudget: 100000, SystemRole: &sysRole}},
		SummarizationPrompt: "summarize it",
	}
	s.lastActiveTools = []string{"bash", "read_file"}

	snap := s.BuildRecipeSnapshot()

	assert.Equal(t, 1, snap.Version)
	assert.Equal(t, "test-recipe", snap.Name)
	assert.Equal(t, "be helpful", snap.SystemPrompt)
	assert.Equal(t, "use tools wisely", snap.ToolGuidance)
	assert.Equal(t, []string{"go test"}, snap.BashPrefixes)
	assert.Equal(t, map[string]string{"bash": "run commands"}, snap.ToolDescriptions)
	assert.Equal(t, []string{"bash", "read_file"}, snap.Tools)
	assert.Equal(t, "summarize it", snap.SummarizationPrompt)

	require.NotNil(t, snap.ModelParams)
	require.NotNil(t, snap.ModelParams.Temperature)
	assert.InDelta(t, 0.7, *snap.ModelParams.Temperature, 1e-9)

	require.NotNil(t, snap.Constraints)
	assert.Equal(t, 5, snap.Constraints.MaxSteps)
	assert.Equal(t, "3m0s", snap.Constraints.Timeout)

	require.NotNil(t, snap.Context)
	assert.Equal(t, 10, snap.Context.MaxHistoryTurns)
	assert.Equal(t, "auto_compact", snap.Context.Strategy)
	assert.InDelta(t, 0.8, snap.Context.CompactThreshold, 1e-9)
	assert.Equal(t, "sequential", snap.Context.TaskMode)

	require.Len(t, snap.SystemReminders, 1)
	assert.Equal(t, "warn", snap.SystemReminders[0].Name)
	assert.Equal(t, "context_usage > 0.75", snap.SystemReminders[0].Trigger)
	assert.Equal(t, "heads up", snap.SystemReminders[0].Content)

	require.Contains(t, snap.OutputProcessing, "bash")
	assert.Equal(t, 50, snap.OutputProcessing["bash"].MaxLines)
	assert.Equal(t, "tail", snap.OutputProcessing["bash"].Truncation)

	require.Contains(t, snap.ModelProfiles, "claude")
	assert.Equal(t, 100000, snap.ModelProfiles["claude"].ContextBudget)
	require.NotNil(t, snap.ModelProfiles["claude"].SystemRole)
	assert.True(t, *snap.ModelProfiles["claude"].SystemRole)
}

func TestBuildRecipeSnapshot_UsesActiveRecipe(t *testing.T) {
	// Set base recipe with "base prompt", set session recipe with "session prompt".
	// Assert BuildRecipeSnapshot().SystemPrompt == "session prompt".
	// Pins that snapshot reads ActiveRecipe (session over base).
	s := tempStore(t)

	s.baseRecipe = &recipe.Recipe{
		Version:      1,
		Name:         "base",
		SystemPrompt: "base prompt",
		ToolGuidance: "base guidance",
	}

	sessionRec := &recipe.Recipe{
		Version:      1,
		Name:         "session",
		SystemPrompt: "session prompt",
		ToolGuidance: "session guidance",
	}
	s.SetSessionRecipe(sessionRec)

	snap := s.BuildRecipeSnapshot()
	assert.Equal(t, "session prompt", snap.SystemPrompt)
	assert.Equal(t, "session guidance", snap.ToolGuidance)
	assert.Equal(t, "session", snap.Name)
}

func TestBuildRecipeSnapshot_SessionOverride_NoFieldMerge(t *testing.T) {
	// Base has SystemPrompt + ToolGuidance; session has only SystemPrompt.
	// Assert snapshot.ToolGuidance is empty (no fallthrough from base).
	// Prevents accidental merge semantics during refactor.
	s := tempStore(t)

	s.baseRecipe = &recipe.Recipe{
		Version:      1,
		Name:         "base",
		SystemPrompt: "base prompt",
		ToolGuidance: "base guidance",
	}

	sessionRec := &recipe.Recipe{
		Version:      1,
		Name:         "session-only-prompt",
		SystemPrompt: "session prompt",
		// ToolGuidance intentionally left empty
	}
	s.SetSessionRecipe(sessionRec)

	snap := s.BuildRecipeSnapshot()
	// Session recipe is active, so snapshot reads from session recipe only.
	assert.Equal(t, "session prompt", snap.SystemPrompt)
	assert.Empty(t, snap.ToolGuidance, "ToolGuidance should be empty — no fallthrough from base")
}

func TestBuildRecipeSnapshot_NilOptionalFields(t *testing.T) {
	s := tempStore(t)
	s.baseRecipe = &recipe.Recipe{
		Version: 1,
		Name:    "minimal",
	}

	snap := s.BuildRecipeSnapshot()
	assert.Equal(t, "minimal", snap.Name)
	assert.Nil(t, snap.Constraints)
	assert.Nil(t, snap.ModelParams)
	assert.Nil(t, snap.Context)
	assert.Nil(t, snap.SystemReminders)
	assert.Nil(t, snap.OutputProcessing)
	assert.Nil(t, snap.ModelProfiles)
	assert.Empty(t, snap.ToolGuidance)
	assert.Nil(t, snap.BashPrefixes)
	assert.Nil(t, snap.ToolDescriptions)
	assert.Empty(t, snap.SummarizationPrompt)
}

// ── Report Path Accumulation ────────────────────────────────────────────────

func TestSetLastReportInfo_AccumulatesPaths(t *testing.T) {
	s := tempStore(t)

	s.SetLastReportInfo("path/report1.json", []string{"bash"})
	s.SetLastReportInfo("path/report2.json", []string{"bash"})
	s.SetLastReportInfo("path/report3.json", []string{"bash"})

	assert.Equal(t, 3, s.ReportPathCount())
	assert.Equal(t, "path/report3.json", s.LastReportPath())
}

func TestSetLastReportInfo_EmptyPathNotAccumulated(t *testing.T) {
	s := tempStore(t)

	s.SetLastReportInfo("path/report1.json", []string{"bash"})
	s.SetLastReportInfo("", []string{"bash"})

	assert.Equal(t, 1, s.ReportPathCount())
}

func TestLoadAllReports_LoadsInOrder(t *testing.T) {
	tmp := t.TempDir()
	s := tempStore(t)

	// Create and save two reports.
	r1 := &output.Report{
		ID:        "rpt_1",
		Prompt:    "first",
		Recipe:    output.RecipeSnapshot{Name: "test"},
		Models:    []output.ModelResult{},
		Aggregate: output.AggregateStats{},
	}
	r2 := &output.Report{
		ID:        "rpt_2",
		Prompt:    "second",
		Recipe:    output.RecipeSnapshot{Name: "test"},
		Models:    []output.ModelResult{},
		Aggregate: output.AggregateStats{},
	}

	p1, err := output.Save(tmp, r1)
	require.NoError(t, err)
	p2, err := output.Save(tmp, r2)
	require.NoError(t, err)

	s.SetLastReportInfo(p1, nil)
	s.SetLastReportInfo(p2, nil)

	reports := s.LoadAllReports()
	require.Len(t, reports, 2)
	assert.Equal(t, "first", reports[0].Prompt)
	assert.Equal(t, "second", reports[1].Prompt)
}

func TestLoadAllReports_SkipsMissing(t *testing.T) {
	tmp := t.TempDir()
	s := tempStore(t)

	r := &output.Report{
		ID:        "rpt_1",
		Prompt:    "valid",
		Recipe:    output.RecipeSnapshot{Name: "test"},
		Models:    []output.ModelResult{},
		Aggregate: output.AggregateStats{},
	}
	p, err := output.Save(tmp, r)
	require.NoError(t, err)

	s.SetLastReportInfo("/nonexistent/report.json", nil)
	s.SetLastReportInfo(p, nil)

	reports := s.LoadAllReports()
	require.Len(t, reports, 1)
	assert.Equal(t, "valid", reports[0].Prompt)
}

func TestClearReportPaths(t *testing.T) {
	s := tempStore(t)

	s.SetLastReportInfo("path/report1.json", nil)
	s.SetLastReportInfo("path/report2.json", nil)
	assert.Equal(t, 2, s.ReportPathCount())

	s.ClearReportPaths()
	assert.Equal(t, 0, s.ReportPathCount())
}
