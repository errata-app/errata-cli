package preferences_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
)

func TestRecordAndLoadAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "claude-sonnet-4-6", LatencyMS: 100},
		{ModelID: "gpt-4o", LatencyMS: 200},
	}

	err := preferences.Record(path, "a test prompt", "claude-sonnet-4-6", "session1", responses)
	require.NoError(t, err)

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Equal(t, "claude-sonnet-4-6", e.Selected)
	assert.Equal(t, "session1", e.SessionID)
	assert.Contains(t, e.PromptPreview, "a test prompt")
	assert.Len(t, e.Models, 2)
	assert.Equal(t, int64(100), e.LatenciesMS["claude-sonnet-4-6"])
}

func TestLoadAll_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	entries := preferences.LoadAll(path)
	assert.Nil(t, entries)
}

func TestSummarize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 1}}

	require.NoError(t, preferences.Record(path, "p1", "a", "s1", responses))
	require.NoError(t, preferences.Record(path, "p2", "b", "s2", responses))
	require.NoError(t, preferences.Record(path, "p3", "a", "s3", responses))

	tally := preferences.Summarize(path)
	assert.Equal(t, 2, tally["a"])
	assert.Equal(t, 1, tally["b"])
}

func TestRecordAndLoadAll_LongPromptTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	err := preferences.Record(path, string(long), "m", "s", nil)
	require.NoError(t, err)

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, 120, len(entries[0].PromptPreview))
}

func TestRecord_PromptHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "write a sort function", "m", "s", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.True(t, len(entries[0].PromptHash) > 0)
	assert.Contains(t, entries[0].PromptHash, "sha256:")
}

func TestRecord_UsesProvidedSessionID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p", "m", "my-session-id", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, "my-session-id", entries[0].SessionID)
}

func TestRecord_LatenciesMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "a", LatencyMS: 111},
		{ModelID: "b", LatencyMS: 222},
	}
	require.NoError(t, preferences.Record(path, "p", "a", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, int64(111), entries[0].LatenciesMS["a"])
	assert.Equal(t, int64(222), entries[0].LatenciesMS["b"])
}

func TestRecord_AppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "m", LatencyMS: 1}}

	require.NoError(t, preferences.Record(path, "first prompt", "m", "s1", responses))
	require.NoError(t, preferences.Record(path, "second prompt", "m", "s2", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 2)
	assert.Equal(t, "first prompt", entries[0].PromptPreview)
	assert.Equal(t, "second prompt", entries[1].PromptPreview)
}

func TestSummarize_EmptyWhenNoRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	tally := preferences.Summarize(path)
	assert.Empty(t, tally)
}
