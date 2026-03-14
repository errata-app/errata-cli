package adapters

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/errata-app/errata-cli/internal/capabilities"
	"github.com/errata-app/errata-cli/internal/models"
)

// AnthropicAdapter implements ModelAdapter for Anthropic Claude models.
type AnthropicAdapter struct {
	modelID string
	apiKey  string
}

// NewAnthropicAdapter creates an AnthropicAdapter.
func NewAnthropicAdapter(modelID, apiKey string) *AnthropicAdapter {
	return &AnthropicAdapter{modelID: modelID, apiKey: apiKey}
}

func (a *AnthropicAdapter) ID() string { return a.modelID }

func (a *AnthropicAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return capabilities.DefaultCapabilities("anthropic", a.modelID)
}

func (a *AnthropicAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	caps := a.Capabilities(ctx)
	return runAnthropicAgentLoop(ctx, &anthropicRunConfig{
		clientOpts:      []option.RequestOption{option.WithAPIKey(a.apiKey)},
		modelID:         a.modelID,
		qualifiedID:     "anthropic/" + a.modelID,
		maxOutputTokens: int64(caps.MaxOutputTokens),
	}, history, prompt, onEvent)
}

func init() {
	// Ensure AnthropicAdapter satisfies ModelAdapter at compile time.
	var _ models.ModelAdapter = (*AnthropicAdapter)(nil)
}
