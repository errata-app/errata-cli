package criteria_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/criteria"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ─── Parse tests ─────────────────────────────────────────────────────────────

func TestParse_AllTypes(t *testing.T) {
	raw := []string{
		"no_errors",
		"has_writes",
		"contains: all tests pass",
		"files_written >= 2",
	}
	parsed := criteria.Parse(raw)
	require.Len(t, parsed, 4)

	assert.Equal(t, "no_errors", parsed[0].Type)
	assert.Empty(t, parsed[0].Arg)

	assert.Equal(t, "has_writes", parsed[1].Type)
	assert.Empty(t, parsed[1].Arg)

	assert.Equal(t, "contains", parsed[2].Type)
	assert.Equal(t, "all tests pass", parsed[2].Arg)

	assert.Equal(t, "files_written", parsed[3].Type)
	assert.Equal(t, "2", parsed[3].Arg)
}

func TestParse_Unknown(t *testing.T) {
	parsed := criteria.Parse([]string{"some weird criterion"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "unknown", parsed[0].Type)
}

func TestParse_Empty(t *testing.T) {
	assert.Empty(t, criteria.Parse(nil))
	assert.Empty(t, criteria.Parse([]string{}))
	assert.Empty(t, criteria.Parse([]string{"", "  "}))
}

func TestParse_FilesWrittenNoSpace(t *testing.T) {
	parsed := criteria.Parse([]string{"files_written >=3"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "files_written", parsed[0].Type)
	assert.Equal(t, "3", parsed[0].Arg)
}

func TestParse_Run(t *testing.T) {
	parsed := criteria.Parse([]string{"run: go test ./..."})
	require.Len(t, parsed, 1)
	assert.Equal(t, "run", parsed[0].Type)
	assert.Equal(t, "go test ./...", parsed[0].Arg)
	assert.Equal(t, 0, parsed[0].Timeout)
}

func TestParse_RunWithTimeout(t *testing.T) {
	parsed := criteria.Parse([]string{"run(timeout=120): go test ./..."})
	require.Len(t, parsed, 1)
	assert.Equal(t, "run", parsed[0].Type)
	assert.Equal(t, "go test ./...", parsed[0].Arg)
	assert.Equal(t, 120, parsed[0].Timeout)
}

func TestParse_RunInvalidTimeout(t *testing.T) {
	parsed := criteria.Parse([]string{"run(timeout=abc): go test ./..."})
	require.Len(t, parsed, 1)
	assert.Equal(t, "unknown", parsed[0].Type)
}

func TestParse_RunEmptyCommand(t *testing.T) {
	parsed := criteria.Parse([]string{"run: "})
	require.Len(t, parsed, 1)
	assert.Equal(t, "unknown", parsed[0].Type)
}

func TestParse_MaxCost(t *testing.T) {
	parsed := criteria.Parse([]string{"max_cost: 0.05"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "max_cost", parsed[0].Type)
	assert.Equal(t, "0.05", parsed[0].Arg)
}

func TestParse_MaxCostInvalid(t *testing.T) {
	parsed := criteria.Parse([]string{"max_cost: abc"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "unknown", parsed[0].Type)
}

func TestParse_MaxLatency(t *testing.T) {
	parsed := criteria.Parse([]string{"max_latency: 30000"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "max_latency", parsed[0].Type)
	assert.Equal(t, "30000", parsed[0].Arg)
}

func TestParse_MaxLatencyInvalid(t *testing.T) {
	parsed := criteria.Parse([]string{"max_latency: slow"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "unknown", parsed[0].Type)
}

func TestParse_ToolUsed(t *testing.T) {
	parsed := criteria.Parse([]string{"tool_used: edit_file"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "tool_used", parsed[0].Type)
	assert.Equal(t, "edit_file", parsed[0].Arg)
}

func TestParse_MaxToolCalls(t *testing.T) {
	parsed := criteria.Parse([]string{"max_tool_calls: 20"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "max_tool_calls", parsed[0].Type)
	assert.Equal(t, "20", parsed[0].Arg)
}

func TestParse_MaxToolCallsInvalid(t *testing.T) {
	parsed := criteria.Parse([]string{"max_tool_calls: many"})
	require.Len(t, parsed, 1)
	assert.Equal(t, "unknown", parsed[0].Type)
}

// ─── Evaluate tests ──────────────────────────────────────────────────────────

func okResp() models.ModelResponse {
	return models.ModelResponse{
		ModelID: "test-model",
		Text:    "all tests pass. done.",
		ProposedWrites: []tools.FileWrite{
			{Path: "a.go", Content: "package a"},
			{Path: "b.go", Content: "package b"},
		},
	}
}

func errResp() models.ModelResponse {
	return models.ModelResponse{
		ModelID: "test-model",
		Error:   "context limit exceeded",
	}
}

func TestEvaluate_NoErrors_Pass(t *testing.T) {
	c := criteria.Parse([]string{"no_errors"})
	results := criteria.Evaluate(c, okResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_NoErrors_Fail(t *testing.T) {
	c := criteria.Parse([]string{"no_errors"})
	results := criteria.Evaluate(c, errResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "context limit")
}

func TestEvaluate_HasWrites_Pass(t *testing.T) {
	c := criteria.Parse([]string{"has_writes"})
	results := criteria.Evaluate(c, okResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_HasWrites_Fail(t *testing.T) {
	c := criteria.Parse([]string{"has_writes"})
	resp := okResp()
	resp.ProposedWrites = nil
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestEvaluate_Contains_Pass(t *testing.T) {
	c := criteria.Parse([]string{"contains: all tests pass"})
	results := criteria.Evaluate(c, okResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_Contains_Fail(t *testing.T) {
	c := criteria.Parse([]string{"contains: compilation error"})
	results := criteria.Evaluate(c, okResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestEvaluate_FilesWritten_Pass(t *testing.T) {
	c := criteria.Parse([]string{"files_written >= 2"})
	results := criteria.Evaluate(c, okResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_FilesWritten_Fail(t *testing.T) {
	c := criteria.Parse([]string{"files_written >= 5"})
	results := criteria.Evaluate(c, okResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "proposed 2 files, need >= 5")
}

func TestEvaluate_Unknown_AlwaysPasses(t *testing.T) {
	c := criteria.Parse([]string{"something unknown"})
	results := criteria.Evaluate(c, errResp(), criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed, "unknown criteria should always pass")
}

func TestEvaluate_MultipleCriteria(t *testing.T) {
	c := criteria.Parse([]string{"no_errors", "has_writes", "contains: missing text"})
	results := criteria.Evaluate(c, okResp(), criteria.EvalContext{})
	require.Len(t, results, 3)
	assert.True(t, results[0].Passed)  // no_errors
	assert.True(t, results[1].Passed)  // has_writes
	assert.False(t, results[2].Passed) // contains: missing text
}

// ─── Run criterion tests ─────────────────────────────────────────────────────

func TestEvaluate_Run_Pass(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("ok"), 0o644))

	c := []criteria.Criterion{{Raw: "run: test -f marker.txt", Type: "run", Arg: "test -f marker.txt"}}
	results := criteria.Evaluate(c, models.ModelResponse{}, criteria.EvalContext{WorkDir: dir})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_Run_Fail(t *testing.T) {
	dir := t.TempDir()

	c := []criteria.Criterion{{Raw: "run: exit 1", Type: "run", Arg: "echo fail output && exit 1"}}
	results := criteria.Evaluate(c, models.ModelResponse{}, criteria.EvalContext{WorkDir: dir})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "fail output")
}

func TestEvaluate_Run_NoWorkDir(t *testing.T) {
	c := []criteria.Criterion{{Raw: "run: echo hi", Type: "run", Arg: "echo hi"}}
	results := criteria.Evaluate(c, models.ModelResponse{}, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "worktree not available")
}

func TestEvaluate_Run_Timeout(t *testing.T) {
	dir := t.TempDir()

	c := []criteria.Criterion{{Raw: "run(timeout=1): sleep 60", Type: "run", Arg: "sleep 60", Timeout: 1}}
	results := criteria.Evaluate(c, models.ModelResponse{}, criteria.EvalContext{WorkDir: dir})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "timed out")
}

func TestEvaluate_Run_OutputTruncation(t *testing.T) {
	dir := t.TempDir()

	// Generate 100 lines of output, then fail.
	cmd := "for i in $(seq 1 100); do echo line_$i; done; exit 1"
	c := []criteria.Criterion{{Raw: "run: " + cmd, Type: "run", Arg: cmd}}
	results := criteria.Evaluate(c, models.ModelResponse{}, criteria.EvalContext{WorkDir: dir})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "truncated")
	// Should still contain the last line.
	assert.Contains(t, results[0].Detail, "line_100")
}

// ─── Max cost criterion tests ────────────────────────────────────────────────

func TestEvaluate_MaxCost_Pass(t *testing.T) {
	c := criteria.Parse([]string{"max_cost: 0.10"})
	resp := models.ModelResponse{CostUSD: 0.05}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_MaxCost_Fail(t *testing.T) {
	c := criteria.Parse([]string{"max_cost: 0.05"})
	resp := models.ModelResponse{CostUSD: 0.10}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "exceeds max")
}

func TestEvaluate_MaxCost_Exact(t *testing.T) {
	c := criteria.Parse([]string{"max_cost: 0.05"})
	resp := models.ModelResponse{CostUSD: 0.05}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed, "exact threshold should pass (<=)")
}

// ─── Max latency criterion tests ─────────────────────────────────────────────

func TestEvaluate_MaxLatency_Pass(t *testing.T) {
	c := criteria.Parse([]string{"max_latency: 5000"})
	resp := models.ModelResponse{LatencyMS: 3000}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_MaxLatency_Fail(t *testing.T) {
	c := criteria.Parse([]string{"max_latency: 5000"})
	resp := models.ModelResponse{LatencyMS: 10000}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "exceeds max")
}

// ─── Tool used criterion tests ───────────────────────────────────────────────

func TestEvaluate_ToolUsed_Pass(t *testing.T) {
	c := criteria.Parse([]string{"tool_used: edit_file"})
	resp := models.ModelResponse{ToolCalls: map[string]int{"edit_file": 3, "read_file": 5}}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_ToolUsed_Fail(t *testing.T) {
	c := criteria.Parse([]string{"tool_used: edit_file"})
	resp := models.ModelResponse{ToolCalls: map[string]int{"read_file": 5}}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "was not used")
}

func TestEvaluate_ToolUsed_NilMap(t *testing.T) {
	c := criteria.Parse([]string{"tool_used: edit_file"})
	resp := models.ModelResponse{}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

// ─── Max tool calls criterion tests ──────────────────────────────────────────

func TestEvaluate_MaxToolCalls_Pass(t *testing.T) {
	c := criteria.Parse([]string{"max_tool_calls: 20"})
	resp := models.ModelResponse{ToolCalls: map[string]int{"read_file": 5, "edit_file": 3}}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_MaxToolCalls_Fail(t *testing.T) {
	c := criteria.Parse([]string{"max_tool_calls: 5"})
	resp := models.ModelResponse{ToolCalls: map[string]int{"read_file": 3, "edit_file": 4}}
	results := criteria.Evaluate(c, resp, criteria.EvalContext{})
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "exceeds max")
}

// ─── tailLines tests ─────────────────────────────────────────────────────────

func TestTailLines_Short(t *testing.T) {
	result := criteria.TailLines("line1\nline2\nline3", 10)
	assert.Equal(t, "line1\nline2\nline3", result)
}

func TestTailLines_Long(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, "line")
	}
	input := strings.Join(lines, "\n")
	result := criteria.TailLines(input, 10)
	assert.Contains(t, result, "truncated 90 lines")
	// Should have exactly 10 "line" entries after the truncation notice.
	parts := strings.SplitN(result, "\n", 2)
	require.Len(t, parts, 2)
	assert.Len(t, strings.Split(parts[1], "\n"), 10)
}

func TestTailLines_TrailingNewline(t *testing.T) {
	result := criteria.TailLines("a\nb\n", 10)
	assert.Equal(t, "a\nb", result)
}

// ─── PassCount tests ─────────────────────────────────────────────────────────

func TestPassCount(t *testing.T) {
	results := []criteria.Result{
		{Passed: true},
		{Passed: false},
		{Passed: true},
	}
	assert.Equal(t, 2, criteria.PassCount(results))
}

func TestPassCount_Empty(t *testing.T) {
	assert.Equal(t, 0, criteria.PassCount(nil))
}
