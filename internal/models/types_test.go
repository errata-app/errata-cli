package models_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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
	e := models.AgentEvent{Type: "reading", Data: "foo.go"}
	assert.Equal(t, "reading", e.Type)
	assert.Equal(t, "foo.go", e.Data)
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
	assert.NoError(t, err)
	assert.Equal(t, "done", resp.Text)
}
