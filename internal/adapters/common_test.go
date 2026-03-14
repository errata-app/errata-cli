package adapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/tools"
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
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, "hello", result)
	require.Len(t, events, 1)
	assert.Equal(t, models.EventReading, events[0].Type)
	assert.Equal(t, relPath, events[0].Data)
	assert.Empty(t, proposed)
}

func TestDispatchTool_WriteEmitsEventAndQueues(t *testing.T) {
	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.WriteToolName, map[string]string{"path": "out.txt", "content": "world"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, writeAck, result)
	require.Len(t, events, 1)
	assert.Equal(t, models.EventWriting, events[0].Type)
	assert.Equal(t, "out.txt", events[0].Data)
	require.Len(t, proposed, 1)
	assert.Equal(t, "out.txt", proposed[0].Path)
	assert.Equal(t, "world", proposed[0].Content)
}

func TestDispatchTool_UnknownToolReturnsFalse(t *testing.T) {
	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), "unknown_tool", map[string]string{}, func(models.AgentEvent) {}, &proposed, nil)
	assert.False(t, ok)
	assert.Empty(t, result)
	assert.Empty(t, proposed)
}

func TestDispatchTool_WriteDoesNotExecute(t *testing.T) {
	// write_file must never touch disk — it only queues.
	dir := t.TempDir()
	t.Chdir(dir)
	const relPath = "should-not-exist.txt"
	var proposed []tools.FileWrite

	_, ok := DispatchTool(context.Background(), tools.WriteToolName, map[string]string{"path": relPath, "content": "data"},
		func(models.AgentEvent) {}, &proposed, nil)

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
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Contains(t, result, "file.txt")
	require.Len(t, events, 1)
	assert.Equal(t, models.EventReading, events[0].Type)
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
		func(models.AgentEvent) {}, &proposed, nil)
	assert.Contains(t, result1, "sub/")
	assert.NotContains(t, result1, "deep.txt")

	// depth=3 should reach deep.txt
	result3, _ := DispatchTool(context.Background(), tools.ListDirToolName, map[string]string{"path": ".", "depth": "3"},
		func(models.AgentEvent) {}, &proposed, nil)
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
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Contains(t, result, "main.go")
	assert.NotContains(t, result, "readme.md")
	require.Len(t, events, 1)
	assert.Equal(t, models.EventReading, events[0].Type)
	assert.Empty(t, proposed)
}

func TestDispatchTool_SearchFilesDoubleStarPattern(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll("internal/tools", 0o755))
	require.NoError(t, os.WriteFile("internal/tools/tools.go", []byte(""), 0o644))

	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), tools.SearchFilesName, map[string]string{"pattern": "**/*.go"},
		func(models.AgentEvent) {}, &proposed, nil)

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
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Contains(t, result, "Hello")
	require.Len(t, events, 1)
	assert.Equal(t, models.EventReading, events[0].Type)
	assert.Empty(t, proposed)
}

func TestDispatchTool_SearchCodeFileGlob(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("foo.go", []byte("needle\n"), 0o644))
	require.NoError(t, os.WriteFile("bar.txt", []byte("needle\n"), 0o644))

	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), tools.SearchCodeName, map[string]string{"pattern": "needle", "file_glob": "*.go"},
		func(models.AgentEvent) {}, &proposed, nil)

	assert.True(t, ok)
	assert.Contains(t, result, "foo.go")
	assert.NotContains(t, result, "bar.txt")
}

func TestDispatchTool_BashEmitsEventAndReturnsOutput(t *testing.T) {
	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(context.Background(), tools.BashToolName,
		map[string]string{"command": "echo hi", "description": "say hi"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, "hi", result)
	require.Len(t, events, 1)
	assert.Equal(t, models.EventBash, events[0].Type)
	assert.Equal(t, "say hi", events[0].Data)
	assert.Empty(t, proposed)
}

func TestDispatchTool_BashFallsBackToCommandAsDesc(t *testing.T) {
	// When description is omitted, the command itself is used as the event Data.
	var events []models.AgentEvent
	var proposed []tools.FileWrite

	_, ok := DispatchTool(context.Background(), tools.BashToolName,
		map[string]string{"command": "echo x"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

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
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.True(t, called)
	assert.Equal(t, "mcp result: hello", result)
	require.Len(t, events, 1)
	assert.Equal(t, models.EventReading, events[0].Type)
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
	resp := BuildErrorResponse("gpt-4o", "openai/gpt-4o", start, 100, 50, 0, fmt.Errorf("api error"))

	assert.Equal(t, "gpt-4o", resp.ModelID)
	assert.GreaterOrEqual(t, resp.LatencyMS, int64(0))
	assert.Equal(t, int64(100), resp.InputTokens)
	assert.Equal(t, int64(50), resp.OutputTokens)
	assert.Equal(t, "api error", resp.Error)
	assert.False(t, resp.OK())
}

func TestBuildErrorResponse_ZeroTokensZeroCost(t *testing.T) {
	resp := BuildErrorResponse("m", "m", time.Now(), 0, 0, 0, fmt.Errorf("e"))
	assert.Zero(t, resp.CostUSD)
}

// ─── BuildSuccessResponse ─────────────────────────────────────────────────────

func TestBuildSuccessResponse_Fields(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	fw := []tools.FileWrite{{Path: "a.go", Content: "package a"}}

	resp := BuildSuccessResponse("claude-sonnet-4-6", "anthropic/claude-sonnet-4-6",
		[]string{"hello ", "world"}, start, 200, 80, 0, fw, nil)

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
	resp := BuildSuccessResponse("m", "m", nil, time.Now(), 0, 0, 0, nil, nil)
	assert.Empty(t, resp.Text)
	assert.Empty(t, resp.ProposedWrites)
}

// ─── BuildInterruptedResponse ─────────────────────────────────────────────────

func TestBuildInterruptedResponse_Fields(t *testing.T) {
	start := time.Now().Add(-75 * time.Millisecond)
	fw := []tools.FileWrite{{Path: "partial.go", Content: "package partial"}}

	resp := BuildInterruptedResponse("gpt-4o", "openai/gpt-4o",
		[]string{"partial ", "text"}, start, 300, 100, 0, fw, nil, fmt.Errorf("context cancelled"))

	assert.Equal(t, "gpt-4o", resp.ModelID)
	assert.Equal(t, "partial text", resp.Text)
	assert.True(t, resp.Interrupted)
	assert.Equal(t, "context cancelled", resp.Error)
	assert.GreaterOrEqual(t, resp.LatencyMS, int64(0))
	assert.Equal(t, int64(300), resp.InputTokens)
	assert.Equal(t, int64(100), resp.OutputTokens)
	require.Len(t, resp.ProposedWrites, 1)
	assert.Equal(t, "partial.go", resp.ProposedWrites[0].Path)
	assert.False(t, resp.OK()) // interrupted + error ⇒ not OK
}

func TestBuildInterruptedResponse_NilWrites(t *testing.T) {
	resp := BuildInterruptedResponse("m", "m", nil, time.Now(), 0, 0, 0, nil, nil, fmt.Errorf("cancelled"))
	assert.True(t, resp.Interrupted)
	assert.Empty(t, resp.ProposedWrites)
	assert.Empty(t, resp.Text)
}

// ─── EmitSnapshot ─────────────────────────────────────────────────────────────

func TestEmitSnapshot_EmitsSnapshotEvent(t *testing.T) {
	var events []models.AgentEvent
	start := time.Now().Add(-200 * time.Millisecond)

	EmitSnapshot(
		func(e models.AgentEvent) { events = append(events, e) },
		"openai/gpt-4o",
		[]string{"hello ", "world"},
		start, 500, 100, 0,
		[]tools.FileWrite{{Path: "f.go", Content: "c"}}, nil,
	)

	require.Len(t, events, 1)
	assert.Equal(t, models.EventSnapshot, events[0].Type)
	assert.Contains(t, events[0].Data, `"text":"hello world"`)
	assert.Contains(t, events[0].Data, `"input_tokens":500`)
	assert.Contains(t, events[0].Data, `"output_tokens":100`)
}

func TestEmitSnapshot_NilWritesOK(t *testing.T) {
	var events []models.AgentEvent
	EmitSnapshot(
		func(e models.AgentEvent) { events = append(events, e) },
		"m", nil, time.Now(), 0, 0, 0, nil, nil,
	)
	require.Len(t, events, 1)
	assert.Equal(t, models.EventSnapshot, events[0].Type)
}

// ─── applyOutputProcessing ──────────────────────────────────────────────────

func TestApplyOutputProcessing_NoRuleReturnsUnchanged(t *testing.T) {
	// No rule in context → output returned as-is.
	output := "line1\nline2\nline3"
	result := applyOutputProcessing(context.Background(), "read_file", output)
	assert.Equal(t, output, result)
}

// ─── DispatchTool MCP error event ───────────────────────────────────────────

func TestDispatchTool_MCPError_EmitsErrorEvent(t *testing.T) {
	dispatchers := map[string]tools.MCPDispatcher{
		"broken_tool": func(args map[string]string) string {
			return "[mcp error: connection lost]"
		},
	}
	ctx := tools.WithMCPDispatchers(context.Background(), dispatchers)

	var events []models.AgentEvent
	var proposed []tools.FileWrite
	result, ok := DispatchTool(ctx, "broken_tool", nil,
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, "[mcp error: connection lost]", result)
	require.Len(t, events, 2)
	assert.Equal(t, models.EventReading, events[0].Type)
	assert.Equal(t, models.EventError, events[1].Type)
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
		func(models.AgentEvent) {}, &proposed, nil)

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
		func(models.AgentEvent) {}, &proposed, nil)

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
		func(models.AgentEvent) {}, &proposed, nil)

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
		func(models.AgentEvent) {}, &proposed, nil)

	assert.True(t, ok)
	assert.Contains(t, result, "max depth reached")
	assert.Empty(t, proposed)
}

// ─── DebugRequestsFromContext ───────────────────────────────────────────────

func TestDebugRequestsFromContext_DefaultFalse(t *testing.T) {
	assert.False(t, DebugRequestsFromContext(context.Background()))
}

func TestWithDebugRequests_SetsFlag(t *testing.T) {
	ctx := WithDebugRequests(context.Background())
	assert.True(t, DebugRequestsFromContext(ctx))
}

// ─── EmitRequest ────────────────────────────────────────────────────────────

func TestEmitRequest_EmitsWhenDebugEnabled(t *testing.T) {
	ctx := WithDebugRequests(context.Background())
	var events []models.AgentEvent
	params := map[string]string{"model": "test-model", "prompt": "hello"}

	EmitRequest(ctx, func(e models.AgentEvent) { events = append(events, e) }, params)

	require.Len(t, events, 1)
	assert.Equal(t, models.EventRequest, events[0].Type)
	assert.Contains(t, events[0].Data, `"model":"test-model"`)
	assert.Contains(t, events[0].Data, `"prompt":"hello"`)
}

func TestEmitRequest_SkipsWhenDebugDisabled(t *testing.T) {
	var events []models.AgentEvent
	params := map[string]string{"model": "test-model"}

	EmitRequest(context.Background(), func(e models.AgentEvent) { events = append(events, e) }, params)

	assert.Empty(t, events)
}

func TestEmitRequest_HandlesUnmarshalableParams(t *testing.T) {
	ctx := WithDebugRequests(context.Background())
	var events []models.AgentEvent

	// Channels cannot be JSON-marshalled.
	EmitRequest(ctx, func(e models.AgentEvent) { events = append(events, e) }, make(chan int))

	assert.Empty(t, events)
}

// ─── DispatchTool tool call tracking ───────────────────────────────────────

func TestDispatchTool_TracksToolCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("a.txt", []byte("hello"), 0o644))

	var proposed []tools.FileWrite
	tc := map[string]int{}

	// Call read_file twice and bash once.
	_, ok := DispatchTool(context.Background(), tools.ReadToolName, map[string]string{"path": "a.txt"},
		func(models.AgentEvent) {}, &proposed, &tc)
	assert.True(t, ok)

	_, ok = DispatchTool(context.Background(), tools.ReadToolName, map[string]string{"path": "a.txt"},
		func(models.AgentEvent) {}, &proposed, &tc)
	assert.True(t, ok)

	_, ok = DispatchTool(context.Background(), tools.BashToolName,
		map[string]string{"command": "echo hi"},
		func(models.AgentEvent) {}, &proposed, &tc)
	assert.True(t, ok)

	assert.Equal(t, 2, tc[tools.ReadToolName])
	assert.Equal(t, 1, tc[tools.BashToolName])
}

func TestDispatchTool_NilToolCalls_NoPanic(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("b.txt", []byte("world"), 0o644))

	var proposed []tools.FileWrite
	// Pass nil — must not panic.
	result, ok := DispatchTool(context.Background(), tools.ReadToolName, map[string]string{"path": "b.txt"},
		func(models.AgentEvent) {}, &proposed, nil)
	assert.True(t, ok)
	assert.Equal(t, "world", result)
}

func TestDispatchTool_UnknownToolDoesNotTrack(t *testing.T) {
	var proposed []tools.FileWrite
	tc := map[string]int{}

	_, ok := DispatchTool(context.Background(), "nonexistent", nil,
		func(models.AgentEvent) {}, &proposed, &tc)
	assert.False(t, ok)
	assert.Empty(t, tc, "unknown tools should not be tracked")
}

// ─── StopReason ─────────────────────────────────────────────────────────────

func TestBuildSuccessResponse_SetsStopReasonComplete(t *testing.T) {
	resp := BuildSuccessResponse("m", "m", []string{"hi"}, time.Now(), 100, 50, 0, nil, nil)
	assert.Equal(t, models.StopReasonComplete, resp.StopReason)
}

func TestBuildErrorResponse_SetsStopReasonError(t *testing.T) {
	resp := BuildErrorResponse("m", "m", time.Now(), 0, 0, 0, fmt.Errorf("boom"))
	assert.Equal(t, models.StopReasonError, resp.StopReason)
}

func TestBuildInterruptedResponse_SetsStopReasonCancelled(t *testing.T) {
	resp := BuildInterruptedResponse("m", "m", nil, time.Now(), 0, 0, 0, nil, nil, fmt.Errorf("cancelled"))
	assert.Equal(t, models.StopReasonCancelled, resp.StopReason)
}

func TestBuildMaxStepsResponse_SetsStopReasonMaxSteps(t *testing.T) {
	resp := BuildMaxStepsResponse("m", "m", []string{"partial"}, time.Now(), 200, 100, 0, nil, map[string]int{"read_file": 3})
	assert.Equal(t, models.StopReasonMaxSteps, resp.StopReason)
	assert.Equal(t, "partial", resp.Text)
	assert.Equal(t, int64(200), resp.InputTokens)
	assert.Equal(t, 3, resp.ToolCalls["read_file"])
}

func TestBuildSuccessResponse_IncludesToolCalls(t *testing.T) {
	tc := map[string]int{"read_file": 3, "bash": 1}
	resp := BuildSuccessResponse("m", "m", []string{"hi"}, time.Now(), 100, 50, 0,
		[]tools.FileWrite{{Path: "f.go", Content: "c"}}, tc)
	assert.Equal(t, tc, resp.ToolCalls)
}

func TestBuildInterruptedResponse_IncludesToolCalls(t *testing.T) {
	tc := map[string]int{"read_file": 2}
	resp := BuildInterruptedResponse("m", "m", []string{"partial"}, time.Now(), 100, 50, 0,
		nil, tc, fmt.Errorf("cancelled"))
	assert.Equal(t, tc, resp.ToolCalls)
}

func TestEmitSnapshot_IncludesToolCalls(t *testing.T) {
	var events []models.AgentEvent
	tc := map[string]int{"read_file": 2, "bash": 1}

	EmitSnapshot(
		func(e models.AgentEvent) { events = append(events, e) },
		"m", nil, time.Now(), 0, 0, 0, nil, tc,
	)

	require.Len(t, events, 1)
	assert.Contains(t, events[0].Data, `"tool_calls"`)
	assert.Contains(t, events[0].Data, `"read_file":2`)
}

// ─── DispatchTool direct writes ─────────────────────────────────────────────

func TestDispatchTool_DirectWrite(t *testing.T) {
	workDir := t.TempDir()
	ctx := tools.WithWorkDir(context.Background(), workDir)
	ctx = tools.WithDirectWrites(ctx, true)

	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(ctx, tools.WriteToolName,
		map[string]string{"path": "direct.txt", "content": "direct content"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, "File written.", result)
	assert.Empty(t, proposed, "direct writes should NOT queue proposals")

	// Verify file exists in the work dir.
	got, err := os.ReadFile(filepath.Join(workDir, "direct.txt"))
	require.NoError(t, err)
	assert.Equal(t, "direct content", string(got))
}

func TestDispatchTool_DirectEdit(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "edit.go"), []byte("func old() {}"), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)
	ctx = tools.WithDirectWrites(ctx, true)

	var events []models.AgentEvent
	var proposed []tools.FileWrite

	result, ok := DispatchTool(ctx, tools.EditToolName,
		map[string]string{"path": "edit.go", "old_string": "old", "new_string": "new"},
		func(e models.AgentEvent) { events = append(events, e) }, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, "File written.", result)
	assert.Empty(t, proposed)

	got, err := os.ReadFile(filepath.Join(workDir, "edit.go"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "new")
	assert.NotContains(t, string(got), "old")
}

func TestDispatchTool_DirectMultiEdit(t *testing.T) {
	workDir := t.TempDir()
	original := "func alpha() {}\nfunc beta() {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "multi.go"), []byte(original), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)
	ctx = tools.WithDirectWrites(ctx, true)

	var proposed []tools.FileWrite

	// First edit.
	result1, ok1 := DispatchTool(ctx, tools.EditToolName,
		map[string]string{"path": "multi.go", "old_string": "alpha", "new_string": "ALPHA"},
		func(models.AgentEvent) {}, &proposed, nil)
	assert.True(t, ok1)
	assert.Equal(t, "File written.", result1)

	// Second edit — reads from disk where first edit landed.
	result2, ok2 := DispatchTool(ctx, tools.EditToolName,
		map[string]string{"path": "multi.go", "old_string": "beta", "new_string": "BETA"},
		func(models.AgentEvent) {}, &proposed, nil)
	assert.True(t, ok2)
	assert.Equal(t, "File written.", result2)

	assert.Empty(t, proposed, "no proposals in direct mode")

	got, err := os.ReadFile(filepath.Join(workDir, "multi.go"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "ALPHA")
	assert.Contains(t, string(got), "BETA")
	assert.NotContains(t, string(got), "alpha")
	assert.NotContains(t, string(got), "beta")
}

func TestDispatchTool_TUIWriteUnchanged(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var proposed []tools.FileWrite
	result, ok := DispatchTool(context.Background(), tools.WriteToolName,
		map[string]string{"path": "queued.txt", "content": "queued"},
		func(models.AgentEvent) {}, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, writeAck, result)
	require.Len(t, proposed, 1)

	// File should NOT exist on disk.
	_, err := os.Stat(filepath.Join(dir, "queued.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestDispatchTool_ReadFromWorkDir(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "data.txt"), []byte("work dir content"), 0o644))

	ctx := tools.WithWorkDir(context.Background(), workDir)

	var proposed []tools.FileWrite
	result, ok := DispatchTool(ctx, tools.ReadToolName,
		map[string]string{"path": "data.txt"},
		func(models.AgentEvent) {}, &proposed, nil)

	assert.True(t, ok)
	assert.Equal(t, "work dir content", result)
}
