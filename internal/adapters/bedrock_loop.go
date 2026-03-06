package adapters

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// bedrockConverser abstracts the Bedrock Converse API for testing.
// The real *bedrockruntime.Client satisfies this implicitly.
type bedrockConverser interface {
	Converse(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// bedrockRunConfig parameterises the Bedrock agentic loop.
type bedrockRunConfig struct {
	client      bedrockConverser
	modelID     string // full display ID (e.g. "bedrock/anthropic.claude-sonnet-4-20250514-v1:0")
	bareModelID string // modelID with "bedrock/" stripped; sent to the Converse API
	qualifiedID string // pricing-compatible ID (e.g. "anthropic/claude-sonnet-4-20250514")
}

// runBedrockAgentLoop is the agentic tool-use loop for the Bedrock adapter.
func runBedrockAgentLoop(
	ctx context.Context,
	cfg *bedrockRunConfig,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	start := time.Now()

	systemMsg := tools.SystemPromptSuffix(ctx)

	toolConfig := buildBedrockToolConfig(ctx)

	// Build message history.
	messages := make([]bedrocktypes.Message, 0, len(history)+1)
	for _, turn := range history {
		var role bedrocktypes.ConversationRole
		switch turn.Role {
		case "user":
			role = bedrocktypes.ConversationRoleUser
		case "assistant":
			role = bedrocktypes.ConversationRoleAssistant
		default:
			continue
		}
		messages = append(messages, bedrocktypes.Message{
			Role: role,
			Content: []bedrocktypes.ContentBlock{
				&bedrocktypes.ContentBlockMemberText{Value: turn.Content},
			},
		})
	}
	messages = append(messages, bedrocktypes.Message{
		Role: bedrocktypes.ConversationRoleUser,
		Content: []bedrocktypes.ContentBlock{
			&bedrocktypes.ContentBlockMemberText{Value: prompt},
		},
	})

	var systemBlocks []bedrocktypes.SystemContentBlock
	if systemMsg != "" {
		systemBlocks = []bedrocktypes.SystemContentBlock{
			&bedrocktypes.SystemContentBlockMemberText{Value: systemMsg},
		}
	}

	var textParts []string
	var proposed []tools.FileWrite
	toolCalls := map[string]int{}
	var totalInput, totalOutput int64

	maxSteps := tools.MaxStepsFromContext(ctx)
	step := 0
	for {
		step++
		if maxSteps > 0 && step > maxSteps {
			r := BuildMaxStepsResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, 0, proposed, toolCalls)
			r.Steps = step - 1
			return r, nil
		}
		input := &bedrockruntime.ConverseInput{
			ModelId:    aws.String(cfg.bareModelID),
			Messages:   messages,
			System:     systemBlocks,
			ToolConfig: toolConfig,
		}
		// Approximate reproducibility via temperature=0 when seed is set.
		if _, ok := tools.SeedFromContext(ctx); ok {
			zero := float32(0)
			input.InferenceConfig = &bedrocktypes.InferenceConfiguration{
				Temperature: &zero,
			}
		}

		EmitRequest(ctx, onEvent, input)
		resp, err := cfg.client.Converse(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				r := BuildInterruptedResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, 0, proposed, toolCalls, err)
				if ctx.Err() == context.DeadlineExceeded {
					r.StopReason = models.StopReasonTimeout
				}
				r.Steps = step
				return r, err
			}
			r := BuildErrorResponse(cfg.modelID, cfg.qualifiedID, start, totalInput, totalOutput, 0, err)
			r.Steps = step
			return r, err
		}

		// Accumulate token usage (nil-checked *int32 → int64).
		if resp.Usage != nil {
			if resp.Usage.InputTokens != nil {
				totalInput += int64(*resp.Usage.InputTokens)
			}
			if resp.Usage.OutputTokens != nil {
				totalOutput += int64(*resp.Usage.OutputTokens)
			}
		}

		// Extract assistant message.
		outputMsg, ok := resp.Output.(*bedrocktypes.ConverseOutputMemberMessage)
		if !ok {
			break
		}
		assistantMsg := outputMsg.Value
		messages = append(messages, assistantMsg)

		// Process content blocks.
		var toolResults []bedrocktypes.ContentBlock
		for _, block := range assistantMsg.Content {
			switch b := block.(type) {
			case *bedrocktypes.ContentBlockMemberText:
				textParts = append(textParts, b.Value)
				onEvent(models.AgentEvent{Type: models.EventText, Data: b.Value})

			case *bedrocktypes.ContentBlockMemberToolUse:
				toolUse := b.Value

				// Unmarshal tool input from Smithy document.
				var inputMap map[string]any
				if toolUse.Input != nil {
					if err := toolUse.Input.UnmarshalSmithyDocument(&inputMap); err != nil {
						log.Printf("warning: failed to unmarshal Bedrock tool input for %s: %v", aws.ToString(toolUse.Name), err)
					}
				}
				if inputMap == nil {
					inputMap = map[string]any{}
				}

				result, dispatched := DispatchTool(ctx, aws.ToString(toolUse.Name), extractStringMap(inputMap), onEvent, &proposed, &toolCalls)
				if !dispatched {
					result = fmt.Sprintf("error: unrecognized tool %q", aws.ToString(toolUse.Name))
				}
				status := bedrocktypes.ToolResultStatusSuccess
				if strings.HasPrefix(result, "error:") || strings.HasPrefix(result, "[mcp error:") {
					status = bedrocktypes.ToolResultStatusError
				}
				toolResults = append(toolResults, &bedrocktypes.ContentBlockMemberToolResult{
					Value: bedrocktypes.ToolResultBlock{
						ToolUseId: toolUse.ToolUseId,
						Content: []bedrocktypes.ToolResultContentBlock{
							&bedrocktypes.ToolResultContentBlockMemberText{Value: result},
						},
						Status: status,
					},
				})
			}
		}

		// Check exit condition: no tool calls or stop reason is not tool_use.
		if len(toolResults) == 0 || resp.StopReason != bedrocktypes.StopReasonToolUse {
			break
		}

		// Send tool results as a user message.
		messages = append(messages, bedrocktypes.Message{
			Role:    bedrocktypes.ConversationRoleUser,
			Content: toolResults,
		})
		EmitSnapshot(onEvent, cfg.qualifiedID, textParts, start, totalInput, totalOutput, 0, proposed, toolCalls)
	}

	r := BuildSuccessResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, 0, proposed, toolCalls)
	r.Steps = step
	return r, nil
}

// buildBedrockToolConfig translates active tool definitions into Bedrock's ToolConfiguration.
func buildBedrockToolConfig(ctx context.Context) *bedrocktypes.ToolConfiguration {
	defs := tools.ActiveToolsFromContext(ctx)
	if len(defs) == 0 {
		return nil
	}

	bedrockTools := make([]bedrocktypes.Tool, 0, len(defs))
	for _, def := range defs {
		props, required := def.JSONSchemaProps()

		desc := def.Description

		schema := map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		}

		toolName := def.Name
		bedrockTools = append(bedrockTools, &bedrocktypes.ToolMemberToolSpec{
			Value: bedrocktypes.ToolSpecification{
				Name:        &toolName,
				Description: &desc,
				InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{
					Value: bedrockdocument.NewLazyDocument(schema),
				},
			},
		})
	}

	return &bedrocktypes.ToolConfiguration{
		Tools: bedrockTools,
		ToolChoice: &bedrocktypes.ToolChoiceMemberAuto{
			Value: bedrocktypes.AutoToolChoice{},
		},
	}
}
