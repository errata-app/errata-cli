package runner_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
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
	ctx context.Context,
	prompt string,
	onEvent func(models.AgentEvent),
	verbose bool,
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
	ctx context.Context,
	prompt string,
	onEvent func(models.AgentEvent),
	verbose bool,
) (models.ModelResponse, error) {
	return models.ModelResponse{}, fmt.Errorf("%s", e.msg)
}

func TestRunAll_Order(t *testing.T) {
	a1 := &stubAdapter{id: "m1", response: models.ModelResponse{ModelID: "m1", Text: "r1"}}
	a2 := &stubAdapter{id: "m2", response: models.ModelResponse{ModelID: "m2", Text: "r2"}}

	results := runner.RunAll(context.Background(), []models.ModelAdapter{a1, a2}, "p", func(string, models.AgentEvent) {}, false)
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
	runner.RunAll(context.Background(), []models.ModelAdapter{a}, "p", func(id string, e models.AgentEvent) {
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
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, "p", func(string, models.AgentEvent) {}, false)
	assert.Len(t, results[0].ProposedWrites, 1)
	assert.Equal(t, "x.txt", results[0].ProposedWrites[0].Path)
}

func TestRunAll_ErrorAdapterDoesNotAffectOthers(t *testing.T) {
	good := &stubAdapter{id: "good", response: models.ModelResponse{ModelID: "good", Text: "ok"}}
	bad := &errorAdapter{id: "bad", msg: "bad failed"}

	results := runner.RunAll(context.Background(), []models.ModelAdapter{good, bad}, "p", func(string, models.AgentEvent) {}, false)
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

func TestRunAll_ErrorSurfacesViaOnEvent(t *testing.T) {
	bad := &errorAdapter{id: "bad", msg: "agent crashed"}

	var mu sync.Mutex
	var received []models.AgentEvent
	runner.RunAll(context.Background(), []models.ModelAdapter{bad}, "p", func(_ string, e models.AgentEvent) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	}, false)

	assert.True(t, len(received) > 0)
	assert.Equal(t, "error", received[len(received)-1].Type)
}

func TestRunAll_LatencyRecorded(t *testing.T) {
	a := &stubAdapter{id: "m", response: models.ModelResponse{ModelID: "m"}}
	results := runner.RunAll(context.Background(), []models.ModelAdapter{a}, "p", func(string, models.AgentEvent) {}, false)
	assert.GreaterOrEqual(t, results[0].LatencyMS, int64(0))
}

func TestRunAll_EmptyAdapters(t *testing.T) {
	results := runner.RunAll(context.Background(), []models.ModelAdapter{}, "p", func(string, models.AgentEvent) {}, false)
	assert.Empty(t, results)
}
