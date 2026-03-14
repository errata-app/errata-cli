package adapters

import (
	"context"
	"strings"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/errata-app/errata-cli/internal/capabilities"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/pricing"
)

const openRouterBaseURL = "https://openrouter.ai/api/v1"

// OpenRouterAdapter implements ModelAdapter using OpenRouter's OpenAI-compatible API.
// Model IDs are in OpenRouter's "provider/model" format (e.g. "anthropic/claude-sonnet-4-6").
type OpenRouterAdapter struct {
	modelID string
	apiKey  string
}

// NewOpenRouterAdapter creates an OpenRouterAdapter.
func NewOpenRouterAdapter(modelID, apiKey string) *OpenRouterAdapter {
	return &OpenRouterAdapter{modelID: modelID, apiKey: apiKey}
}

func (a *OpenRouterAdapter) ID() string { return a.modelID }

// Capabilities infers capabilities from the sub-provider in the model ID
// (e.g. "anthropic/claude-sonnet-4-6" → anthropic defaults) and uses the
// context window from pricing data when available.
func (a *OpenRouterAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	var caps models.ModelCapabilities

	// OpenRouter model IDs are "provider/model" — infer from sub-provider.
	if i := strings.Index(a.modelID, "/"); i >= 0 {
		subProvider := a.modelID[:i]
		subModel := a.modelID[i+1:]
		caps = capabilities.DefaultCapabilities(subProvider, subModel)
		caps.ModelID = a.modelID
		caps.Provider = "openrouter"
	} else {
		caps = capabilities.DefaultCapabilities("openrouter", a.modelID)
	}

	// Use context window from pricing data if available (sourced from OpenRouter API).
	if cw := pricing.ContextWindowTokens(a.modelID); cw > 0 {
		caps.ContextWindow = int(cw)
		caps.ContextWindowSource = models.SourceAPI
	}

	return caps
}

func (a *OpenRouterAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	client := openai.NewClient(
		option.WithAPIKey(a.apiKey),
		option.WithBaseURL(openRouterBaseURL),
	)
	return runOpenAIAgentLoop(ctx, &openaiRunConfig{
		client:      client,
		modelID:     a.modelID,
		apiModelID:  a.modelID,
		qualifiedID: a.modelID,
	}, history, prompt, onEvent)
}

func init() {
	var _ models.ModelAdapter = (*OpenRouterAdapter)(nil)
}
