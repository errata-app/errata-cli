package criteria_test

import (
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
	assert.Equal(t, "", parsed[0].Arg)

	assert.Equal(t, "has_writes", parsed[1].Type)
	assert.Equal(t, "", parsed[1].Arg)

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
	results := criteria.Evaluate(c, okResp())
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_NoErrors_Fail(t *testing.T) {
	c := criteria.Parse([]string{"no_errors"})
	results := criteria.Evaluate(c, errResp())
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "context limit")
}

func TestEvaluate_HasWrites_Pass(t *testing.T) {
	c := criteria.Parse([]string{"has_writes"})
	results := criteria.Evaluate(c, okResp())
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_HasWrites_Fail(t *testing.T) {
	c := criteria.Parse([]string{"has_writes"})
	resp := okResp()
	resp.ProposedWrites = nil
	results := criteria.Evaluate(c, resp)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestEvaluate_Contains_Pass(t *testing.T) {
	c := criteria.Parse([]string{"contains: all tests pass"})
	results := criteria.Evaluate(c, okResp())
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_Contains_Fail(t *testing.T) {
	c := criteria.Parse([]string{"contains: compilation error"})
	results := criteria.Evaluate(c, okResp())
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestEvaluate_FilesWritten_Pass(t *testing.T) {
	c := criteria.Parse([]string{"files_written >= 2"})
	results := criteria.Evaluate(c, okResp())
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestEvaluate_FilesWritten_Fail(t *testing.T) {
	c := criteria.Parse([]string{"files_written >= 5"})
	results := criteria.Evaluate(c, okResp())
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
	assert.Contains(t, results[0].Detail, "proposed 2 files, need >= 5")
}

func TestEvaluate_Unknown_AlwaysPasses(t *testing.T) {
	c := criteria.Parse([]string{"something unknown"})
	results := criteria.Evaluate(c, errResp())
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed, "unknown criteria should always pass")
}

func TestEvaluate_MultipleCriteria(t *testing.T) {
	c := criteria.Parse([]string{"no_errors", "has_writes", "contains: missing text"})
	results := criteria.Evaluate(c, okResp())
	require.Len(t, results, 3)
	assert.True(t, results[0].Passed)  // no_errors
	assert.True(t, results[1].Passed)  // has_writes
	assert.False(t, results[2].Passed) // contains: missing text
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
