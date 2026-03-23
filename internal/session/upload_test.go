package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectForUpload_Basic(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	m := SessionMetadata{
		ID:           "ses_1",
		CreatedAt:    now,
		LastActiveAt: now,
		Models:       []string{"m1", "m2"},
		PromptCount:  2,
		ConfigHash:   "cfg_v1_abc",
		FirstPrompt:  "sensitive prompt text",
		LastPrompt:   "also sensitive",
		Runs: []RunSummary{
			{
				Timestamp:     now,
				PromptHash:    "ph_aaa",
				PromptPreview: "should be stripped",
				Models:        []string{"m1", "m2"},
				Selected:      "m1",
				LatenciesMS:   map[string]int64{"m1": 1000, "m2": 800},
				AppliedFiles:  []string{"main.go"},
				Note:          "should be stripped",
				ConfigHash:    "cfg_v1_abc",
			},
		},
	}

	path := filepath.Join(dir, "ses_1", "session_metadata.json")
	require.NoError(t, SaveMetadata(path, m))

	nameLookup := func(hash string) string {
		if hash == "cfg_v1_abc" {
			return "Test Recipe"
		}
		return ""
	}

	sessions := CollectForUpload(dir, time.Time{}, nameLookup)
	require.Len(t, sessions, 1)

	s := sessions[0]
	assert.Equal(t, "ses_1", s.ID)
	assert.Equal(t, "Test Recipe", s.RecipeName)
	assert.Equal(t, "cfg_v1_abc", s.ConfigHash)
	assert.Equal(t, 2, s.PromptCount)

	require.Len(t, s.Runs, 1)
	r := s.Runs[0]
	assert.Equal(t, "ph_aaa", r.PromptHash)
	assert.Equal(t, "m1", r.Selected)
	assert.Equal(t, map[string]int64{"m1": 1000, "m2": 800}, r.LatenciesMS)
	assert.Equal(t, "cfg_v1_abc", r.ConfigHash)
}

func TestCollectForUpload_StripsPrivateFields(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	m := SessionMetadata{
		ID:           "ses_2",
		CreatedAt:    now,
		LastActiveAt: now,
		FirstPrompt:  "private first prompt",
		LastPrompt:   "private last prompt",
		Runs: []RunSummary{
			{
				PromptHash:    "ph_bbb",
				PromptPreview: "private preview",
				Models:        []string{"m1"},
				Selected:      "m1",
				AppliedFiles:  []string{"secret/path.go"},
				Note:          "private note",
				ConfigHash:    "cfg_v1_xyz",
			},
		},
	}

	path := filepath.Join(dir, "ses_2", "session_metadata.json")
	require.NoError(t, SaveMetadata(path, m))

	sessions := CollectForUpload(dir, time.Time{}, nil)
	require.Len(t, sessions, 1)

	// Safe fields are preserved.
	r := sessions[0].Runs[0]
	assert.Equal(t, "ph_bbb", r.PromptHash)
	assert.Equal(t, []string{"m1"}, r.Models)
	assert.Equal(t, "m1", r.Selected)
	assert.Equal(t, "cfg_v1_xyz", r.ConfigHash)

	// Marshal to JSON and verify sensitive fields are absent.
	data, err := json.Marshal(sessions[0])
	require.NoError(t, err)
	jsonStr := string(data)
	assert.NotContains(t, jsonStr, "private first prompt")
	assert.NotContains(t, jsonStr, "private last prompt")
	assert.NotContains(t, jsonStr, "private preview")
	assert.NotContains(t, jsonStr, "secret/path.go")
	assert.NotContains(t, jsonStr, "private note")
	assert.NotContains(t, jsonStr, "first_prompt")
	assert.NotContains(t, jsonStr, "last_prompt")
	assert.NotContains(t, jsonStr, "prompt_preview")
	assert.NotContains(t, jsonStr, "applied_files")
	assert.NotContains(t, jsonStr, "\"note\"")
}

func TestCollectForUpload_FiltersBySince(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	recent := time.Now().Truncate(time.Second)

	m1 := SessionMetadata{
		ID:           "ses_old",
		LastActiveAt: old,
		Runs:         []RunSummary{{PromptHash: "ph_1", Models: []string{"m1"}}},
	}
	m2 := SessionMetadata{
		ID:           "ses_new",
		LastActiveAt: recent,
		Runs:         []RunSummary{{PromptHash: "ph_2", Models: []string{"m1"}}},
	}

	require.NoError(t, SaveMetadata(filepath.Join(dir, "ses_old", "session_metadata.json"), m1))
	require.NoError(t, SaveMetadata(filepath.Join(dir, "ses_new", "session_metadata.json"), m2))

	// With since = 1 hour ago, only ses_new should appear.
	since := time.Now().Add(-1 * time.Hour)
	sessions := CollectForUpload(dir, since, nil)
	require.Len(t, sessions, 1)
	assert.Equal(t, "ses_new", sessions[0].ID)
}

func TestCollectForUpload_SkipsEmptyRuns(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	m := SessionMetadata{
		ID:           "ses_empty",
		LastActiveAt: now,
		Runs:         []RunSummary{},
	}
	require.NoError(t, SaveMetadata(filepath.Join(dir, "ses_empty", "session_metadata.json"), m))

	sessions := CollectForUpload(dir, time.Time{}, nil)
	assert.Empty(t, sessions)
}

func TestCollectForUpload_ExcludesRewoundRuns(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	m := SessionMetadata{
		ID:           "ses_rw",
		LastActiveAt: now,
		Runs: []RunSummary{
			{PromptHash: "ph_x", Models: []string{"m1"}, Selected: "m1"},
			{PromptHash: "ph_x", Type: "rewind"},
		},
	}
	require.NoError(t, SaveMetadata(filepath.Join(dir, "ses_rw", "session_metadata.json"), m))

	sessions := CollectForUpload(dir, time.Time{}, nil)
	// Both runs cancel out — the normal run is excluded by filterRewound,
	// the rewind marker is also excluded. No runs → session is skipped.
	assert.Empty(t, sessions)
}

func TestCollectForUpload_NilNameLookup(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	m := SessionMetadata{
		ID:           "ses_nil",
		LastActiveAt: now,
		ConfigHash:   "cfg_v1_xyz",
		Runs:         []RunSummary{{PromptHash: "ph_1", Models: []string{"m1"}}},
	}
	require.NoError(t, SaveMetadata(filepath.Join(dir, "ses_nil", "session_metadata.json"), m))

	sessions := CollectForUpload(dir, time.Time{}, nil)
	require.Len(t, sessions, 1)
	assert.Empty(t, sessions[0].RecipeName)
}

func TestCollectForUpload_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	sessions := CollectForUpload(dir, time.Time{}, nil)
	assert.Empty(t, sessions)
}

func TestCollectForUpload_NonexistentDir(t *testing.T) {
	sessions := CollectForUpload("/nonexistent/path", time.Time{}, nil)
	assert.Nil(t, sessions)
}
