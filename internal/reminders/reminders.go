// Package reminders provides conditional mid-conversation prompt injection.
//
// Reminders are user-authored prompts with trigger conditions. When a trigger
// evaluates to true, the reminder content is injected into the model context
// (either as a system message or a user message with [SYSTEM REMINDER] prefix,
// depending on model capabilities).
//
// Each reminder fires at most once per trigger event: it fires when the
// condition transitions from false → true, then re-arms when the condition
// becomes false again.
//
// All evaluation is deterministic — no LLM calls.
package reminders

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ─── Trigger ────────────────────────────────────────────────────────────────

// Trigger describes a condition that causes a reminder to fire.
type Trigger struct {
	Kind     string // "context_usage", "turn_count", "last_response_tokens", "last_tool_call_failed", "tool_used", "manual"
	Operator string // ">", ">=", "==", "<", "<=" (unused for bool/string triggers)
	Value    string // numeric threshold, tool name, or "true"/"false"
}

// ParseTrigger parses a trigger expression string.
//
// Supported formats:
//
//	"context_usage > 0.75"
//	"turn_count >= 10"
//	"last_response_tokens > 4000"
//	"last_tool_call_failed"           → implied "== true"
//	"tool_used == bash"
//	"manual"                          → only fires via explicit /remind
func ParseTrigger(s string) (Trigger, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Trigger{}, fmt.Errorf("empty trigger expression")
	}

	// "manual" — special case, no condition.
	if s == "manual" {
		return Trigger{Kind: "manual"}, nil
	}

	// "last_tool_call_failed" — implied bool check.
	if s == "last_tool_call_failed" {
		return Trigger{Kind: "last_tool_call_failed", Operator: "==", Value: "true"}, nil
	}

	// Parse "kind operator value" form.
	parts := strings.Fields(s)
	if len(parts) < 3 {
		return Trigger{}, fmt.Errorf("invalid trigger expression: %q (expected 'kind operator value')", s)
	}
	kind := parts[0]
	op := parts[1]
	value := strings.Join(parts[2:], " ") // allow multi-word values

	switch kind {
	case "context_usage", "turn_count", "last_response_tokens", "last_tool_call_failed", "tool_used":
		// valid
	default:
		return Trigger{}, fmt.Errorf("unknown trigger kind: %q", kind)
	}

	switch op {
	case ">", ">=", "==", "<", "<=":
		// valid
	default:
		return Trigger{}, fmt.Errorf("unknown operator: %q", op)
	}

	return Trigger{Kind: kind, Operator: op, Value: value}, nil
}

// ─── Reminder ───────────────────────────────────────────────────────────────

// Reminder is a parsed, ready-to-evaluate conditional injection.
type Reminder struct {
	Name    string
	Trigger Trigger
	Content string
}

// ─── EvalContext ─────────────────────────────────────────────────────────────

// EvalContext carries runtime metrics for trigger evaluation.
type EvalContext struct {
	ContextUsage       float64 // 0.0–1.0
	LastResponseTokens int64
	LastToolFailed     bool
	TurnCount          int
	LastToolName       string
}

// ─── State ──────────────────────────────────────────────────────────────────

// State tracks per-reminder armed/fired status for fire-once semantics.
type State struct {
	reminders []Reminder
	// lastEval[name] records whether the condition was true on the previous check.
	// A reminder fires on false→true transition only.
	lastEval map[string]bool
}

// NewState creates a State that tracks the given reminders.
func NewState(reminders []Reminder) *State {
	return &State{
		reminders: reminders,
		lastEval:  make(map[string]bool, len(reminders)),
	}
}

// Evaluate checks all reminders against the current eval context and returns
// those that should fire (false→true transition). Manual reminders never fire
// from Evaluate — use FireManual instead.
func (s *State) Evaluate(ec EvalContext) []Reminder {
	var fired []Reminder
	for _, r := range s.reminders {
		if r.Trigger.Kind == "manual" {
			continue
		}
		current := evalTrigger(r.Trigger, ec)
		prev := s.lastEval[r.Name]
		s.lastEval[r.Name] = current

		// Fire on false→true transition.
		if current && !prev {
			fired = append(fired, r)
		}
	}
	return fired
}

// FireManual returns the reminder with the given name (for /remind command),
// regardless of trigger state. Returns (Reminder{}, false) if not found.
func (s *State) FireManual(name string) (Reminder, bool) {
	for _, r := range s.reminders {
		if r.Name == name {
			return r, true
		}
	}
	return Reminder{}, false
}

// ReminderNames returns the names of all tracked reminders.
func (s *State) ReminderNames() []string {
	names := make([]string, len(s.reminders))
	for i, r := range s.reminders {
		names[i] = r.Name
	}
	return names
}

// evalTrigger evaluates a single trigger against the eval context.
func evalTrigger(t Trigger, ec EvalContext) bool {
	switch t.Kind {
	case "context_usage":
		threshold, err := strconv.ParseFloat(t.Value, 64)
		if err != nil {
			return false
		}
		return compareFloat(ec.ContextUsage, t.Operator, threshold)

	case "turn_count":
		threshold, err := strconv.Atoi(t.Value)
		if err != nil {
			return false
		}
		return compareInt(ec.TurnCount, t.Operator, threshold)

	case "last_response_tokens":
		threshold, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			return false
		}
		return compareInt64(ec.LastResponseTokens, t.Operator, threshold)

	case "last_tool_call_failed":
		want := strings.ToLower(t.Value) == "true"
		return ec.LastToolFailed == want

	case "tool_used":
		if t.Operator == "==" {
			return ec.LastToolName == t.Value
		}
		return false

	case "manual":
		return false // never auto-fires
	}
	return false
}

// ─── Comparison helpers ─────────────────────────────────────────────────────

func compareFloat(a float64, op string, b float64) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "==":
		return a == b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

func compareInt(a int, op string, b int) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "==":
		return a == b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

func compareInt64(a int64, op string, b int64) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "==":
		return a == b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

// ─── Context-based delivery ─────────────────────────────────────────────────

type stateKey struct{}

// WithState returns a context carrying reminder state.
func WithState(ctx context.Context, s *State) context.Context {
	return context.WithValue(ctx, stateKey{}, s)
}

// StateFromContext retrieves reminder state from ctx. Returns nil when absent.
func StateFromContext(ctx context.Context) *State {
	s, _ := ctx.Value(stateKey{}).(*State)
	return s
}
