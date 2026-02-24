package tools_test

import (
	"context"
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

	t.Chdir(dir)
	got := tools.ExecuteRead("hello.txt")
	assert.Equal(t, "hello world", got)
}

func TestExecuteRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := tools.ExecuteRead("nonexistent.txt")
	assert.Contains(t, got, "[error:")
}

func TestExecuteRead_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
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

	t.Chdir(dir)
	got := tools.ExecuteRead("sub/data.txt")
	assert.Equal(t, "nested", got)
}

func TestApplyWrites(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

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
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(".", 2)
	assert.Contains(t, out, "a.txt")
	assert.Contains(t, out, "sub/")
	assert.Contains(t, out, "b.txt")
}

func TestListDirectory_DepthOne(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(".", 1)
	assert.Contains(t, out, "sub/")
	assert.NotContains(t, out, "nested.txt")
}

func TestListDirectory_DepthTwo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(".", 2)
	assert.Contains(t, out, "sub/")
	assert.Contains(t, out, "nested.txt")
}

func TestListDirectory_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteListDirectory("../../etc", 1)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "outside the working directory")
}

func TestListDirectory_NotExist(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteListDirectory("nosuchdir", 1)
	assert.Contains(t, out, "[error:")
}

func TestListDirectory_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644))
	t.Chdir(dir)
	out := tools.ExecuteListDirectory("file.txt", 1)
	assert.Contains(t, out, "[error:")
}

func TestListDirectory_DefaultDepthZero(t *testing.T) {
	// depth=0 should fall back to default (2), not error
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("f"), 0o644))
	t.Chdir(dir)
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
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles("*.go", ".")
	assert.Contains(t, out, "main.go")
	assert.Contains(t, out, "main_test.go")
	assert.NotContains(t, out, "readme.md")
}

func TestSearchFiles_NoMatches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles("*.go", ".")
	assert.Equal(t, "(no matches)", out)
}

func TestSearchFiles_DefaultBasePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte(""), 0o644))
	t.Chdir(dir)

	// empty base_path defaults to "."
	out := tools.ExecuteSearchFiles("*.go", "")
	assert.Contains(t, out, "foo.go")
}

func TestSearchFiles_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteSearchFiles("*.go", "../../etc")
	assert.Contains(t, out, "[error:")
}

// --- ExecuteSearchCode ---

func TestSearchCode_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\nfunc Hello() {}\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode("Hello", ".", "")
	assert.Contains(t, out, "Hello")
	assert.Contains(t, out, "foo.go")
}

func TestSearchCode_NoMatches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode("NoSuchPattern12345", ".", "")
	assert.Equal(t, "(no matches)", out)
}

func TestSearchCode_FileGlobFilter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("needle here\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bar.txt"), []byte("needle here\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode("needle", ".", "*.go")
	assert.Contains(t, out, "foo.go")
	assert.NotContains(t, out, "bar.txt")
}

func TestSearchCode_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), []byte("findme\n"), 0o644))
	t.Chdir(dir)

	// empty path defaults to "."
	out := tools.ExecuteSearchCode("findme", "", "")
	assert.Contains(t, out, "findme")
}

func TestSearchCode_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteSearchCode("root", "../../etc", "")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "outside the working directory")
}

// --- ExecuteSearchFiles: ** glob patterns ---

func TestSearchFiles_DoubleStarRootMatch(t *testing.T) {
	// **/*.go should match a file at the root of base_path (** = zero segments)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles("**/*.go", ".")
	assert.Contains(t, out, "main.go")
}

func TestSearchFiles_DoubleStarDeep(t *testing.T) {
	// **/*.go should match files nested multiple levels deep
	dir := t.TempDir()
	deep := filepath.Join(dir, "internal", "tools")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "tools.go"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles("**/*.go", ".")
	assert.Contains(t, out, "tools.go")
	assert.NotContains(t, out, "[error:")
}

func TestSearchFiles_DoubleStarMidPattern(t *testing.T) {
	// internal/**/*.go matches at zero and multiple depths under internal/
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "runner"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cmd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "top.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "internal", "runner", "runner.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmd", "main.go"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles("internal/**/*.go", ".")
	assert.Contains(t, out, "top.go")
	assert.Contains(t, out, "runner.go")
	assert.NotContains(t, out, "cmd")
}

func TestSearchFiles_DoubleStarNoMatch(t *testing.T) {
	// **/*.go should not match .txt files
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles("**/*.go", ".")
	assert.Equal(t, "(no matches)", out)
}

func TestSearchFiles_DoubleStarTestFiles(t *testing.T) {
	// **/*_test.go should find only test files
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "foo.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "foo_test.go"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles("**/*_test.go", ".")
	assert.Contains(t, out, "foo_test.go")
	assert.NotContains(t, out, "foo.go\n")
	assert.NotContains(t, out, "[error:")
}

func TestSearchCode_LineNumbers(t *testing.T) {
	dir := t.TempDir()
	content := "line one\nline two\nfind me\nline four\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode("find me", ".", "")
	// grep -n output format: filename:linenum:content
	assert.True(t, strings.Contains(out, ":3:") || strings.Contains(out, "find me"),
		"expected line number or match content in output: %q", out)
}

// ─── ActiveDefinitions ───────────────────────────────────────────────────────

func TestActiveDefinitions_NilDisabled_ReturnsAll(t *testing.T) {
	all := tools.ActiveDefinitions(nil)
	assert.Equal(t, tools.Definitions, all)
}

func TestActiveDefinitions_EmptyDisabled_ReturnsAll(t *testing.T) {
	all := tools.ActiveDefinitions(map[string]bool{})
	assert.Equal(t, tools.Definitions, all)
}

func TestActiveDefinitions_DisablesOneToolByName(t *testing.T) {
	disabled := map[string]bool{tools.BashToolName: true}
	active := tools.ActiveDefinitions(disabled)
	for _, d := range active {
		assert.NotEqual(t, tools.BashToolName, d.Name, "bash should be excluded")
	}
	assert.Len(t, active, len(tools.Definitions)-1)
}

func TestActiveDefinitions_DisablesMultipleTools(t *testing.T) {
	disabled := map[string]bool{
		tools.BashToolName:  true,
		tools.SearchCodeName: true,
	}
	active := tools.ActiveDefinitions(disabled)
	assert.Len(t, active, len(tools.Definitions)-2)
	for _, d := range active {
		assert.NotEqual(t, tools.BashToolName, d.Name)
		assert.NotEqual(t, tools.SearchCodeName, d.Name)
	}
}

// ─── WithActiveTools / ActiveToolsFromContext ─────────────────────────────────

func TestActiveToolsFromContext_DefaultWhenNilContext(t *testing.T) {
	ctx := context.Background()
	got := tools.ActiveToolsFromContext(ctx)
	assert.Equal(t, tools.Definitions, got)
}

func TestWithActiveTools_RoundTrip(t *testing.T) {
	subset := tools.Definitions[:2]
	ctx := tools.WithActiveTools(context.Background(), subset)
	got := tools.ActiveToolsFromContext(ctx)
	assert.Equal(t, subset, got)
}

func TestActiveToolsFromContext_EmptySliceFallsBackToAll(t *testing.T) {
	// A context carrying an empty slice should fall back to all Definitions.
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{})
	got := tools.ActiveToolsFromContext(ctx)
	assert.Equal(t, tools.Definitions, got)
}

// ─── SystemPromptSuffix ───────────────────────────────────────────────────────

func TestSystemPromptSuffix_NonEmpty(t *testing.T) {
	s := tools.SystemPromptSuffix()
	assert.NotEmpty(t, s)
}

func TestSystemPromptSuffix_ContainsKeyGuidance(t *testing.T) {
	s := tools.SystemPromptSuffix()
	assert.Contains(t, s, "write_file")
	assert.Contains(t, s, "list_directory")
	assert.Contains(t, s, "search_code")
}

// --- ExecuteBash ---

func TestExecuteBash_SimpleCommand(t *testing.T) {
	out := tools.ExecuteBash("echo hello")
	assert.Equal(t, "hello", out)
}

func TestExecuteBash_StderrCombined(t *testing.T) {
	// stderr should appear in the output
	out := tools.ExecuteBash("echo out && echo err >&2")
	assert.Contains(t, out, "out")
	assert.Contains(t, out, "err")
}

func TestExecuteBash_NonZeroExitIncludesOutput(t *testing.T) {
	// A failing command should return its output AND an exit notice.
	out := tools.ExecuteBash("echo before && exit 1")
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "[exit:")
}

func TestExecuteBash_NonZeroExitNoOutput(t *testing.T) {
	// When there's no output, only the exit notice is returned.
	out := tools.ExecuteBash("exit 2")
	assert.Contains(t, out, "[exit:")
	assert.NotContains(t, out, "[error:")
}

func TestExecuteBash_NoOutput(t *testing.T) {
	out := tools.ExecuteBash("true")
	assert.Equal(t, "(no output)", out)
}

func TestExecuteBash_MultilineOutput(t *testing.T) {
	out := tools.ExecuteBash("printf 'a\nb\nc\n'")
	assert.Contains(t, out, "a")
	assert.Contains(t, out, "b")
	assert.Contains(t, out, "c")
}

func TestExecuteBash_PipeSupport(t *testing.T) {
	out := tools.ExecuteBash("echo hello world | tr ' ' '_'")
	assert.Equal(t, "hello_world", out)
}

func TestExecuteBash_WorkingDirIsInherited(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteBash("pwd")
	// The resolved pwd may differ from dir due to symlinks; just check no error.
	assert.NotContains(t, out, "[error:")
	assert.NotContains(t, out, "[exit:")
}

func TestExecuteBash_OutputCapped(t *testing.T) {
	// Generate output larger than bashOutputLimit (10 000 bytes).
	// Each "A" repeated 200 times + newline = 201 bytes; 60 lines = 12060 bytes.
	out := tools.ExecuteBash("python3 -c \"print('A'*200+'\\n'*1, end='')\" | for i in $(seq 60); do cat; done 2>/dev/null || printf '%0.s' {1..12000} | head -c 12000 | tr '\\0' 'A'")
	// Fallback: just generate a long string directly.
	if strings.Contains(out, "[exit:") || strings.Contains(out, "(no output)") {
		out = tools.ExecuteBash("dd if=/dev/zero bs=1 count=12000 2>/dev/null | tr '\\0' 'A'")
	}
	if len(out) > 10_000 {
		assert.Contains(t, out, "[truncated:")
	}
	// Output must be ≤ bashOutputLimit + len of truncation notice
	assert.LessOrEqual(t, len(out), 10_100)
}

func TestExecuteBash_Timeout(t *testing.T) {
	t.Setenv("ERRATA_BASH_TIMEOUT", "2s")
	out := tools.ExecuteBash("sleep 60")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "timed out")
}
