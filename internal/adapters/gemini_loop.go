package adapters

import (
	"context"
	"log"
	"math"
	"time"

	"google.golang.org/genai"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// geminiRunConfig parameterises the shared Gemini agentic loop.
type geminiRunConfig struct {
	client      *genai.Client
	modelID     string // display ID (set on ModelResponse)
	apiModelID  string // sent to the API
	qualifiedID string // provider-prefixed ID for pricing/snapshot
}

// runGeminiAgentLoop is the shared agentic tool-use loop used by both
// Gemini (API key) and Vertex AI (ADC) adapters.
func runGeminiAgentLoop(
	ctx context.Context,
	cfg geminiRunConfig,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	start := time.Now()

	systemMsg := tools.SystemPromptSuffix()

	config := &genai.GenerateContentConfig{
		Tools:             buildGeminiTools(ctx),
		SystemInstruction: genai.NewContentFromText(systemMsg, ""),
	}
	if seed, ok := tools.SeedFromContext(ctx); ok {
		s := int32(min(max(seed, math.MinInt32), math.MaxInt32)) //nolint:gosec // G115: overflow prevented by min/max clamping
		config.Seed = &s
	}

	contents := make([]*genai.Content, 0, len(history)+1)
	for _, turn := range history {
		switch turn.Role {
		case "user":
			contents = append(contents, genai.NewContentFromText(turn.Content, genai.RoleUser))
		case "assistant":
			contents = append(contents, genai.NewContentFromText(turn.Content, "model"))
		}
	}
	contents = append(contents, genai.NewContentFromText(prompt, genai.RoleUser))

	var textParts []string
	var proposed []tools.FileWrite
	var totalInput, totalOutput int64

	for {
		EmitRequest(ctx, onEvent, struct {
			Model    string                       `json:"model"`
			Contents []*genai.Content              `json:"contents"`
			Config   *genai.GenerateContentConfig  `json:"config"`
		}{cfg.apiModelID, contents, config})
		resp, err := cfg.client.Models.GenerateContent(ctx, cfg.apiModelID, contents, config)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(cfg.modelID, cfg.qualifiedID, start, totalInput, totalOutput, err), err
		}

		if resp.UsageMetadata != nil {
			totalInput += int64(resp.UsageMetadata.PromptTokenCount)
			totalOutput += int64(resp.UsageMetadata.CandidatesTokenCount)
		}

		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			break
		}
		modelContent := resp.Candidates[0].Content
		contents = append(contents, modelContent)

		var toolResults []*genai.Part
		for _, part := range modelContent.Parts {
			if part.Text != "" {
				textParts = append(textParts, part.Text)
				onEvent(models.AgentEvent{Type: models.EventText, Data: part.Text})
			}

			if part.FunctionCall != nil {
				fc := part.FunctionCall
				result, ok := DispatchTool(ctx, fc.Name, extractStringMap(fc.Args), onEvent, &proposed)
				if ok {
					toolResults = append(toolResults, genai.NewPartFromFunctionResponse(fc.Name, map[string]any{"result": result}))
				}
			}
		}

		if len(toolResults) == 0 {
			break
		}
		contents = append(contents, genai.NewContentFromParts(toolResults, genai.RoleUser))
		EmitSnapshot(onEvent, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed)
	}

	return BuildSuccessResponse(cfg.modelID, cfg.qualifiedID, textParts, start, totalInput, totalOutput, proposed), nil
}

// queryGeminiCapabilities queries the Gemini/Vertex models API for token
// limits and merges them into the base capabilities.
func queryGeminiCapabilities(ctx context.Context, client *genai.Client, apiModelID string, baseCaps models.ModelCapabilities) models.ModelCapabilities {
	info, err := client.Models.Get(ctx, apiModelID, nil)
	if err != nil {
		log.Printf("capabilities: Gemini API lookup failed for %s: %v", apiModelID, err)
		return baseCaps
	}

	if info.InputTokenLimit > 0 {
		baseCaps.ContextWindow = int(info.InputTokenLimit)
		baseCaps.ContextWindowSource = models.SourceAPI
	}
	if info.OutputTokenLimit > 0 {
		baseCaps.MaxOutputTokens = int(info.OutputTokenLimit)
	}
	return baseCaps
}

// buildGeminiTools converts the active tool definitions from context into
// Gemini FunctionDeclarations.
func buildGeminiTools(ctx context.Context) []*genai.Tool {
	active := tools.ActiveToolsFromContext(ctx)
	decls := make([]*genai.FunctionDeclaration, 0, len(active))
	for _, def := range active {
		props := map[string]*genai.Schema{}
		for name, p := range def.Properties {
			schemaType := genai.TypeString
			if p.Type == "integer" {
				schemaType = genai.TypeInteger
			}
			props[name] = &genai.Schema{
				Type:        schemaType,
				Description: p.Description,
			}
		}
		required := make([]string, len(def.Required))
		copy(required, def.Required)

		decls = append(decls, &genai.FunctionDeclaration{
			Name:        def.Name,
			Description: def.Description,
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: props,
				Required:   required,
			},
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}
