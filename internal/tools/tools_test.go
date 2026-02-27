package tools_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/tools"
)

func TestExecuteRead_FileFound(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello world"), 0o644))

	t.Chdir(dir)
	got := tools.ExecuteRead("hello.txt", 0, 0)
	assert.Equal(t, "hello world", got)
}

func TestExecuteRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := tools.ExecuteRead("nonexistent.txt", 0, 0)
	assert.Contains(t, got, "[error:")
}

func TestExecuteRead_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := tools.ExecuteRead("../../etc/passwd", 0, 0)
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
	got := tools.ExecuteRead("sub/data.txt", 0, 0)
	assert.Equal(t, "nested", got)
}

func TestExecuteRead_OffsetSkipsLines(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead("f.txt", 3, 0)
	assert.True(t, strings.HasPrefix(got, "line3"), "offset=3 should start at line3, got: %q", got)
	assert.NotContains(t, got, "line1")
	assert.NotContains(t, got, "line2")
}

func TestExecuteRead_LimitCapsLines(t *testing.T) {
	dir := t.TempDir()
	content := "a\nb\nc\nd\ne\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead("f.txt", 1, 2)
	// First 2 lines should be present; remainder should not.
	assert.True(t, strings.HasPrefix(got, "a\nb"), "expected first 2 lines, got: %q", got)
	assert.NotContains(t, got, "c\n")
	assert.NotContains(t, got, "d\n")
}

func TestExecuteRead_TruncationNotice(t *testing.T) {
	dir := t.TempDir()
	// 5 lines; read with limit=2 from offset=1 → 3 lines omitted
	content := "a\nb\nc\nd\ne\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead("f.txt", 1, 2)
	assert.Contains(t, got, "lines omitted")
	assert.Contains(t, got, "offset=3")
}

func TestExecuteRead_OffsetBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("only one line"), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead("f.txt", 999, 0)
	assert.Contains(t, got, "[error:")
	assert.Contains(t, got, "offset")
}

// TestExecuteRead_HardCap verifies that files exceeding maxReadLines are truncated.
func TestExecuteRead_HardCap(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for range 2005 {
		sb.WriteString("line\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead("big.txt", 0, 0)
	assert.Contains(t, got, "lines omitted")
	assert.Contains(t, got, "offset=2001")
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

	out := tools.ExecuteSearchCode("Hello", ".", "", 0)
	assert.Contains(t, out, "Hello")
	assert.Contains(t, out, "foo.go")
}

func TestSearchCode_NoMatches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode("NoSuchPattern12345", ".", "", 0)
	assert.Equal(t, "(no matches)", out)
}

func TestSearchCode_FileGlobFilter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("needle here\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bar.txt"), []byte("needle here\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode("needle", ".", "*.go", 0)
	assert.Contains(t, out, "foo.go")
	assert.NotContains(t, out, "bar.txt")
}

func TestSearchCode_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), []byte("findme\n"), 0o644))
	t.Chdir(dir)

	// empty path defaults to "."
	out := tools.ExecuteSearchCode("findme", "", "", 0)
	assert.Contains(t, out, "findme")
}

func TestSearchCode_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteSearchCode("root", "../../etc", "", 0)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "outside the working directory")
}

func TestSearchCode_WithContextLines(t *testing.T) {
	dir := t.TempDir()
	content := "before\ntarget\nafter\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ctx.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode("target", ".", "", 1)
	assert.Contains(t, out, "target")
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "after")
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

	out := tools.ExecuteSearchCode("find me", ".", "", 0)
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
	out := tools.ExecuteBash(context.Background(),"echo hello")
	assert.Equal(t, "hello", out)
}

func TestExecuteBash_StderrCombined(t *testing.T) {
	// stderr should appear in the output
	out := tools.ExecuteBash(context.Background(),"echo out && echo err >&2")
	assert.Contains(t, out, "out")
	assert.Contains(t, out, "err")
}

func TestExecuteBash_NonZeroExitIncludesOutput(t *testing.T) {
	// A failing command should return its output AND an exit notice.
	out := tools.ExecuteBash(context.Background(),"echo before && exit 1")
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "[exit:")
}

func TestExecuteBash_NonZeroExitNoOutput(t *testing.T) {
	// When there's no output, only the exit notice is returned.
	out := tools.ExecuteBash(context.Background(),"exit 2")
	assert.Contains(t, out, "[exit:")
	assert.NotContains(t, out, "[error:")
}

func TestExecuteBash_NoOutput(t *testing.T) {
	out := tools.ExecuteBash(context.Background(),"true")
	assert.Equal(t, "(no output)", out)
}

func TestExecuteBash_MultilineOutput(t *testing.T) {
	out := tools.ExecuteBash(context.Background(),"printf 'a\nb\nc\n'")
	assert.Contains(t, out, "a")
	assert.Contains(t, out, "b")
	assert.Contains(t, out, "c")
}

func TestExecuteBash_PipeSupport(t *testing.T) {
	out := tools.ExecuteBash(context.Background(),"echo hello world | tr ' ' '_'")
	assert.Equal(t, "hello_world", out)
}

func TestExecuteBash_WorkingDirIsInherited(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteBash(context.Background(),"pwd")
	// The resolved pwd may differ from dir due to symlinks; just check no error.
	assert.NotContains(t, out, "[error:")
	assert.NotContains(t, out, "[exit:")
}

func TestExecuteBash_OutputCapped(t *testing.T) {
	// Generate output larger than bashOutputLimit (10 000 bytes).
	// Each "A" repeated 200 times + newline = 201 bytes; 60 lines = 12060 bytes.
	out := tools.ExecuteBash(context.Background(),"python3 -c \"print('A'*200+'\\n'*1, end='')\" | for i in $(seq 60); do cat; done 2>/dev/null || printf '%0.s' {1..12000} | head -c 12000 | tr '\\0' 'A'")
	// Fallback: just generate a long string directly.
	if strings.Contains(out, "[exit:") || strings.Contains(out, "(no output)") {
		out = tools.ExecuteBash(context.Background(),"dd if=/dev/zero bs=1 count=12000 2>/dev/null | tr '\\0' 'A'")
	}
	if len(out) > 10_000 {
		assert.Contains(t, out, "[truncated:")
	}
	// Output must be ≤ bashOutputLimit + len of truncation notice
	assert.LessOrEqual(t, len(out), 10_100)
}

func TestExecuteBash_Timeout(t *testing.T) {
	tools.SetBashTimeout(2 * time.Second)
	defer tools.SetBashTimeout(0)
	out := tools.ExecuteBash(context.Background(), "sleep 60")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "timed out")
}

// ─── ExecuteEditFile ──────────────────────────────────────────────────────────

func TestExecuteEditFile_Success(t *testing.T) {
	dir := t.TempDir()
	original := "package main\n\nfunc hello() {\n\treturn \"old\"\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(original), 0o644))
	t.Chdir(dir)

	newContent, errMsg := tools.ExecuteEditFile("main.go", `return "old"`, `return "new"`)
	require.Empty(t, errMsg)
	assert.Contains(t, newContent, `return "new"`)
	assert.NotContains(t, newContent, `return "old"`)
	// Surrounding content must be preserved
	assert.Contains(t, newContent, "func hello()")
}

func TestExecuteEditFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte("package main\n"), 0o644))
	t.Chdir(dir)

	_, errMsg := tools.ExecuteEditFile("f.go", "nonexistent string xyz", "replacement")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "not found")
}

func TestExecuteEditFile_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	content := "duplicate\nduplicate\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte(content), 0o644))
	t.Chdir(dir)

	_, errMsg := tools.ExecuteEditFile("f.go", "duplicate", "replacement")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "ambiguous")
	assert.Contains(t, errMsg, "2 matches")
}

func TestExecuteEditFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	_, errMsg := tools.ExecuteEditFile("nosuchfile.go", "old", "new")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "not found")
}

func TestExecuteEditFile_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	_, errMsg := tools.ExecuteEditFile("../../etc/passwd", "root", "toor")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "outside the working directory")
}

// ─── list_directory file size hints ──────────────────────────────────────────

func TestListDirectory_ShowsFileSizes(t *testing.T) {
	dir := t.TempDir()
	// Write a file with known content so its size is predictable.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "small.txt"), []byte("hi"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(".", 1)
	assert.Contains(t, out, "small.txt")
	// Size hint should be present in some form (KB bracket).
	assert.Contains(t, out, "(")
	assert.Contains(t, out, "KB")
}

func TestListDirectory_DirectoriesHaveNoSizeHint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(".", 1)
	// The directory line should be "subdir/" with no "(N KB)" attached.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "subdir/") {
			assert.NotContains(t, line, "KB", "directory entry should not have a size hint")
		}
	}
}

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

// ─── ExecuteWebFetch ──────────────────────────────────────────────────────────

func TestExecuteWebFetch_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	out := tools.ExecuteWebFetch(srv.URL)
	assert.Equal(t, "hello world", out)
}

func TestExecuteWebFetch_HTMLStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>T</title></head><body><p>visible text</p><script>alert(1)</script></body></html>`))
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	out := tools.ExecuteWebFetch(srv.URL)
	assert.Contains(t, out, "visible text")
	assert.NotContains(t, out, "<p>")
	assert.NotContains(t, out, "alert(1)")
}

func TestExecuteWebFetch_HTTP4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	out := tools.ExecuteWebFetch(srv.URL)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "404")
}

func TestExecuteWebFetch_InvalidScheme(t *testing.T) {
	out := tools.ExecuteWebFetch("file:///etc/passwd")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "http")
}

func TestExecuteWebFetch_LocalhostBlocked(t *testing.T) {
	out := tools.ExecuteWebFetch("http://localhost:9999/test")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "localhost")
}

func TestExecuteWebFetch_InvalidURL(t *testing.T) {
	out := tools.ExecuteWebFetch("not a url at all ://")
	assert.Contains(t, out, "[error:")
}

func TestExecuteWebFetch_ConcurrentSameURLDeduplicates(t *testing.T) {
	// Verify that two concurrent requests for the same URL result in exactly
	// one HTTP request (singleflight deduplication).
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("poem content"))
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	results := make([]string, 2)
	done := make(chan struct{}, 2)
	for i := range results {
		go func() {
			results[i] = tools.ExecuteWebFetch(srv.URL)
			done <- struct{}{}
		}()
	}
	<-done
	<-done

	// Both must have gotten the same content.
	assert.Equal(t, "poem content", results[0])
	assert.Equal(t, results[0], results[1])
	// Only one HTTP request should have been made.
	assert.Equal(t, 1, requestCount, "singleflight should deduplicate concurrent requests")
}

// ─── ExecuteWebSearch ─────────────────────────────────────────────────────────

// ddgJSON builds a minimal DuckDuckGo instant-answers JSON payload for tests.
func ddgJSON(abstract, source, abstractURL, answer string, topics []map[string]string) string {
	topicsJSON := "[]"
	if len(topics) > 0 {
		var parts []string
		for _, t := range topics {
			parts = append(parts, `{"Text":"`+t["text"]+`","FirstURL":"`+t["url"]+`"}`)
		}
		topicsJSON = "[" + strings.Join(parts, ",") + "]"
	}
	return `{"AbstractText":"` + abstract + `","AbstractURL":"` + abstractURL +
		`","AbstractSource":"` + source + `","Answer":"` + answer +
		`","Definition":"","DefinitionURL":"","RelatedTopics":` + topicsJSON + `,"Results":[]}`
}

func TestExecuteWebSearch_AbstractResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("Go is a language.", "Wikipedia", "https://en.wikipedia.org/wiki/Go", "", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("golang")
	assert.Contains(t, out, "Go is a language.")
	assert.Contains(t, out, "Wikipedia")
}

func TestExecuteWebSearch_AnswerField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "42 is the answer.", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("answer to life")
	assert.Contains(t, out, "42 is the answer.")
}

func TestExecuteWebSearch_RelatedTopics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "", []map[string]string{
			{"text": "Topic A", "url": "https://example.com/a"},
			{"text": "Topic B", "url": "https://example.com/b"},
		})))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("something")
	assert.Contains(t, out, "Topic A")
	assert.Contains(t, out, "Topic B")
}

func TestExecuteWebSearch_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("xyzzy12345")
	assert.Contains(t, out, "no instant answer found")
	assert.Contains(t, out, "web_fetch")
}

func TestExecuteWebSearch_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("anything")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "500")
}

func TestExecuteWebSearch_EmptyQuery(t *testing.T) {
	out := tools.ExecuteWebSearch("")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "empty")
}

func TestExecuteWebSearch_QueryForwardedToServer(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	tools.ExecuteWebSearch("my test query")
	assert.Equal(t, "my test query", receivedQuery)
}

// ─── ToolsForRole ─────────────────────────────────────────────────────────────

func TestToolsForRole_Explorer(t *testing.T) {
	defs := tools.ToolsForRole(tools.RoleExplorer, tools.Definitions)
	names := toolNames(defs)
	assert.Contains(t, names, tools.ReadToolName)
	assert.Contains(t, names, tools.ListDirToolName)
	assert.Contains(t, names, tools.SearchFilesName)
	assert.Contains(t, names, tools.SearchCodeName)
	assert.Contains(t, names, tools.WebFetchToolName)
	assert.Contains(t, names, tools.WebSearchToolName)
	// Explorer must not include write or bash tools.
	assert.NotContains(t, names, tools.WriteToolName)
	assert.NotContains(t, names, tools.EditToolName)
	assert.NotContains(t, names, tools.BashToolName)
	assert.NotContains(t, names, tools.SpawnAgentToolName)
}

func TestToolsForRole_Planner(t *testing.T) {
	defs := tools.ToolsForRole(tools.RolePlanner, tools.Definitions)
	names := toolNames(defs)
	assert.Contains(t, names, tools.ReadToolName)
	assert.Contains(t, names, tools.BashToolName)
	// Planner must not include write tools.
	assert.NotContains(t, names, tools.WriteToolName)
	assert.NotContains(t, names, tools.EditToolName)
	assert.NotContains(t, names, tools.SpawnAgentToolName)
}

func TestToolsForRole_Coder(t *testing.T) {
	// Coder returns parentDefs unchanged.
	parent := tools.Definitions
	defs := tools.ToolsForRole(tools.RoleCoder, parent)
	assert.Equal(t, parent, defs)
}

func TestToolsForRole_Full_AliasForCoder(t *testing.T) {
	parent := tools.Definitions
	coder := tools.ToolsForRole(tools.RoleCoder, parent)
	full := tools.ToolsForRole(tools.RoleFull, parent)
	assert.Equal(t, coder, full)
}

func TestToolsForRole_UnknownRole_DefaultsToCoder(t *testing.T) {
	parent := tools.Definitions
	defs := tools.ToolsForRole("mystery-role", parent)
	assert.Equal(t, parent, defs)
}

// ─── Sub-agent context helpers ────────────────────────────────────────────────

func TestSubagentDepth_RoundTrip(t *testing.T) {
	ctx := tools.WithSubagentDepth(context.Background(), 3)
	assert.Equal(t, 3, tools.SubagentDepthFromContext(ctx))
}

func TestSubagentDepth_DefaultZero(t *testing.T) {
	assert.Equal(t, 0, tools.SubagentDepthFromContext(context.Background()))
}

func TestSubagentDispatcher_RoundTrip(t *testing.T) {
	called := false
	var d tools.SubagentDispatcher = func(_ context.Context, _ map[string]string) (string, []tools.FileWrite, string) {
		called = true
		return "ok", nil, ""
	}
	ctx := tools.WithSubagentDispatcher(context.Background(), d)
	got := tools.SubagentDispatcherFromContext(ctx)
	require.NotNil(t, got)
	text, _, _ := got(context.Background(), nil)
	assert.True(t, called)
	assert.Equal(t, "ok", text)
}

func TestSubagentDispatcher_NilWhenAbsent(t *testing.T) {
	got := tools.SubagentDispatcherFromContext(context.Background())
	assert.Nil(t, got)
}

// toolNames extracts tool name strings from a ToolDef slice.
func toolNames(defs []tools.ToolDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

// ─── Context function round-trips ───────────────────────────────────────────

func TestWithBashPrefixes_RoundTrip(t *testing.T) {
	prefixes := []string{"go *", "npm *"}
	ctx := tools.WithBashPrefixes(context.Background(), prefixes)
	got := tools.BashPrefixesFromContext(ctx)
	assert.Equal(t, prefixes, got)
}

func TestBashPrefixesFromContext_NilWhenAbsent(t *testing.T) {
	got := tools.BashPrefixesFromContext(context.Background())
	assert.Nil(t, got)
}

func TestWithMCPDispatchers_RoundTrip(t *testing.T) {
	d := map[string]tools.MCPDispatcher{
		"search": func(args map[string]string) string { return "found" },
	}
	ctx := tools.WithMCPDispatchers(context.Background(), d)
	got := tools.MCPDispatchersFromContext(ctx)
	assert.NotNil(t, got)
	assert.Equal(t, "found", got["search"](nil))
}

func TestMCPDispatchersFromContext_NilWhenAbsent(t *testing.T) {
	got := tools.MCPDispatchersFromContext(context.Background())
	assert.Nil(t, got)
}

func TestWithSeed_RoundTrip(t *testing.T) {
	ctx := tools.WithSeed(context.Background(), 12345)
	seed, ok := tools.SeedFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, int64(12345), seed)
}

func TestSeedFromContext_FalseWhenAbsent(t *testing.T) {
	_, ok := tools.SeedFromContext(context.Background())
	assert.False(t, ok)
}

func TestWithSeed_ZeroValue(t *testing.T) {
	ctx := tools.WithSeed(context.Background(), 0)
	seed, ok := tools.SeedFromContext(ctx)
	assert.True(t, ok, "zero seed should still be present")
	assert.Equal(t, int64(0), seed)
}

// ─── DefinitionsAllowed ─────────────────────────────────────────────────────

func TestDefinitionsAllowed_AllowlistOnly(t *testing.T) {
	allowlist := []string{tools.ReadToolName, tools.BashToolName}
	got := tools.DefinitionsAllowed(allowlist, nil)
	names := toolNames(got)
	assert.Len(t, got, 2)
	assert.Contains(t, names, tools.ReadToolName)
	assert.Contains(t, names, tools.BashToolName)
}

func TestDefinitionsAllowed_AllowlistPlusDisabled(t *testing.T) {
	allowlist := []string{tools.ReadToolName, tools.BashToolName}
	disabled := map[string]bool{tools.BashToolName: true}
	got := tools.DefinitionsAllowed(allowlist, disabled)
	assert.Len(t, got, 1)
	assert.Equal(t, tools.ReadToolName, got[0].Name)
}

func TestDefinitionsAllowed_NilAllowlist_UsesAll(t *testing.T) {
	disabled := map[string]bool{tools.BashToolName: true}
	got := tools.DefinitionsAllowed(nil, disabled)
	names := toolNames(got)
	assert.NotContains(t, names, tools.BashToolName)
	assert.NotEmpty(t, got)
}

func TestDefinitionsAllowed_InvalidNames(t *testing.T) {
	got := tools.DefinitionsAllowed([]string{"nonexistent_tool"}, nil)
	assert.Empty(t, got)
}

// ─── FilterDefs ─────────────────────────────────────────────────────────────

func TestFilterDefs_PreservesOrder(t *testing.T) {
	defs := []tools.ToolDef{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	disabled := map[string]bool{"b": true}
	got := tools.FilterDefs(defs, disabled)
	assert.Equal(t, []string{"a", "c"}, toolNames(got))
}

func TestFilterDefs_NilDisabled(t *testing.T) {
	defs := []tools.ToolDef{{Name: "a"}, {Name: "b"}}
	got := tools.FilterDefs(defs, nil)
	assert.Len(t, got, 2)
}

// ─── SystemPrompt ───────────────────────────────────────────────────────────

func TestSetSystemPromptExtra_AffectsSuffix(t *testing.T) {
	original := tools.SystemPromptSuffix()
	tools.SetSystemPromptExtra("TEST_EXTRA_CONTENT")
	modified := tools.SystemPromptSuffix()
	assert.Contains(t, modified, "TEST_EXTRA_CONTENT")
	assert.NotEqual(t, original, modified)
	// Cleanup
	tools.SetSystemPromptExtra("")
}

func TestSystemPromptGuidance_IsSubsetOfSuffix(t *testing.T) {
	guidance := tools.SystemPromptGuidance()
	suffix := tools.SystemPromptSuffix()
	assert.True(t, strings.HasPrefix(suffix, guidance),
		"SystemPromptSuffix should start with SystemPromptGuidance")
}

// ─── Bash prefix restriction ────────────────────────────────────────────────

func TestExecuteBash_WithPrefixRestriction_Allowed(t *testing.T) {
	ctx := tools.WithBashPrefixes(context.Background(), []string{"echo *"})
	out := tools.ExecuteBash(ctx, "echo hello")
	assert.Contains(t, out, "hello")
	assert.NotContains(t, out, "not allowed")
}

func TestExecuteBash_WithPrefixRestriction_Denied(t *testing.T) {
	ctx := tools.WithBashPrefixes(context.Background(), []string{"echo *"})
	out := tools.ExecuteBash(ctx, "ls /tmp")
	assert.Contains(t, out, "not allowed")
}

func TestExecuteBash_NoPrefixes_AllAllowed(t *testing.T) {
	out := tools.ExecuteBash(context.Background(), "echo unrestricted")
	assert.Contains(t, out, "unrestricted")
}

// ─── SetToolGuidance / DefaultToolGuidance ─────────────────────────────────

func TestDefaultToolGuidance_ContainsKeyTools(t *testing.T) {
	g := tools.DefaultToolGuidance()
	assert.Contains(t, g, "list_directory")
	assert.Contains(t, g, "write_file")
	assert.Contains(t, g, "search_code")
}

func TestSetToolGuidance_OverridesEffectiveGuidance(t *testing.T) {
	original := tools.SystemPromptSuffix()
	tools.SetToolGuidance("Custom guidance: use tools wisely.")
	defer tools.SetToolGuidance("")

	modified := tools.SystemPromptSuffix()
	assert.Contains(t, modified, "Custom guidance: use tools wisely.")
	assert.NotContains(t, modified, "list_directory")
	assert.NotEqual(t, original, modified)
}

func TestSetToolGuidance_ClearRestoresDefault(t *testing.T) {
	original := tools.SystemPromptSuffix()
	tools.SetToolGuidance("temporary override")
	tools.SetToolGuidance("")

	restored := tools.SystemPromptSuffix()
	assert.Equal(t, original, restored)
}

func TestSetToolGuidance_WithSystemPromptExtra(t *testing.T) {
	tools.SetToolGuidance("Custom guidance.")
	tools.SetSystemPromptExtra("Extra context.")
	defer func() {
		tools.SetToolGuidance("")
		tools.SetSystemPromptExtra("")
	}()

	s := tools.SystemPromptSuffix()
	assert.Contains(t, s, "Custom guidance.")
	assert.Contains(t, s, "Extra context.")
	assert.NotContains(t, s, "list_directory")
}

func TestSystemPromptGuidance_ReflectsOverride(t *testing.T) {
	tools.SetToolGuidance("Overridden guidance.")
	defer tools.SetToolGuidance("")

	g := tools.SystemPromptGuidance()
	assert.Contains(t, g, "Overridden guidance.")
	assert.NotContains(t, g, "list_directory")
}
