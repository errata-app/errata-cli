package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// openaiRunConfig parameterises the shared OpenAI-compatible agentic loop.
type openaiRunConfig struct {
	client      openai.Client
	modelID     string // display ID (set on ModelResponse)
	apiModelID  string // sent to the API as ChatModel
	qualifiedID string // provider-prefixed ID for pricing/snapshot
}

// runOpenAIAgentLoop is the shared agentic tool-use loop used by all four
// OpenAI-compatible adapters (OpenAI, OpenRouter, LiteLLM, AzureOpenAI).
func runOpenAIAgentLoop(
	ctx context.Context,
	cfg *openaiRunConfig,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	systemMsg := tools.SystemPromptSuffix(ctx)

	toolParams := buildOpenAITools(ctx)
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)+2)
	if systemMsg != "" {
		messages = append(messages, openai.SystemMessage(systemMsg))
	}
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
		params := openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(cfg.apiModelID),
			Tools:    toolParams,
			Messages: messages,
		}
		if seed, ok := tools.SeedFromContext(ctx); ok {
			params.Seed = openai.Int(seed)
		}
		EmitRequest(ctx, onEvent, params)
		resp, err := cfg.client.Chat.Completions.New(ctx, params)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(cfg.modelID, cfg.qualifiedID, start, totalInput, totalOutput, err), err
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
			onEvent(models.AgentEvent{Type: models.EventText, Data: msg.Content})
		}

		if len(msg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			break
		}

		for _, tc := range msg.ToolCalls {
			var input map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
				onEvent(models.AgentEvent{Type: models.EventError, Data: fmt.Sprintf("bad tool args for %s: %v", tc.Function.Name, err)})
				messages = append(messages, openai.ToolMessage(fmt.Sprintf("error parsing arguments: %v", err), tc.ID))
				continue
			}
			result, ok := DispatchTool(ctx, tc.Function.Name, extractStringMap(input), onEvent, &proposed)
			if ok {
				messages = append(messages, openai.ToolMessage(result, tc.ID))
			} else {
				messages = append(messages, openai.ToolMessage(fmt.Sprintf("error: unrecognized tool %q", tc.Function.Name), tc.ID))
			}
		}
		EmitSnapshot(onEvent, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed)
	}

	return BuildSuccessResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed), nil
}

// buildOpenAITools converts the active tool definitions from context into
// OpenAI-compatible tool parameters.
func buildOpenAITools(ctx context.Context) []openai.ChatCompletionToolParam {
	active := tools.ActiveToolsFromContext(ctx)
	if len(active) == 0 {
		return nil
	}
	out := make([]openai.ChatCompletionToolParam, 0, len(active))
	for _, def := range active {
		props, required := def.JSONSchemaProps()

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
