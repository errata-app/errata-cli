package adapters

import (
	"context"
	"encoding/json"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// AnthropicAdapter implements ModelAdapter for Anthropic Claude models.
type AnthropicAdapter struct {
	modelID string
	apiKey  string
}

// NewAnthropicAdapter creates an AnthropicAdapter.
func NewAnthropicAdapter(modelID, apiKey string) *AnthropicAdapter {
	return &AnthropicAdapter{modelID: modelID, apiKey: apiKey}
}

func (a *AnthropicAdapter) ID() string { return a.modelID }

func (a *AnthropicAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	client := anthropic.NewClient(option.WithAPIKey(a.apiKey))

	toolParams := buildAnthropicTools(ctx)
	messages := make([]anthropic.MessageParam, 0, len(history)+1)
	for _, turn := range history {
		switch turn.Role {
		case "user":
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(turn.Content)))
		case "assistant":
			messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(turn.Content)))
		}
	}
	messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)))

	var textParts []string
	var proposed []tools.FileWrite
	var totalInput, totalOutput int64
	start := time.Now()

	for {
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(a.modelID),
			MaxTokens: 8096,
			System:    []anthropic.TextBlockParam{{Text: tools.SystemPromptSuffix()}},
			Tools:     toolParams,
			Messages:  messages,
		})
		if err != nil {
			return BuildErrorResponse(a.modelID, "anthropic/"+a.modelID, start, totalInput, totalOutput, err), err
		}
		totalInput += resp.Usage.InputTokens
		totalOutput += resp.Usage.OutputTokens

		// Append assistant turn
		messages = append(messages, resp.ToParam())

		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				text := block.AsText().Text
				textParts = append(textParts, text)
				onEvent(models.AgentEvent{Type: "text", Data: text})

			case "tool_use":
				tu := block.AsToolUse()
				var input map[string]any
				_ = json.Unmarshal(tu.Input, &input)
				result, ok := DispatchTool(ctx, tu.Name, extractStringMap(input), onEvent, &proposed)
				if ok {
					toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, result, false))
				}
			}
		}

		if resp.StopReason == anthropic.StopReasonEndTurn || len(toolResults) == 0 {
			break
		}

		messages = append(messages, anthropic.NewUserMessage(toolResults...))
	}

	return BuildSuccessResponse(a.modelID, "anthropic/"+a.modelID, textParts, start, totalInput, totalOutput, proposed), nil
}

func buildAnthropicTools(ctx context.Context) []anthropic.ToolUnionParam {
	var out []anthropic.ToolUnionParam
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

		tp := anthropic.ToolParam{
			Name:        def.Name,
			Description: anthropic.String(def.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: props,
				Required:   required,
			},
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out
}

func init() {
	// Ensure AnthropicAdapter satisfies ModelAdapter at compile time.
	var _ models.ModelAdapter = (*AnthropicAdapter)(nil)
}
