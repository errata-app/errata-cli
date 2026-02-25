package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
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

func (a *OpenAIAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	client := openai.NewClient(option.WithAPIKey(a.apiKey))

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
		resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(a.modelID),
			Tools:    toolParams,
			Messages: messages,
		})
		if err != nil {
			return BuildErrorResponse(a.modelID, "openai/"+a.modelID, start, totalRegularInput+totalCacheRead, totalOutput, err), err
		}

		if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
			// OpenAI's PromptTokens = total including cached. CachedTokens is a subset.
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

		// Append assistant turn
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

	return BuildSuccessResponse(a.modelID, "openai/"+a.modelID, textParts, start, totalRegularInput, totalCacheRead, 0, totalOutput, proposed), nil
}

func buildOpenAITools(ctx context.Context) []openai.ChatCompletionToolParam {
	var out []openai.ChatCompletionToolParam
	for _, def := range tools.ActiveToolsFromContext(ctx) {
		props := map[string]any{}
		for name, p := range def.Properties {
			props[name] = map[string]any{
				"type":        p.Type,
				"description": p.Description,
			}
		}
		required := make([]string, len(def.Required))
		copy(required, def.Required)

		params := shared.FunctionParameters{
			"type":       "object",
			"properties": props,
			"required":   required,
		}
		fd := shared.FunctionDefinitionParam{
			Name:        def.Name,
			Description: openai.String(def.Description),
			Parameters:  params,
		}
		out = append(out, openai.ChatCompletionToolParam{
			Function: fd,
		})
	}
	return out
}

func init() {
	var _ models.ModelAdapter = (*OpenAIAdapter)(nil)
}
