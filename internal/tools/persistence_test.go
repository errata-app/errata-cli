package tools_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/tools"
)

// ─── LoadDisabledTools / SaveDisabledTools ────────────────────────────────────

func TestLoadDisabledTools_EmptyPath(t *testing.T) {
	m, err := tools.LoadDisabledTools("")
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestLoadDisabledTools_NonExistentFile(t *testing.T) {
	m, err := tools.LoadDisabledTools(t.TempDir() + "/nope.json")
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestSaveAndLoadDisabledTools_RoundTrip(t *testing.T) {
	path := t.TempDir() + "/tools.json"
	disabled := map[string]bool{"bash": true, "write_file": true}
	require.NoError(t, tools.SaveDisabledTools(path, disabled))
	loaded, err := tools.LoadDisabledTools(path)
	require.NoError(t, err)
	assert.Equal(t, disabled, loaded)
}

func TestSaveDisabledTools_EmptyPathIsNoop(t *testing.T) {
	// Saving with empty path should not error and not create any file.
	err := tools.SaveDisabledTools("", map[string]bool{"bash": true})
	assert.NoError(t, err)
}

func TestSaveDisabledTools_NilRemovesFile(t *testing.T) {
	path := t.TempDir() + "/tools.json"
	require.NoError(t, tools.SaveDisabledTools(path, map[string]bool{"bash": true}))
	// Now save nil — file should be removed.
	require.NoError(t, tools.SaveDisabledTools(path, nil))
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "file should be removed when disabled set is nil")
}

func TestSaveDisabledTools_EmptyMapRemovesFile(t *testing.T) {
	path := t.TempDir() + "/tools.json"
	require.NoError(t, tools.SaveDisabledTools(path, map[string]bool{"bash": true}))
	require.NoError(t, tools.SaveDisabledTools(path, map[string]bool{}))
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "file should be removed when disabled set is empty")
}
