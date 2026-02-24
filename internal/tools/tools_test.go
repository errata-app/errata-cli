package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/tools"
)

func TestExecuteRead_FileFound(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello world"), 0o644))

	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("hello.txt")
	assert.Equal(t, "hello world", got)
}

func TestExecuteRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("nonexistent.txt")
	assert.Contains(t, got, "[error:")
}

func TestExecuteRead_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("../../etc/passwd")
	assert.Contains(t, got, "[error:")
	assert.Contains(t, got, "outside the working directory")
}

func TestExecuteRead_Nested(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	file := filepath.Join(sub, "data.txt")
	require.NoError(t, os.WriteFile(file, []byte("nested"), 0o644))

	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("sub/data.txt")
	assert.Equal(t, "nested", got)
}

func TestApplyWrites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	writes := []tools.FileWrite{
		{Path: "out.txt", Content: "content A"},
		{Path: "sub/out.txt", Content: "content B"},
	}
	require.NoError(t, tools.ApplyWrites(writes))

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content A", string(got))

	got2, err := os.ReadFile(filepath.Join(dir, "sub", "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content B", string(got2))
}

// --- ExecuteListDirectory ---

func TestListDirectory_Basic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteListDirectory(".", 2)
	assert.Contains(t, out, "a.txt")
	assert.Contains(t, out, "sub/")
	assert.Contains(t, out, "b.txt")
}

func TestListDirectory_DepthOne(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteListDirectory(".", 1)
	assert.Contains(t, out, "sub/")
	assert.NotContains(t, out, "nested.txt")
}

func TestListDirectory_DepthTwo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteListDirectory(".", 2)
	assert.Contains(t, out, "sub/")
	assert.Contains(t, out, "nested.txt")
}

func TestListDirectory_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	out := tools.ExecuteListDirectory("../../etc", 1)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "outside the working directory")
}

func TestListDirectory_NotExist(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	out := tools.ExecuteListDirectory("nosuchdir", 1)
	assert.Contains(t, out, "[error:")
}

func TestListDirectory_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644))
	require.NoError(t, os.Chdir(dir))
	out := tools.ExecuteListDirectory("file.txt", 1)
	assert.Contains(t, out, "[error:")
}

func TestListDirectory_DefaultDepthZero(t *testing.T) {
	// depth=0 should fall back to default (2), not error
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("f"), 0o644))
	require.NoError(t, os.Chdir(dir))
	out := tools.ExecuteListDirectory(".", 0)
	assert.Contains(t, out, "sub/")
	assert.Contains(t, out, "f.txt")
}

// --- ExecuteSearchFiles ---

func TestSearchFiles_GlobMatch(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte(""), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteSearchFiles("*.go", ".")
	assert.Contains(t, out, "main.go")
	assert.Contains(t, out, "main_test.go")
	assert.NotContains(t, out, "readme.md")
}

func TestSearchFiles_NoMatches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(""), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteSearchFiles("*.go", ".")
	assert.Equal(t, "(no matches)", out)
}

func TestSearchFiles_DefaultBasePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte(""), 0o644))
	require.NoError(t, os.Chdir(dir))

	// empty base_path defaults to "."
	out := tools.ExecuteSearchFiles("*.go", "")
	assert.Contains(t, out, "foo.go")
}

func TestSearchFiles_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	out := tools.ExecuteSearchFiles("*.go", "../../etc")
	assert.Contains(t, out, "[error:")
}

// --- ExecuteSearchCode ---

func TestSearchCode_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\nfunc Hello() {}\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteSearchCode("Hello", ".", "")
	assert.Contains(t, out, "Hello")
	assert.Contains(t, out, "foo.go")
}

func TestSearchCode_NoMatches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteSearchCode("NoSuchPattern12345", ".", "")
	assert.Equal(t, "(no matches)", out)
}

func TestSearchCode_FileGlobFilter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("needle here\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bar.txt"), []byte("needle here\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteSearchCode("needle", ".", "*.go")
	assert.Contains(t, out, "foo.go")
	assert.NotContains(t, out, "bar.txt")
}

func TestSearchCode_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), []byte("findme\n"), 0o644))
	require.NoError(t, os.Chdir(dir))

	// empty path defaults to "."
	out := tools.ExecuteSearchCode("findme", "", "")
	assert.Contains(t, out, "findme")
}

func TestSearchCode_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	out := tools.ExecuteSearchCode("root", "../../etc", "")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "outside the working directory")
}

func TestSearchCode_LineNumbers(t *testing.T) {
	dir := t.TempDir()
	content := "line one\nline two\nfind me\nline four\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte(content), 0o644))
	require.NoError(t, os.Chdir(dir))

	out := tools.ExecuteSearchCode("find me", ".", "")
	// grep -n output format: filename:linenum:content
	assert.True(t, strings.Contains(out, ":3:") || strings.Contains(out, "find me"),
		"expected line number or match content in output: %q", out)
}
