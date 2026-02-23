package models

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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
	ctx context.Context,
	prompt string,
	onEvent func(AgentEvent),
	verbose bool,
) (ModelResponse, error) {
	client := anthropic.NewClient(option.WithAPIKey(a.apiKey))

	toolParams := buildAnthropicTools()
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
	}

	var textParts []string
	var proposed []tools.FileWrite
	var totalInput, totalOutput int64
	var resolvedModel string
	start := time.Now()

	for {
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(a.modelID),
			MaxTokens: 8096,
			Tools:     toolParams,
			Messages:  messages,
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
			resolvedModel = string(resp.Model)
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
				if verbose {
					onEvent(AgentEvent{Type: "text", Data: text})
				}

			case "tool_use":
				tu := block.AsToolUse()
				var input map[string]any
				_ = json.Unmarshal(tu.Input, &input)
				args := extractStringMap(input)

				switch tu.Name {
				case tools.ReadToolName:
					path := args["path"]
					onEvent(AgentEvent{Type: "reading", Data: path})
					content := tools.ExecuteRead(path)
					toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, content, false))

				case tools.WriteToolName:
					path := args["path"]
					onEvent(AgentEvent{Type: "writing", Data: path})
					proposed = append(proposed, tools.FileWrite{Path: path, Content: args["content"]})
					toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, "Write queued — will be applied if selected.", false))
				}
			}
		}

		if resp.StopReason == anthropic.StopReasonEndTurn || len(toolResults) == 0 {
			break
		}

		messages = append(messages, anthropic.NewUserMessage(toolResults...))
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

func buildAnthropicTools() []anthropic.ToolUnionParam {
	var out []anthropic.ToolUnionParam
	for _, def := range tools.Definitions {
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

func extractStringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func join(parts []string) string {
	return strings.Join(parts, "")
}

func init() {
	// Ensure AnthropicAdapter satisfies ModelAdapter at compile time.
	var _ ModelAdapter = (*AnthropicAdapter)(nil)
}
