// Package criteria parses and evaluates success criteria from Errata recipe files.
//
// Criteria are defined as bullet points in the recipe's ## Success Criteria section
// and evaluated against each model's ModelResponse after a headless task run.
package criteria

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/errata-app/errata-cli/internal/models"
)

// Criterion is a single parsed success criterion.
type Criterion struct {
	Raw     string // original string from the recipe
	Type    string // "no_errors" | "has_writes" | "contains" | "files_written" | "run" | "max_cost" | "max_latency" | "tool_used" | "max_tool_calls" | "unknown"
	Arg     string // comparison value when applicable
	Timeout int    // seconds; used only by "run" type (0 = default 60s)
}

// EvalContext provides environmental data for criterion evaluation.
type EvalContext struct {
	WorkDir string // absolute path to model's worktree; "" if unavailable
}

// Result is the evaluation of one criterion against one model response.
type Result struct {
	Criterion string `json:"criterion"`
	Passed    bool   `json:"passed"`
	Detail    string `json:"detail,omitempty"`
}

// Parse converts raw criterion strings (from a recipe) into typed Criterion values.
// Unknown formats are returned with Type "unknown" and will always pass evaluation.
func Parse(raw []string) []Criterion {
	out := make([]Criterion, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, parseSingle(s))
	}
	return out
}

func parseSingle(s string) Criterion {
	lower := strings.ToLower(s)

	switch lower {
	case "no_errors":
		return Criterion{Raw: s, Type: "no_errors"}
	case "has_writes":
		return Criterion{Raw: s, Type: "has_writes"}
	}

	// "contains: <text>"
	if strings.HasPrefix(lower, "contains:") {
		arg := strings.TrimSpace(s[len("contains:"):])
		return Criterion{Raw: s, Type: "contains", Arg: arg}
	}

	// "files_written >= N"
	if strings.HasPrefix(lower, "files_written") {
		rest := strings.TrimSpace(lower[len("files_written"):])
		rest = strings.TrimPrefix(rest, ">=")
		rest = strings.TrimSpace(rest)
		if _, err := strconv.Atoi(rest); err == nil {
			return Criterion{Raw: s, Type: "files_written", Arg: rest}
		}
	}

	// "run(timeout=N): <cmd>" or "run: <cmd>"
	if strings.HasPrefix(lower, "run(") {
		// Extract timeout from run(timeout=N):
		closeParen := strings.Index(lower, ")")
		if closeParen > 0 {
			inside := lower[len("run("):closeParen]
			after := strings.TrimSpace(s[closeParen+1:])
			after = strings.TrimPrefix(after, ":")
			after = strings.TrimSpace(after)
			if strings.HasPrefix(inside, "timeout=") {
				tStr := inside[len("timeout="):]
				if t, err := strconv.Atoi(tStr); err == nil && t > 0 && after != "" {
					return Criterion{Raw: s, Type: "run", Arg: after, Timeout: t}
				}
			}
		}
		// Invalid run(timeout=...) syntax falls through to unknown.
	} else if strings.HasPrefix(lower, "run:") {
		arg := strings.TrimSpace(s[len("run:"):])
		if arg != "" {
			return Criterion{Raw: s, Type: "run", Arg: arg}
		}
	}

	// "max_cost: <float>"
	if strings.HasPrefix(lower, "max_cost:") {
		arg := strings.TrimSpace(s[len("max_cost:"):])
		if _, err := strconv.ParseFloat(arg, 64); err == nil {
			return Criterion{Raw: s, Type: "max_cost", Arg: arg}
		}
	}

	// "max_latency: <int>"
	if strings.HasPrefix(lower, "max_latency:") {
		arg := strings.TrimSpace(s[len("max_latency:"):])
		if _, err := strconv.Atoi(arg); err == nil {
			return Criterion{Raw: s, Type: "max_latency", Arg: arg}
		}
	}

	// "tool_used: <name>"
	if strings.HasPrefix(lower, "tool_used:") {
		arg := strings.TrimSpace(s[len("tool_used:"):])
		if arg != "" {
			return Criterion{Raw: s, Type: "tool_used", Arg: arg}
		}
	}

	// "max_tool_calls: <int>"
	if strings.HasPrefix(lower, "max_tool_calls:") {
		arg := strings.TrimSpace(s[len("max_tool_calls:"):])
		if _, err := strconv.Atoi(arg); err == nil {
			return Criterion{Raw: s, Type: "max_tool_calls", Arg: arg}
		}
	}

	fmt.Fprintf(os.Stderr, "criteria: unknown criterion %q, will always pass\n", s)
	return Criterion{Raw: s, Type: "unknown"}
}

// Evaluate runs all criteria against a single ModelResponse and returns the results.
func Evaluate(criteria []Criterion, resp models.ModelResponse, ectx EvalContext) []Result {
	results := make([]Result, len(criteria))
	for i, c := range criteria {
		results[i] = evaluateSingle(c, resp, ectx)
	}
	return results
}

func evaluateSingle(c Criterion, resp models.ModelResponse, ectx EvalContext) Result {
	switch c.Type {
	case "no_errors":
		if resp.Error == "" {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: "error: " + resp.Error}

	case "has_writes":
		if len(resp.ProposedWrites) > 0 {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: "no files proposed"}

	case "contains":
		if strings.Contains(resp.Text, c.Arg) {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: fmt.Sprintf("text does not contain %q", c.Arg)}

	case "files_written":
		n, _ := strconv.Atoi(c.Arg)
		if len(resp.ProposedWrites) >= n {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: fmt.Sprintf("proposed %d files, need >= %d", len(resp.ProposedWrites), n)}

	case "run":
		return evaluateRun(c, ectx)

	case "max_cost":
		threshold, _ := strconv.ParseFloat(c.Arg, 64)
		if resp.CostUSD <= threshold {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: fmt.Sprintf("cost $%.4f exceeds max $%.4f", resp.CostUSD, threshold)}

	case "max_latency":
		threshold, _ := strconv.Atoi(c.Arg)
		if resp.LatencyMS <= int64(threshold) {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: fmt.Sprintf("latency %dms exceeds max %dms", resp.LatencyMS, threshold)}

	case "tool_used":
		if resp.ToolCalls != nil && resp.ToolCalls[c.Arg] > 0 {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: fmt.Sprintf("tool %q was not used", c.Arg)}

	case "max_tool_calls":
		threshold, _ := strconv.Atoi(c.Arg)
		total := 0
		for _, count := range resp.ToolCalls {
			total += count
		}
		if total <= threshold {
			return Result{Criterion: c.Raw, Passed: true}
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: fmt.Sprintf("total tool calls %d exceeds max %d", total, threshold)}

	default: // "unknown" — always passes
		return Result{Criterion: c.Raw, Passed: true}
	}
}

func evaluateRun(c Criterion, ectx EvalContext) Result {
	if ectx.WorkDir == "" {
		return Result{Criterion: c.Raw, Passed: false, Detail: "run: worktree not available"}
	}

	timeout := 60 * time.Second
	if c.Timeout > 0 {
		timeout = time.Duration(c.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", c.Arg)
	cmd.Dir = ectx.WorkDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := TailLines(string(out), 50)
		if ctx.Err() == context.DeadlineExceeded {
			detail += fmt.Sprintf("\n[timed out after %ds]", int(timeout.Seconds()))
		}
		return Result{Criterion: c.Raw, Passed: false, Detail: detail}
	}
	return Result{Criterion: c.Raw, Passed: true}
}

// TailLines returns the last n lines of s. If s has more than n lines,
// the result is prefixed with a truncation notice.
func TailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	// Trim trailing empty line from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	truncated := len(lines) - n
	tail := lines[len(lines)-n:]
	return fmt.Sprintf("[...truncated %d lines...]\n%s", truncated, strings.Join(tail, "\n"))
}

// PassCount returns how many results passed.
func PassCount(results []Result) int {
	n := 0
	for _, r := range results {
		if r.Passed {
			n++
		}
	}
	return n
}
