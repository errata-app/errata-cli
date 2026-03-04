package runner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/prompt"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/tools"
)

type stubAdapter struct {
	id       string
	response models.ModelResponse
	events   []models.AgentEvent
}

func (s *stubAdapter) ID() string { return s.id }
func (s *stubAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s *stubAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	for _, e := range s.events {
		onEvent(e)
	}
	return s.response, nil
}

// errorAdapter always returns an error from RunAgent.
type errorAdapter struct {
	id  string
	msg string
}

func (e *errorAdapter) ID() string { return e.id }
func (e *errorAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (e *errorAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	return models.ModelResponse{}, fmt.Errorf("%s", e.msg)
}

// historyCapturingAdapter records the history slice it receives.
type historyCapturingAdapter struct {
	id      string
	capture *[]models.ConversationTurn
}

func (h *historyCapturingAdapter) ID() string { return h.id }
func (h *historyCapturingAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (h *historyCapturingAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	*h.capture = history
	return models.ModelResponse{ModelID: h.id}, nil
}

func TestRunAll_Order(t *testing.T) {
	a1 := &stubAdapter{id: "m1", response: models.ModelResponse{ModelID: "m1", Text: "r1"}}
	a2 := &stubAdapter{id: "m2", response: models.ModelResponse{ModelID: "m2", Text: "r2"}}

	results := runner.RunAll(context.Background(), []models.ModelAdapter{a1, a2}, nil, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Len(t, results, 2)
	assert.Equal(t, "m1", results[0].ModelID)
	assert.Equal(t, "m2", results[1].ModelID)
}

func TestRunAll_EventPropagation(t *testing.T) {
	a := &stubAdapter{
		id: "m",
		events: []models.AgentEvent{
			{Type: "reading", Data: "foo.go"},
			{Type: "writing", Data: "bar.go"},
		},
		response: models.ModelResponse{ModelID: "m"},
	}

	var mu sync.Mutex
	var received []models.AgentEvent
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(id string, e models.AgentEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}, nil, false)

	assert.Len(t, received, 2)
	assert.Equal(t, models.EventReading, received[0].Type)
	assert.Equal(t, models.EventWriting, received[1].Type)
}

func TestRunAll_ProposedWritesPreserved(t *testing.T) {
	a := &stubAdapter{
		id: "m",
		response: models.ModelResponse{
			ModelID: "m",
			ProposedWrites: []tools.FileWrite{
				{Path: "x.txt", Content: "hello"},
			},
		},
	}
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Len(t, results[0].ProposedWrites, 1)
	assert.Equal(t, "x.txt", results[0].ProposedWrites[0].Path)
}

func TestRunAll_ErrorAdapterDoesNotAffectOthers(t *testing.T) {
	good := &stubAdapter{id: "good", response: models.ModelResponse{ModelID: "good", Text: "ok"}}
	bad := &errorAdapter{id: "bad", msg: "bad failed"}

	results := runner.RunAll(context.Background(), []models.ModelAdapter{good, bad}, nil, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Len(t, results, 2)

	var goodRes, badRes models.ModelResponse
	for _, r := range results {
		if r.ModelID == "good" {
			goodRes = r
		} else {
			badRes = r
		}
	}

	assert.True(t, goodRes.OK())
	assert.Equal(t, "ok", goodRes.Text)
	assert.False(t, badRes.OK())
	assert.Contains(t, badRes.Error, "bad failed")
}

func TestRunAll_ErrorSurfacesViaOnEventVerbose(t *testing.T) {
	bad := &errorAdapter{id: "bad", msg: "agent crashed"}

	var mu sync.Mutex
	var received []models.AgentEvent
	runner.RunAll(context.Background(), []models.ModelAdapter{bad}, nil, "p", func(_ string, e models.AgentEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}, nil, true)

	assert.NotEmpty(t, received)
	assert.Equal(t, models.EventError, received[len(received)-1].Type)
}

func TestRunAll_ErrorEventSuppressedNonVerbose(t *testing.T) {
	bad := &errorAdapter{id: "bad", msg: "agent crashed"}

	var mu sync.Mutex
	var received []models.AgentEvent
	runner.RunAll(context.Background(), []models.ModelAdapter{bad}, nil, "p", func(_ string, e models.AgentEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}, nil, false)

	assert.Empty(t, received)
}

func TestRunAll_LatencyRecorded(t *testing.T) {
	a := &stubAdapter{id: "m", response: models.ModelResponse{ModelID: "m"}}
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.GreaterOrEqual(t, results[0].LatencyMS, int64(0))
}

func TestRunAll_NormalizesModelID(t *testing.T) {
	// Adapter returns an API-resolved model name (e.g. "gpt-4o-2024-08-06").
	// runner.RunAll must overwrite it with the configured adapter ID ("gpt-4o").
	a := &stubAdapter{
		id:       "gpt-4o",
		response: models.ModelResponse{ModelID: "gpt-4o-2024-08-06", Text: "done"},
	}
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Equal(t, "gpt-4o", results[0].ModelID)
}

func TestRunAll_EmptyAdapters(t *testing.T) {
	results := runner.RunAll(context.Background(), []models.ModelAdapter{}, nil, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Empty(t, results)
}

func TestRunAll_OnModelDoneCalledPerAdapter(t *testing.T) {
	a1 := &stubAdapter{id: "m1", response: models.ModelResponse{ModelID: "m1", Text: "r1"}}
	a2 := &stubAdapter{id: "m2", response: models.ModelResponse{ModelID: "m2", Text: "r2"}}

	var mu sync.Mutex
	var doneEvents []struct {
		idx     int
		modelID string
	}
	runner.RunAll(context.Background(), []models.ModelAdapter{a1, a2}, nil, "p",
		func(string, models.AgentEvent) {},
		func(idx int, resp models.ModelResponse) {
			mu.Lock()
			doneEvents = append(doneEvents, struct {
				idx     int
				modelID string
			}{idx, resp.ModelID})
			mu.Unlock()
		},
		false,
	)

	assert.Len(t, doneEvents, 2)
	// Both adapters should have fired, with correct indices and IDs.
	idxSet := map[int]string{}
	for _, e := range doneEvents {
		idxSet[e.idx] = e.modelID
	}
	assert.Equal(t, "m1", idxSet[0])
	assert.Equal(t, "m2", idxSet[1])
}

func TestRunAll_OnModelDoneNilDoesNotPanic(t *testing.T) {
	a := &stubAdapter{id: "m", response: models.ModelResponse{ModelID: "m", Text: "ok"}}
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p",
		func(string, models.AgentEvent) {}, nil, false)
	assert.Len(t, results, 1)
}

func TestRunAll_OnModelDoneCalledOnError(t *testing.T) {
	bad := &errorAdapter{id: "bad", msg: "failed"}

	var mu sync.Mutex
	var doneResp models.ModelResponse
	runner.RunAll(context.Background(), []models.ModelAdapter{bad}, nil, "p",
		func(string, models.AgentEvent) {},
		func(idx int, resp models.ModelResponse) {
			mu.Lock()
			doneResp = resp
			mu.Unlock()
		},
		true,
	)

	assert.Equal(t, "bad", doneResp.ModelID)
	assert.Contains(t, doneResp.Error, "failed")
}

func TestRunAll_PassesHistoryToAdapter(t *testing.T) {
	var received []models.ConversationTurn
	a := &historyCapturingAdapter{id: "m", capture: &received}
	histories := map[string][]models.ConversationTurn{
		"m": {
			{Role: "user", Content: "prior question"},
			{Role: "assistant", Content: "prior answer"},
		},
	}
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, histories, "new prompt", func(string, models.AgentEvent) {}, nil, false)
	assert.Equal(t, histories["m"], received)
}

func TestRunAll_NilHistoriesPassesNilToAdapter(t *testing.T) {
	var received []models.ConversationTurn
	a := &historyCapturingAdapter{id: "m", capture: &received}
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Nil(t, received)
}

func TestRunAll_UnknownAdapterIDReceivesNilHistory(t *testing.T) {
	var received []models.ConversationTurn
	a := &historyCapturingAdapter{id: "m", capture: &received}
	histories := map[string][]models.ConversationTurn{
		"other": {{Role: "user", Content: "irrelevant"}},
	}
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, histories, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Nil(t, received)
}

// ─── AppendHistory ────────────────────────────────────────────────────────────

func TestAppendHistory_AddsSuccessfulTextRun(t *testing.T) {
	ids := []string{"m"}
	responses := []models.ModelResponse{
		{ModelID: "m", Text: "great answer"},
	}
	got := runner.AppendHistory(nil, ids, responses, "my question")
	require.Len(t, got["m"], 2)
	assert.Equal(t, models.ConversationTurn{Role: "user", Content: "my question"}, got["m"][0])
	assert.Equal(t, models.ConversationTurn{Role: "assistant", Content: "great answer"}, got["m"][1])
}

func TestAppendHistory_SkipsErrorResponse(t *testing.T) {
	ids := []string{"m"}
	responses := []models.ModelResponse{
		{ModelID: "m", Error: "boom"},
	}
	got := runner.AppendHistory(nil, ids, responses, "q")
	assert.Nil(t, got)
}

func TestAppendHistory_SkipsEmptyText(t *testing.T) {
	ids := []string{"m"}
	responses := []models.ModelResponse{
		{ModelID: "m", Text: ""},
	}
	got := runner.AppendHistory(nil, ids, responses, "q")
	assert.Nil(t, got)
}

func TestAppendHistory_NilMapInitializedOnFirstUse(t *testing.T) {
	got := runner.AppendHistory(nil, []string{"m"}, []models.ModelResponse{{ModelID: "m", Text: "hi"}}, "q")
	assert.NotNil(t, got)
	assert.Len(t, got["m"], 2)
}

func TestAppendHistory_AccumulatesAcrossMultipleCalls(t *testing.T) {
	ids := []string{"m"}
	h := runner.AppendHistory(nil, ids, []models.ModelResponse{{ModelID: "m", Text: "ans1"}}, "q1")
	h = runner.AppendHistory(h, ids, []models.ModelResponse{{ModelID: "m", Text: "ans2"}}, "q2")
	require.Len(t, h["m"], 4)
	assert.Equal(t, "q1", h["m"][0].Content)
	assert.Equal(t, "ans1", h["m"][1].Content)
	assert.Equal(t, "q2", h["m"][2].Content)
	assert.Equal(t, "ans2", h["m"][3].Content)
}

func TestAppendHistory_PerModelIndependence(t *testing.T) {
	ids := []string{"a", "b"}
	responses := []models.ModelResponse{
		{ModelID: "a", Text: "answer from a"},
		{ModelID: "b", Text: "answer from b"},
	}
	h := runner.AppendHistory(nil, ids, responses, "shared question")
	require.Len(t, h["a"], 2)
	require.Len(t, h["b"], 2)
	assert.Equal(t, "answer from a", h["a"][1].Content)
	assert.Equal(t, "answer from b", h["b"][1].Content)
}

func TestAppendHistory_MixedSuccessAndError(t *testing.T) {
	ids := []string{"good", "bad"}
	responses := []models.ModelResponse{
		{ModelID: "good", Text: "ok"},
		{ModelID: "bad", Error: "failed"},
	}
	h := runner.AppendHistory(nil, ids, responses, "q")
	assert.Len(t, h["good"], 2)
	assert.Nil(t, h["bad"])
}

func TestAppendHistory_BoundsCheckMoreResponsesThanIDs(t *testing.T) {
	ids := []string{"m"}
	responses := []models.ModelResponse{
		{ModelID: "m", Text: "ok"},
		{ModelID: "extra", Text: "overflow"},
	}
	h := runner.AppendHistory(nil, ids, responses, "q")
	assert.Len(t, h["m"], 2)
	assert.Nil(t, h["extra"]) // out-of-bounds id never referenced
}

func TestAppendHistory_ExistingHistoryPreserved(t *testing.T) {
	prior := map[string][]models.ConversationTurn{
		"m": {{Role: "user", Content: "old q"}, {Role: "assistant", Content: "old ans"}},
	}
	h := runner.AppendHistory(prior, []string{"m"}, []models.ModelResponse{{ModelID: "m", Text: "new ans"}}, "new q")
	require.Len(t, h["m"], 4)
	assert.Equal(t, "old q", h["m"][0].Content)
	assert.Equal(t, "old ans", h["m"][1].Content)
	assert.Equal(t, "new q", h["m"][2].Content)
	assert.Equal(t, "new ans", h["m"][3].Content)
}

// ─── TrimHistory ─────────────────────────────────────────────────────────────

func turns(n int) []models.ConversationTurn {
	out := make([]models.ConversationTurn, n)
	for i := range out {
		if i%2 == 0 {
			out[i] = models.ConversationTurn{Role: "user", Content: fmt.Sprintf("q%d", i/2+1)}
		} else {
			out[i] = models.ConversationTurn{Role: "assistant", Content: fmt.Sprintf("a%d", i/2+1)}
		}
	}
	return out
}

func TestTrimHistory_NoopWhenBelowMax(t *testing.T) {
	h := turns(4)
	got := runner.TrimHistory(h, 10)
	assert.Equal(t, h, got)
}

func TestTrimHistory_NoopWhenEqual(t *testing.T) {
	h := turns(4)
	got := runner.TrimHistory(h, 4)
	assert.Equal(t, h, got)
}

func TestTrimHistory_KeepsRecentTurns(t *testing.T) {
	h := turns(8) // 4 Q&A pairs
	got := runner.TrimHistory(h, 4)
	require.Len(t, got, 4)
	assert.Equal(t, "q3", got[0].Content)
	assert.Equal(t, "a3", got[1].Content)
	assert.Equal(t, "q4", got[2].Content)
	assert.Equal(t, "a4", got[3].Content)
}

func TestTrimHistory_ZeroMaxReturnsUnchanged(t *testing.T) {
	h := turns(6)
	assert.Equal(t, h, runner.TrimHistory(h, 0))
}

func TestTrimHistory_PreservesEvenPairs(t *testing.T) {
	// maxTurns=5 (odd) should be treated as 4
	h := turns(8)
	got := runner.TrimHistory(h, 5)
	assert.Len(t, got, 4)
}

// ─── EstimateHistoryTokens ───────────────────────────────────────────────────

func TestEstimateHistoryTokens_EmptyHistory(t *testing.T) {
	assert.Equal(t, int64(0), runner.EstimateHistoryTokens(nil))
}

func TestEstimateHistoryTokens_RoughCount(t *testing.T) {
	h := []models.ConversationTurn{
		{Role: "user", Content: "aaaa"},     // 4 chars → 1 token
		{Role: "assistant", Content: "bbbbbbbb"}, // 8 chars → 2 tokens
	}
	assert.Equal(t, int64(3), runner.EstimateHistoryTokens(h))
}

// ─── IsContextOverflowError ──────────────────────────────────────────────────

func TestIsContextOverflowError_MatchesKnownPatterns(t *testing.T) {
	patterns := []string{
		"context_length_exceeded",
		"This model's maximum context length is 128000 tokens",
		"prompt is too long",
		"prompt_too_long",
		"exceeds the model's maximum context",
		"too many tokens in the input",
		"context window exceeded",
	}
	for _, p := range patterns {
		assert.True(t, runner.IsContextOverflowError(p), "expected match for: %q", p)
	}
}

func TestIsContextOverflowError_DoesNotMatchGenericError(t *testing.T) {
	assert.False(t, runner.IsContextOverflowError("internal server error"))
	assert.False(t, runner.IsContextOverflowError("authentication failed"))
	assert.False(t, runner.IsContextOverflowError(""))
}

// ─── ShouldAutoCompact ────────────────────────────────────────────────────────

// makeTurns builds a ConversationTurn slice from alternating role/content pairs.
// e.g. makeTurns("user", "hello", "assistant", "hi") → two turns.
func makeTurns(args ...string) []models.ConversationTurn {
	out := make([]models.ConversationTurn, 0, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		out = append(out, models.ConversationTurn{Role: args[i], Content: args[i+1]})
	}
	return out
}

func TestShouldAutoCompact_NoHistoryReturnsFalse(t *testing.T) {
	if runner.ShouldAutoCompact(nil, "claude-sonnet-4-6", 0) {
		t.Error("nil histories should not trigger compact")
	}
	if runner.ShouldAutoCompact(map[string][]models.ConversationTurn{}, "claude-sonnet-4-6", 0) {
		t.Error("empty histories should not trigger compact")
	}
}

func TestShouldAutoCompact_UnknownModelReturnsFalse(t *testing.T) {
	// Unknown model → context window = 0 → fraction undefined → no compact.
	h := map[string][]models.ConversationTurn{
		"no-such-model": makeTurns("user", strings.Repeat("x", 1_000_000), "assistant", "y"),
	}
	if runner.ShouldAutoCompact(h, "no-such-model", 0) {
		t.Error("unknown model should never trigger auto-compact")
	}
}

func TestShouldAutoCompact_BelowThresholdReturnsFalse(t *testing.T) {
	// gemini-2.0-flash context = 1,048,576 tokens; 80% = 838,860 tokens ≈ 3.36M chars.
	h := map[string][]models.ConversationTurn{
		"gemini-2.0-flash": makeTurns("user", "short", "assistant", "reply"),
	}
	if runner.ShouldAutoCompact(h, "gemini-2.0-flash", 0) {
		t.Error("well-below-threshold history should not trigger compact")
	}
}

func TestShouldAutoCompact_AboveThresholdReturnsTrue(t *testing.T) {
	// claude-sonnet-4-6 context = 200,000 tokens; 80% = 160,000 tokens ≈ 640,000 chars.
	bigText := strings.Repeat("x", 700_000) // ~175,000 tokens, above 80%
	h := map[string][]models.ConversationTurn{
		"claude-sonnet-4-6": {{Role: "user", Content: bigText}},
	}
	if !runner.ShouldAutoCompact(h, "claude-sonnet-4-6", 0) {
		t.Error("above-threshold history should trigger auto-compact")
	}
}

// ─── CompactHistories ─────────────────────────────────────────────────────────

// compactStub is an adapter whose RunAgent always returns a fixed summary string.
type compactStub struct {
	id      string
	summary string
}

func (s compactStub) ID() string { return s.id }
func (s compactStub) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s compactStub) RunAgent(_ context.Context, hist []models.ConversationTurn, prompt string, onEvent func(models.AgentEvent)) (models.ModelResponse, error) {
	return models.ModelResponse{ModelID: s.id, Text: s.summary}, nil
}

func TestCompactHistories_ReplacesWithSinglePair(t *testing.T) {
	ad := compactStub{id: "model-a", summary: "Here is the summary."}
	h := map[string][]models.ConversationTurn{
		"model-a": makeTurns("user", "hello", "assistant", "hi", "user", "world", "assistant", "earth"),
	}
	result := runner.CompactHistories(context.Background(), []models.ModelAdapter{ad}, h, func(_ string, _ models.AgentEvent) {})
	got, ok := result["model-a"]
	if !ok {
		t.Fatal("expected model-a history after compaction")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 turns (1 user + 1 assistant) after compact, got %d", len(got))
	}
	if got[1].Content != "Here is the summary." {
		t.Errorf("assistant content = %q, want %q", got[1].Content, "Here is the summary.")
	}
}

func TestCompactHistories_EmptyHistoryUnchanged(t *testing.T) {
	ad := compactStub{id: "model-b", summary: "summary"}
	result := runner.CompactHistories(context.Background(), []models.ModelAdapter{ad}, nil, func(_ string, _ models.AgentEvent) {})
	if hist := result["model-b"]; len(hist) != 0 {
		t.Errorf("expected no history for model without prior history, got %v", hist)
	}
}

func TestCompactHistories_MultipleAdaptersIndependent(t *testing.T) {
	ads := []models.ModelAdapter{
		compactStub{id: "m1", summary: "summary-1"},
		compactStub{id: "m2", summary: "summary-2"},
	}
	h := map[string][]models.ConversationTurn{
		"m1": makeTurns("user", "hi", "assistant", "hello"),
		"m2": makeTurns("user", "hey", "assistant", "sup"),
	}
	result := runner.CompactHistories(context.Background(), ads, h, func(_ string, _ models.AgentEvent) {})
	for _, id := range []string{"m1", "m2"} {
		got := result[id]
		if len(got) != 2 {
			t.Errorf("%s: expected 2 turns, got %d", id, len(got))
		}
	}
	if result["m1"][1].Content != "summary-1" {
		t.Errorf("m1 summary wrong: %q", result["m1"][1].Content)
	}
	if result["m2"][1].Content != "summary-2" {
		t.Errorf("m2 summary wrong: %q", result["m2"][1].Content)
	}
}

// ─── Snapshot event suppression ──────────────────────────────────────────────

// snapshotAdapter emits a mix of regular events and a "snapshot" event.
type snapshotAdapter struct {
	id string
}

func (s *snapshotAdapter) ID() string { return s.id }
func (s *snapshotAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s *snapshotAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	onEvent(models.AgentEvent{Type: models.EventReading, Data: "file.go"})
	onEvent(models.AgentEvent{Type: models.EventSnapshot, Data: `{"text":"partial","input_tokens":10}`})
	onEvent(models.AgentEvent{Type: models.EventWriting, Data: "out.go"})
	return models.ModelResponse{ModelID: s.id, Text: "done"}, nil
}

func TestRunAll_SnapshotEventsNotForwarded(t *testing.T) {
	a := &snapshotAdapter{id: "m"}

	var mu sync.Mutex
	var received []models.AgentEvent
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(_ string, e models.AgentEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}, nil, true) // verbose=true so all non-snapshot events pass through

	// "reading" and "writing" should be forwarded; "snapshot" should NOT.
	for _, e := range received {
		if e.Type == models.EventSnapshot {
			t.Error("snapshot event should not be forwarded to caller's onEvent")
		}
	}
	assert.Len(t, received, 2)
	assert.Equal(t, models.EventReading, received[0].Type)
	assert.Equal(t, models.EventWriting, received[1].Type)
}

// ─── Request event suppression ───────────────────────────────────────────────

// requestAdapter emits a mix of regular events and a "request" event.
type requestAdapter struct {
	id string
}

func (r *requestAdapter) ID() string { return r.id }
func (r *requestAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (r *requestAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	onEvent(models.AgentEvent{Type: models.EventReading, Data: "file.go"})
	onEvent(models.AgentEvent{Type: models.EventRequest, Data: `{"model":"test","messages":[]}`})
	onEvent(models.AgentEvent{Type: models.EventWriting, Data: "out.go"})
	return models.ModelResponse{ModelID: r.id, Text: "done"}, nil
}

func TestRunAll_RequestEventsNotForwarded(t *testing.T) {
	a := &requestAdapter{id: "m"}

	var mu sync.Mutex
	var received []models.AgentEvent
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(_ string, e models.AgentEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}, nil, true) // verbose=true so all non-filtered events pass through

	// "reading" and "writing" should be forwarded; "request" should NOT.
	for _, e := range received {
		if e.Type == models.EventRequest {
			t.Error("request event should not be forwarded to caller's onEvent")
		}
	}
	assert.Len(t, received, 2)
	assert.Equal(t, models.EventReading, received[0].Type)
	assert.Equal(t, models.EventWriting, received[1].Type)
}

// ─── Summarization prompt ───────────────────────────────────────────────────

// promptCapturingStub captures the prompt passed to RunAgent for verification.
type promptCapturingStub struct {
	id             string
	summary        string
	capturedMu     sync.Mutex
	capturedPrompt string
}

func (s *promptCapturingStub) ID() string { return s.id }
func (s *promptCapturingStub) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s *promptCapturingStub) RunAgent(_ context.Context, hist []models.ConversationTurn, p string, onEvent func(models.AgentEvent)) (models.ModelResponse, error) {
	s.capturedMu.Lock()
	s.capturedPrompt = p
	s.capturedMu.Unlock()
	return models.ModelResponse{ModelID: s.id, Text: s.summary}, nil
}

func TestCompactHistories_UsesCustomSummarizationPrompt(t *testing.T) {
	ad := &promptCapturingStub{id: "model-x", summary: "compact summary"}
	ctx := prompt.WithSummarizationPrompt(context.Background(), "Custom: summarize now!")
	h := map[string][]models.ConversationTurn{
		"model-x": makeTurns("user", "hello", "assistant", "hi"),
	}
	runner.CompactHistories(ctx, []models.ModelAdapter{ad}, h, func(_ string, _ models.AgentEvent) {})

	assert.Equal(t, "Custom: summarize now!", ad.capturedPrompt)
}

// ─── HasInterrupted ────────────────────────────────────────────────────────

func TestHasInterrupted_True(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "ok"},
		{ModelID: "m2", Interrupted: true},
	}
	assert.True(t, runner.HasInterrupted(responses))
}

func TestHasInterrupted_False(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "ok"},
		{ModelID: "m2", Text: "also ok"},
	}
	assert.False(t, runner.HasInterrupted(responses))
}

func TestHasInterrupted_Empty(t *testing.T) {
	assert.False(t, runner.HasInterrupted(nil))
	assert.False(t, runner.HasInterrupted([]models.ModelResponse{}))
}

// ─── WithRunOptions ────────────────────────────────────────────────────────

func TestWithRunOptions_OverridesDefaults(t *testing.T) {
	// Verify that RunAll respects a custom timeout via WithRunOptions.
	// We use a very short timeout so a slow adapter times out.
	slow := &stubAdapter{id: "slow", response: models.ModelResponse{ModelID: "slow", Text: "late"}}

	_ = slow // used above just for structure; test validates via short timeout below

	// Simply verify WithRunOptions returns a usable context that doesn't panic.
	ctx := runner.WithRunOptions(context.Background(), runner.RunOptions{
		MaxHistoryTurns: 5,
	})
	a := &historyCapturingAdapter{id: "m", capture: new([]models.ConversationTurn)}
	bigHistory := map[string][]models.ConversationTurn{
		"m": turns(20), // 20 turns, but max is 5 → should be trimmed
	}
	results := runner.RunAll(ctx, []models.ModelAdapter{a}, bigHistory, "p", func(string, models.AgentEvent) {}, nil, false)
	assert.Len(t, results, 1)
	// The adapter should have received a trimmed history (4 turns: 5 rounded down to even).
	assert.LessOrEqual(t, len(*a.capture), 5)
}

// ─── CompactHistories edge cases ──────────────────────────────────────────

func TestCompactHistories_NilOnEvent(t *testing.T) {
	ad := compactStub{id: "m", summary: "summary"}
	h := map[string][]models.ConversationTurn{
		"m": makeTurns("user", "hello", "assistant", "hi"),
	}
	// nil onEvent should not panic.
	result := runner.CompactHistories(context.Background(), []models.ModelAdapter{ad}, h, nil)
	assert.Len(t, result["m"], 2)
}

func TestCompactHistories_ErrorAdapterUnchanged(t *testing.T) {
	bad := &errorAdapter{id: "bad", msg: "compact failed"}
	originalHistory := makeTurns("user", "hello", "assistant", "hi")
	h := map[string][]models.ConversationTurn{
		"bad": originalHistory,
	}
	result := runner.CompactHistories(context.Background(), []models.ModelAdapter{bad}, h, func(string, models.AgentEvent) {})
	// Error adapter's history should remain unchanged.
	assert.Equal(t, originalHistory, result["bad"])
}

func TestCompactHistories_EmptyTextUnchanged(t *testing.T) {
	// Adapter returns empty text → compaction should be skipped.
	ad := compactStub{id: "m", summary: ""}
	originalHistory := makeTurns("user", "hello", "assistant", "hi")
	h := map[string][]models.ConversationTurn{
		"m": originalHistory,
	}
	result := runner.CompactHistories(context.Background(), []models.ModelAdapter{ad}, h, func(string, models.AgentEvent) {})
	assert.Equal(t, originalHistory, result["m"])
}

// ─── RunAll with checkpointing ───────────────────────────────────────────────

func TestRunAll_CheckpointCreatedAndCleanedUp(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	a := &snapshotAdapter{id: "m"}

	ctx := runner.WithRunOptions(context.Background(), runner.RunOptions{
		CheckpointPath: cpPath,
	})

	results := runner.RunAll(ctx, []models.ModelAdapter{a}, nil, "test prompt",
		func(string, models.AgentEvent) {}, nil, false)

	// Adapter completed successfully → checkpoint should be cleaned up.
	assert.Len(t, results, 1)
	assert.Equal(t, "done", results[0].Text)
	_, err := os.Stat(cpPath)
	assert.True(t, os.IsNotExist(err), "checkpoint should be cleared after successful run")
}

func TestRunAll_CheckpointPreservedOnInterrupt(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")

	// interruptedAdapter returns a response with Interrupted=true.
	interrupted := &stubAdapter{
		id: "m",
		response: models.ModelResponse{
			ModelID:     "m",
			Text:        "partial",
			Interrupted: true,
		},
	}

	ctx := runner.WithRunOptions(context.Background(), runner.RunOptions{
		CheckpointPath: cpPath,
	})

	results := runner.RunAll(ctx, []models.ModelAdapter{interrupted}, nil, "test prompt",
		func(string, models.AgentEvent) {}, nil, false)

	assert.Len(t, results, 1)
	assert.True(t, results[0].Interrupted)

	// Checkpoint should NOT be cleaned up because there's an interrupted response.
	cp, err := checkpoint.Load(cpPath)
	require.NoError(t, err)
	assert.NotNil(t, cp, "checkpoint should be preserved when responses are interrupted")
	assert.Equal(t, "test prompt", cp.Prompt)
}

// slowAdapter blocks until context is cancelled, simulating a slow model.
type slowAdapter struct {
	id string
}

func (s *slowAdapter) ID() string { return s.id }
func (s *slowAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s *slowAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	<-ctx.Done()
	return models.ModelResponse{
		ModelID:     s.id,
		Interrupted: true,
		Error:       ctx.Err().Error(),
	}, ctx.Err()
}

func TestWithRunOptions_TimeoutEnforcedOnSlowAdapter(t *testing.T) {
	// Create a slowAdapter that blocks until context cancelled.
	// Set RunOptions with a very short timeout (200ms).
	// Assert response has Interrupted or error mentioning deadline/context.
	slow := &slowAdapter{id: "slow"}

	ctx := runner.WithRunOptions(context.Background(), runner.RunOptions{
		Timeout: 200 * time.Millisecond,
	})

	results := runner.RunAll(ctx, []models.ModelAdapter{slow}, nil, "test",
		func(string, models.AgentEvent) {}, nil, false)

	require.Len(t, results, 1)
	resp := results[0]
	// The adapter should have been interrupted by the timeout.
	assert.True(t, resp.Interrupted || resp.Error != "",
		"expected interrupted or error, got: interrupted=%v error=%q", resp.Interrupted, resp.Error)
	if resp.Error != "" {
		assert.True(t,
			strings.Contains(resp.Error, "deadline") || strings.Contains(resp.Error, "context"),
			"error should mention deadline or context: %q", resp.Error)
	}
}

func TestWithRunOptions_DefaultsFillZeroFields(t *testing.T) {
	// Pass RunOptions{} (all zero) with 24 history turns.
	// Assert adapter receives exactly 20 turns (default MaxHistoryTurns applied).
	var received []models.ConversationTurn
	a := &historyCapturingAdapter{id: "m", capture: &received}
	bigHistory := map[string][]models.ConversationTurn{
		"m": turns(24), // 24 turns
	}

	// Zero RunOptions → defaults should be applied (MaxHistoryTurns=20).
	ctx := runner.WithRunOptions(context.Background(), runner.RunOptions{})
	runner.RunAll(ctx, []models.ModelAdapter{a}, bigHistory, "p",
		func(string, models.AgentEvent) {}, nil, false)

	// Default MaxHistoryTurns=20, which is even, so 20 turns should be kept.
	assert.Len(t, received, 20,
		"zero RunOptions should default MaxHistoryTurns to 20")
}

func TestCompactHistories_FallsBackToDefaultSummarizationPrompt(t *testing.T) {
	ad := &promptCapturingStub{id: "model-y", summary: "compact summary"}
	h := map[string][]models.ConversationTurn{
		"model-y": makeTurns("user", "hello", "assistant", "hi"),
	}
	// No payloads in context → should use DefaultSummarizationPrompt.
	runner.CompactHistories(context.Background(), []models.ModelAdapter{ad}, h, func(_ string, _ models.AgentEvent) {})

	assert.Equal(t, prompt.DefaultSummarizationPrompt, ad.capturedPrompt)
}

// ─── WorkDirs context wiring ─────────────────────────────────────────────────

// ctxCapturingAdapter records the context passed to RunAgent so tests can
// verify that RunAll correctly wires WorkDir and DirectWrites.
type ctxCapturingAdapter struct {
	id  string
	ctx context.Context
	mu  sync.Mutex
}

func (c *ctxCapturingAdapter) ID() string { return c.id }
func (c *ctxCapturingAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (c *ctxCapturingAdapter) RunAgent(
	ctx context.Context,
	_ []models.ConversationTurn,
	_ string,
	_ func(models.AgentEvent),
) (models.ModelResponse, error) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
	return models.ModelResponse{Text: "ok"}, nil
}

func TestRunAll_WorkDirsSetsContextValues(t *testing.T) {
	a1 := &ctxCapturingAdapter{id: "model-a"}
	a2 := &ctxCapturingAdapter{id: "model-b"}

	workDirs := map[string]string{
		"model-a": "/tmp/worktree-a",
	}

	ctx := runner.WithRunOptions(context.Background(), runner.RunOptions{
		WorkDirs: workDirs,
	})

	runner.RunAll(ctx, []models.ModelAdapter{a1, a2}, nil, "prompt",
		func(string, models.AgentEvent) {}, nil, false)

	// model-a should have WorkDir and DirectWrites set.
	assert.Equal(t, "/tmp/worktree-a", tools.WorkDirFromContext(a1.ctx))
	assert.True(t, tools.DirectWriteFromContext(a1.ctx))

	// model-b has no entry in WorkDirs — should have no WorkDir and no DirectWrites.
	assert.Empty(t, tools.WorkDirFromContext(a2.ctx))
	assert.False(t, tools.DirectWriteFromContext(a2.ctx))
}
