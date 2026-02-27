package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/azure"
	"github.com/suarezc/errata/internal/capabilities"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
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

	qualifiedID := "azure/" + a.deployName

	systemMsg := tools.SystemPromptSuffix()

	toolParams := buildOpenAITools(ctx)
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)+2)
	messages = append(messages, openai.SystemMessage(systemMsg))
	for _, turn := range history {
		switch turn.Role {
		case "user":
			messages = append(messages, openai.UserMessage(turn.Content))
		case "assistant":
			messages = append(messages, openai.ChatCompletionMessage{Role: "assistant", Content: turn.Content}.ToParam())
		}
	}
	messages = append(messages, openai.UserMessage(prompt))

	var textParts []string
	var proposed []tools.FileWrite
	var totalRegularInput, totalOutput, totalCacheRead int64
	start := time.Now()

	for {
		params := openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(a.deployName),
			Tools:    toolParams,
			Messages: messages,
		}
		if seed, ok := tools.SeedFromContext(ctx); ok {
			params.Seed = openai.Int(seed)
		}
		resp, err := client.Chat.Completions.New(ctx, params)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(a.modelID, qualifiedID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(a.modelID, qualifiedID, start, totalRegularInput+totalCacheRead, totalOutput, err), err
		}

		if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
			// OpenAI-compat API: PromptTokens = total including cached. CachedTokens is a subset.
			cached := resp.Usage.PromptTokensDetails.CachedTokens
			totalRegularInput += resp.Usage.PromptTokens - cached
			totalOutput += resp.Usage.CompletionTokens
			totalCacheRead += cached
		}

		if len(resp.Choices) == 0 {
			break
		}
		choice := resp.Choices[0]
		msg := choice.Message

		messages = append(messages, msg.ToParam())

		if msg.Content != "" {
			textParts = append(textParts, msg.Content)
			onEvent(models.AgentEvent{Type: "text", Data: msg.Content})
		}

		if len(msg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			break
		}

		for _, tc := range msg.ToolCalls {
			var input map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				onEvent(models.AgentEvent{Type: "error", Data: fmt.Sprintf("bad tool args for %s: %v", tc.Function.Name, err)})
				messages = append(messages, openai.ToolMessage(fmt.Sprintf("error parsing arguments: %v", err), tc.ID))
				continue
			}
			result, ok := DispatchTool(ctx, tc.Function.Name, extractStringMap(input), onEvent, &proposed)
			if ok {
				messages = append(messages, openai.ToolMessage(result, tc.ID))
			}
		}
		EmitSnapshot(onEvent, qualifiedID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed)
	}

	return BuildSuccessResponse(a.modelID, qualifiedID, textParts, start, totalRegularInput, totalCacheRead, 0, totalOutput, proposed), nil
}

func init() {
	var _ models.ModelAdapter = (*AzureOpenAIAdapter)(nil)
}
