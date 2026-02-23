package adapters

import (
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

	result, ok := DispatchTool(tools.ReadToolName, map[string]string{"path": relPath},
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

	result, ok := DispatchTool(tools.WriteToolName, map[string]string{"path": "out.txt", "content": "world"},
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
	result, ok := DispatchTool("unknown_tool", map[string]string{}, func(models.AgentEvent) {}, &proposed)
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

	_, ok := DispatchTool(tools.WriteToolName, map[string]string{"path": relPath, "content": "data"},
		func(models.AgentEvent) {}, &proposed)

	assert.True(t, ok)
	_, err := os.Stat(relPath)
	assert.True(t, os.IsNotExist(err), "write_file must not write to disk")
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
