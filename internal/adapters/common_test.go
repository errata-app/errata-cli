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
		[]string{"hello ", "world"}, start, 200, 0, 0, 80, fw)

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
	resp := BuildSuccessResponse("m", "m", nil, time.Now(), 0, 0, 0, 0, nil)
	assert.Equal(t, "", resp.Text)
	assert.Empty(t, resp.ProposedWrites)
}

// ─── Cache token tests ────────────────────────────────────────────────────────

// TestBuildSuccessResponse_InputTokensIsTotal verifies InputTokens is the sum
// of all three input categories for display purposes.
func TestBuildSuccessResponse_InputTokensIsTotal(t *testing.T) {
	resp := BuildSuccessResponse(
		"claude-sonnet-4-6", "anthropic/claude-sonnet-4-6",
		nil, time.Now(),
		800_000, 150_000, 50_000, 100_000, nil,
	)
	assert.Equal(t, int64(1_000_000), resp.InputTokens,
		"InputTokens must equal regularInput + cacheRead + cacheCreation")
}

// TestBuildSuccessResponse_CacheFieldsStored verifies cache token fields are
// stored exactly as passed and not conflated with InputTokens.
func TestBuildSuccessResponse_CacheFieldsStored(t *testing.T) {
	resp := BuildSuccessResponse(
		"claude-sonnet-4-6", "anthropic/claude-sonnet-4-6",
		nil, time.Now(),
		500_000, 200_000, 300_000, 0, nil,
	)
	assert.Equal(t, int64(200_000), resp.CacheReadTokens)
	assert.Equal(t, int64(300_000), resp.CacheCreationTokens)
}

// TestBuildSuccessResponse_CacheReadCheaperThanRegular verifies that a run
// with cache reads costs less than the same token count at the regular rate.
// Uses claude-sonnet-4-6: CacheReadPMT=$0.30/M vs InputPMT=$3.00/M.
func TestBuildSuccessResponse_CacheReadCheaperThanRegular(t *testing.T) {
	respWithCache := BuildSuccessResponse(
		"claude-sonnet-4-6", "anthropic/claude-sonnet-4-6",
		nil, time.Now(), 0, 1_000_000, 0, 0, nil,
	)
	respAllRegular := BuildSuccessResponse(
		"claude-sonnet-4-6", "anthropic/claude-sonnet-4-6",
		nil, time.Now(), 1_000_000, 0, 0, 0, nil,
	)
	assert.Less(t, respWithCache.CostUSD, respAllRegular.CostUSD,
		"cache reads should cost less than regular input tokens")
}

// TestBuildSuccessResponse_ZeroCacheTokens_BackwardsCompat verifies that zero
// cache tokens leaves CacheReadTokens and CacheCreationTokens as zero and
// InputTokens equals the regular input (pre-cache behaviour unchanged).
func TestBuildSuccessResponse_ZeroCacheTokens_BackwardsCompat(t *testing.T) {
	resp := BuildSuccessResponse(
		"gpt-4o", "openai/gpt-4o",
		nil, time.Now(), 1_000_000, 0, 0, 500_000, nil,
	)
	assert.Equal(t, int64(1_000_000), resp.InputTokens)
	assert.Equal(t, int64(0), resp.CacheReadTokens)
	assert.Equal(t, int64(0), resp.CacheCreationTokens)
}

// TestBuildErrorResponse_NoCacheTokens verifies error responses always have
// zero cache token fields (partial-run errors don't track cache breakdown).
func TestBuildErrorResponse_NoCacheTokens(t *testing.T) {
	resp := BuildErrorResponse(
		"claude-sonnet-4-6", "anthropic/claude-sonnet-4-6",
		time.Now(), 500_000, 0, fmt.Errorf("timeout"),
	)
	assert.Equal(t, int64(0), resp.CacheReadTokens)
	assert.Equal(t, int64(0), resp.CacheCreationTokens)
	assert.GreaterOrEqual(t, resp.CostUSD, 0.0)
}

// ─── spawn_agent dispatch ─────────────────────────────────────────────────────

func TestDispatchTool_SpawnAgent_CallsDispatcher(t *testing.T) {
	called := false
	var dispatcher tools.SubagentDispatcher = func(_ context.Context, args map[string]string) (string, []tools.FileWrite, string) {
		called = true
		return "sub result: " + args["task"], nil, ""
	}
	ctx := tools.WithSubagentDispatcher(context.Background(), dispatcher)

	var proposed []tools.FileWrite
	result, ok := DispatchTool(ctx, tools.SpawnAgentToolName,
		map[string]string{"task": "find all errors"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok)
	assert.True(t, called)
	assert.Equal(t, "sub result: find all errors", result)
	assert.Empty(t, proposed)
}

func TestDispatchTool_SpawnAgent_MergesWrites(t *testing.T) {
	writes := []tools.FileWrite{
		{Path: "out.go", Content: "package main"},
		{Path: "readme.md", Content: "# hi"},
	}
	var dispatcher tools.SubagentDispatcher = func(_ context.Context, _ map[string]string) (string, []tools.FileWrite, string) {
		return "done", writes, ""
	}
	ctx := tools.WithSubagentDispatcher(context.Background(), dispatcher)

	var proposed []tools.FileWrite
	result, ok := DispatchTool(ctx, tools.SpawnAgentToolName,
		map[string]string{"task": "write some files"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok)
	assert.Equal(t, "done", result)
	require.Len(t, proposed, 2)
	assert.Equal(t, "out.go", proposed[0].Path)
	assert.Equal(t, "readme.md", proposed[1].Path)
}

func TestDispatchTool_SpawnAgent_NoDispatcherReturnsError(t *testing.T) {
	// Without a dispatcher in context, spawn_agent returns a graceful error string.
	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), tools.SpawnAgentToolName,
		map[string]string{"task": "do something"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok) // ok=true: the tool was recognised and handled
	assert.Contains(t, result, "not configured")
	assert.Empty(t, proposed)
}

func TestDispatchTool_SpawnAgent_DispatcherErrorPropagates(t *testing.T) {
	var dispatcher tools.SubagentDispatcher = func(_ context.Context, _ map[string]string) (string, []tools.FileWrite, string) {
		return "", nil, "[spawn_agent error: max depth reached]"
	}
	ctx := tools.WithSubagentDispatcher(context.Background(), dispatcher)

	var proposed []tools.FileWrite
	result, ok := DispatchTool(ctx, tools.SpawnAgentToolName,
		map[string]string{"task": "recurse"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok)
	assert.Contains(t, result, "max depth reached")
	assert.Empty(t, proposed)
}
