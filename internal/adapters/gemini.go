package adapters

import (
	"context"
	"time"

	"google.golang.org/genai"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// GeminiAdapter implements ModelAdapter for Gemini models.
type GeminiAdapter struct {
	modelID string
	apiKey  string
}

// NewGeminiAdapter creates a GeminiAdapter.
func NewGeminiAdapter(modelID, apiKey string) *GeminiAdapter {
	return &GeminiAdapter{modelID: modelID, apiKey: apiKey}
}

func (a *GeminiAdapter) ID() string { return a.modelID }

func (a *GeminiAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	start := time.Now()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: a.apiKey})
	if err != nil {
		return BuildErrorResponse(a.modelID, "google/"+a.modelID, start, 0, 0, err), err
	}

	config := &genai.GenerateContentConfig{
		Tools:             buildGeminiTools(ctx),
		SystemInstruction: genai.NewContentFromText(tools.SystemPromptSuffix(), ""),
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
		resp, err := client.Models.GenerateContent(ctx, a.modelID, contents, config)
		if err != nil {
			return BuildErrorResponse(a.modelID, "google/"+a.modelID, start, totalInput, totalOutput, err), err
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
				onEvent(models.AgentEvent{Type: "text", Data: part.Text})
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
	}

	return BuildSuccessResponse(a.modelID, "google/"+a.modelID, textParts, start, totalInput, totalOutput, proposed), nil
}

func buildGeminiTools(ctx context.Context) []*genai.Tool {
	var decls []*genai.FunctionDeclaration
	for _, def := range tools.ActiveToolsFromContext(ctx) {
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

func init() {
	var _ models.ModelAdapter = (*GeminiAdapter)(nil)
}
