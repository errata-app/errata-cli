package models_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// stubAdapter is a minimal ModelAdapter for testing.
type stubAdapter struct {
	id       string
	response models.ModelResponse
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
	return s.response, nil
}

var _ models.ModelAdapter = (*stubAdapter)(nil)

func TestModelResponse_OK(t *testing.T) {
	ok := models.ModelResponse{ModelID: "m", Error: ""}
	assert.True(t, ok.OK())

	fail := models.ModelResponse{ModelID: "m", Error: "boom"}
	assert.False(t, fail.OK())
}

func TestModelResponse_ProposedWrites(t *testing.T) {
	r := models.ModelResponse{
		ModelID: "m",
		ProposedWrites: []tools.FileWrite{
			{Path: "a.txt", Content: "hello"},
		},
	}
	assert.Len(t, r.ProposedWrites, 1)
	assert.Equal(t, "a.txt", r.ProposedWrites[0].Path)
}

func TestAgentEvent(t *testing.T) {
	e := models.AgentEvent{Type: models.EventReading, Data: "foo.go"}
	assert.Equal(t, models.EventReading, e.Type)
	assert.Equal(t, "foo.go", e.Data)
}

func TestStopReason_ConstantsDistinct(t *testing.T) {
	reasons := []models.StopReason{
		models.StopReasonComplete,
		models.StopReasonTimeout,
		models.StopReasonMaxSteps,
		models.StopReasonContextOverflow,
		models.StopReasonCancelled,
		models.StopReasonError,
	}
	seen := make(map[models.StopReason]bool, len(reasons))
	for _, r := range reasons {
		assert.False(t, seen[r], "duplicate StopReason: %s", r)
		assert.NotEmpty(t, string(r), "StopReason should not be empty")
		seen[r] = true
	}
	assert.Len(t, seen, 6)
}

func TestStubAdapter_RunAgent(t *testing.T) {
	a := &stubAdapter{
		id: "stub",
		response: models.ModelResponse{
			ModelID: "stub",
			Text:    "done",
		},
	}
	resp, err := a.RunAgent(context.Background(), nil, "prompt", func(models.AgentEvent) {})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Text)
}
