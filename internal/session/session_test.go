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
	assert.Equal(t, filepath.Join(base, id, "session_metadata.json"), paths.MetadataPath)
	assert.Equal(t, filepath.Join(base, id, "session_content.json"), paths.ContentPath)
	assert.Equal(t, filepath.Join(base, id, "checkpoint.json"), paths.CheckpointPath)
	assert.Equal(t, filepath.Join(base, id, "recipe.md"), paths.RecipePath)
}

func TestPathsFor_ReturnsCorrectPaths(t *testing.T) {
	base := "/tmp/sessions"
	id := "ses_019505e2-c38a-7b1e-8b3c-4d5e6f7a8b9c"
	paths := PathsFor(base, id)
	assert.Equal(t, "/tmp/sessions/"+id, paths.Dir)
	assert.Equal(t, "/tmp/sessions/"+id+"/session_metadata.json", paths.MetadataPath)
	assert.Equal(t, "/tmp/sessions/"+id+"/session_content.json", paths.ContentPath)
	assert.Equal(t, "/tmp/sessions/"+id+"/checkpoint.json", paths.CheckpointPath)
	assert.Equal(t, "/tmp/sessions/"+id+"/recipe.md", paths.RecipePath)
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
		require.NoError(t, SaveMetadata(paths.MetadataPath, SessionMetadata{
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
	require.NoError(t, SaveMetadata(goodPaths.MetadataPath, SessionMetadata{ID: goodID, CreatedAt: now, LastActiveAt: now}))

	// Create a corrupt session.
	badDir := filepath.Join(base, "ses_01950000-0000-7000-8000-corrupt")
	require.NoError(t, os.MkdirAll(badDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "session_metadata.json"), []byte("bad"), 0o600))

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
		require.NoError(t, SaveMetadata(paths.MetadataPath, SessionMetadata{
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
