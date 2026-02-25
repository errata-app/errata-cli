package adapters

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ─── DispatchTool ─────────────────────────────────────────────────────────────

func TestDispatchTool_ReadEmitsEventAndReturnsContent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // make dir cwd so relative path passes ExecuteRead's boundary check
	const relPath = "hello.txt"
	require.NoError(t, os.WriteFile(relPath, []byte("hello"), 0o644))

	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.ReadToolName, map[string]string{"path": relPath},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	assert.Equal(t, "hello", result)
	require.Len(t, events, 1)
	assert.Equal(t, "reading", events[0].Type)
	assert.Equal(t, relPath, events[0].Data)
	assert.Empty(t, proposed)
}

func TestDispatchTool_WriteEmitsEventAndQueues(t *testing.T) {
	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.WriteToolName, map[string]string{"path": "out.txt", "content": "world"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	assert.Equal(t, writeAck, result)
	require.Len(t, events, 1)
	assert.Equal(t, "writing", events[0].Type)
	assert.Equal(t, "out.txt", events[0].Data)
	require.Len(t, proposed, 1)
	assert.Equal(t, "out.txt", proposed[0].Path)
	assert.Equal(t, "world", proposed[0].Content)
}

func TestDispatchTool_UnknownToolReturnsFalse(t *testing.T) {
	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), "unknown_tool", map[string]string{}, func(models.AgentEvent) {}, &proposed)
	assert.False(t, ok)
	assert.Equal(t, "", result)
	assert.Empty(t, proposed)
}

func TestDispatchTool_WriteDoesNotExecute(t *testing.T) {
	// write_file must never touch disk — it only queues.
	dir := t.TempDir()
	t.Chdir(dir)
	const relPath = "should-not-exist.txt"
	var proposed []tools.FileWrite

	_, ok := DispatchTool(context.Background(), tools.WriteToolName, map[string]string{"path": relPath, "content": "data"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok)
	_, err := os.Stat(relPath)
	assert.True(t, os.IsNotExist(err), "write_file must not write to disk")
}

func TestDispatchTool_ListDirectoryEmitsEventAndReturnsTree(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("file.txt", []byte(""), 0o644))

	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.ListDirToolName, map[string]string{"path": "."},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	assert.Contains(t, result, "file.txt")
	require.Len(t, events, 1)
	assert.Equal(t, "reading", events[0].Type)
	assert.Empty(t, proposed)
}

func TestDispatchTool_ListDirectoryDepthParam(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll("sub/nested", 0o755))
	require.NoError(t, os.WriteFile("sub/nested/deep.txt", []byte(""), 0o644))

	var proposed []tools.FileWrite

	// depth=1 should not recurse into nested/
	result1, _ := DispatchTool(context.Background(), tools.ListDirToolName, map[string]string{"path": ".", "depth": "1"},
		func(models.AgentEvent) {}, &proposed)
	assert.Contains(t, result1, "sub/")
	assert.NotContains(t, result1, "deep.txt")

	// depth=3 should reach deep.txt
	result3, _ := DispatchTool(context.Background(), tools.ListDirToolName, map[string]string{"path": ".", "depth": "3"},
		func(models.AgentEvent) {}, &proposed)
	assert.Contains(t, result3, "deep.txt")
}

func TestDispatchTool_SearchFilesEmitsEventAndReturnsMatches(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("main.go", []byte(""), 0o644))
	require.NoError(t, os.WriteFile("readme.md", []byte(""), 0o644))

	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.SearchFilesName, map[string]string{"pattern": "*.go"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	assert.Contains(t, result, "main.go")
	assert.NotContains(t, result, "readme.md")
	require.Len(t, events, 1)
	assert.Equal(t, "reading", events[0].Type)
	assert.Empty(t, proposed)
}

func TestDispatchTool_SearchFilesDoubleStarPattern(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll("internal/tools", 0o755))
	require.NoError(t, os.WriteFile("internal/tools/tools.go", []byte(""), 0o644))

	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), tools.SearchFilesName, map[string]string{"pattern": "**/*.go"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok)
	assert.Contains(t, result, "tools.go")
	assert.NotContains(t, result, "[error:")
}

func TestDispatchTool_SearchCodeEmitsEventAndReturnsMatches(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("foo.go", []byte("package main\nfunc Hello() {}\n"), 0o644))

	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.SearchCodeName, map[string]string{"pattern": "Hello"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	assert.Contains(t, result, "Hello")
	require.Len(t, events, 1)
	assert.Equal(t, "reading", events[0].Type)
	assert.Empty(t, proposed)
}

func TestDispatchTool_SearchCodeFileGlob(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("foo.go", []byte("needle\n"), 0o644))
	require.NoError(t, os.WriteFile("bar.txt", []byte("needle\n"), 0o644))

	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), tools.SearchCodeName, map[string]string{"pattern": "needle", "file_glob": "*.go"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok)
	assert.Contains(t, result, "foo.go")
	assert.NotContains(t, result, "bar.txt")
}

func TestDispatchTool_BashEmitsEventAndReturnsOutput(t *testing.T) {
	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.BashToolName,
		map[string]string{"command": "echo hi", "description": "say hi"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	assert.Equal(t, "hi", result)
	require.Len(t, events, 1)
	assert.Equal(t, "bash", events[0].Type)
	assert.Equal(t, "say hi", events[0].Data)
	assert.Empty(t, proposed)
}

func TestDispatchTool_BashFallsBackToCommandAsDesc(t *testing.T) {
	// When description is omitted, the command itself is used as the event Data.
	var events []models.AgentEvent
	var proposed []tools.FileWrite

	_, ok := DispatchTool(context.Background(), tools.BashToolName,
		map[string]string{"command": "echo x"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	require.Len(t, events, 1)
	assert.Equal(t, "echo x", events[0].Data)
}

func TestDispatchTool_MCPDispatcherTakesPriority(t *testing.T) {
	// An MCP dispatcher registered in context should be called instead of built-in tools.
	called := false
	dispatchers := map[string]tools.MCPDispatcher{
		"my_mcp_tool": func(args map[string]string) string {
			called = true
			return "mcp result: " + args["input"]
		},
	}
	ctx := tools.WithMCPDispatchers(context.Background(), dispatchers)

	var events []models.AgentEvent
	var proposed []tools.FileWrite
	result, ok := DispatchTool(ctx, "my_mcp_tool", map[string]string{"input": "hello"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed)

	assert.True(t, ok)
	assert.True(t, called)
	assert.Equal(t, "mcp result: hello", result)
	require.Len(t, events, 1)
	assert.Equal(t, "reading", events[0].Type)
	assert.Contains(t, events[0].Data, "my_mcp_tool")
}

// ─── extractStringMap ─────────────────────────────────────────────────────────

func TestExtractStringMap_StringValues(t *testing.T) {
	in := map[string]any{"path": "/tmp/foo", "content": "hello"}
	out := extractStringMap(in)
	assert.Equal(t, "/tmp/foo", out["path"])
	assert.Equal(t, "hello", out["content"])
}

func TestExtractStringMap_DropsNonStringValues(t *testing.T) {
	in := map[string]any{"name": "foo", "count": 42, "flag": true}
	out := extractStringMap(in)
	assert.Equal(t, map[string]string{"name": "foo"}, out)
}

func TestExtractStringMap_EmptyInput(t *testing.T) {
	out := extractStringMap(map[string]any{})
	assert.Empty(t, out)
}

// ─── BuildErrorResponse ───────────────────────────────────────────────────────

func TestBuildErrorResponse_Fields(t *testing.T) {
	start := time.Now().Add(-50 * time.Millisecond)
	resp := BuildErrorResponse("gpt-4o", "openai/gpt-4o", start, 100, 50, fmt.Errorf("api error"))

	assert.Equal(t, "gpt-4o", resp.ModelID)
	assert.GreaterOrEqual(t, resp.LatencyMS, int64(0))
	assert.Equal(t, int64(100), resp.InputTokens)
	assert.Equal(t, int64(50), resp.OutputTokens)
	assert.Equal(t, "api error", resp.Error)
	assert.False(t, resp.OK())
}

func TestBuildErrorResponse_ZeroTokensZeroCost(t *testing.T) {
	resp := BuildErrorResponse("m", "m", time.Now(), 0, 0, fmt.Errorf("e"))
	assert.Equal(t, 0.0, resp.CostUSD)
}

// ─── BuildSuccessResponse ─────────────────────────────────────────────────────

func TestBuildSuccessResponse_Fields(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	fw := []tools.FileWrite{{Path: "a.go", Content: "package a"}}

	resp := BuildSuccessResponse("claude-sonnet-4-6", "anthropic/claude-sonnet-4-6",
		[]string{"hello ", "world"}, start, 200, 80, fw)

	assert.Equal(t, "claude-sonnet-4-6", resp.ModelID)
	assert.Equal(t, "hello world", resp.Text)
	assert.GreaterOrEqual(t, resp.LatencyMS, int64(0))
	assert.Equal(t, int64(200), resp.InputTokens)
	assert.Equal(t, int64(80), resp.OutputTokens)
	assert.Len(t, resp.ProposedWrites, 1)
	assert.Equal(t, "a.go", resp.ProposedWrites[0].Path)
	assert.True(t, resp.OK())
}

func TestBuildSuccessResponse_EmptyParts(t *testing.T) {
	resp := BuildSuccessResponse("m", "m", nil, time.Now(), 0, 0, nil)
	assert.Equal(t, "", resp.Text)
	assert.Empty(t, resp.ProposedWrites)
}
