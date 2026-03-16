package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveMetadata_LoadMetadata_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_metadata.json")

	now := time.Now().Truncate(time.Second)
	m := SessionMetadata{
		ID:           "ses_019505e2-c38a-7b1e-8b3c-4d5e6f7a8b9c",
		CreatedAt:    now,
		LastActiveAt: now.Add(time.Hour),
		Models:       []string{"claude-sonnet-4-6", "gpt-4o"},
		FirstPrompt:  "fix the bug",
		LastPrompt:   "add tests",
		PromptCount:  5,
		ConfigHash:   "abc123",
		Runs: []RunSummary{
			{
				Timestamp:     now,
				PromptHash:    "sha256:deadbeef",
				PromptPreview: "fix the bug",
				Models:        []string{"claude-sonnet-4-6", "gpt-4o"},
				Selected:      "claude-sonnet-4-6",
				LatenciesMS:   map[string]int64{"claude-sonnet-4-6": 1200, "gpt-4o": 800},
				CostsUSD:      map[string]float64{"claude-sonnet-4-6": 0.01, "gpt-4o": 0.02},
				InputTokens:   map[string]int64{"claude-sonnet-4-6": 100, "gpt-4o": 120},
				OutputTokens:  map[string]int64{"claude-sonnet-4-6": 200, "gpt-4o": 180},
				AppliedFiles:  []string{"main.go"},
				Note:          "Applied: main.go",
			},
		},
	}

	require.NoError(t, SaveMetadata(path, m))

	loaded, err := LoadMetadata(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, m.ID, loaded.ID)
	assert.True(t, m.CreatedAt.Equal(loaded.CreatedAt))
	assert.True(t, m.LastActiveAt.Equal(loaded.LastActiveAt))
	assert.Equal(t, m.Models, loaded.Models)
	assert.Equal(t, m.FirstPrompt, loaded.FirstPrompt)
	assert.Equal(t, m.LastPrompt, loaded.LastPrompt)
	assert.Equal(t, m.PromptCount, loaded.PromptCount)
	assert.Equal(t, m.ConfigHash, loaded.ConfigHash)

	require.Len(t, loaded.Runs, 1)
	r := loaded.Runs[0]
	assert.Equal(t, "sha256:deadbeef", r.PromptHash)
	assert.Equal(t, "claude-sonnet-4-6", r.Selected)
	assert.Equal(t, []string{"main.go"}, r.AppliedFiles)
	assert.Equal(t, "Applied: main.go", r.Note)
	assert.Equal(t, map[string]int64{"claude-sonnet-4-6": 1200, "gpt-4o": 800}, r.LatenciesMS)
}

func TestLoadMetadata_MissingFile(t *testing.T) {
	m, err := LoadMetadata("/nonexistent/path/session_metadata.json")
	require.NoError(t, err)
	assert.Nil(t, m)
}

func TestLoadMetadata_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_metadata.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid json"), 0o600))

	m, err := LoadMetadata(path)
	require.Error(t, err)
	assert.Nil(t, m)
}

func TestSaveMetadata_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "session_metadata.json")

	m := SessionMetadata{ID: "ses_test"}
	require.NoError(t, SaveMetadata(path, m))

	loaded, err := LoadMetadata(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "ses_test", loaded.ID)
}

func TestSaveMetadata_RunSummaryToolCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_metadata.json")

	m := SessionMetadata{
		ID: "ses_tc",
		Runs: []RunSummary{
			{
				PromptPreview: "test",
				Models:        []string{"m1"},
				ToolCalls:     map[string]map[string]int{"m1": {"read_file": 3, "bash": 1}},
				ProposedWritesCount: map[string]int{"m1": 2},
			},
		},
	}

	require.NoError(t, SaveMetadata(path, m))

	loaded, err := LoadMetadata(path)
	require.NoError(t, err)
	require.Len(t, loaded.Runs, 1)
	assert.Equal(t, map[string]map[string]int{"m1": {"read_file": 3, "bash": 1}}, loaded.Runs[0].ToolCalls)
	assert.Equal(t, map[string]int{"m1": 2}, loaded.Runs[0].ProposedWritesCount)
}

func TestSaveMetadata_RunSummaryRewind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_metadata.json")

	m := SessionMetadata{
		ID: "ses_rw",
		Runs: []RunSummary{
			{PromptPreview: "test", Type: "rewind"},
		},
	}

	require.NoError(t, SaveMetadata(path, m))

	loaded, err := LoadMetadata(path)
	require.NoError(t, err)
	require.Len(t, loaded.Runs, 1)
	assert.Equal(t, "rewind", loaded.Runs[0].Type)
}

func TestSaveMetadata_EmptyRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_metadata.json")

	m := SessionMetadata{ID: "ses_empty", Runs: []RunSummary{}}

	require.NoError(t, SaveMetadata(path, m))

	loaded, err := LoadMetadata(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Empty(t, loaded.Runs)
}
