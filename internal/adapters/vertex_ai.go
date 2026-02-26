package adapters

import (
	"context"
	"log"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/suarezc/errata/internal/capabilities"
	"github.com/suarezc/errata/internal/models"
	promptpkg "github.com/suarezc/errata/internal/prompt"
	"github.com/suarezc/errata/internal/tools"
)

// VertexAIAdapter implements ModelAdapter for Google Vertex AI (Gemini models).
//
// Model IDs are configured with a "vertex/" prefix (e.g. "vertex/gemini-2.0-flash").
// The prefix is stripped before the API call; the full prefixed ID is preserved for
// display and logging.
//
// Authentication uses Application Default Credentials (ADC):
//   - gcloud auth application-default login (local development)
//   - GOOGLE_APPLICATION_CREDENTIALS (service account key file)
//   - GCE/GKE/Cloud Run metadata server
//
// Set VERTEX_AI_PROJECT and VERTEX_AI_LOCATION to enable this adapter.
type VertexAIAdapter struct {
	modelID     string // display ID, e.g. "vertex/gemini-2.0-flash"
	bareModelID string // stripped prefix; sent to the Vertex AI API
	project     string // GCP project ID
	location    string // GCP region, e.g. "us-central1"
}

// NewVertexAIAdapter creates a VertexAIAdapter.
func NewVertexAIAdapter(modelID, project, location string) *VertexAIAdapter {
	return &VertexAIAdapter{
		modelID:     modelID,
		bareModelID: strings.TrimPrefix(modelID, "vertex/"),
		project:     project,
		location:    location,
	}
}

func (a *VertexAIAdapter) ID() string { return a.modelID }

// Capabilities queries the Vertex AI models API for context/output token limits,
// falling back to hardcoded defaults on error.
func (a *VertexAIAdapter) Capabilities(ctx context.Context) models.ModelCapabilities {
	// Vertex AI hosts Gemini models — use Google defaults.
	caps := capabilities.DefaultCapabilities("google", a.bareModelID)
	caps.ModelID = a.modelID
	caps.Provider = "vertex"

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  a.project,
		Location: a.location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		log.Printf("capabilities: vertex AI client creation failed for %s: %v", a.bareModelID, err)
		return caps
	}

	info, err := client.Models.Get(ctx, a.bareModelID, nil)
	if err != nil {
		log.Printf("capabilities: vertex AI API lookup failed for %s: %v", a.bareModelID, err)
		return caps
	}

	if info.InputTokenLimit > 0 {
		caps.ContextWindow = int(info.InputTokenLimit)
		caps.ContextWindowSource = models.SourceAPI
	}
	if info.OutputTokenLimit > 0 {
		caps.MaxOutputTokens = int(info.OutputTokenLimit)
	}
	return caps
}

func (a *VertexAIAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	start := time.Now()
	qualifiedID := "google/" + a.bareModelID

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  a.project,
		Location: a.location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return BuildErrorResponse(a.modelID, qualifiedID, start, 0, 0, err), err
	}

	// Resolve system prompt: prefer payload from context, fall back to built-in.
	var systemMsg string
	var toolDescOverrides map[string]string
	if payload, ok := promptpkg.PayloadFromContext(ctx, a.modelID); ok {
		systemMsg = promptpkg.BuildSystemMessage(payload, tools.SystemPromptGuidance())
		toolDescOverrides = payload.ToolDescriptions
	} else {
		systemMsg = tools.SystemPromptSuffix()
	}

	config := &genai.GenerateContentConfig{
		Tools:             buildGeminiTools(ctx, toolDescOverrides),
		SystemInstruction: genai.NewContentFromText(systemMsg, ""),
	}
	if seed, ok := tools.SeedFromContext(ctx); ok {
		s := int32(seed)
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
	var totalRegularInput, totalOutput, totalCacheRead int64

	for {
		resp, err := client.Models.GenerateContent(ctx, a.bareModelID, contents, config)
		if err != nil {
			if ctx.Err() != nil {
				return BuildInterruptedResponse(a.modelID, qualifiedID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed, err), err
			}
			return BuildErrorResponse(a.modelID, qualifiedID, start, totalRegularInput+totalCacheRead, totalOutput, err), err
		}

		if resp.UsageMetadata != nil {
			// Gemini's PromptTokenCount = total including cached. CachedContentTokenCount is a subset.
			cached := int64(resp.UsageMetadata.CachedContentTokenCount)
			totalRegularInput += int64(resp.UsageMetadata.PromptTokenCount) - cached
			totalOutput += int64(resp.UsageMetadata.CandidatesTokenCount)
			totalCacheRead += cached
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
		EmitSnapshot(onEvent, qualifiedID, textParts, start, totalRegularInput+totalCacheRead, totalOutput, proposed)
	}

	return BuildSuccessResponse(a.modelID, qualifiedID, textParts, start, totalRegularInput, totalCacheRead, 0, totalOutput, proposed), nil
}

func init() {
	var _ models.ModelAdapter = (*VertexAIAdapter)(nil)
}
