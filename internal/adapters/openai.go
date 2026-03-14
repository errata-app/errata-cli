package adapters

import (
	"context"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/errata-app/errata-cli/internal/capabilities"
	"github.com/errata-app/errata-cli/internal/models"
)

// OpenAIAdapter implements ModelAdapter for OpenAI models.
type OpenAIAdapter struct {
	modelID string
	apiKey  string
}

// NewOpenAIAdapter creates an OpenAIAdapter.
func NewOpenAIAdapter(modelID, apiKey string) *OpenAIAdapter {
	return &OpenAIAdapter{modelID: modelID, apiKey: apiKey}
}

func (a *OpenAIAdapter) ID() string { return a.modelID }

func (a *OpenAIAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return capabilities.DefaultCapabilities("openai", a.modelID)
}

func (a *OpenAIAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	client := openai.NewClient(option.WithAPIKey(a.apiKey))
	return runOpenAIAgentLoop(ctx, &openaiRunConfig{
		client:      client,
		modelID:     a.modelID,
		apiModelID:  a.modelID,
		qualifiedID: "openai/" + a.modelID,
	}, history, prompt, onEvent)
}

func init() {
	var _ models.ModelAdapter = (*OpenAIAdapter)(nil)
}
