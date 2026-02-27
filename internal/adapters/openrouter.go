package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/suarezc/errata/internal/capabilities"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/tools"
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
			Model:    openai.ChatModel(a.modelID),
			Tools:    toolParams,
			Messages: messages,
		}
		if seed, ok := tools.SeedFromContext(ctx); ok {
			params.Seed = openai.Int(seed)
		}
		resp, err := client.Chat.Completions.New(ctx, params)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(a.modelID, a.modelID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(a.modelID, a.modelID, start, totalRegularInput+totalCacheRead, totalOutput, err), err
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
		EmitSnapshot(onEvent, a.modelID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed)
	}

	return BuildSuccessResponse(a.modelID, a.modelID, textParts, start, totalRegularInput, totalCacheRead, 0, totalOutput, proposed), nil
}

func init() {
	var _ models.ModelAdapter = (*OpenRouterAdapter)(nil)
}
