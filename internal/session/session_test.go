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
	// 16-character hex string (8 random bytes)
	matched, err := regexp.MatchString(`^[0-9a-f]{16}$`, id)
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
	paths := PathsFor(base, "a3f2b1c9d4e5f607")
	assert.Equal(t, "/tmp/sessions/a3f2b1c9d4e5f607", paths.Dir)
	assert.Equal(t, "/tmp/sessions/a3f2b1c9d4e5f607/history.json", paths.HistoryPath)
	assert.Equal(t, "/tmp/sessions/a3f2b1c9d4e5f607/checkpoint.json", paths.CheckpointPath)
	assert.Equal(t, "/tmp/sessions/a3f2b1c9d4e5f607/meta.json", paths.MetaPath)
	assert.Equal(t, "/tmp/sessions/a3f2b1c9d4e5f607/feed.json", paths.FeedPath)
	assert.Equal(t, "/tmp/sessions/a3f2b1c9d4e5f607/recipe.md", paths.RecipePath)
}

func TestSaveMeta_LoadMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")

	now := time.Now().Truncate(time.Second)
	m := Meta{
		ID:           "a3f2b1c9d4e5f607",
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
	assert.NoError(t, err)
	assert.Nil(t, m)
}

func TestLoadMeta_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid json"), 0o600))

	m, err := LoadMeta(path)
	assert.Error(t, err)
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
	assert.NoError(t, err)
	assert.Nil(t, entries)
}

func TestList_NewestFirst(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	// Create two sessions with different timestamps.
	for i, id := range []string{"aaaa000000000001", "bbbb000000000002"} {
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
	assert.Equal(t, "bbbb000000000002", metas[0].ID) // newer
	assert.Equal(t, "aaaa000000000001", metas[1].ID) // older
}

func TestList_SkipsCorrupt(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	// Create one valid session.
	goodID := "aaaa000000000001"
	goodPaths := PathsFor(base, goodID)
	require.NoError(t, os.MkdirAll(goodPaths.Dir, 0o750))
	require.NoError(t, SaveMeta(goodPaths.MetaPath, Meta{ID: goodID, CreatedAt: now, LastActiveAt: now}))

	// Create a corrupt session.
	badDir := filepath.Join(base, "bbbbcorruptbad01")
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

	for i, id := range []string{"aaaa000000000001", "bbbb000000000002"} {
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
	assert.Equal(t, "bbbb000000000002", id)
}

func TestLatestID_NoSessions(t *testing.T) {
	base := t.TempDir()
	_, err := LatestID(base)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no sessions found")
}

func TestResolve_ExactMatch(t *testing.T) {
	base := t.TempDir()
	id := "a3f2b1c9d4e5f607"
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o750))

	resolved, err := Resolve(base, id)
	require.NoError(t, err)
	assert.Equal(t, id, resolved)
}

func TestResolve_PrefixMatch(t *testing.T) {
	base := t.TempDir()
	id := "a3f2b1c9d4e5f607"
	require.NoError(t, os.MkdirAll(filepath.Join(base, id), 0o750))

	resolved, err := Resolve(base, "a3f2")
	require.NoError(t, err)
	assert.Equal(t, id, resolved)
}

func TestResolve_Ambiguous(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "abcd000000000001"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "abcd000000000002"), 0o750))

	_, err := Resolve(base, "abcd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestResolve_NotFound(t *testing.T) {
	base := t.TempDir()
	_, err := Resolve(base, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no session")
}

func TestResolve_MissingDir(t *testing.T) {
	_, err := Resolve("/nonexistent/sessions", "abcd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no sessions found")
}
