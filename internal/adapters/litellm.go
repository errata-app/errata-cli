package adapters

import (
	"context"
	"strings"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/errata-app/errata-cli/internal/capabilities"
	"github.com/errata-app/errata-cli/internal/models"
)

// LiteLLMAdapter implements ModelAdapter for a LiteLLM proxy using its
// OpenAI-compatible API.
//
// Model IDs are configured with a "litellm/" prefix to distinguish them from
// native adapters (e.g. "litellm/claude-sonnet-4-6"). The prefix is stripped
// before the API call; the full prefixed ID is preserved for display and logging.
//
// Set LITELLM_BASE_URL to the proxy base URL including the /v1 path
// (e.g. "http://localhost:4000/v1"). LITELLM_API_KEY is optional.
type LiteLLMAdapter struct {
	modelID     string // full ID as configured, e.g. "litellm/claude-sonnet-4-6"
	bareModelID string // modelID with "litellm/" stripped; sent to the API
	apiKey      string
	baseURL     string
}

// NewLiteLLMAdapter creates a LiteLLMAdapter.
func NewLiteLLMAdapter(modelID, apiKey, baseURL string) *LiteLLMAdapter {
	return &LiteLLMAdapter{
		modelID:     modelID,
		bareModelID: strings.TrimPrefix(modelID, "litellm/"),
		apiKey:      apiKey,
		baseURL:     baseURL,
	}
}

func (a *LiteLLMAdapter) ID() string { return a.modelID }

func (a *LiteLLMAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return capabilities.DefaultCapabilities("litellm", a.modelID)
}

func (a *LiteLLMAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	opts := []option.RequestOption{
		option.WithBaseURL(a.baseURL),
	}
	if a.apiKey != "" {
		opts = append(opts, option.WithAPIKey(a.apiKey))
	} else {
		opts = append(opts, option.WithAPIKey("litellm")) // placeholder; some deployments require non-empty
	}
	client := openai.NewClient(opts...)

	return runOpenAIAgentLoop(ctx, &openaiRunConfig{
		client:      client,
		modelID:     a.modelID,
		apiModelID:  a.bareModelID,
		qualifiedID: a.bareModelID,
	}, history, prompt, onEvent)
}

func init() {
	var _ models.ModelAdapter = (*LiteLLMAdapter)(nil)
}
