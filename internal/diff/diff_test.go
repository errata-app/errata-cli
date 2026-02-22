package diff_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/diff"
)

func TestCompute_NewFile(t *testing.T) {
	fd := diff.Compute("nonexistent.txt", "hello\nworld\n")
	assert.True(t, fd.IsNew)
	assert.Equal(t, 2, fd.Adds)
	assert.Equal(t, 0, fd.Removes)
}

func TestCompute_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(file, []byte("line1\nline2\nline3\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("test.txt", "line1\nchanged\nline3\n")
	assert.False(t, fd.IsNew)
	assert.Equal(t, 1, fd.Adds)
	assert.Equal(t, 1, fd.Removes)
}

func TestCompute_Truncated(t *testing.T) {
	// Generate more lines than MaxDiffLines to trigger truncation.
	var new_ string
	for range diff.MaxDiffLines + 10 {
		new_ += "changed\n"
	}
	fd := diff.Compute("phantom.txt", new_)
	assert.True(t, fd.IsNew)
	// Lines should be capped at MaxDiffLines
	assert.LessOrEqual(t, len(fd.Lines), diff.MaxDiffLines)
	assert.Greater(t, fd.Truncated, 0)
}

func TestCompute_UnchangedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "same.txt")
	content := "no change\n"
	require.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("same.txt", content)
	assert.Equal(t, 0, fd.Adds)
	assert.Equal(t, 0, fd.Removes)
}
