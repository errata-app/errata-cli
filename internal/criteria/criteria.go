// Package criteria parses and evaluates success criteria from Errata recipe files.
//
// Criteria are defined as bullet points in the recipe's ## Success Criteria section
// and evaluated against each model's ModelResponse after a headless task run.
package criteria

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/suarezc/errata/internal/models"
)

// Criterion is a single parsed success criterion.
type Criterion struct {
	Raw  string // original string from the recipe
	Type string // "no_errors" | "has_writes" | "contains" | "files_written" | "unknown"
	Arg  string // comparison value when applicable
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

	fmt.Fprintf(os.Stderr, "criteria: unknown criterion %q, will always pass\n", s)
	return Criterion{Raw: s, Type: "unknown"}
}

// Evaluate runs all criteria against a single ModelResponse and returns the results.
func Evaluate(criteria []Criterion, resp models.ModelResponse) []Result {
	results := make([]Result, len(criteria))
	for i, c := range criteria {
		results[i] = evaluateSingle(c, resp)
	}
	return results
}

func evaluateSingle(c Criterion, resp models.ModelResponse) Result {
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

	default: // "unknown" — always passes
		return Result{Criterion: c.Raw, Passed: true}
	}
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
