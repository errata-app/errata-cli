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

func TestCompute_WordSpans_AdjacentPair(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "code.go")
	require.NoError(t, os.WriteFile(file, []byte("func foo(a int) {}\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("code.go", "func foo(a string) {}\n")
	require.Equal(t, 1, fd.Adds)
	require.Equal(t, 1, fd.Removes)

	var removeLine, addLine *diff.DiffLine
	for i := range fd.Lines {
		switch fd.Lines[i].Kind {
		case diff.Remove:
			removeLine = &fd.Lines[i]
		case diff.Add:
			addLine = &fd.Lines[i]
		}
	}
	require.NotNil(t, removeLine, "expected a Remove line")
	require.NotNil(t, addLine, "expected an Add line")

	// Both lines should have word-level spans.
	assert.NotEmpty(t, removeLine.Spans, "remove line should have spans")
	assert.NotEmpty(t, addLine.Spans, "add line should have spans")

	// Changed spans exist on both sides.
	var removeChanged, addChanged string
	for _, sp := range removeLine.Spans {
		if sp.Changed {
			removeChanged += sp.Text
		}
	}
	for _, sp := range addLine.Spans {
		if sp.Changed {
			addChanged += sp.Text
		}
	}
	assert.NotEmpty(t, removeChanged, "remove line should have at least one changed span")
	assert.NotEmpty(t, addChanged, "add line should have at least one changed span")

	// Span texts reconstruct the content (minus the leading prefix char).
	var removeReconstructed, addReconstructed string
	for _, sp := range removeLine.Spans {
		removeReconstructed += sp.Text
	}
	for _, sp := range addLine.Spans {
		addReconstructed += sp.Text
	}
	assert.Equal(t, removeLine.Content[1:], removeReconstructed)
	assert.Equal(t, addLine.Content[1:], addReconstructed)
}

func TestCompute_WordSpans_NewFileNoSpans(t *testing.T) {
	// A new file produces only Add lines with no Remove pair — no Spans expected.
	fd := diff.Compute("phantom.txt", "hello world\n")
	require.True(t, fd.IsNew)
	for _, line := range fd.Lines {
		if line.Kind == diff.Add {
			assert.Nil(t, line.Spans, "unpaired Add line should not have spans")
		}
	}
}

func TestCompute_WordSpans_SpanTextReconstructsLine(t *testing.T) {
	// Concatenating span texts should equal the line content minus the leading prefix char.
	dir := t.TempDir()
	file := filepath.Join(dir, "code.go")
	require.NoError(t, os.WriteFile(file, []byte("return x + y\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("code.go", "return x * y\n")
	for _, line := range fd.Lines {
		if len(line.Spans) == 0 {
			continue
		}
		var reconstructed string
		for _, sp := range line.Spans {
			reconstructed += sp.Text
		}
		assert.Equal(t, line.Content[1:], reconstructed,
			"span texts for %q should reconstruct the content without prefix", line.Content)
	}
}
