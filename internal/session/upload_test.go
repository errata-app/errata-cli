package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/api"
	"github.com/errata-app/errata-cli/internal/models"
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
		ConfigHash:   "rcp_v1_abc",
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
				ConfigHash:    "rcp_v1_abc",
			},
		},
	}

	path := filepath.Join(dir, "ses_1", "session_metadata.json")
	require.NoError(t, SaveMetadata(path, m))

	nameLookup := func(hash string) string {
		if hash == "rcp_v1_abc" {
			return "Test Recipe"
		}
		return ""
	}

	sessions := CollectForUpload(dir, time.Time{}, nameLookup)
	require.Len(t, sessions, 1)

	s := sessions[0]
	assert.Equal(t, "ses_1", s.ID)
	assert.Equal(t, "Test Recipe", s.RecipeName)
	assert.Equal(t, "rcp_v1_abc", s.ConfigHash)
	assert.Equal(t, 2, s.PromptCount)

	require.Len(t, s.Runs, 1)
	r := s.Runs[0]
	assert.Equal(t, "ph_aaa", r.PromptHash)
	assert.Equal(t, "m1", r.Selected)
	assert.Equal(t, map[string]int64{"m1": 1000, "m2": 800}, r.LatenciesMS)
	assert.Equal(t, "rcp_v1_abc", r.ConfigHash)
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
				ConfigHash:    "rcp_v1_xyz",
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
	assert.Equal(t, "rcp_v1_xyz", r.ConfigHash)

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
		ConfigHash:   "rcp_v1_xyz",
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

func TestCollectContentForUpload_Basic(t *testing.T) {
	dir := t.TempDir()
	sp := PathsFor(dir, "ses_c1")
	content := SessionContent{
		Runs: []RunContent{
			{
				Prompt: "fix bug",
				Models: []ModelRunContent{
					{
						ModelID:    "m1",
						Text:       "Done.",
						StopReason: "complete",
						Steps:      2,
					},
				},
			},
		},
		Histories: map[string][]models.ConversationTurn{
			"m1": {{Role: "user", Content: "fix bug"}, {Role: "assistant", Content: "Done."}},
		},
	}
	require.NoError(t, SaveContent(sp.ContentPath, content))

	result := CollectContentForUpload(dir, []string{"ses_c1"})
	require.Len(t, result, 1)
	assert.Equal(t, "ses_c1", result[0].SessionID)
	require.Len(t, result[0].Runs, 1)
	assert.Equal(t, "fix bug", result[0].Runs[0].Prompt)
	require.Len(t, result[0].Runs[0].Models, 1)
	assert.Equal(t, "m1", result[0].Runs[0].Models[0].ModelID)
	assert.Equal(t, "Done.", result[0].Runs[0].Models[0].Text)
	assert.Equal(t, "complete", result[0].Runs[0].Models[0].StopReason)
	require.Len(t, result[0].Histories, 1)
	assert.Len(t, result[0].Histories["m1"], 2)
}

func TestCollectContentForUpload_MissingSessions(t *testing.T) {
	dir := t.TempDir()
	// Create one session with content and leave "ses_missing" absent.
	sp := PathsFor(dir, "ses_exists")
	content := SessionContent{
		Runs: []RunContent{{Prompt: "hello", Models: []ModelRunContent{{ModelID: "m1", Text: "hi"}}}},
	}
	require.NoError(t, SaveContent(sp.ContentPath, content))

	result := CollectContentForUpload(dir, []string{"ses_missing", "ses_exists"})
	require.Len(t, result, 1)
	assert.Equal(t, "ses_exists", result[0].SessionID)
}

func TestCollectContentForUpload_EmptyList(t *testing.T) {
	result := CollectContentForUpload(t.TempDir(), nil)
	assert.Nil(t, result)
}

func TestCollectConfigHashes_DeduplicatesAcrossSessions(t *testing.T) {
	sessions := []api.SessionUpload{
		{
			ConfigHash: "rcp_v1_aaa",
			Runs: []api.RunUpload{
				{ConfigHash: "rcp_v1_aaa"},
				{ConfigHash: "rcp_v1_bbb"},
			},
		},
		{
			ConfigHash: "rcp_v1_bbb",
			Runs: []api.RunUpload{
				{ConfigHash: "rcp_v1_ccc"},
				{ConfigHash: "rcp_v1_aaa"},
			},
		},
	}

	hashes := CollectConfigHashes(sessions)
	assert.Len(t, hashes, 3)
	// Verify all three unique hashes are present (order not guaranteed).
	hashSet := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		hashSet[h] = true
	}
	assert.True(t, hashSet["rcp_v1_aaa"])
	assert.True(t, hashSet["rcp_v1_bbb"])
	assert.True(t, hashSet["rcp_v1_ccc"])
}

func TestCollectConfigHashes_SkipsEmpty(t *testing.T) {
	sessions := []api.SessionUpload{
		{
			ConfigHash: "",
			Runs: []api.RunUpload{
				{ConfigHash: ""},
				{ConfigHash: "rcp_v1_aaa"},
			},
		},
	}

	hashes := CollectConfigHashes(sessions)
	assert.Len(t, hashes, 1)
	assert.Equal(t, "rcp_v1_aaa", hashes[0])
}

func TestCollectConfigHashes_EmptyInput(t *testing.T) {
	hashes := CollectConfigHashes(nil)
	assert.Empty(t, hashes)
}

func TestCollectConfigHashes_NoHashes(t *testing.T) {
	sessions := []api.SessionUpload{
		{Runs: []api.RunUpload{{PromptHash: "ph_1"}}},
	}
	hashes := CollectConfigHashes(sessions)
	assert.Empty(t, hashes)
}
