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

func TestFormatDoneSummary_WithCachedTokens(t *testing.T) {
	p := &panelState{
		inputTokens:     8000,
		outputTokens:    2000,
		cacheReadTokens: 3000,
		latencyMS:       1000,
	}
	got := formatDoneSummary(p)
	if !strings.Contains(got, "10.0k tokens") {
		t.Errorf("expected total token count, got: %s", got)
	}
	if !strings.Contains(got, "3.0k cached") {
		t.Errorf("expected cached token indicator, got: %s", got)
	}
}

func TestAddEvent_CountsToolUses(t *testing.T) {
	p := newPanelState("test-model", 0)
	p.addEvent(models.AgentEvent{Type: "reading", Data: "foo.go"})
	p.addEvent(models.AgentEvent{Type: "text", Data: "some text"})
	p.addEvent(models.AgentEvent{Type: "writing", Data: "bar.go"})
	p.addEvent(models.AgentEvent{Type: "bash", Data: "go test"})
	p.addEvent(models.AgentEvent{Type: "error", Data: "something failed"})
	p.addEvent(models.AgentEvent{Type: "text", Data: "more text"})

	if p.toolUseCount != 3 {
		t.Errorf("expected toolUseCount=3 (reading+writing+bash), got %d", p.toolUseCount)
	}
	if len(p.events) != 6 {
		t.Errorf("expected 6 events stored, got %d", len(p.events))
	}
}

func TestAddEvent_EventCapping(t *testing.T) {
	p := newPanelState("test-model", 0)
	for i := 0; i < 25; i++ {
		p.addEvent(models.AgentEvent{Type: "reading", Data: "file"})
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
	p.addEvent(models.AgentEvent{Type: "reading", Data: "main.go"})
	p.addEvent(models.AgentEvent{Type: "writing", Data: "utils.go"})
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
	p.addEvent(models.AgentEvent{Type: "reading", Data: "first.go"})
	p.addEvent(models.AgentEvent{Type: "reading", Data: "second.go"})
	p.addEvent(models.AgentEvent{Type: "reading", Data: "third.go"})
	p.addEvent(models.AgentEvent{Type: "reading", Data: "fourth.go"})
	p.addEvent(models.AgentEvent{Type: "reading", Data: "fifth.go"})
	out := renderInlinePanel(p, 80)
	// first.go should be trimmed from display (only last 4 shown)
	if strings.Contains(out, "first.go") {
		t.Errorf("first event should be trimmed from running view, got:\n%s", out)
	}
	if !strings.Contains(out, "fifth.go") {
		t.Errorf("last event should be visible, got:\n%s", out)
	}
}

func TestRenderInlinePanel_DoneCollapsedShowsSummary(t *testing.T) {
	p := newPanelState("gpt-4o", 1)
	p.done = true
	p.toolUseCount = 3
	p.inputTokens = 8400
	p.latencyMS = 1234
	p.costUSD = 0.0042
	out := renderInlinePanel(p, 80)
	if !strings.Contains(out, "Done") {
		t.Errorf("expected 'Done' in collapsed output, got:\n%s", out)
	}
	if !strings.Contains(out, "3 tool uses") {
		t.Errorf("expected tool count in summary, got:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+o to expand") {
		t.Errorf("expected expand hint in collapsed view, got:\n%s", out)
	}
}

func TestRenderInlinePanel_DoneExpandedShowsEvents(t *testing.T) {
	p := newPanelState("gpt-4o", 1)
	p.addEvent(models.AgentEvent{Type: "reading", Data: "foo.go"})
	p.addEvent(models.AgentEvent{Type: "writing", Data: "bar.go"})
	p.done = true
	p.expanded = true
	p.toolUseCount = 2
	p.latencyMS = 500
	out := renderInlinePanel(p, 80)
	if !strings.Contains(out, "foo.go") {
		t.Errorf("expanded view should show events, got:\n%s", out)
	}
	if !strings.Contains(out, "bar.go") {
		t.Errorf("expanded view should show all events, got:\n%s", out)
	}
	if !strings.Contains(out, "Done") {
		t.Errorf("expanded view should still show summary, got:\n%s", out)
	}
	if strings.Contains(out, "ctrl+o to expand") {
		t.Errorf("expanded view should not show expand hint, got:\n%s", out)
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
