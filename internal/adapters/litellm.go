package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
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

	toolParams := buildOpenAITools(ctx)
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)+2)
	messages = append(messages, openai.SystemMessage(tools.SystemPromptSuffix()))
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
			Model:    openai.ChatModel(a.bareModelID),
			Tools:    toolParams,
			Messages: messages,
		}
		if seed, ok := tools.SeedFromContext(ctx); ok {
			params.Seed = openai.Int(seed)
		}
		resp, err := client.Chat.Completions.New(ctx, params)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(a.modelID, a.bareModelID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(a.modelID, a.bareModelID, start, totalRegularInput+totalCacheRead, totalOutput, err), err
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
	}

	return BuildSuccessResponse(a.modelID, a.bareModelID, textParts, start, totalRegularInput, totalCacheRead, 0, totalOutput, proposed), nil
}

func init() {
	var _ models.ModelAdapter = (*LiteLLMAdapter)(nil)
}
