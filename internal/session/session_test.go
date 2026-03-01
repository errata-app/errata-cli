package session

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateID_Format(t *testing.T) {
	id := GenerateID()
	// ses_ prefix + UUID v7
	matched, err := regexp.MatchString(
		`^ses_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, id)
	require.NoError(t, err)
	assert.True(t, matched, "ID %q does not match expected format", id)
}

func TestGenerateID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for range 100 {
		id := GenerateID()
		assert.False(t, ids[id], "duplicate ID generated: %s", id)
		ids[id] = true
	}
}

func TestNew_ReturnsCorrectPaths(t *testing.T) {
	base := t.TempDir()
	id, paths := New(base)
	assert.NotEmpty(t, id)
	assert.Equal(t, filepath.Join(base, id), paths.Dir)
	assert.Equal(t, filepath.Join(base, id, "history.json"), paths.HistoryPath)
	assert.Equal(t, filepath.Join(base, id, "checkpoint.json"), paths.CheckpointPath)
	assert.Equal(t, filepath.Join(base, id, "meta.json"), paths.MetaPath)
	assert.Equal(t, filepath.Join(base, id, "feed.json"), paths.FeedPath)
	assert.Equal(t, filepath.Join(base, id, "recipe.md"), paths.RecipePath)
}

func TestPathsFor_ReturnsCorrectPaths(t *testing.T) {
	base := "/tmp/sessions"
	id := "ses_019505e2-c38a-7b1e-8b3c-4d5e6f7a8b9c"
	paths := PathsFor(base, id)
	assert.Equal(t, "/tmp/sessions/"+id, paths.Dir)
	assert.Equal(t, "/tmp/sessions/"+id+"/history.json", paths.HistoryPath)
	assert.Equal(t, "/tmp/sessions/"+id+"/checkpoint.json", paths.CheckpointPath)
	assert.Equal(t, "/tmp/sessions/"+id+"/meta.json", paths.MetaPath)
	assert.Equal(t, "/tmp/sessions/"+id+"/feed.json", paths.FeedPath)
	assert.Equal(t, "/tmp/sessions/"+id+"/recipe.md", paths.RecipePath)
}

func TestSaveMeta_LoadMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")

	now := time.Now().Truncate(time.Second)
	m := Meta{
		ID:           "ses_019505e2-c38a-7b1e-8b3c-4d5e6f7a8b9c",
		FirstPrompt:  "fix the bug",
		LastPrompt:   "add tests",
		CreatedAt:    now,
		LastActiveAt: now.Add(time.Hour),
		PromptCount:  5,
		Models:       []string{"claude-sonnet-4-6", "gpt-4o"},
	}

	require.NoError(t, SaveMeta(path, m))

	loaded, err := LoadMeta(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, m.ID, loaded.ID)
	assert.Equal(t, m.FirstPrompt, loaded.FirstPrompt)
	assert.Equal(t, m.LastPrompt, loaded.LastPrompt)
	assert.True(t, m.CreatedAt.Equal(loaded.CreatedAt))
	assert.True(t, m.LastActiveAt.Equal(loaded.LastActiveAt))
	assert.Equal(t, m.PromptCount, loaded.PromptCount)
	assert.Equal(t, m.Models, loaded.Models)
}

func TestLoadMeta_MissingFile(t *testing.T) {
	m, err := LoadMeta("/nonexistent/path/meta.json")
	require.NoError(t, err)
	assert.Nil(t, m)
}

func TestLoadMeta_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid json"), 0o600))

	m, err := LoadMeta(path)
	require.Error(t, err)
	assert.Nil(t, m)
}

func TestSaveFeed_LoadFeed_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "feed.json")

	entries := []FeedEntry{
		{Kind: "message", Text: "Welcome!"},
		{
			Kind:   "run",
			Prompt: "fix the bug",
			Models: []ModelEntry{
				{ID: "claude-sonnet-4-6", Text: "I fixed it.", ProposedFiles: []string{"main.go"}},
				{ID: "gpt-4o", Text: "Done."},
			},
			Note: "Applied: main.go",
		},
	}

	require.NoError(t, SaveFeed(path, entries))

	loaded, err := LoadFeed(path)
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	assert.Equal(t, "message", loaded[0].Kind)
	assert.Equal(t, "Welcome!", loaded[0].Text)
	assert.Equal(t, "run", loaded[1].Kind)
	assert.Equal(t, "fix the bug", loaded[1].Prompt)
	require.Len(t, loaded[1].Models, 2)
	assert.Equal(t, "claude-sonnet-4-6", loaded[1].Models[0].ID)
	assert.Equal(t, []string{"main.go"}, loaded[1].Models[0].ProposedFiles)
	assert.Equal(t, "Applied: main.go", loaded[1].Note)
}

func TestLoadFeed_MissingFile(t *testing.T) {
	entries, err := LoadFeed("/nonexistent/path/feed.json")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestList_NewestFirst(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	ids := []string{
		"ses_01950000-0000-7000-8000-000000000001",
		"ses_01950000-0000-7000-8000-000000000002",
	}
	for i, id := range ids {
		paths := PathsFor(base, id)
		require.NoError(t, os.MkdirAll(paths.Dir, 0o750))
		require.NoError(t, SaveMeta(paths.MetaPath, Meta{
			ID:           id,
			CreatedAt:    now,
			LastActiveAt: now.Add(time.Duration(i) * time.Hour),
		}))
	}

	metas, err := List(base)
	require.NoError(t, err)
	require.Len(t, metas, 2)
	assert.Equal(t, ids[1], metas[0].ID) // newer
	assert.Equal(t, ids[0], metas[1].ID) // older
}

func TestList_SkipsCorrupt(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	// Create one valid session.
	goodID := "ses_01950000-0000-7000-8000-000000000001"
	goodPaths := PathsFor(base, goodID)
	require.NoError(t, os.MkdirAll(goodPaths.Dir, 0o750))
	require.NoError(t, SaveMeta(goodPaths.MetaPath, Meta{ID: goodID, CreatedAt: now, LastActiveAt: now}))

	// Create a corrupt session.
	badDir := filepath.Join(base, "ses_01950000-0000-7000-8000-corrupt")
	require.NoError(t, os.MkdirAll(badDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "meta.json"), []byte("bad"), 0o600))

	metas, err := List(base)
	require.NoError(t, err)
	require.Len(t, metas, 1)
	assert.Equal(t, goodID, metas[0].ID)
}

func TestList_EmptyDir(t *testing.T) {
	base := t.TempDir()
	metas, err := List(base)
	require.NoError(t, err)
	assert.Empty(t, metas)
}

func TestList_MissingDir(t *testing.T) {
	metas, err := List("/nonexistent/sessions")
	require.NoError(t, err)
	assert.Nil(t, metas)
}

func TestLatestID_ReturnsNewest(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	ids := []string{
		"ses_01950000-0000-7000-8000-000000000001",
		"ses_01950000-0000-7000-8000-000000000002",
	}
	for i, id := range ids {
		paths := PathsFor(base, id)
		require.NoError(t, os.MkdirAll(paths.Dir, 0o750))
		require.NoError(t, SaveMeta(paths.MetaPath, Meta{
			ID:           id,
			CreatedAt:    now,
			LastActiveAt: now.Add(time.Duration(i) * time.Hour),
		}))
	}

	id, err := LatestID(base)
	require.NoError(t, err)
	assert.Equal(t, ids[1], id)
}

func TestLatestID_NoSessions(t *testing.T) {
	base := t.TempDir()
	_, err := LatestID(base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sessions found")
}

func TestResolve_ExactMatch(t *testing.T) {
	base := t.TempDir()
	id := "ses_019505e2-c38a-7b1e-8b3c-4d5e6f7a8b9c"
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o750))

	resolved, err := Resolve(base, id)
	require.NoError(t, err)
	assert.Equal(t, id, resolved)
}

func TestResolve_PrefixMatch(t *testing.T) {
	base := t.TempDir()
	id := "ses_019505e2-c38a-7b1e-8b3c-4d5e6f7a8b9c"
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o750))

	resolved, err := Resolve(base, "ses_0195")
	require.NoError(t, err)
	assert.Equal(t, id, resolved)
}

func TestResolve_Ambiguous(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "ses_019505e2-aaaa-7000-8000-000000000001"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "ses_019505e2-aaaa-7000-8000-000000000002"), 0o750))

	_, err := Resolve(base, "ses_019505e2-aaaa")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestResolve_NotFound(t *testing.T) {
	base := t.TempDir()
	_, err := Resolve(base, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no session")
}

func TestResolve_MissingDir(t *testing.T) {
	_, err := Resolve("/nonexistent/sessions", "abcd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sessions found")
}
