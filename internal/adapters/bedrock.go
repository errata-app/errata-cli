package adapters

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/suarezc/errata/internal/capabilities"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// BedrockAdapter implements ModelAdapter for Amazon Bedrock using the Converse API.
//
// Model IDs are configured with a "bedrock/" prefix (e.g. "bedrock/anthropic.claude-sonnet-4-20250514-v1:0").
// The prefix is stripped before the API call; the full prefixed ID is preserved for display and logging.
//
// Authentication uses the AWS SDK default credential chain:
//   - AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY environment variables
//   - AWS_PROFILE for named profiles in ~/.aws/credentials
//   - EC2/ECS/Lambda instance roles
//
// Set AWS_REGION (or AWS_DEFAULT_REGION) to specify the Bedrock endpoint region.
type BedrockAdapter struct {
	modelID     string // full ID as configured, e.g. "bedrock/anthropic.claude-sonnet-4-20250514-v1:0"
	bareModelID string // modelID with "bedrock/" stripped; sent to the Converse API
	region      string // AWS region for the Bedrock endpoint
}

// NewBedrockAdapter creates a BedrockAdapter.
func NewBedrockAdapter(modelID, region string) *BedrockAdapter {
	return &BedrockAdapter{
		modelID:     modelID,
		bareModelID: strings.TrimPrefix(modelID, "bedrock/"),
		region:      region,
	}
}

func (a *BedrockAdapter) ID() string { return a.modelID }

// Capabilities infers defaults from the sub-provider in the Bedrock model ID
// (e.g. "anthropic.claude-*" → anthropic defaults).
func (a *BedrockAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	// Bedrock model IDs use "provider.model" format — infer sub-provider.
	if i := strings.Index(a.bareModelID, "."); i >= 0 {
		subProvider := a.bareModelID[:i]
		caps := capabilities.DefaultCapabilities(subProvider, a.bareModelID)
		caps.ModelID = a.modelID
		caps.Provider = "bedrock"
		return caps
	}
	return capabilities.DefaultCapabilities("bedrock", a.bareModelID)
}

func (a *BedrockAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	start := time.Now()
	qualifiedID := bedrockQualifiedID(a.bareModelID)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(a.region))
	if err != nil {
		return BuildErrorResponse(a.modelID, qualifiedID, start, 0, 0, err), err
	}
	client := bedrockruntime.NewFromConfig(awsCfg)

	systemMsg := tools.SystemPromptSuffix()

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

	systemBlocks := []bedrocktypes.SystemContentBlock{
		&bedrocktypes.SystemContentBlockMemberText{Value: systemMsg},
	}

	var textParts []string
	var proposed []tools.FileWrite
	var totalRegularInput, totalOutput, totalCacheRead int64

	for {
		input := &bedrockruntime.ConverseInput{
			ModelId:    aws.String(a.bareModelID),
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

		resp, err := client.Converse(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(a.modelID, qualifiedID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(a.modelID, qualifiedID, start, totalRegularInput+totalCacheRead, totalOutput, err), err
		}

		// Accumulate token usage (nil-checked *int32 → int64).
		if resp.Usage != nil {
			if resp.Usage.InputTokens != nil {
				totalRegularInput += int64(*resp.Usage.InputTokens)
			}
			if resp.Usage.OutputTokens != nil {
				totalOutput += int64(*resp.Usage.OutputTokens)
			}
			if resp.Usage.CacheReadInputTokens != nil {
				cr := int64(*resp.Usage.CacheReadInputTokens)
				totalCacheRead += cr
				// CacheReadInputTokens is a subset of InputTokens — subtract to get regular.
				totalRegularInput -= cr
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
				onEvent(models.AgentEvent{Type: "text", Data: b.Value})

			case *bedrocktypes.ContentBlockMemberToolUse:
				toolUse := b.Value

				// Unmarshal tool input from Smithy document.
				var inputMap map[string]any
				if toolUse.Input != nil {
					_ = toolUse.Input.UnmarshalSmithyDocument(&inputMap)
				}
				if inputMap == nil {
					inputMap = map[string]any{}
				}

				result, dispatched := DispatchTool(ctx, aws.ToString(toolUse.Name), extractStringMap(inputMap), onEvent, &proposed)
				if dispatched {
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
		EmitSnapshot(onEvent, qualifiedID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed)
	}

	return BuildSuccessResponse(a.modelID, qualifiedID, textParts, start, totalRegularInput, totalCacheRead, 0, totalOutput, proposed), nil
}

// buildBedrockToolConfig translates active tool definitions into Bedrock's ToolConfiguration.
func buildBedrockToolConfig(ctx context.Context) *bedrocktypes.ToolConfiguration {
	defs := tools.ActiveToolsFromContext(ctx)
	if len(defs) == 0 {
		return nil
	}

	bedrockTools := make([]bedrocktypes.Tool, 0, len(defs))
	for _, def := range defs {
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

func init() {
	var _ models.ModelAdapter = (*BedrockAdapter)(nil)
}

// bedrockQualifiedID returns a pricing-compatible qualified ID for a Bedrock model.
// It maps provider-prefixed Bedrock model IDs to the OpenRouter-style "provider/model" format.
// E.g. "anthropic.claude-sonnet-4-20250514-v1:0" → "anthropic/claude-sonnet-4-20250514-v1:0".
func bedrockQualifiedID(bareModelID string) string {
	if provider, model, ok := strings.Cut(bareModelID, "."); ok {
		// Strip Bedrock version suffix (":0", ":1") for pricing lookup.
		if j := strings.LastIndex(model, ":"); j >= 0 {
			model = model[:j]
		}
		// Strip Bedrock "-v1" suffix.
		if strings.HasSuffix(model, "-v1") || strings.HasSuffix(model, "-v2") {
			model = model[:len(model)-3]
		}
		return provider + "/" + model
	}
	return bareModelID
}

