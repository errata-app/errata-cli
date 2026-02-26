package history_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// turns returns a slice of ConversationTurn values for use in test cases.
func turns(pairs ...string) []models.ConversationTurn {
	if len(pairs)%2 != 0 {
		panic("turns: pairs must be even (role, content, role, content, ...)")
	}
	out := make([]models.ConversationTurn, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, models.ConversationTurn{Role: pairs[i], Content: pairs[i+1]})
	}
	return out
}

// tmpPath returns a path inside t.TempDir() with the given filename.
func tmpPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// ─── Save / Load round-trip ───────────────────────────────────────────────────

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	path := tmpPath(t, "history.json")

	input := map[string][]models.ConversationTurn{
		"claude-sonnet-4-6": turns(
			"user", "write a sort function",
			"assistant", "here is a sort function: ...",
		),
		"gpt-4o": turns(
			"user", "explain recursion",
			"assistant", "recursion is when a function calls itself",
		),
	}

	require.NoError(t, history.Save(path, input))

	got, err := history.Load(path)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, len(input), len(got), "loaded map should have the same number of keys")

	for modelID, wantTurns := range input {
		gotTurns, ok := got[modelID]
		require.True(t, ok, "model %q missing from loaded history", modelID)
		require.Len(t, gotTurns, len(wantTurns), "wrong number of turns for model %q", modelID)
		for i, wt := range wantTurns {
			assert.Equal(t, wt.Role, gotTurns[i].Role,
				"model %q turn %d: role mismatch", modelID, i)
			assert.Equal(t, wt.Content, gotTurns[i].Content,
				"model %q turn %d: content mismatch", modelID, i)
		}
	}
}

// ─── Load: missing file ───────────────────────────────────────────────────────

func TestLoad_MissingFile(t *testing.T) {
	path := tmpPath(t, "does-not-exist.json")

	got, err := history.Load(path)
	assert.NoError(t, err, "missing file should not be an error")
	assert.Nil(t, got, "missing file should return nil map")
}

// ─── Load: corrupt JSON ───────────────────────────────────────────────────────

func TestLoad_CorruptJSON(t *testing.T) {
	path := tmpPath(t, "corrupt.json")
	require.NoError(t, os.WriteFile(path, []byte("{this is not valid json!!!"), 0o644))

	got, err := history.Load(path)
	assert.Error(t, err, "corrupt JSON should return a non-nil error")
	assert.Nil(t, got, "corrupt JSON should return nil map")
}

// ─── Save: creates parent directory ──────────────────────────────────────────

func TestSave_CreatesDirectory(t *testing.T) {
	// The parent directory (subdir) does not yet exist.
	path := filepath.Join(t.TempDir(), "subdir", "history.json")

	input := map[string][]models.ConversationTurn{
		"gpt-4o": turns("user", "hello", "assistant", "world"),
	}

	err := history.Save(path, input)
	require.NoError(t, err, "Save should create the parent directory automatically")

	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "history file should exist after Save")
}

// ─── Clear: removes file ──────────────────────────────────────────────────────

func TestClear_RemovesFile(t *testing.T) {
	path := tmpPath(t, "history.json")

	input := map[string][]models.ConversationTurn{
		"claude-sonnet-4-6": turns("user", "hi", "assistant", "hello"),
	}
	require.NoError(t, history.Save(path, input))

	// Confirm the file exists before we clear it.
	_, err := os.Stat(path)
	require.NoError(t, err, "file should exist before Clear")

	require.NoError(t, history.Clear(path))

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "file should not exist after Clear")
}

// ─── Clear: missing file is not an error ─────────────────────────────────────

func TestClear_MissingFile(t *testing.T) {
	path := tmpPath(t, "never-created.json")

	err := history.Clear(path)
	assert.NoError(t, err, "Clear on a missing file should not return an error")
}

// ─── Save: empty/nil map is a no-op ─────────────────────────────────────────

func TestSave_EmptyMap(t *testing.T) {
	path := tmpPath(t, "history.json")
	err := history.Save(path, map[string][]models.ConversationTurn{})
	assert.NoError(t, err, "Save with empty map should not error")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "empty map should not create a file")
}

func TestSave_NilMap(t *testing.T) {
	path := tmpPath(t, "history.json")
	err := history.Save(path, nil)
	assert.NoError(t, err, "Save with nil map should not error")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "nil map should not create a file")
}

// ─── Load: ReadFile error that is not IsNotExist ────────────────────────────

func TestLoad_ReadError(t *testing.T) {
	// Use a directory path as if it were a file → ReadFile returns a non-IsNotExist error.
	dir := t.TempDir()
	got, err := history.Load(dir)
	assert.Error(t, err)
	assert.Nil(t, got)
}

// ─── Load: valid but empty JSON object ──────────────────────────────────────

func TestLoad_EmptyJSONObject(t *testing.T) {
	path := tmpPath(t, "empty.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o644))
	got, err := history.Load(path)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// ─── Save: MkdirAll error ───────────────────────────────────────────────────

func TestSave_MkdirAllError(t *testing.T) {
	// /dev/null is a file, not a directory — MkdirAll will fail.
	input := map[string][]models.ConversationTurn{
		"m": turns("user", "hello", "assistant", "world"),
	}
	err := history.Save("/dev/null/sub/history.json", input)
	assert.Error(t, err, "MkdirAll with impossible path should error")
}

// ─── Save: WriteFile error (read-only directory) ────────────────────────────

func TestSave_WriteFileError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o444))
	defer os.Chmod(dir, 0o755)

	input := map[string][]models.ConversationTurn{
		"m": turns("user", "hello", "assistant", "world"),
	}
	err := history.Save(filepath.Join(dir, "history.json"), input)
	assert.Error(t, err, "Save to read-only dir should error")
}

// ─── Clear: non-IsNotExist error ────────────────────────────────────────────

func TestClear_DirectoryReturnsError(t *testing.T) {
	// Passing a non-empty directory to os.Remove returns an error that is
	// not IsNotExist (directories can't be removed with os.Remove when non-empty).
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	// Write a file inside the dir so Remove on dir fails.
	require.NoError(t, os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o644))
	err := history.Clear(sub)
	assert.Error(t, err, "Clear on non-empty directory should error")
}

// ─── Save: no .tmp file left behind (atomic write) ───────────────────────────

func TestSave_Atomic(t *testing.T) {
	path := tmpPath(t, "history.json")
	tmp := path + ".tmp"

	input := map[string][]models.ConversationTurn{
		"gemini-2.0-flash": turns("user", "test", "assistant", "ok"),
	}

	require.NoError(t, history.Save(path, input))

	_, err := os.Stat(tmp)
	assert.True(t, os.IsNotExist(err), ".tmp file must not be left behind after Save")

	// Confirm the real file is present and readable.
	_, err = os.Stat(path)
	assert.NoError(t, err, "history file should exist after atomic Save")
}
