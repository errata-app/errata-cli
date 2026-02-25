package tooloutput_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/suarezc/errata/internal/tooloutput"
)

func lines(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = strings.Repeat("x", 20)
	}
	return strings.Join(parts, "\n")
}

func TestProcess_NoRule_PassThrough(t *testing.T) {
	input := lines(100)
	assert.Equal(t, input, tooloutput.Process(input, tooloutput.Rule{}))
}

func TestProcess_EmptyInput(t *testing.T) {
	assert.Equal(t, "", tooloutput.Process("", tooloutput.Rule{MaxLines: 10}))
}

func TestProcess_WithinLimits_PassThrough(t *testing.T) {
	input := lines(5)
	result := tooloutput.Process(input, tooloutput.Rule{MaxLines: 10})
	assert.Equal(t, input, result)
}

func TestProcess_TailTruncation(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:   2,
		Truncation: "tail",
	})
	assert.Contains(t, result, "line4")
	assert.Contains(t, result, "line5")
	assert.NotContains(t, result, "line1\n")
	assert.Contains(t, result, "Truncated")
}

func TestProcess_HeadTruncation(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:   2,
		Truncation: "head",
	})
	assert.Contains(t, result, "line1")
	assert.Contains(t, result, "line2")
	assert.NotContains(t, result, "line5\n")
	assert.Contains(t, result, "Truncated")
}

func TestProcess_HeadTailTruncation(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:   4,
		Truncation: "head_tail",
	})
	assert.Contains(t, result, "line1")
	assert.Contains(t, result, "line2")
	assert.Contains(t, result, "...")
	assert.Contains(t, result, "line9")
	assert.Contains(t, result, "line10")
	assert.Contains(t, result, "Truncated")
}

func TestProcess_CustomTruncationMessage(t *testing.T) {
	input := "a\nb\nc\nd\ne"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:          2,
		TruncationMessage: "Output had {line_count} lines, showing {max_lines}",
	})
	assert.Contains(t, result, "Output had 5 lines, showing 2")
}

func TestProcess_DefaultTruncationIsTail(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines: 2,
		// No Truncation specified — should default to "tail"
	})
	assert.Contains(t, result, "line4")
	assert.Contains(t, result, "line5")
}

func TestProcess_TokenTruncation(t *testing.T) {
	// Create multi-line input that exceeds the token budget.
	// 20 lines × 40 chars each = 800 chars ≈ 200 tokens.
	parts := make([]string, 20)
	for i := range parts {
		parts[i] = strings.Repeat("a", 40)
	}
	input := strings.Join(parts, "\n")
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxTokens: 50, // ~200 chars budget → should keep only a few lines
	})
	// Should be truncated: fewer lines than original.
	assert.Less(t, len(result), len(input))
}

func TestRulesFromContext_Present(t *testing.T) {
	rules := map[string]tooloutput.Rule{
		"bash": {MaxLines: 100, Truncation: "tail"},
	}
	ctx := tooloutput.WithRules(context.Background(), rules)

	got := tooloutput.RulesFromContext(ctx)
	assert.Equal(t, 100, got["bash"].MaxLines)
}

func TestRulesFromContext_Absent(t *testing.T) {
	got := tooloutput.RulesFromContext(context.Background())
	assert.Nil(t, got)
}

func TestRuleForTool_Exists(t *testing.T) {
	rules := map[string]tooloutput.Rule{
		"bash": {MaxLines: 50},
	}
	ctx := tooloutput.WithRules(context.Background(), rules)
	rule := tooloutput.RuleForTool(ctx, "bash")
	assert.Equal(t, 50, rule.MaxLines)
}

func TestRuleForTool_NotExists(t *testing.T) {
	rules := map[string]tooloutput.Rule{
		"bash": {MaxLines: 50},
	}
	ctx := tooloutput.WithRules(context.Background(), rules)
	rule := tooloutput.RuleForTool(ctx, "read_file")
	assert.Equal(t, 0, rule.MaxLines) // zero-value = no-op
}
