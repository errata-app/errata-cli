package diff_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/diff"
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
	var sb strings.Builder
	for range diff.MaxDiffLines + 10 {
		sb.WriteString("changed\n")
	}
	fd := diff.Compute("phantom.txt", sb.String())
	assert.True(t, fd.IsNew)
	// Lines should be capped at MaxDiffLines
	assert.LessOrEqual(t, len(fd.Lines), diff.MaxDiffLines)
	assert.Positive(t, fd.Truncated)
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

func TestCompute_EmptyNewContent(t *testing.T) {
	// Replacing a file with empty content should produce only Remove lines.
	dir := t.TempDir()
	file := filepath.Join(dir, "doomed.txt")
	require.NoError(t, os.WriteFile(file, []byte("line1\nline2\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("doomed.txt", "")
	assert.Equal(t, 0, fd.Adds)
	assert.Equal(t, 2, fd.Removes)
	for _, line := range fd.Lines {
		assert.NotEqual(t, diff.Add, line.Kind)
	}
}

func TestCompute_BothEmpty(t *testing.T) {
	// Nonexistent file + empty new content = no diff.
	fd := diff.Compute("nonexistent_empty.txt", "")
	assert.True(t, fd.IsNew)
	assert.Equal(t, 0, fd.Adds)
	assert.Equal(t, 0, fd.Removes)
	assert.Empty(t, fd.Lines)
}

func TestCompute_CRLFLineEndings(t *testing.T) {
	// CRLF content should still produce meaningful diffs.
	dir := t.TempDir()
	file := filepath.Join(dir, "crlf.txt")
	require.NoError(t, os.WriteFile(file, []byte("line1\r\nline2\r\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("crlf.txt", "line1\r\nchanged\r\n")
	assert.Positive(t, fd.Adds)
	assert.Positive(t, fd.Removes)
}

func TestCompute_SingleLineNoNewline(t *testing.T) {
	// File and new content with no trailing newline.
	dir := t.TempDir()
	file := filepath.Join(dir, "single.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("single.txt", "world")
	assert.Positive(t, fd.Adds)
	assert.Positive(t, fd.Removes)
}

func TestCompute_ContentPrefixCharacters(t *testing.T) {
	// Every DiffLine.Content should start with '+', '-', or ' '.
	dir := t.TempDir()
	file := filepath.Join(dir, "prefix.txt")
	require.NoError(t, os.WriteFile(file, []byte("old\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	fd := diff.Compute("prefix.txt", "new\n")
	for _, line := range fd.Lines {
		if line.Kind == diff.Hunk {
			continue
		}
		require.NotEmpty(t, line.Content, "line content should not be empty")
		prefix := line.Content[0]
		assert.True(t, prefix == '+' || prefix == '-' || prefix == ' ',
			"unexpected prefix %q in content %q", string(prefix), line.Content)
	}
}

func TestComputeDeleted(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "victim.txt")
	require.NoError(t, os.WriteFile(file, []byte("line1\nline2\nline3\n"), 0o644))

	fd := diff.ComputeDeleted(file)
	assert.True(t, fd.IsDeleted)
	assert.False(t, fd.IsNew)
	assert.Equal(t, 3, fd.Removes)
	assert.Equal(t, 0, fd.Adds)
	require.Len(t, fd.Lines, 3)
	for _, line := range fd.Lines {
		assert.Equal(t, diff.Remove, line.Kind)
		assert.True(t, strings.HasPrefix(line.Content, "-"), "expected '-' prefix, got %q", line.Content)
	}
}

func TestComputeDeleted_NonexistentFile(t *testing.T) {
	fd := diff.ComputeDeleted("/no/such/file.txt")
	assert.True(t, fd.IsDeleted)
	assert.Equal(t, 0, fd.Removes)
	assert.Empty(t, fd.Lines)
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
		var sb2 strings.Builder
		for _, sp := range line.Spans {
			sb2.WriteString(sp.Text)
		}
		assert.Equal(t, line.Content[1:], sb2.String(),
			"span texts for %q should reconstruct the content without prefix", line.Content)
	}
}
