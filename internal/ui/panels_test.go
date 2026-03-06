package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/suarezc/errata/internal/models"
)

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "500ms"},
		{0, "0ms"},
		{1 * time.Second, "1s"},
		{45 * time.Second, "45s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m 30s"},
		{78 * time.Second, "1m 18s"},
		{3600 * time.Second, "60m"},
	}
	for _, tt := range tests {
		got := formatElapsed(tt.d)
		if got != tt.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatDoneSummary_WithAllFields(t *testing.T) {
	p := &panelState{
		toolUseCount: 5,
		inputTokens:  10000,
		outputTokens: 2400,
		latencyMS:    78000,
		costUSD:      0.0083,
	}
	got := formatDoneSummary(p)
	if !strings.Contains(got, "5 tool uses") {
		t.Errorf("expected tool use count, got: %s", got)
	}
	if !strings.Contains(got, "12.4k tokens") {
		t.Errorf("expected token count, got: %s", got)
	}
	if !strings.Contains(got, "1m 18s") {
		t.Errorf("expected latency, got: %s", got)
	}
	if !strings.Contains(got, "$0.0083") {
		t.Errorf("expected cost, got: %s", got)
	}
	if !strings.HasPrefix(got, "Done (") {
		t.Errorf("expected 'Done (' prefix, got: %s", got)
	}
}

func TestFormatDoneSummary_WithReasoningTokens(t *testing.T) {
	p := &panelState{
		toolUseCount:    5,
		inputTokens:     529000,
		outputTokens:    15000,
		reasoningTokens: 14500,
		latencyMS:       5000,
		costUSD:         1.18,
	}
	got := formatDoneSummary(p)
	if !strings.Contains(got, "14.5k reasoning") {
		t.Errorf("expected reasoning token info, got: %s", got)
	}
	if !strings.Contains(got, "544.0k tokens") {
		t.Errorf("expected total token count, got: %s", got)
	}
}

func TestFormatDoneSummary_NoReasoningTokensWhenZero(t *testing.T) {
	p := &panelState{
		inputTokens:  1000,
		outputTokens: 500,
		latencyMS:    500,
	}
	got := formatDoneSummary(p)
	if strings.Contains(got, "reasoning") {
		t.Errorf("should not show reasoning when zero, got: %s", got)
	}
}

func TestFormatDoneSummary_EmptyPanel(t *testing.T) {
	p := &panelState{}
	got := formatDoneSummary(p)
	if got != "Done" {
		t.Errorf("expected 'Done' for empty panel, got: %s", got)
	}
}

func TestFormatDoneSummary_SingleToolUse(t *testing.T) {
	p := &panelState{toolUseCount: 1, latencyMS: 500}
	got := formatDoneSummary(p)
	if !strings.Contains(got, "1 tool use") {
		t.Errorf("expected singular 'use', got: %s", got)
	}
	if strings.Contains(got, "uses") {
		t.Errorf("should not contain plural 'uses', got: %s", got)
	}
}

func TestAddEvent_CountsToolUses(t *testing.T) {
	p := newPanelState("test-model", 0)
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "foo.go"})
	p.addEvent(models.AgentEvent{Type: models.EventText, Data: "some text"})
	p.addEvent(models.AgentEvent{Type: models.EventWriting, Data: "bar.go"})
	p.addEvent(models.AgentEvent{Type: models.EventBash, Data: "go test"})
	p.addEvent(models.AgentEvent{Type: models.EventError, Data: "something failed"})
	p.addEvent(models.AgentEvent{Type: models.EventText, Data: "more text"})

	if p.toolUseCount != 3 {
		t.Errorf("expected toolUseCount=3 (reading+writing+bash), got %d", p.toolUseCount)
	}
	if len(p.events) != 6 {
		t.Errorf("expected 6 events stored, got %d", len(p.events))
	}
}

func TestAddEvent_EventCapping(t *testing.T) {
	p := newPanelState("test-model", 0)
	for range 25 {
		p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "file"})
	}
	if len(p.events) != maxPanelEvents {
		t.Errorf("expected %d events after capping, got %d", maxPanelEvents, len(p.events))
	}
	if p.toolUseCount != 25 {
		t.Errorf("tool use count should reflect all events, not capped: got %d", p.toolUseCount)
	}
}

func TestRenderInlinePanel_RunningShowsModelAndEvents(t *testing.T) {
	p := newPanelState("claude-sonnet-4-6", 0)
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "main.go"})
	p.addEvent(models.AgentEvent{Type: models.EventWriting, Data: "utils.go"})
	out := renderInlinePanel(p, 80)
	if !strings.Contains(out, "claude-sonnet-4-6") {
		t.Errorf("expected model ID in output, got:\n%s", out)
	}
	if !strings.Contains(out, "reading") {
		t.Errorf("expected reading event, got:\n%s", out)
	}
	if !strings.Contains(out, "writing") {
		t.Errorf("expected writing event, got:\n%s", out)
	}
	if !strings.Contains(out, "2 tool uses") {
		t.Errorf("expected tool use count, got:\n%s", out)
	}
	if !strings.Contains(out, "⏺") {
		t.Errorf("expected status dot, got:\n%s", out)
	}
}

func TestRenderInlinePanel_RunningShowsLastEvents(t *testing.T) {
	p := newPanelState("model", 0)
	// Add more than maxInlineEvents
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "first.go"})
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "second.go"})
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "third.go"})
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "fourth.go"})
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "fifth.go"})
	out := renderInlinePanel(p, 80)
	// first.go should be trimmed from display (only last 4 shown)
	if strings.Contains(out, "first.go") {
		t.Errorf("first event should be trimmed from running view, got:\n%s", out)
	}
	if !strings.Contains(out, "fifth.go") {
		t.Errorf("last event should be visible, got:\n%s", out)
	}
}

func TestRenderInlinePanel_DoneShowsSummary(t *testing.T) {
	p := newPanelState("gpt-4o", 1)
	p.done = true
	p.toolUseCount = 3
	p.inputTokens = 8400
	p.latencyMS = 1234
	p.costUSD = 0.0042
	out := renderInlinePanel(p, 80)
	if !strings.Contains(out, "Done") {
		t.Errorf("expected 'Done' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "3 tool uses") {
		t.Errorf("expected tool count in summary, got:\n%s", out)
	}
}

func TestRenderInlinePanel_DoneShowsEvents(t *testing.T) {
	p := newPanelState("gpt-4o", 1)
	p.addEvent(models.AgentEvent{Type: models.EventReading, Data: "foo.go"})
	p.addEvent(models.AgentEvent{Type: models.EventWriting, Data: "bar.go"})
	p.done = true
	p.toolUseCount = 2
	p.latencyMS = 500
	out := renderInlinePanel(p, 80)
	if !strings.Contains(out, "foo.go") {
		t.Errorf("done view should show events, got:\n%s", out)
	}
	if !strings.Contains(out, "bar.go") {
		t.Errorf("done view should show all events, got:\n%s", out)
	}
	if !strings.Contains(out, "Done") {
		t.Errorf("done view should still show summary, got:\n%s", out)
	}
}

func TestRenderInlinePanel_ErrorShowsMessage(t *testing.T) {
	p := newPanelState("err-model", 0)
	p.done = true
	p.errMsg = "context limit reached"
	out := renderInlinePanel(p, 80)
	if !strings.Contains(out, "Error") {
		t.Errorf("expected 'Error' prefix, got:\n%s", out)
	}
	if !strings.Contains(out, "context limit") {
		t.Errorf("expected error message, got:\n%s", out)
	}
}

func TestRenderInlinePanel_LongErrorTruncated(t *testing.T) {
	p := newPanelState("err-model", 0)
	p.done = true
	p.errMsg = strings.Repeat("x", 100)
	out := renderInlinePanel(p, 80)
	if !strings.Contains(out, "…") {
		t.Errorf("long error should be truncated with ellipsis, got:\n%s", out)
	}
}

func TestRenderInlinePanels_StacksVertically(t *testing.T) {
	p1 := newPanelState("model-a", 0)
	p2 := newPanelState("model-b", 1)
	p1.done = true
	p2.done = true
	out := renderInlinePanels([]*panelState{p1, p2}, 80)
	idxA := strings.Index(out, "model-a")
	idxB := strings.Index(out, "model-b")
	if idxA < 0 || idxB < 0 {
		t.Fatalf("expected both model IDs in output, got:\n%s", out)
	}
	if idxA >= idxB {
		t.Errorf("model-a should appear before model-b (vertical stacking), got:\n%s", out)
	}
}

// ─── fmtTokens ──────────────────────────────────────────────────────────────

func TestFmtTokens(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0k"},
		{1234, "1.2k"},
		{12400, "12.4k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{1234567, "1.2M"},
	}
	for _, tt := range tests {
		got := fmtTokens(tt.n)
		if got != tt.want {
			t.Errorf("fmtTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ─── truncateLine ───────────────────────────────────────────────────────────

func TestTruncateLine(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"empty", "", 10, ""},
		{"short", "hello", 10, "hello"},
		{"newline_strips", "hello\nworld", 70, "hello"},
		{"newline_at_start", "\nrest", 70, ""},
		{"exceeds_rune_limit", "abcdefghij", 5, "abcde…"},
		{"newline_then_rune_limit", "ab\ncdef", 1, "a…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateLine(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncateLine(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

// ─── renderInlineEvent ──────────────────────────────────────────────────────

func TestRenderInlineEvent_EventTypes(t *testing.T) {
	types := []struct {
		typ  models.EventType
		data string
	}{
		{models.EventReading, "main.go"},
		{models.EventWriting, "out.go"},
		{models.EventBash, "go test"},
		{models.EventError, "something failed"},
		{models.EventText, "some output"},
		{"unknown", "fallback data"},
	}
	for _, tt := range types {
		t.Run(string(tt.typ), func(t *testing.T) {
			e := models.AgentEvent{Type: tt.typ, Data: tt.data}
			result := renderInlineEvent(e, 80)
			if !strings.Contains(result, tt.data) {
				t.Errorf("renderInlineEvent(%s) should contain %q, got %q", tt.typ, tt.data, result)
			}
		})
	}
}

func TestRenderInlineEvent_LongDataTruncated(t *testing.T) {
	longData := strings.Repeat("x", 100)
	e := models.AgentEvent{Type: models.EventReading, Data: longData}
	result := renderInlineEvent(e, 80)
	if strings.Contains(result, longData) {
		t.Error("long data should be truncated in renderInlineEvent")
	}
	if !strings.Contains(result, "…") {
		t.Error("truncated data should contain ellipsis")
	}
}

// ─── renderInlinePanels (empty) ─────────────────────────────────────────────

func TestRenderInlinePanels_Empty(t *testing.T) {
	out := renderInlinePanels(nil, 80)
	if out != "" {
		t.Errorf("expected empty string for nil panels, got: %q", out)
	}
}

func TestNewPanelState_SetsStartedAt(t *testing.T) {
	before := time.Now()
	p := newPanelState("test", 0)
	after := time.Now()
	if p.startedAt.Before(before) || p.startedAt.After(after) {
		t.Errorf("startedAt should be between before and after, got: %v", p.startedAt)
	}
}
