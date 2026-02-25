package subagent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/subagent"
	"github.com/suarezc/errata/internal/tools"
)

// ─── stub adapter ─────────────────────────────────────────────────────────────

type stubAdapter struct {
	id       string
	response models.ModelResponse
}

func (s *stubAdapter) ID() string { return s.id }
func (s *stubAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s *stubAdapter) RunAgent(_ context.Context, _ []models.ConversationTurn, prompt string, _ func(models.AgentEvent)) (models.ModelResponse, error) {
	r := s.response
	r.ModelID = s.id
	return r, nil
}

// ─── TestNewDispatcher_DepthLimit ─────────────────────────────────────────────

func TestNewDispatcher_DepthLimit(t *testing.T) {
	adapter := &stubAdapter{id: "test-model", response: models.ModelResponse{Text: "hello"}}
	cfg := config.Config{SubagentMaxDepth: 1}

	dispatcher := subagent.NewDispatcher([]models.ModelAdapter{adapter}, cfg, nil,
		func(string, models.AgentEvent) {})

	// At depth == maxDepth the dispatcher should refuse.
	ctx := tools.WithSubagentDepth(context.Background(), 1)
	_, _, errMsg := dispatcher(ctx, map[string]string{"task": "do work"})
	assert.Contains(t, errMsg, "max sub-agent depth")
}

func TestNewDispatcher_BelowDepthLimit_Succeeds(t *testing.T) {
	adapter := &stubAdapter{id: "test-model", response: models.ModelResponse{Text: "result"}}
	cfg := config.Config{SubagentMaxDepth: 2}

	dispatcher := subagent.NewDispatcher([]models.ModelAdapter{adapter}, cfg, nil,
		func(string, models.AgentEvent) {})

	ctx := tools.WithSubagentDepth(context.Background(), 1) // depth 1 < maxDepth 2
	text, _, errMsg := dispatcher(ctx, map[string]string{"task": "do work"})
	assert.Equal(t, "", errMsg)
	assert.Equal(t, "result", text)
}

// ─── TestNewDispatcher_WritesMerged ───────────────────────────────────────────

func TestNewDispatcher_WritesMerged(t *testing.T) {
	writes := []tools.FileWrite{
		{Path: "foo.go", Content: "package foo"},
	}
	adapter := &stubAdapter{
		id: "writer",
		response: models.ModelResponse{
			Text:           "wrote files",
			ProposedWrites: writes,
		},
	}
	cfg := config.Config{SubagentMaxDepth: 1}

	dispatcher := subagent.NewDispatcher([]models.ModelAdapter{adapter}, cfg, nil,
		func(string, models.AgentEvent) {})

	ctx := tools.WithSubagentDepth(context.Background(), 0)
	text, gotWrites, errMsg := dispatcher(ctx, map[string]string{"task": "write foo"})
	assert.Equal(t, "", errMsg)
	assert.Equal(t, "wrote files", text)
	require.Len(t, gotWrites, 1)
	assert.Equal(t, "foo.go", gotWrites[0].Path)
}

// ─── TestNewDispatcher_ModelIDOverride ────────────────────────────────────────

func TestNewDispatcher_ModelIDOverride(t *testing.T) {
	first := &stubAdapter{id: "first-model", response: models.ModelResponse{Text: "from first"}}
	second := &stubAdapter{id: "second-model", response: models.ModelResponse{Text: "from second"}}
	cfg := config.Config{SubagentMaxDepth: 1}

	dispatcher := subagent.NewDispatcher(
		[]models.ModelAdapter{first, second}, cfg, nil,
		func(string, models.AgentEvent) {},
	)

	ctx := tools.WithSubagentDepth(context.Background(), 0)
	text, _, errMsg := dispatcher(ctx, map[string]string{
		"task":     "do work",
		"model_id": "second-model",
	})
	assert.Equal(t, "", errMsg)
	assert.Equal(t, "from second", text)
}

// ─── TestNewDispatcher_FallbackToSubagentModel ────────────────────────────────

func TestNewDispatcher_FallbackToSubagentModel(t *testing.T) {
	first := &stubAdapter{id: "first-model", response: models.ModelResponse{Text: "from first"}}
	second := &stubAdapter{id: "preferred-model", response: models.ModelResponse{Text: "from preferred"}}
	cfg := config.Config{
		SubagentMaxDepth: 1,
		SubagentModel:    "preferred-model",
	}

	dispatcher := subagent.NewDispatcher(
		[]models.ModelAdapter{first, second}, cfg, nil,
		func(string, models.AgentEvent) {},
	)

	ctx := tools.WithSubagentDepth(context.Background(), 0)
	// No model_id arg — should fall back to cfg.SubagentModel.
	text, _, errMsg := dispatcher(ctx, map[string]string{"task": "do work"})
	assert.Equal(t, "", errMsg)
	assert.Equal(t, "from preferred", text)
}

// ─── TestNewDispatcher_SpawnAgentRemovedAtMaxDepth ────────────────────────────

func TestNewDispatcher_SpawnAgentRemovedAtMaxDepth(t *testing.T) {
	var capturedDefs []tools.ToolDef
	adapter := &stubAdapter{id: "m", response: models.ModelResponse{Text: "ok"}}

	// Override RunAgent to capture the active tool set from context.
	type capturingAdapter struct {
		*stubAdapter
	}
	cap := &struct {
		models.ModelAdapter
		captured []tools.ToolDef
	}{
		ModelAdapter: adapter,
	}
	_ = cap

	// Use a dispatcher that checks whether spawn_agent is present in the sub-context.
	var innerDefs []tools.ToolDef
	checkAdapter := &checkingAdapter{
		id: "m",
		fn: func(ctx context.Context) models.ModelResponse {
			innerDefs = tools.ActiveToolsFromContext(ctx)
			return models.ModelResponse{Text: "ok"}
		},
	}

	// maxDepth=1: sub-agent at depth 1 must not see spawn_agent.
	cfg := config.Config{SubagentMaxDepth: 1}
	parentDefs := tools.Definitions // includes spawn_agent

	dispatcher := subagent.NewDispatcher([]models.ModelAdapter{checkAdapter}, cfg, nil,
		func(string, models.AgentEvent) {})

	ctx := tools.WithSubagentDepth(context.Background(), 0)
	ctx = tools.WithActiveTools(ctx, parentDefs)
	_, _, errMsg := dispatcher(ctx, map[string]string{"task": "check tools"})
	assert.Equal(t, "", errMsg)

	// spawn_agent must have been stripped from the sub-agent's tool set.
	for _, d := range innerDefs {
		assert.NotEqual(t, tools.SpawnAgentToolName, d.Name,
			"spawn_agent must not be visible to sub-agents at max depth")
	}
	capturedDefs = innerDefs
	assert.NotEmpty(t, capturedDefs)
}

// checkingAdapter is a test adapter that calls fn during RunAgent so tests
// can inspect the context received by the sub-agent.
type checkingAdapter struct {
	id string
	fn func(ctx context.Context) models.ModelResponse
}

func (c *checkingAdapter) ID() string { return c.id }
func (c *checkingAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (c *checkingAdapter) RunAgent(ctx context.Context, _ []models.ConversationTurn, _ string, _ func(models.AgentEvent)) (models.ModelResponse, error) {
	r := c.fn(ctx)
	r.ModelID = c.id
	return r, nil
}
