package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/suarezc/errata/internal/capabilities"
	"github.com/suarezc/errata/internal/models"
	promptpkg "github.com/suarezc/errata/internal/prompt"
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

func (a *AnthropicAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return capabilities.DefaultCapabilities("anthropic", a.modelID)
}

func (a *AnthropicAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	client := anthropic.NewClient(option.WithAPIKey(a.apiKey))

	// Resolve system prompt: prefer payload from context, fall back to built-in.
	var systemMsg string
	var toolDescOverrides map[string]string
	if payload, ok := promptpkg.PayloadFromContext(ctx, a.modelID); ok {
		systemMsg = promptpkg.BuildSystemMessage(payload, tools.SystemPromptGuidance())
		toolDescOverrides = payload.ToolDescriptions
	} else {
		systemMsg = tools.SystemPromptSuffix()
	}

	toolParams := buildAnthropicTools(ctx, toolDescOverrides)
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
	var totalRegularInput, totalOutput, totalCacheRead, totalCacheCreation int64
	start := time.Now()

	// Anthropic does not support a seed parameter. If a seed is set,
	// use temperature 0 as a best-effort approximation for determinism.
	var temperature *float64
	if _, hasSeed := tools.SeedFromContext(ctx); hasSeed {
		zero := 0.0
		temperature = &zero
	}

	for {
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(a.modelID),
			MaxTokens: 8096,
			System:    []anthropic.TextBlockParam{{Text: systemMsg}},
			Tools:     toolParams,
			Messages:  messages,
		}
		if temperature != nil {
			params.Temperature = anthropic.Float(*temperature)
		}
		resp, err := client.Messages.New(ctx, params)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(a.modelID, "anthropic/"+a.modelID, textParts, start, totalRegularInput+totalCacheRead+totalCacheCreation, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(a.modelID, "anthropic/"+a.modelID, start, totalRegularInput+totalCacheRead+totalCacheCreation, totalOutput, err), err
		}
		// Anthropic's InputTokens = regular (non-cached) tokens only.
		// CacheReadInputTokens and CacheCreationInputTokens are separate categories.
		totalRegularInput += resp.Usage.InputTokens
		totalOutput += resp.Usage.OutputTokens
		totalCacheRead += resp.Usage.CacheReadInputTokens
		totalCacheCreation += resp.Usage.CacheCreationInputTokens

		// Append assistant turn
		messages = append(messages, resp.ToParam())

		var toolResults []anthropic.ContentBlockParamUnion
		for i := range resp.Content {
			block := &resp.Content[i]
			switch block.Type {
			case "text":
				text := block.AsText().Text
				textParts = append(textParts, text)
				onEvent(models.AgentEvent{Type: "text", Data: text})

			case "tool_use":
				tu := block.AsToolUse()
				var input map[string]any
				if err := json.Unmarshal(tu.Input, &input); err != nil {
					onEvent(models.AgentEvent{Type: "error", Data: fmt.Sprintf("bad tool args for %s: %v", tu.Name, err)})
					toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, fmt.Sprintf("error parsing arguments: %v", err), true))
					continue
				}
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
		EmitSnapshot(onEvent, "anthropic/"+a.modelID, textParts, start, totalRegularInput+totalCacheRead+totalCacheCreation, totalOutput, proposed)
	}

	return BuildSuccessResponse(a.modelID, "anthropic/"+a.modelID, textParts, start, totalRegularInput, totalCacheRead, totalCacheCreation, totalOutput, proposed), nil
}

func buildAnthropicTools(ctx context.Context, descOverrides map[string]string) []anthropic.ToolUnionParam {
	active := tools.ActiveToolsFromContext(ctx)
	out := make([]anthropic.ToolUnionParam, 0, len(active))
	for _, def := range active {
		props := map[string]any{}
		for name, p := range def.Properties {
			props[name] = map[string]any{
				"type":        p.Type,
				"description": p.Description,
			}
		}
		required := make([]string, len(def.Required))
		copy(required, def.Required)

		desc := def.Description
		if d, ok := descOverrides[def.Name]; ok {
			desc = d
		}

		tp := anthropic.ToolParam{
			Name:        def.Name,
			Description: anthropic.String(desc),
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
