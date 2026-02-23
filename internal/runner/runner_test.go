package runner_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/tools"
)

type stubAdapter struct {
	id       string
	response models.ModelResponse
	events   []models.AgentEvent
}

func (s *stubAdapter) ID() string { return s.id }
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

	results := runner.RunAll(context.Background(), []models.ModelAdapter{a1, a2}, nil, "p", func(string, models.AgentEvent) {}, false)
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
	}, false)

	assert.Len(t, received, 2)
	assert.Equal(t, "reading", received[0].Type)
	assert.Equal(t, "writing", received[1].Type)
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
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(string, models.AgentEvent) {}, false)
	assert.Len(t, results[0].ProposedWrites, 1)
	assert.Equal(t, "x.txt", results[0].ProposedWrites[0].Path)
}

func TestRunAll_ErrorAdapterDoesNotAffectOthers(t *testing.T) {
	good := &stubAdapter{id: "good", response: models.ModelResponse{ModelID: "good", Text: "ok"}}
	bad := &errorAdapter{id: "bad", msg: "bad failed"}

	results := runner.RunAll(context.Background(), []models.ModelAdapter{good, bad}, nil, "p", func(string, models.AgentEvent) {}, false)
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
	}, true)

	assert.True(t, len(received) > 0)
	assert.Equal(t, "error", received[len(received)-1].Type)
}

func TestRunAll_ErrorEventSuppressedNonVerbose(t *testing.T) {
	bad := &errorAdapter{id: "bad", msg: "agent crashed"}

	var mu sync.Mutex
	var received []models.AgentEvent
	runner.RunAll(context.Background(), []models.ModelAdapter{bad}, nil, "p", func(_ string, e models.AgentEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}, false)

	assert.Empty(t, received)
}

func TestRunAll_LatencyRecorded(t *testing.T) {
	a := &stubAdapter{id: "m", response: models.ModelResponse{ModelID: "m"}}
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(string, models.AgentEvent) {}, false)
	assert.GreaterOrEqual(t, results[0].LatencyMS, int64(0))
}

func TestRunAll_EmptyAdapters(t *testing.T) {
	results := runner.RunAll(context.Background(), []models.ModelAdapter{}, nil, "p", func(string, models.AgentEvent) {}, false)
	assert.Empty(t, results)
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
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, histories, "new prompt", func(string, models.AgentEvent) {}, false)
	assert.Equal(t, histories["m"], received)
}

func TestRunAll_NilHistoriesPassesNilToAdapter(t *testing.T) {
	var received []models.ConversationTurn
	a := &historyCapturingAdapter{id: "m", capture: &received}
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, nil, "p", func(string, models.AgentEvent) {}, false)
	assert.Nil(t, received)
}

func TestRunAll_UnknownAdapterIDReceivesNilHistory(t *testing.T) {
	var received []models.ConversationTurn
	a := &historyCapturingAdapter{id: "m", capture: &received}
	histories := map[string][]models.ConversationTurn{
		"other": {{Role: "user", Content: "irrelevant"}},
	}
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, histories, "p", func(string, models.AgentEvent) {}, false)
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
