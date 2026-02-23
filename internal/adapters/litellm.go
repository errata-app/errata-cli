package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
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

	toolParams := buildOpenAITools()
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)+1)
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
	var totalInput, totalOutput int64
	start := time.Now()

	for {
		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(a.bareModelID),
			Tools:    toolParams,
			Messages: messages,
		})
		if err != nil {
			return models.ModelResponse{
				ModelID:      a.modelID,
				LatencyMS:    time.Since(start).Milliseconds(),
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
				CostUSD:      pricing.CostUSD(a.bareModelID, totalInput, totalOutput),
				Error:        err.Error(),
			}, err
		}

		if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
			totalInput += resp.Usage.PromptTokens
			totalOutput += resp.Usage.CompletionTokens
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
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			args := extractStringMap(input)

			switch tc.Function.Name {
			case tools.ReadToolName:
				path := args["path"]
				onEvent(models.AgentEvent{Type: "reading", Data: path})
				content := tools.ExecuteRead(path)
				messages = append(messages, openai.ToolMessage(content, tc.ID))

			case tools.WriteToolName:
				path := args["path"]
				onEvent(models.AgentEvent{Type: "writing", Data: path})
				proposed = append(proposed, tools.FileWrite{Path: path, Content: args["content"]})
				messages = append(messages, openai.ToolMessage("Write queued — will be applied if selected.", tc.ID))
			}
		}
	}

	return models.ModelResponse{
		ModelID:        a.modelID,
		Text:           join(textParts),
		LatencyMS:      time.Since(start).Milliseconds(),
		InputTokens:    totalInput,
		OutputTokens:   totalOutput,
		CostUSD:        pricing.CostUSD(a.bareModelID, totalInput, totalOutput),
		ProposedWrites: proposed,
	}, nil
}

func init() {
	var _ models.ModelAdapter = (*LiteLLMAdapter)(nil)
}
