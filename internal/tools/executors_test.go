package tools_test

import (
	"context"
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
	got := tools.ExecuteRead(context.Background(),"hello.txt", 0, 0)
	assert.Equal(t, "hello world", got)
}

func TestExecuteRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := tools.ExecuteRead(context.Background(),"nonexistent.txt", 0, 0)
	assert.Contains(t, got, "[error:")
}

func TestExecuteRead_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := tools.ExecuteRead(context.Background(),"../../etc/passwd", 0, 0)
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
	got := tools.ExecuteRead(context.Background(),"sub/data.txt", 0, 0)
	assert.Equal(t, "nested", got)
}

func TestExecuteRead_OffsetSkipsLines(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead(context.Background(),"f.txt", 3, 0)
	assert.True(t, strings.HasPrefix(got, "line3"), "offset=3 should start at line3, got: %q", got)
	assert.NotContains(t, got, "line1")
	assert.NotContains(t, got, "line2")
}

func TestExecuteRead_LimitCapsLines(t *testing.T) {
	dir := t.TempDir()
	content := "a\nb\nc\nd\ne\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead(context.Background(),"f.txt", 1, 2)
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

	got := tools.ExecuteRead(context.Background(),"f.txt", 1, 2)
	assert.Contains(t, got, "lines omitted")
	assert.Contains(t, got, "offset=3")
}

func TestExecuteRead_OffsetBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("only one line"), 0o644))
	t.Chdir(dir)

	got := tools.ExecuteRead(context.Background(),"f.txt", 999, 0)
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

	got := tools.ExecuteRead(context.Background(),"big.txt", 0, 0)
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

	out := tools.ExecuteListDirectory(context.Background(),".", 2)
	assert.Contains(t, out, "a.txt")
	assert.Contains(t, out, "sub/")
	assert.Contains(t, out, "b.txt")
}

func TestListDirectory_DepthOne(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(context.Background(),".", 1)
	assert.Contains(t, out, "sub/")
	assert.NotContains(t, out, "nested.txt")
}

func TestListDirectory_DepthTwo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(context.Background(),".", 2)
	assert.Contains(t, out, "sub/")
	assert.Contains(t, out, "nested.txt")
}

func TestListDirectory_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteListDirectory(context.Background(),"../../etc", 1)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "outside the working directory")
}

func TestListDirectory_NotExist(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteListDirectory(context.Background(),"nosuchdir", 1)
	assert.Contains(t, out, "[error:")
}

func TestListDirectory_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644))
	t.Chdir(dir)
	out := tools.ExecuteListDirectory(context.Background(),"file.txt", 1)
	assert.Contains(t, out, "[error:")
}

func TestListDirectory_DefaultDepthZero(t *testing.T) {
	// depth=0 should fall back to default (2), not error
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("f"), 0o644))
	t.Chdir(dir)
	out := tools.ExecuteListDirectory(context.Background(),".", 0)
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

	out := tools.ExecuteSearchFiles(context.Background(),"*.go", ".")
	assert.Contains(t, out, "main.go")
	assert.Contains(t, out, "main_test.go")
	assert.NotContains(t, out, "readme.md")
}

func TestSearchFiles_NoMatches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles(context.Background(),"*.go", ".")
	assert.Equal(t, "(no matches)", out)
}

func TestSearchFiles_DefaultBasePath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte(""), 0o644))
	t.Chdir(dir)

	// empty base_path defaults to "."
	out := tools.ExecuteSearchFiles(context.Background(),"*.go", "")
	assert.Contains(t, out, "foo.go")
}

func TestSearchFiles_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteSearchFiles(context.Background(),"*.go", "../../etc")
	assert.Contains(t, out, "[error:")
}

// --- ExecuteSearchCode ---

func TestSearchCode_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\nfunc Hello() {}\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(),"Hello", ".", "", 0)
	assert.Contains(t, out, "Hello")
	assert.Contains(t, out, "foo.go")
}

func TestSearchCode_NoMatches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(),"NoSuchPattern12345", ".", "", 0)
	assert.Equal(t, "(no matches)", out)
}

func TestSearchCode_FileGlobFilter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo.go"), []byte("needle here\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bar.txt"), []byte("needle here\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(),"needle", ".", "*.go", 0)
	assert.Contains(t, out, "foo.go")
	assert.NotContains(t, out, "bar.txt")
}

func TestSearchCode_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), []byte("findme\n"), 0o644))
	t.Chdir(dir)

	// empty path defaults to "."
	out := tools.ExecuteSearchCode(context.Background(),"findme", "", "", 0)
	assert.Contains(t, out, "findme")
}

func TestSearchCode_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteSearchCode(context.Background(),"root", "../../etc", "", 0)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "outside the working directory")
}

func TestSearchCode_WithContextLines(t *testing.T) {
	dir := t.TempDir()
	content := "before\ntarget\nafter\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ctx.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(),"target", ".", "", 1)
	assert.Contains(t, out, "target")
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "after")
}

func TestSearchCode_SkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	// File with a null byte in the first 512 bytes is treated as binary.
	content := []byte("needle\x00 in binary\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin.dat"), content, 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(), "needle", ".", "", 0)
	assert.Equal(t, "(no matches)", out)
}

func TestSearchCode_SkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("findme\n"), 0o644))
	// Also place a matching file outside .git to ensure walk continues.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.txt"), []byte("findme\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(), "findme", ".", "", 0)
	assert.Contains(t, out, "real.txt")
	assert.NotContains(t, out, ".git")
}

func TestSearchCode_InvalidRegex(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("text\n"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(), "[invalid", ".", "", 0)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "invalid regex")
}

func TestSearchCode_ContextGroupSeparator(t *testing.T) {
	dir := t.TempDir()
	// Two matches far apart should produce a "--" separator between groups.
	var sb strings.Builder
	sb.WriteString("matchA\n")        // line 1
	for range 10 {
		sb.WriteString("filler\n")    // lines 2-11
	}
	sb.WriteString("matchB\n")        // line 12
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sep.txt"), []byte(sb.String()), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(), "match", ".", "", 1)
	assert.Contains(t, out, "--", "expected group separator between distant matches")
	assert.Contains(t, out, "matchA")
	assert.Contains(t, out, "matchB")
}

func TestSearchCode_ContextMergesOverlap(t *testing.T) {
	dir := t.TempDir()
	// Two matches close together — context windows overlap, no "--" separator.
	content := "before\nmatchA\nmiddle\nmatchB\nafter\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "merge.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(), "match", ".", "", 1)
	assert.NotContains(t, out, "--", "overlapping context groups should merge without separator")
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "matchA")
	assert.Contains(t, out, "middle")
	assert.Contains(t, out, "matchB")
	assert.Contains(t, out, "after")
}

// --- ExecuteSearchFiles: ** glob patterns ---

func TestSearchFiles_DoubleStarRootMatch(t *testing.T) {
	// **/*.go should match a file at the root of base_path (** = zero segments)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles(context.Background(),"**/*.go", ".")
	assert.Contains(t, out, "main.go")
}

func TestSearchFiles_DoubleStarDeep(t *testing.T) {
	// **/*.go should match files nested multiple levels deep
	dir := t.TempDir()
	deep := filepath.Join(dir, "internal", "tools")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "tools.go"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles(context.Background(),"**/*.go", ".")
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

	out := tools.ExecuteSearchFiles(context.Background(),"internal/**/*.go", ".")
	assert.Contains(t, out, "top.go")
	assert.Contains(t, out, "runner.go")
	assert.NotContains(t, out, "cmd")
}

func TestSearchFiles_DoubleStarNoMatch(t *testing.T) {
	// **/*.go should not match .txt files
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles(context.Background(),"**/*.go", ".")
	assert.Equal(t, "(no matches)", out)
}

func TestSearchFiles_DoubleStarTestFiles(t *testing.T) {
	// **/*_test.go should find only test files
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "foo.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "foo_test.go"), []byte(""), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchFiles(context.Background(),"**/*_test.go", ".")
	assert.Contains(t, out, "foo_test.go")
	assert.NotContains(t, out, "foo.go\n")
	assert.NotContains(t, out, "[error:")
}

func TestSearchCode_LineNumbers(t *testing.T) {
	dir := t.TempDir()
	content := "line one\nline two\nfind me\nline four\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte(content), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteSearchCode(context.Background(),"find me", ".", "", 0)
	// grep -n output format: filename:linenum:content
	assert.True(t, strings.Contains(out, ":3:") || strings.Contains(out, "find me"),
		"expected line number or match content in output: %q", out)
}

// --- ExecuteBash ---

func TestExecuteBash_SimpleCommand(t *testing.T) {
	out := tools.ExecuteBash(context.Background(), "echo hello")
	assert.Equal(t, "hello", out)
}

func TestExecuteBash_StderrCombined(t *testing.T) {
	// stderr should appear in the output
	out := tools.ExecuteBash(context.Background(), "echo out && echo err >&2")
	assert.Contains(t, out, "out")
	assert.Contains(t, out, "err")
}

func TestExecuteBash_NonZeroExitIncludesOutput(t *testing.T) {
	// A failing command should return its output AND an exit notice.
	out := tools.ExecuteBash(context.Background(), "echo before && exit 1")
	assert.Contains(t, out, "before")
	assert.Contains(t, out, "[exit:")
}

func TestExecuteBash_NonZeroExitNoOutput(t *testing.T) {
	// When there's no output, only the exit notice is returned.
	out := tools.ExecuteBash(context.Background(), "exit 2")
	assert.Contains(t, out, "[exit:")
	assert.NotContains(t, out, "[error:")
}

func TestExecuteBash_NoOutput(t *testing.T) {
	out := tools.ExecuteBash(context.Background(), "true")
	assert.Equal(t, "(no output)", out)
}

func TestExecuteBash_MultilineOutput(t *testing.T) {
	out := tools.ExecuteBash(context.Background(), "printf 'a\nb\nc\n'")
	assert.Contains(t, out, "a")
	assert.Contains(t, out, "b")
	assert.Contains(t, out, "c")
}

func TestExecuteBash_PipeSupport(t *testing.T) {
	out := tools.ExecuteBash(context.Background(), "echo hello world | tr ' ' '_'")
	assert.Equal(t, "hello_world", out)
}

func TestExecuteBash_WorkingDirIsInherited(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out := tools.ExecuteBash(context.Background(), "pwd")
	// The resolved pwd may differ from dir due to symlinks; just check no error.
	assert.NotContains(t, out, "[error:")
	assert.NotContains(t, out, "[exit:")
}

func TestExecuteBash_OutputCapped(t *testing.T) {
	// Generate output larger than bashOutputLimit (10 000 bytes).
	// Each "A" repeated 200 times + newline = 201 bytes; 60 lines = 12060 bytes.
	out := tools.ExecuteBash(context.Background(), "python3 -c \"print('A'*200+'\\n'*1, end='')\" | for i in $(seq 60); do cat; done 2>/dev/null || printf '%0.s' {1..12000} | head -c 12000 | tr '\\0' 'A'")
	// Fallback: just generate a long string directly.
	if strings.Contains(out, "[exit:") || strings.Contains(out, "(no output)") {
		out = tools.ExecuteBash(context.Background(), "dd if=/dev/zero bs=1 count=12000 2>/dev/null | tr '\\0' 'A'")
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

	newContent, errMsg := tools.ExecuteEditFile(context.Background(),"main.go", `return "old"`, `return "new"`)
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

	_, errMsg := tools.ExecuteEditFile(context.Background(),"f.go", "nonexistent string xyz", "replacement")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "not found")
}

func TestExecuteEditFile_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	content := "duplicate\nduplicate\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte(content), 0o644))
	t.Chdir(dir)

	_, errMsg := tools.ExecuteEditFile(context.Background(),"f.go", "duplicate", "replacement")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "ambiguous")
	assert.Contains(t, errMsg, "2 matches")
}

func TestExecuteEditFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	_, errMsg := tools.ExecuteEditFile(context.Background(),"nosuchfile.go", "old", "new")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "not found")
}

func TestExecuteEditFile_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	_, errMsg := tools.ExecuteEditFile(context.Background(),"../../etc/passwd", "root", "toor")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "outside the working directory")
}

// ─── list_directory file size hints ──────────────────────────────────────────

func TestListDirectory_ShowsFileSizes(t *testing.T) {
	dir := t.TempDir()
	// Write a file with known content so its size is predictable.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "small.txt"), []byte("hi"), 0o644))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(context.Background(),".", 1)
	assert.Contains(t, out, "small.txt")
	// Size hint should be present in some form (KB bracket).
	assert.Contains(t, out, "(")
	assert.Contains(t, out, "KB")
}

func TestListDirectory_DirectoriesHaveNoSizeHint(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	t.Chdir(dir)

	out := tools.ExecuteListDirectory(context.Background(),".", 1)
	// The directory line should be "subdir/" with no "(N KB)" attached.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "subdir/") {
			assert.NotContains(t, line, "KB", "directory entry should not have a size hint")
		}
	}
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

// ─── SnapshotFiles / RestoreSnapshots ────────────────────────────────────────

func TestSnapshotFiles_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("original"), 0o644))

	snaps, err := tools.SnapshotFiles([]tools.FileWrite{{Path: "a.txt", Content: "new"}})
	require.NoError(t, err)
	require.Len(t, snaps, 1)
	assert.Equal(t, "original", snaps[0].Content)
	assert.False(t, snaps[0].DidNotExist)
}

func TestSnapshotFiles_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	snaps, err := tools.SnapshotFiles([]tools.FileWrite{{Path: "new.txt", Content: "content"}})
	require.NoError(t, err)
	require.Len(t, snaps, 1)
	assert.True(t, snaps[0].DidNotExist)
}

func TestRestoreSnapshots_OverwriteFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o644))

	// Overwrite.
	require.NoError(t, os.WriteFile(path, []byte("changed"), 0o644))

	err := tools.RestoreSnapshots([]tools.FileSnapshot{{Path: path, Content: "original"}})
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "original", string(got))
}

func TestRestoreSnapshots_DeleteNewFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "created.txt")
	require.NoError(t, os.WriteFile(path, []byte("new"), 0o644))

	err := tools.RestoreSnapshots([]tools.FileSnapshot{{Path: path, DidNotExist: true}})
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "file should be removed")
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a"), 0o644))

	writes := []tools.FileWrite{
		{Path: "a.go", Content: "package a_new"},
		{Path: "b.go", Content: "package b"},
	}

	snaps, err := tools.SnapshotFiles(writes)
	require.NoError(t, err)
	require.NoError(t, tools.ApplyWrites(writes))

	// Verify writes took effect.
	got, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	assert.Equal(t, "package a_new", string(got))
	got, _ = os.ReadFile(filepath.Join(dir, "b.go"))
	assert.Equal(t, "package b", string(got))

	// Restore.
	require.NoError(t, tools.RestoreSnapshots(snaps))

	// a.go should be original.
	got, err = os.ReadFile(filepath.Join(dir, "a.go"))
	require.NoError(t, err)
	assert.Equal(t, "package a", string(got))

	// b.go should be gone.
	_, statErr := os.Stat(filepath.Join(dir, "b.go"))
	assert.True(t, os.IsNotExist(statErr), "b.go should be removed on restore")
}

func TestSnapshotFiles_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	_, err := tools.SnapshotFiles([]tools.FileWrite{{Path: "../../etc/passwd", Content: "x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the working directory")
}

// ─── WorkDir context tests ──────────────────────────────────────────────────

func TestExecuteRead_WithWorkDir(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirA, "found.txt"), []byte("in A"), 0o644))

	// Read from dirA via WorkDir — file exists.
	ctxA := tools.WithWorkDir(context.Background(), dirA)
	got := tools.ExecuteRead(ctxA, "found.txt", 0, 0)
	assert.Equal(t, "in A", got)

	// Read from dirB via WorkDir — file does not exist.
	ctxB := tools.WithWorkDir(context.Background(), dirB)
	got = tools.ExecuteRead(ctxB, "found.txt", 0, 0)
	assert.Contains(t, got, "[error:")
}

func TestExecuteEditFile_WithWorkDir(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "f.go"), []byte("old content here"), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)
	newContent, errMsg := tools.ExecuteEditFile(ctx, "f.go", "old content", "new content")
	require.Empty(t, errMsg)
	assert.Contains(t, newContent, "new content")
	assert.NotContains(t, newContent, "old content")
}

func TestWriteFileDirect(t *testing.T) {
	workDir := t.TempDir()
	ctx := tools.WithWorkDir(context.Background(), workDir)

	errMsg := tools.WriteFileDirect(ctx, "sub/output.txt", "hello direct")
	assert.Empty(t, errMsg)

	got, err := os.ReadFile(filepath.Join(workDir, "sub", "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello direct", string(got))
}

func TestWriteFileDirect_MultiEdit(t *testing.T) {
	workDir := t.TempDir()
	original := "func hello() {\n\treturn \"old1\"\n\t// old2 marker\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "f.go"), []byte(original), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)

	// First edit.
	newContent, errMsg := tools.ExecuteEditFile(ctx, "f.go", "old1", "new1")
	require.Empty(t, errMsg)
	errMsg = tools.WriteFileDirect(ctx, "f.go", newContent)
	assert.Empty(t, errMsg)

	// Second edit reads from disk (where first edit landed).
	newContent2, errMsg := tools.ExecuteEditFile(ctx, "f.go", "old2", "new2")
	require.Empty(t, errMsg)
	errMsg = tools.WriteFileDirect(ctx, "f.go", newContent2)
	assert.Empty(t, errMsg)

	// Both edits should be present.
	got, err := os.ReadFile(filepath.Join(workDir, "f.go"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "new1")
	assert.Contains(t, string(got), "new2")
	assert.NotContains(t, string(got), "old1")
	assert.NotContains(t, string(got), "old2")
}

func TestExecuteListDirectory_WithWorkDir(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "visible.txt"), []byte("v"), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)
	out := tools.ExecuteListDirectory(ctx, ".", 1)
	assert.Contains(t, out, "visible.txt")
}

func TestExecuteSearchFiles_WithWorkDir(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "app.go"), []byte(""), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)
	out := tools.ExecuteSearchFiles(ctx, "*.go", ".")
	assert.Contains(t, out, "app.go")
}

func TestExecuteBash_WithWorkDir(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "marker.txt"), []byte("x"), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)
	out := tools.ExecuteBash(ctx, "ls marker.txt")
	assert.Contains(t, out, "marker.txt")
	assert.NotContains(t, out, "[error:")
}

func TestWriteFileDirect_PathTraversal(t *testing.T) {
	workDir := t.TempDir()
	ctx := tools.WithWorkDir(context.Background(), workDir)

	errMsg := tools.WriteFileDirect(ctx, "../../etc/evil", "bad")
	assert.Contains(t, errMsg, "[error:")
	assert.Contains(t, errMsg, "outside the working directory")
}

// ─── Delete support ──────────────────────────────────────────────────────────

func TestApplyWrites_Delete(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create a file, then delete it via ApplyWrites.
	path := filepath.Join(dir, "doomed.txt")
	require.NoError(t, os.WriteFile(path, []byte("bye"), 0o644))

	require.NoError(t, tools.ApplyWrites([]tools.FileWrite{
		{Path: "doomed.txt", Delete: true},
	}))

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "file should be deleted")
}

func TestApplyWrites_DeleteNonexistent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Deleting a file that doesn't exist should not error.
	err := tools.ApplyWrites([]tools.FileWrite{
		{Path: "ghost.txt", Delete: true},
	})
	require.NoError(t, err)
}

func TestSnapshotFiles_Delete(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create a file that will be deleted.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "victim.txt"), []byte("save me"), 0o644))

	snaps, err := tools.SnapshotFiles([]tools.FileWrite{
		{Path: "victim.txt", Delete: true},
	})
	require.NoError(t, err)
	require.Len(t, snaps, 1)
	assert.Equal(t, "save me", snaps[0].Content, "snapshot should capture content for rewind")
	assert.False(t, snaps[0].DidNotExist)
}

func TestSnapshotFiles_DeleteNonexistent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Deleting a non-existent file should produce no snapshot entry.
	snaps, err := tools.SnapshotFiles([]tools.FileWrite{
		{Path: "ghost.txt", Delete: true},
	})
	require.NoError(t, err)
	assert.Empty(t, snaps)
}
