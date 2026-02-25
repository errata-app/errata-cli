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

func TestRuleForTool_NoRulesInContext(t *testing.T) {
	rule := tooloutput.RuleForTool(context.Background(), "bash")
	assert.Equal(t, tooloutput.Rule{}, rule)
}

// ─── Token truncation modes ─────────────────────────────────────────────────

func TestProcess_TokenTruncation_Head(t *testing.T) {
	// 5 lines × 40 chars = 200 chars. Budget: 25 tokens = 100 chars → ~2 lines.
	parts := []string{"AAAA-line1-aaaa-aaaa-aaaa-aaaa-aaaa-aaaa",
		"BBBB-line2-bbbb-bbbb-bbbb-bbbb-bbbb-bbbb",
		"CCCC-line3-cccc-cccc-cccc-cccc-cccc-cccc",
		"DDDD-line4-dddd-dddd-dddd-dddd-dddd-dddd",
		"EEEE-line5-eeee-eeee-eeee-eeee-eeee-eeee"}
	input := strings.Join(parts, "\n")
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxTokens:  25,
		Truncation: "head",
	})
	assert.Contains(t, result, "AAAA")
	assert.Contains(t, result, "BBBB")
	assert.NotContains(t, result, "EEEE")
}

func TestProcess_TokenTruncation_HeadTail(t *testing.T) {
	parts := make([]string, 10)
	for i := range parts {
		parts[i] = strings.Repeat("x", 40)
	}
	parts[0] = "FIRST-LINE-" + strings.Repeat("x", 29)
	parts[9] = "LAST-LINE--" + strings.Repeat("x", 29)
	input := strings.Join(parts, "\n")
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxTokens:  50, // 200 chars → ~4 lines worth
		Truncation: "head_tail",
	})
	assert.Contains(t, result, "FIRST")
	assert.Contains(t, result, "LAST")
	assert.Contains(t, result, "...")
	assert.Less(t, len(result), len(input))
}

// ─── Combined MaxTokens + MaxLines ──────────────────────────────────────────

func TestProcess_BothTokenAndLineLimit(t *testing.T) {
	// 10 lines × 40 chars. Token budget: 50 tokens (200 chars) → ~4 lines.
	// Then MaxLines=3 trims further.
	parts := make([]string, 10)
	for i := range parts {
		parts[i] = strings.Repeat("a", 40)
	}
	input := strings.Join(parts, "\n")
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxTokens:  50,
		MaxLines:   3,
		Truncation: "tail",
	})
	resultLines := strings.Split(result, "\n")
	// Should have 3 content lines + truncation message.
	assert.Contains(t, result, "Truncated")
	// The number of lines in the output (including the message) should be <= 5
	assert.LessOrEqual(t, len(resultLines), 5)
}

// ─── Line truncation edge cases ─────────────────────────────────────────────

func TestProcess_MaxLinesOne(t *testing.T) {
	input := "line1\nline2\nline3"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:   1,
		Truncation: "tail",
	})
	assert.Contains(t, result, "line3")
	assert.Contains(t, result, "Truncated")
}

func TestProcess_HeadTail_OddMaxLines(t *testing.T) {
	input := "a\nb\nc\nd\ne\nf\ng"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:   3, // half=1, tail=2 → 1 head + "..." + 2 tail
		Truncation: "head_tail",
	})
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "...")
	assert.Contains(t, result, "f")
	assert.Contains(t, result, "g")
}

// ─── Token truncation edge cases ────────────────────────────────────────────

func TestProcess_TokenBudgetSmallerThanOneLine(t *testing.T) {
	// Each line is 100 chars. Budget: 5 tokens = 20 chars. Head mode should
	// keep at least one line (the "always keep at least one" guarantee).
	input := strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 100)
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxTokens:  5,
		Truncation: "head",
	})
	assert.NotEmpty(t, result)
	assert.Contains(t, result, strings.Repeat("x", 100))
}

func TestProcess_TokenBudgetSmallerThanOneLine_Tail(t *testing.T) {
	input := strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 100)
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxTokens:  5,
		Truncation: "tail",
	})
	assert.NotEmpty(t, result)
	assert.Contains(t, result, strings.Repeat("y", 100))
}

// ─── Message formatting ─────────────────────────────────────────────────────

func TestProcess_TokenCountPlaceholder(t *testing.T) {
	input := "a\nb\nc\nd\ne"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:          2,
		TruncationMessage: "tokens={token_count} lines={line_count} max={max_lines}",
	})
	assert.Contains(t, result, "tokens=")
	assert.Contains(t, result, "lines=5")
	assert.Contains(t, result, "max=2")
}

func TestProcess_MessageWithNoPlaceholders(t *testing.T) {
	input := "a\nb\nc\nd\ne"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:          2,
		TruncationMessage: "[output truncated]",
	})
	assert.Contains(t, result, "[output truncated]")
}

// ─── Invalid truncation mode defaults to tail ───────────────────────────────

func TestProcess_InvalidTruncationMode(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5"
	result := tooloutput.Process(input, tooloutput.Rule{
		MaxLines:   2,
		Truncation: "middle", // invalid → should default to tail
	})
	assert.Contains(t, result, "line4")
	assert.Contains(t, result, "line5")
}

// ─── Negative MaxLines/MaxTokens treated as no-op ───────────────────────────

func TestProcess_NegativeMaxLines(t *testing.T) {
	input := "a\nb\nc"
	assert.Equal(t, input, tooloutput.Process(input, tooloutput.Rule{MaxLines: -1}))
}

func TestProcess_NegativeMaxTokens(t *testing.T) {
	input := "a\nb\nc"
	assert.Equal(t, input, tooloutput.Process(input, tooloutput.Rule{MaxTokens: -1}))
}
