package adapters

import (
	"context"
	"strings"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/errata-app/errata-cli/internal/capabilities"
	"github.com/errata-app/errata-cli/internal/models"
)

// AzureOpenAIAdapter implements ModelAdapter for Azure OpenAI Service.
//
// Model IDs are configured with an "azure/" prefix (e.g. "azure/gpt-4o").
// The prefix is stripped before the API call; the bare portion is used as
// the Azure deployment name. The full prefixed ID is preserved for display
// and logging.
//
// Set AZURE_OPENAI_API_KEY and AZURE_OPENAI_ENDPOINT to enable this adapter.
// AZURE_OPENAI_API_VERSION defaults to "2024-10-21".
type AzureOpenAIAdapter struct {
	modelID    string // display ID, e.g. "azure/gpt-4o"
	deployName string // stripped prefix; used as Azure deployment name
	apiKey     string
	endpoint   string // e.g. "https://myresource.openai.azure.com"
	apiVersion string // e.g. "2024-10-21"
}

// NewAzureOpenAIAdapter creates an AzureOpenAIAdapter.
func NewAzureOpenAIAdapter(modelID, apiKey, endpoint, apiVersion string) *AzureOpenAIAdapter {
	return &AzureOpenAIAdapter{
		modelID:    modelID,
		deployName: strings.TrimPrefix(modelID, "azure/"),
		apiKey:     apiKey,
		endpoint:   endpoint,
		apiVersion: apiVersion,
	}
}

func (a *AzureOpenAIAdapter) ID() string { return a.modelID }

func (a *AzureOpenAIAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	// Azure hosts OpenAI models — use OpenAI defaults for the underlying model.
	caps := capabilities.DefaultCapabilities("openai", a.deployName)
	caps.ModelID = a.modelID
	caps.Provider = "azure"
	return caps
}

func (a *AzureOpenAIAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	client := openai.NewClient(
		azure.WithEndpoint(a.endpoint, a.apiVersion),
		azure.WithAPIKey(a.apiKey),
	)
	return runOpenAIAgentLoop(ctx, &openaiRunConfig{
		client:      client,
		modelID:     a.modelID,
		apiModelID:  a.deployName,
		qualifiedID: "azure/" + a.deployName,
	}, history, prompt, onEvent)
}

func init() {
	var _ models.ModelAdapter = (*AzureOpenAIAdapter)(nil)
}
