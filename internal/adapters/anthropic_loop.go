package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// anthropicRunConfig parameterises the Anthropic agentic loop.
type anthropicRunConfig struct {
	clientOpts      []option.RequestOption
	modelID         string // display ID (set on ModelResponse)
	qualifiedID     string // provider-prefixed ID for pricing/snapshot (e.g. "anthropic/claude-sonnet-4-6")
	maxOutputTokens int64  // from capabilities; Anthropic API requires this field
}

// runAnthropicAgentLoop is the agentic tool-use loop for the Anthropic adapter.
func runAnthropicAgentLoop(
	ctx context.Context,
	cfg *anthropicRunConfig,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	client := anthropic.NewClient(cfg.clientOpts...)

	systemMsg := tools.SystemPromptSuffix(ctx)

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
	toolCalls := map[string]int{}
	var totalInput, totalOutput int64
	start := time.Now()

	// Anthropic does not support a seed parameter. If a seed is set,
	// use temperature 0 as a best-effort approximation for determinism.
	var temperature *float64
	if _, hasSeed := tools.SeedFromContext(ctx); hasSeed {
		zero := 0.0
		temperature = &zero
	}

	maxSteps := tools.MaxStepsFromContext(ctx)
	step := 0
	for {
		step++
		if maxSteps > 0 && step > maxSteps {
			return BuildMaxStepsResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed, toolCalls), nil
		}
		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(cfg.modelID),
			MaxTokens: cfg.maxOutputTokens,
			Tools:     toolParams,
			Messages:  messages,
		}
		if systemMsg != "" {
			params.System = []anthropic.TextBlockParam{{Text: systemMsg}}
		}
		if temperature != nil {
			params.Temperature = anthropic.Float(*temperature)
		}
		EmitRequest(ctx, onEvent, params)
		resp, err := client.Messages.New(ctx, params)
		if err != nil {
			if ctx.Err() != nil {
				r := BuildInterruptedResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed, toolCalls, err)
				if ctx.Err() == context.DeadlineExceeded {
					r.StopReason = models.StopReasonTimeout
				}
				return r, err
			}
			return BuildErrorResponse(cfg.modelID, cfg.qualifiedID, start, totalInput, totalOutput, err), err
		}
		totalInput += resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens
		totalOutput += resp.Usage.OutputTokens

		// Append assistant turn
		messages = append(messages, resp.ToParam())

		var toolResults []anthropic.ContentBlockParamUnion
		for i := range resp.Content {
			block := &resp.Content[i]
			switch block.Type {
			case "text":
				text := block.AsText().Text
				textParts = append(textParts, text)
				onEvent(models.AgentEvent{Type: models.EventText, Data: text})

			case "tool_use":
				tu := block.AsToolUse()
				var input map[string]any
				if err := json.Unmarshal(tu.Input, &input); err != nil {
					onEvent(models.AgentEvent{Type: models.EventError, Data: fmt.Sprintf("bad tool args for %s: %v", tu.Name, err)})
					toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, fmt.Sprintf("error parsing arguments: %v", err), true))
					continue
				}
				result, ok := DispatchTool(ctx, tu.Name, extractStringMap(input), onEvent, &proposed, &toolCalls)
				if ok {
					toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, result, false))
				} else {
					toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, fmt.Sprintf("error: unrecognized tool %q", tu.Name), true))
				}
			}
		}

		if resp.StopReason == anthropic.StopReasonEndTurn || len(toolResults) == 0 {
			break
		}

		messages = append(messages, anthropic.NewUserMessage(toolResults...))
		EmitSnapshot(onEvent, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed, toolCalls)
	}

	return BuildSuccessResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed, toolCalls), nil
}

// buildAnthropicTools converts the active tool definitions from context into
// Anthropic tool parameters.
func buildAnthropicTools(ctx context.Context) []anthropic.ToolUnionParam {
	active := tools.ActiveToolsFromContext(ctx)
	if len(active) == 0 {
		return nil
	}
	out := make([]anthropic.ToolUnionParam, 0, len(active))
	for _, def := range active {
		props, required := def.JSONSchemaProps()

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
