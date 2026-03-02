package datastore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/prompthistory"
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
