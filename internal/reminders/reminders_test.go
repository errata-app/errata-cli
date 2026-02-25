package reminders_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/reminders"
)

// ─── ParseTrigger ───────────────────────────────────────────────────────────

func TestParseTrigger_ContextUsage(t *testing.T) {
	tr, err := reminders.ParseTrigger("context_usage > 0.75")
	require.NoError(t, err)
	assert.Equal(t, "context_usage", tr.Kind)
	assert.Equal(t, ">", tr.Operator)
	assert.Equal(t, "0.75", tr.Value)
}

func TestParseTrigger_TurnCount(t *testing.T) {
	tr, err := reminders.ParseTrigger("turn_count >= 10")
	require.NoError(t, err)
	assert.Equal(t, "turn_count", tr.Kind)
	assert.Equal(t, ">=", tr.Operator)
	assert.Equal(t, "10", tr.Value)
}

func TestParseTrigger_LastResponseTokens(t *testing.T) {
	tr, err := reminders.ParseTrigger("last_response_tokens > 4000")
	require.NoError(t, err)
	assert.Equal(t, "last_response_tokens", tr.Kind)
	assert.Equal(t, ">", tr.Operator)
	assert.Equal(t, "4000", tr.Value)
}

func TestParseTrigger_ToolUsed(t *testing.T) {
	tr, err := reminders.ParseTrigger("tool_used == bash")
	require.NoError(t, err)
	assert.Equal(t, "tool_used", tr.Kind)
	assert.Equal(t, "==", tr.Operator)
	assert.Equal(t, "bash", tr.Value)
}

func TestParseTrigger_LastToolCallFailed_Shorthand(t *testing.T) {
	tr, err := reminders.ParseTrigger("last_tool_call_failed")
	require.NoError(t, err)
	assert.Equal(t, "last_tool_call_failed", tr.Kind)
	assert.Equal(t, "==", tr.Operator)
	assert.Equal(t, "true", tr.Value)
}

func TestParseTrigger_Manual(t *testing.T) {
	tr, err := reminders.ParseTrigger("manual")
	require.NoError(t, err)
	assert.Equal(t, "manual", tr.Kind)
}

func TestParseTrigger_Empty(t *testing.T) {
	_, err := reminders.ParseTrigger("")
	assert.Error(t, err)
}

func TestParseTrigger_UnknownKind(t *testing.T) {
	_, err := reminders.ParseTrigger("unknown_thing > 5")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown trigger kind")
}

func TestParseTrigger_UnknownOperator(t *testing.T) {
	_, err := reminders.ParseTrigger("turn_count != 5")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown operator")
}

func TestParseTrigger_TooFewParts(t *testing.T) {
	_, err := reminders.ParseTrigger("turn_count")
	assert.Error(t, err)
}

// ─── Evaluate: each trigger type ────────────────────────────────────────────

func TestEvaluate_ContextUsage(t *testing.T) {
	r := reminders.Reminder{
		Name:    "ctx_warn",
		Trigger: reminders.Trigger{Kind: "context_usage", Operator: ">", Value: "0.75"},
		Content: "Context is getting full.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	// Below threshold → no fire.
	fired := s.Evaluate(reminders.EvalContext{ContextUsage: 0.5})
	assert.Empty(t, fired)

	// Above threshold → fire.
	fired = s.Evaluate(reminders.EvalContext{ContextUsage: 0.8})
	assert.Len(t, fired, 1)
	assert.Equal(t, "ctx_warn", fired[0].Name)
}

func TestEvaluate_TurnCount(t *testing.T) {
	r := reminders.Reminder{
		Name:    "many_turns",
		Trigger: reminders.Trigger{Kind: "turn_count", Operator: ">=", Value: "10"},
		Content: "Many turns elapsed.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	fired := s.Evaluate(reminders.EvalContext{TurnCount: 5})
	assert.Empty(t, fired)

	fired = s.Evaluate(reminders.EvalContext{TurnCount: 10})
	assert.Len(t, fired, 1)
}

func TestEvaluate_LastResponseTokens(t *testing.T) {
	r := reminders.Reminder{
		Name:    "long_response",
		Trigger: reminders.Trigger{Kind: "last_response_tokens", Operator: ">", Value: "4000"},
		Content: "Be more concise.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	fired := s.Evaluate(reminders.EvalContext{LastResponseTokens: 2000})
	assert.Empty(t, fired)

	fired = s.Evaluate(reminders.EvalContext{LastResponseTokens: 5000})
	assert.Len(t, fired, 1)
}

func TestEvaluate_LastToolCallFailed(t *testing.T) {
	r := reminders.Reminder{
		Name:    "tool_error",
		Trigger: reminders.Trigger{Kind: "last_tool_call_failed", Operator: "==", Value: "true"},
		Content: "A tool failed. Check your approach.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	fired := s.Evaluate(reminders.EvalContext{LastToolFailed: false})
	assert.Empty(t, fired)

	fired = s.Evaluate(reminders.EvalContext{LastToolFailed: true})
	assert.Len(t, fired, 1)
}

func TestEvaluate_ToolUsed(t *testing.T) {
	r := reminders.Reminder{
		Name:    "bash_used",
		Trigger: reminders.Trigger{Kind: "tool_used", Operator: "==", Value: "bash"},
		Content: "Remember to check exit codes.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	fired := s.Evaluate(reminders.EvalContext{LastToolName: "read_file"})
	assert.Empty(t, fired)

	fired = s.Evaluate(reminders.EvalContext{LastToolName: "bash"})
	assert.Len(t, fired, 1)
}

// ─── Fire-once semantics ────────────────────────────────────────────────────

func TestEvaluate_FireOnce_NotEveryTurn(t *testing.T) {
	r := reminders.Reminder{
		Name:    "ctx_warn",
		Trigger: reminders.Trigger{Kind: "context_usage", Operator: ">", Value: "0.75"},
		Content: "Context is getting full.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	// First evaluation above threshold → fires.
	fired := s.Evaluate(reminders.EvalContext{ContextUsage: 0.8})
	assert.Len(t, fired, 1)

	// Second evaluation still above threshold → does NOT fire again.
	fired = s.Evaluate(reminders.EvalContext{ContextUsage: 0.85})
	assert.Empty(t, fired)
}

func TestEvaluate_ReArming(t *testing.T) {
	r := reminders.Reminder{
		Name:    "ctx_warn",
		Trigger: reminders.Trigger{Kind: "context_usage", Operator: ">", Value: "0.75"},
		Content: "Context is getting full.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	// false → true: fires.
	fired := s.Evaluate(reminders.EvalContext{ContextUsage: 0.8})
	assert.Len(t, fired, 1)

	// true → true: does not fire.
	fired = s.Evaluate(reminders.EvalContext{ContextUsage: 0.9})
	assert.Empty(t, fired)

	// true → false: re-arms (does not fire).
	fired = s.Evaluate(reminders.EvalContext{ContextUsage: 0.5})
	assert.Empty(t, fired)

	// false → true: fires again.
	fired = s.Evaluate(reminders.EvalContext{ContextUsage: 0.8})
	assert.Len(t, fired, 1)
}

// ─── Manual reminders ───────────────────────────────────────────────────────

func TestEvaluate_ManualNeverAutoFires(t *testing.T) {
	r := reminders.Reminder{
		Name:    "custom",
		Trigger: reminders.Trigger{Kind: "manual"},
		Content: "Custom injection.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	// Manual reminders never fire from Evaluate.
	fired := s.Evaluate(reminders.EvalContext{ContextUsage: 1.0, TurnCount: 999})
	assert.Empty(t, fired)
}

func TestFireManual_Found(t *testing.T) {
	r := reminders.Reminder{
		Name:    "custom",
		Trigger: reminders.Trigger{Kind: "manual"},
		Content: "Manual content.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	got, ok := s.FireManual("custom")
	assert.True(t, ok)
	assert.Equal(t, "Manual content.", got.Content)
}

func TestFireManual_NotFound(t *testing.T) {
	s := reminders.NewState(nil)
	_, ok := s.FireManual("nonexistent")
	assert.False(t, ok)
}

// ─── Multiple reminders ─────────────────────────────────────────────────────

func TestEvaluate_MultipleReminders(t *testing.T) {
	rs := []reminders.Reminder{
		{
			Name:    "ctx_warn",
			Trigger: reminders.Trigger{Kind: "context_usage", Operator: ">", Value: "0.75"},
			Content: "Context warning.",
		},
		{
			Name:    "turn_warn",
			Trigger: reminders.Trigger{Kind: "turn_count", Operator: ">=", Value: "5"},
			Content: "Turn warning.",
		},
	}
	s := reminders.NewState(rs)

	// Both fire when both conditions met.
	fired := s.Evaluate(reminders.EvalContext{ContextUsage: 0.8, TurnCount: 5})
	assert.Len(t, fired, 2)
}

// ─── ReminderNames ──────────────────────────────────────────────────────────

func TestReminderNames(t *testing.T) {
	rs := []reminders.Reminder{
		{Name: "a", Trigger: reminders.Trigger{Kind: "manual"}, Content: "A"},
		{Name: "b", Trigger: reminders.Trigger{Kind: "manual"}, Content: "B"},
	}
	s := reminders.NewState(rs)
	assert.Equal(t, []string{"a", "b"}, s.ReminderNames())
}

// ─── Context delivery ───────────────────────────────────────────────────────

func TestStateFromContext_Present(t *testing.T) {
	s := reminders.NewState(nil)
	ctx := reminders.WithState(context.Background(), s)
	got := reminders.StateFromContext(ctx)
	assert.Same(t, s, got)
}

func TestStateFromContext_Absent(t *testing.T) {
	got := reminders.StateFromContext(context.Background())
	assert.Nil(t, got)
}

// ─── Edge cases ─────────────────────────────────────────────────────────────

func TestEvaluate_InvalidThreshold_NoFire(t *testing.T) {
	r := reminders.Reminder{
		Name:    "bad",
		Trigger: reminders.Trigger{Kind: "context_usage", Operator: ">", Value: "not_a_number"},
		Content: "Should not fire.",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	fired := s.Evaluate(reminders.EvalContext{ContextUsage: 0.99})
	assert.Empty(t, fired, "invalid threshold should not fire")
}

func TestEvaluate_EmptyReminders(t *testing.T) {
	s := reminders.NewState(nil)
	fired := s.Evaluate(reminders.EvalContext{ContextUsage: 0.99})
	assert.Empty(t, fired)
}

// ─── Operator coverage (table-driven) ────────────────────────────────────────

func TestEvaluate_AllOperators_ContextUsage(t *testing.T) {
	tests := []struct {
		name  string
		op    string
		value string
		usage float64
		want  bool
	}{
		{"gt_true", ">", "0.5", 0.6, true},
		{"gt_false", ">", "0.5", 0.5, false},
		{"gte_equal", ">=", "0.5", 0.5, true},
		{"gte_below", ">=", "0.5", 0.4, false},
		{"eq_true", "==", "0.5", 0.5, true},
		{"eq_false", "==", "0.5", 0.6, false},
		{"lt_true", "<", "0.5", 0.4, true},
		{"lt_false", "<", "0.5", 0.5, false},
		{"lte_equal", "<=", "0.5", 0.5, true},
		{"lte_above", "<=", "0.5", 0.6, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reminders.Reminder{
				Name:    "test",
				Trigger: reminders.Trigger{Kind: "context_usage", Operator: tt.op, Value: tt.value},
				Content: "test",
			}
			s := reminders.NewState([]reminders.Reminder{r})
			fired := s.Evaluate(reminders.EvalContext{ContextUsage: tt.usage})
			if tt.want {
				assert.Len(t, fired, 1)
			} else {
				assert.Empty(t, fired)
			}
		})
	}
}

func TestEvaluate_AllOperators_TurnCount(t *testing.T) {
	tests := []struct {
		name  string
		op    string
		value string
		turns int
		want  bool
	}{
		{"gt_true", ">", "5", 6, true},
		{"gt_false", ">", "5", 5, false},
		{"eq_true", "==", "5", 5, true},
		{"lt_true", "<", "5", 4, true},
		{"lte_equal", "<=", "5", 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reminders.Reminder{
				Name:    "test",
				Trigger: reminders.Trigger{Kind: "turn_count", Operator: tt.op, Value: tt.value},
				Content: "test",
			}
			s := reminders.NewState([]reminders.Reminder{r})
			fired := s.Evaluate(reminders.EvalContext{TurnCount: tt.turns})
			if tt.want {
				assert.Len(t, fired, 1)
			} else {
				assert.Empty(t, fired)
			}
		})
	}
}

// ─── tool_used with unsupported operator ─────────────────────────────────────

func TestEvaluate_ToolUsed_NonEqualOperator(t *testing.T) {
	r := reminders.Reminder{
		Name:    "test",
		Trigger: reminders.Trigger{Kind: "tool_used", Operator: ">", Value: "bash"},
		Content: "test",
	}
	s := reminders.NewState([]reminders.Reminder{r})
	fired := s.Evaluate(reminders.EvalContext{LastToolName: "bash"})
	assert.Empty(t, fired, "tool_used only supports ==; other operators should not fire")
}

// ─── last_tool_call_failed with explicit false ───────────────────────────────

func TestEvaluate_LastToolCallFailed_ExplicitFalse(t *testing.T) {
	r := reminders.Reminder{
		Name:    "test",
		Trigger: reminders.Trigger{Kind: "last_tool_call_failed", Operator: "==", Value: "false"},
		Content: "test",
	}
	s := reminders.NewState([]reminders.Reminder{r})

	// When tool has NOT failed, "== false" should fire.
	fired := s.Evaluate(reminders.EvalContext{LastToolFailed: false})
	assert.Len(t, fired, 1)
}

// ─── Invalid thresholds for all numeric types ────────────────────────────────

func TestEvaluate_InvalidThreshold_TurnCount(t *testing.T) {
	r := reminders.Reminder{
		Name:    "bad",
		Trigger: reminders.Trigger{Kind: "turn_count", Operator: ">", Value: "abc"},
		Content: "test",
	}
	s := reminders.NewState([]reminders.Reminder{r})
	fired := s.Evaluate(reminders.EvalContext{TurnCount: 999})
	assert.Empty(t, fired)
}

func TestEvaluate_InvalidThreshold_LastResponseTokens(t *testing.T) {
	r := reminders.Reminder{
		Name:    "bad",
		Trigger: reminders.Trigger{Kind: "last_response_tokens", Operator: ">", Value: "xyz"},
		Content: "test",
	}
	s := reminders.NewState([]reminders.Reminder{r})
	fired := s.Evaluate(reminders.EvalContext{LastResponseTokens: 999})
	assert.Empty(t, fired)
}

// ─── ParseTrigger: multi-word values ─────────────────────────────────────────

func TestParseTrigger_MultiWordValue(t *testing.T) {
	tr, err := reminders.ParseTrigger("tool_used == bash command")
	require.NoError(t, err)
	assert.Equal(t, "bash command", tr.Value)
}

// ─── FireManual on non-manual trigger type ───────────────────────────────────

func TestFireManual_WorksForAnyTriggerType(t *testing.T) {
	r := reminders.Reminder{
		Name:    "auto_reminder",
		Trigger: reminders.Trigger{Kind: "context_usage", Operator: ">", Value: "0.5"},
		Content: "content",
	}
	s := reminders.NewState([]reminders.Reminder{r})
	got, ok := s.FireManual("auto_reminder")
	assert.True(t, ok, "FireManual should find any reminder by name")
	assert.Equal(t, "content", got.Content)
}

// ─── Unknown trigger kind in evalTrigger ─────────────────────────────────────

func TestEvaluate_AllOperators_LastResponseTokens(t *testing.T) {
	tests := []struct {
		name   string
		op     string
		value  string
		tokens int64
		want   bool
	}{
		{"gt_true", ">", "100", 200, true},
		{"gt_false", ">", "100", 100, false},
		{"gte_equal", ">=", "100", 100, true},
		{"gte_below", ">=", "100", 50, false},
		{"eq_true", "==", "100", 100, true},
		{"eq_false", "==", "100", 200, false},
		{"lt_true", "<", "100", 50, true},
		{"lt_false", "<", "100", 100, false},
		{"lte_equal", "<=", "100", 100, true},
		{"lte_above", "<=", "100", 200, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reminders.Reminder{
				Name:    "test",
				Trigger: reminders.Trigger{Kind: "last_response_tokens", Operator: tt.op, Value: tt.value},
				Content: "test",
			}
			s := reminders.NewState([]reminders.Reminder{r})
			fired := s.Evaluate(reminders.EvalContext{LastResponseTokens: tt.tokens})
			if tt.want {
				assert.Len(t, fired, 1)
			} else {
				assert.Empty(t, fired)
			}
		})
	}
}

func TestEvaluate_UnknownTriggerKind_NoFire(t *testing.T) {
	// Bypass ParseTrigger validation by constructing directly.
	r := reminders.Reminder{
		Name:    "test",
		Trigger: reminders.Trigger{Kind: "unknown_kind", Operator: "==", Value: "1"},
		Content: "test",
	}
	s := reminders.NewState([]reminders.Reminder{r})
	fired := s.Evaluate(reminders.EvalContext{})
	assert.Empty(t, fired)
}
