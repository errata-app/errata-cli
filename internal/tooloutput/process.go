// Package tooloutput provides deterministic truncation of tool output
// before it is fed back into the model context. This prevents large
// outputs (e.g. verbose build logs) from consuming disproportionate
// context window space.
//
// All processing is deterministic — no LLM-based summarization.
// If LLM-based output summarization is needed in the future, it must
// be behind a flag to avoid contaminating evaluation.
package tooloutput

import (
	"context"
	"fmt"
	"strings"
)

// Rule defines truncation parameters for a specific tool's output.
// A zero-value Rule is a no-op: output passes through unchanged.
type Rule struct {
	MaxLines          int    // 0 = unlimited
	MaxTokens         int    // 0 = unlimited (approximate: 4 chars ≈ 1 token)
	Truncation        string // "head", "tail", "head_tail"; default "tail"
	TruncationMessage string // template with {line_count}, {token_count}
}

// Process applies the rule to toolOutput, truncating if necessary.
// Returns the original output if the rule is zero-value or output is
// within limits. Never returns empty for non-empty input.
func Process(toolOutput string, rule Rule) string {
	if toolOutput == "" {
		return toolOutput
	}
	if rule.MaxLines <= 0 && rule.MaxTokens <= 0 {
		return toolOutput
	}

	lines := strings.Split(toolOutput, "\n")
	totalLines := len(lines)

	// Token-based truncation: approximate by character count.
	if rule.MaxTokens > 0 {
		maxChars := rule.MaxTokens * 4
		if len(toolOutput) > maxChars {
			// Re-split to lines that fit within the token budget.
			lines = truncateByChars(lines, maxChars, rule.truncationMode())
			totalLines = len(strings.Split(toolOutput, "\n"))
		}
	}

	// Line-based truncation.
	if rule.MaxLines > 0 && len(lines) > rule.MaxLines {
		truncated := truncateLines(lines, rule.MaxLines, rule.truncationMode())
		msg := rule.formatMessage(totalLines, estimateTokens(toolOutput))
		return strings.Join(truncated, "\n") + "\n" + msg
	}

	return strings.Join(lines, "\n")
}

func (r Rule) truncationMode() string {
	switch r.Truncation {
	case "head", "tail", "head_tail":
		return r.Truncation
	default:
		return "tail"
	}
}

func (r Rule) formatMessage(lineCount, tokenCount int) string {
	tmpl := r.TruncationMessage
	if tmpl == "" {
		tmpl = "[Truncated to {max_lines} lines. Full output: {line_count} lines]"
	}
	tmpl = strings.ReplaceAll(tmpl, "{line_count}", fmt.Sprintf("%d", lineCount))
	tmpl = strings.ReplaceAll(tmpl, "{token_count}", fmt.Sprintf("%d", tokenCount))
	tmpl = strings.ReplaceAll(tmpl, "{max_lines}", fmt.Sprintf("%d", r.MaxLines))
	return tmpl
}

// truncateLines returns a subset of lines according to the truncation mode.
func truncateLines(lines []string, maxLines int, mode string) []string {
	switch mode {
	case "head":
		return lines[:maxLines]
	case "head_tail":
		half := maxLines / 2
		tail := maxLines - half
		result := make([]string, 0, maxLines+1)
		result = append(result, lines[:half]...)
		result = append(result, "...")
		result = append(result, lines[len(lines)-tail:]...)
		return result
	default: // "tail"
		return lines[len(lines)-maxLines:]
	}
}

// truncateByChars returns lines that fit within maxChars according to the
// truncation mode. This is an approximation — it doesn't split mid-line.
func truncateByChars(lines []string, maxChars int, mode string) []string {
	switch mode {
	case "head":
		var result []string
		chars := 0
		for _, line := range lines {
			chars += len(line) + 1 // +1 for newline
			if chars > maxChars {
				break
			}
			result = append(result, line)
		}
		if len(result) == 0 && len(lines) > 0 {
			result = lines[:1] // always keep at least one line
		}
		return result
	case "head_tail":
		halfChars := maxChars / 2
		var head, tail []string
		chars := 0
		for _, line := range lines {
			chars += len(line) + 1
			if chars > halfChars {
				break
			}
			head = append(head, line)
		}
		chars = 0
		for i := len(lines) - 1; i >= 0; i-- {
			chars += len(lines[i]) + 1
			if chars > halfChars {
				break
			}
			tail = append([]string{lines[i]}, tail...)
		}
		result := make([]string, 0, len(head)+len(tail)+1)
		result = append(result, head...)
		result = append(result, "...")
		result = append(result, tail...)
		return result
	default: // "tail"
		var result []string
		chars := 0
		for i := len(lines) - 1; i >= 0; i-- {
			chars += len(lines[i]) + 1
			if chars > maxChars {
				break
			}
			result = append([]string{lines[i]}, result...)
		}
		if len(result) == 0 && len(lines) > 0 {
			result = lines[len(lines)-1:] // always keep at least one line
		}
		return result
	}
}

func estimateTokens(s string) int {
	return len(s) / 4
}

// ─── Context-based rule delivery ─────────────────────────────────────────────

type rulesKey struct{}

// WithRules returns a context carrying per-tool output processing rules.
func WithRules(ctx context.Context, rules map[string]Rule) context.Context {
	return context.WithValue(ctx, rulesKey{}, rules)
}

// RulesFromContext retrieves the output processing rules from ctx.
// Returns nil when no rules are stored.
func RulesFromContext(ctx context.Context) map[string]Rule {
	m, _ := ctx.Value(rulesKey{}).(map[string]Rule)
	return m
}

// RuleForTool returns the output processing rule for the named tool,
// or a zero-value Rule (no-op) if no rule exists.
func RuleForTool(ctx context.Context, toolName string) Rule {
	rules := RulesFromContext(ctx)
	if rules == nil {
		return Rule{}
	}
	return rules[toolName]
}
