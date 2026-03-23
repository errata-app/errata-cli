package datastore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
	"github.com/errata-app/errata-cli/internal/prompthistory"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/pkg/recipestore"
	"github.com/errata-app/errata-cli/internal/session"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	tmp := t.TempDir()
	sp := session.PathsFor(tmp, "temp")
	s, err := New(Options{
		PromptHistPath: filepath.Join(tmp, "prompt_history.jsonl"),
		SessionPaths:   sp,
	})
	require.NoError(t, err)
	return s
}

func tempStoreWithSession(t *testing.T) *Store {
	t.Helper()
	tmp := t.TempDir()
	sp := session.PathsFor(tmp, "test-session")
	require.NoError(t, os.MkdirAll(sp.Dir, 0o750))
	s, err := New(Options{
		PromptHistPath: filepath.Join(tmp, "prompt_history.jsonl"),
		SessionPaths:   sp,
		SessionID:      "test-session",
		Meta:           session.SessionMetadata{ID: "test-session"},
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

func TestNew_LoadsExistingContent(t *testing.T) {
	tmp := t.TempDir()
	sp := session.PathsFor(tmp, "ses")
	require.NoError(t, os.MkdirAll(sp.Dir, 0o750))

	c := session.SessionContent{
		Histories: map[string][]models.ConversationTurn{
			"m1": {
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi"},
			},
		},
	}
	require.NoError(t, session.SaveContent(sp.ContentPath, c))

	s, err := New(Options{
		PromptHistPath: filepath.Join(tmp, "prompt.jsonl"),
		SessionPaths:   sp,
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

	sp := session.PathsFor(tmp, "ses")
	s, err := New(Options{
		PromptHistPath: phPath,
		SessionPaths:   sp,
	})
	require.NoError(t, err)
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

	assert.Equal(t, 0, preLens["m1"])
	assert.Equal(t, 0, preLens["m2"])
	assert.Len(t, s.Histories()["m1"], 2)
	assert.Len(t, s.Histories()["m2"], 2)

	// Verify disk persistence via session content.
	loaded, err := session.LoadContent(s.contentPath)
	require.NoError(t, err)
	assert.Len(t, loaded.Histories["m1"], 2)
	assert.Len(t, loaded.Histories["m2"], 2)
}

func TestAppendHistories_PreLengthsCapturePrior(t *testing.T) {
	s := tempStore(t)

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
	assert.Equal(t, 2, preLens["m1"])
	assert.Len(t, s.Histories()["m1"], 4)
}

func TestAppendHistories_ErrorResponseSkipped(t *testing.T) {
	s := tempStore(t)

	s.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Error: "something broke"}},
		"question",
	)

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

	loaded, err := session.LoadContent(s.contentPath)
	require.NoError(t, err)
	assert.Len(t, loaded.Histories["m1"], 2)
}

// ── History: TruncateHistories ──────────────────────────────────────────────

func TestTruncateHistories_RestoresLengths(t *testing.T) {
	s := tempStore(t)

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

	s.TruncateHistories(preLens)
	assert.Len(t, s.Histories()["m1"], 2)

	loaded, err := session.LoadContent(s.contentPath)
	require.NoError(t, err)
	assert.Len(t, loaded.Histories["m1"], 2)
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
	// Content should still exist but with nil histories.
	loaded, err := session.LoadContent(s.contentPath)
	require.NoError(t, err)
	assert.Nil(t, loaded.Histories)
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
	sp := session.PathsFor(tmp, "ses")
	require.NoError(t, os.MkdirAll(sp.Dir, 0o750))
	opts := Options{
		PromptHistPath: filepath.Join(tmp, "prompt.jsonl"),
		SessionPaths:   sp,
	}

	s1, err := New(opts)
	require.NoError(t, err)
	s1.AppendHistories(
		[]string{"m1"},
		[]models.ModelResponse{{ModelID: "m1", Text: "answer"}},
		"question",
	)

	s2, err := New(opts)
	require.NoError(t, err)
	require.Len(t, s2.Histories()["m1"], 2)
	assert.Equal(t, "question", s2.Histories()["m1"][0].Content)
	assert.Equal(t, "answer", s2.Histories()["m1"][1].Content)
}

func TestRoundTrip_PromptHistoryPersistsAcrossInstances(t *testing.T) {
	tmp := t.TempDir()
	sp := session.PathsFor(tmp, "ses")
	opts := Options{
		PromptHistPath: filepath.Join(tmp, "prompt.jsonl"),
		SessionPaths:   sp,
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
	s.PersistRunState("fix bug", responses, nil, nil)

	meta := s.Metadata()
	assert.Equal(t, 1, meta.PromptCount)
	assert.Equal(t, "fix bug", meta.FirstPrompt)
	assert.Equal(t, "fix bug", meta.LastPrompt)
	assert.WithinDuration(t, before, meta.LastActiveAt, 2*time.Second)
	require.Len(t, meta.Runs, 1)
	assert.Equal(t, "fix bug", meta.Runs[0].PromptPreview)
}

func TestPersistRunState_SecondCallPreservesFirst(t *testing.T) {
	s := tempStoreWithSession(t)

	s.PersistRunState("first prompt", []models.ModelResponse{{ModelID: "m1", Text: "r1"}}, nil, nil)
	s.PersistRunState("second prompt", []models.ModelResponse{{ModelID: "m1", Text: "r2"}}, nil, nil)

	meta := s.Metadata()
	assert.Equal(t, 2, meta.PromptCount)
	assert.Equal(t, "first prompt", meta.FirstPrompt)
	assert.Equal(t, "second prompt", meta.LastPrompt)
	assert.Len(t, meta.Runs, 2)
}

func TestPersistRunState_MetaSavedToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("disk test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	loaded, err := session.LoadMetadata(s.MetadataPath())
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, 1, loaded.PromptCount)
	assert.Equal(t, "disk test", loaded.FirstPrompt)
}

func TestPersistRunState_ContentSavedToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("content test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	loaded, err := session.LoadContent(s.ContentPath())
	require.NoError(t, err)
	require.Len(t, loaded.Runs, 1)
	assert.Equal(t, "content test", loaded.Runs[0].Prompt)
	require.Len(t, loaded.Runs[0].Models, 1)
	assert.Equal(t, "m1", loaded.Runs[0].Models[0].ModelID)
	assert.Equal(t, "ok", loaded.Runs[0].Models[0].Text)
}

func TestPersistRunState_ContentAccumulates(t *testing.T) {
	s := tempStoreWithSession(t)

	s.PersistRunState("prompt 1", []models.ModelResponse{{ModelID: "m1", Text: "r1"}}, nil, nil)
	s.PersistRunState("prompt 2", []models.ModelResponse{{ModelID: "m1", Text: "r2"}}, nil, nil)

	content := s.Content()
	assert.Len(t, content.Runs, 2)

	loaded, err := session.LoadContent(s.ContentPath())
	require.NoError(t, err)
	assert.Len(t, loaded.Runs, 2)
}

func TestPersistRunState_LongPromptTruncated(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState(strings.Repeat("x", 200), []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	meta := s.Metadata()
	assert.LessOrEqual(t, len([]rune(meta.FirstPrompt)), 120)
	assert.LessOrEqual(t, len([]rune(meta.LastPrompt)), 120)
}

func TestPersistRunState_CollectorEventsStored(t *testing.T) {
	s := tempStoreWithSession(t)
	collector := output.NewCollector()
	// Simulate collecting events.
	wrap := collector.WrapOnEvent(func(string, models.AgentEvent) {})
	wrap("m1", models.AgentEvent{Type: models.EventReading, Data: "main.go"})
	wrap("m1", models.AgentEvent{Type: models.EventText, Data: "result"})

	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, collector, []string{"bash"})

	loaded, err := session.LoadContent(s.ContentPath())
	require.NoError(t, err)
	require.Len(t, loaded.Runs[0].Models[0].Events, 2)
	assert.Equal(t, models.EventReading, loaded.Runs[0].Models[0].Events[0].Type)
}

// ── persistSessionRecipe (tested through PersistRunState) ───────────────────

func TestPersistRunState_RecipeWrittenToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.SetSessionRecipe(&recipe.Recipe{Name: "session-recipe"})
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	data, err := os.ReadFile(s.SessionRecipePath())
	require.NoError(t, err)
	assert.Contains(t, string(data), "session-recipe")
}

func TestPersistRunState_NilRecipeNoFile(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	_, err := os.Stat(s.SessionRecipePath())
	assert.True(t, os.IsNotExist(err))
}

// ── UpdateLastRunNote ───────────────────────────────────────────────────────

func TestUpdateLastRunNote_EmptyRunsNoop(t *testing.T) {
	s := tempStoreWithSession(t)
	assert.NotPanics(t, func() {
		s.UpdateLastRunNote("should not panic")
	})
}

func TestUpdateLastRunNote_UpdatesLastRun(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("first", []models.ModelResponse{{ModelID: "m1", Text: "r1"}}, nil, nil)
	s.PersistRunState("second", []models.ModelResponse{{ModelID: "m1", Text: "r2"}}, nil, nil)

	s.UpdateLastRunNote("Applied: foo.go")

	meta := s.Metadata()
	assert.Empty(t, meta.Runs[0].Note)
	assert.Equal(t, "Applied: foo.go", meta.Runs[1].Note)
}

func TestUpdateLastRunNote_SavesToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	s.UpdateLastRunNote("Skipped.")

	loaded, err := session.LoadMetadata(s.MetadataPath())
	require.NoError(t, err)
	require.Len(t, loaded.Runs, 1)
	assert.Equal(t, "Skipped.", loaded.Runs[0].Note)
}

func TestUpdateLastRunNote_OverwritesPrevious(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	s.UpdateLastRunNote("first note")
	s.UpdateLastRunNote("second note")

	assert.Equal(t, "second note", s.Metadata().Runs[0].Note)
}

// ── RecordSelection ─────────────────────────────────────────────────────────

func TestRecordSelection_UpdatesLastRun(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{
		{ModelID: "m1", Text: "r1"},
		{ModelID: "m2", Text: "r2"},
	}, nil, nil)

	s.RecordSelection(SelectionParams{
		Prompt:          "test",
		SelectedModelID: "m1",
		AppliedFiles:    []string{"foo.go"},
	})

	meta := s.Metadata()
	require.Len(t, meta.Runs, 1)
	assert.Equal(t, "m1", meta.Runs[0].Selected)
	assert.Equal(t, []string{"foo.go"}, meta.Runs[0].AppliedFiles)
}

func TestRecordSelection_Rating(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "r1"}}, nil, nil)

	s.RecordSelection(SelectionParams{
		Prompt:          "test",
		SelectedModelID: "m1",
		Rating:          "good",
	})

	meta := s.Metadata()
	assert.Equal(t, "good", meta.Runs[0].Rating)
}

func TestRecordSelection_SavesToDisk(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)

	s.RecordSelection(SelectionParams{SelectedModelID: "m1"})

	loaded, err := session.LoadMetadata(s.MetadataPath())
	require.NoError(t, err)
	assert.Equal(t, "m1", loaded.Runs[0].Selected)
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

func TestRewind_AnnotatesMetadataRun(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("fix bug", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)
	s.metadata.Runs[0].Note = "Applied: foo.go"
	s.PushRewindEntry(RewindEntry{
		FeedIndex:      0,
		Prompt:         "fix bug",
		HistoryLengths: map[string]int{},
	})

	result, err := s.Rewind()
	require.NoError(t, err)

	assert.Equal(t, "[rewound] Applied: foo.go", result.Note)
	// Rewind adds a rewind marker run to metadata.
	assert.GreaterOrEqual(t, len(s.Metadata().Runs), 2)
}

func TestRewind_EmptyNoteBecomes_Rewound(t *testing.T) {
	s := tempStoreWithSession(t)
	s.PersistRunState("test", []models.ModelResponse{{ModelID: "m1", Text: "ok"}}, nil, nil)
	s.PushRewindEntry(RewindEntry{
		FeedIndex:      0,
		Prompt:         "test",
		HistoryLengths: map[string]int{},
	})

	result, err := s.Rewind()
	require.NoError(t, err)

	assert.Equal(t, "[rewound]", result.Note)
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
	s.baseRecipe = &recipe.Recipe{
		Version:      1,
		Name:         "test-recipe",
		SystemPrompt: "be helpful",
		Tools: &recipe.ToolsConfig{
			Allowlist:    []string{"bash"},
			BashPrefixes: []string{"go test"},
			Guidance:     map[string]string{"bash": "use tools wisely"},
		},
		ToolDescriptions:    map[string]string{"bash": "run commands"},
		Constraints:         recipe.ConstraintsConfig{MaxSteps: 5, Timeout: 3 * time.Minute},
		Context:             recipe.ContextConfig{MaxHistoryTurns: 10, Strategy: "auto_compact", CompactThreshold: 0.8, TaskMode: "sequential"},
		OutputProcessing:    map[string]recipe.OutputRuleConfig{"bash": {MaxLines: 50, Truncation: "tail"}},
		ModelProfiles:       map[string]recipe.ModelProfileConfig{"claude": {ContextBudget: 100000}},
		SummarizationPrompt: "summarize it",
	}
	s.lastActiveTools = []string{"bash", "read_file"}

	snap := s.BuildRecipeSnapshot()

	assert.Equal(t, 1, snap.Version)
	assert.Equal(t, "test-recipe", snap.Name)
	assert.Equal(t, "be helpful", snap.SystemPrompt)
	assert.Equal(t, map[string]string{"bash": "use tools wisely"}, snap.ToolGuidance)
	assert.Equal(t, []string{"go test"}, snap.BashPrefixes)
	assert.Equal(t, map[string]string{"bash": "run commands"}, snap.ToolDescriptions)
	assert.Equal(t, []string{"bash", "read_file"}, snap.Tools)
	assert.Equal(t, "summarize it", snap.SummarizationPrompt)
	require.NotNil(t, snap.Constraints)
	require.NotNil(t, snap.Context)
}

func TestBuildRecipeSnapshot_UsesActiveRecipe(t *testing.T) {
	s := tempStore(t)
	s.baseRecipe = &recipe.Recipe{Name: "base", SystemPrompt: "base prompt"}
	sessionRec := &recipe.Recipe{Name: "session", SystemPrompt: "session prompt"}
	s.SetSessionRecipe(sessionRec)

	snap := s.BuildRecipeSnapshot()
	assert.Equal(t, "session prompt", snap.SystemPrompt)
	assert.Equal(t, "session", snap.Name)
}

func TestSessionsDir(t *testing.T) {
	tmp := t.TempDir()
	sp := session.PathsFor(filepath.Join(tmp, "sessions"), "ses_abc")
	s, err := New(Options{
		PromptHistPath: filepath.Join(tmp, "prompt_history.jsonl"),
		SessionPaths:   sp,
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmp, "sessions"), s.SessionsDir())
}

func TestRecipeNameLookup_Hit(t *testing.T) {
	tmp := t.TempDir()
	sp := session.PathsFor(tmp, "ses_test")
	rs := recipestore.New(filepath.Join(tmp, "configs.json"))
	hash := rs.Put(&recipestore.RecipeSnapshot{Name: "My Recipe", SystemPrompt: "test"})

	s, err := New(Options{
		PromptHistPath: filepath.Join(tmp, "prompt_history.jsonl"),
		SessionPaths:   sp,
		RecipeStore:    rs,
	})
	require.NoError(t, err)

	lookup := s.RecipeNameLookup()
	assert.Equal(t, "My Recipe", lookup(hash))
}

func TestRecipeNameLookup_Miss(t *testing.T) {
	tmp := t.TempDir()
	sp := session.PathsFor(tmp, "ses_test")
	rs := recipestore.New(filepath.Join(tmp, "configs.json"))

	s, err := New(Options{
		PromptHistPath: filepath.Join(tmp, "prompt_history.jsonl"),
		SessionPaths:   sp,
		RecipeStore:    rs,
	})
	require.NoError(t, err)

	lookup := s.RecipeNameLookup()
	assert.Empty(t, lookup("cfg_v1_nonexistent"))
}

func TestRecipeNameLookup_NilStore(t *testing.T) {
	s := tempStore(t)
	lookup := s.RecipeNameLookup()
	assert.Empty(t, lookup("cfg_v1_anything"))
}
