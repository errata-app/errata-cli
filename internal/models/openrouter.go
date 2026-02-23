package models

import (
	"context"
	"encoding/json"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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

func (a *OpenRouterAdapter) RunAgent(
	ctx     context.Context,
	history []ConversationTurn,
	prompt  string,
	onEvent func(AgentEvent),
) (ModelResponse, error) {
	client := openai.NewClient(
		option.WithAPIKey(a.apiKey),
		option.WithBaseURL(openRouterBaseURL),
	)

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
	var resolvedModel string
	start := time.Now()

	for {
		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(a.modelID),
			Tools:    toolParams,
			Messages: messages,
		})
		if err != nil {
			return ModelResponse{
				ModelID:      a.modelID,
				LatencyMS:    time.Since(start).Milliseconds(),
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
				CostUSD:      CostUSD(a.modelID, totalInput, totalOutput),
				Error:        err.Error(),
			}, err
		}

		if resolvedModel == "" {
			resolvedModel = resp.Model
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
			onEvent(AgentEvent{Type: "text", Data: msg.Content})
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
				onEvent(AgentEvent{Type: "reading", Data: path})
				content := tools.ExecuteRead(path)
				messages = append(messages, openai.ToolMessage(content, tc.ID))

			case tools.WriteToolName:
				path := args["path"]
				onEvent(AgentEvent{Type: "writing", Data: path})
				proposed = append(proposed, tools.FileWrite{Path: path, Content: args["content"]})
				messages = append(messages, openai.ToolMessage("Write queued — will be applied if selected.", tc.ID))
			}
		}
	}

	if resolvedModel == "" {
		resolvedModel = a.modelID
	}
	return ModelResponse{
		ModelID:        resolvedModel,
		Text:           join(textParts),
		LatencyMS:      time.Since(start).Milliseconds(),
		InputTokens:    totalInput,
		OutputTokens:   totalOutput,
		CostUSD:        CostUSD(a.modelID, totalInput, totalOutput),
		ProposedWrites: proposed,
	}, nil
}

func init() {
	var _ ModelAdapter = (*OpenRouterAdapter)(nil)
}
