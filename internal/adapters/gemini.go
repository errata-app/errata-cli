package adapters

import (
	"context"
	"log"
	"time"

	"google.golang.org/genai"

	"github.com/suarezc/errata/internal/capabilities"
	"github.com/suarezc/errata/internal/models"
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

// Capabilities queries the Gemini models API for context/output token limits,
// falling back to hardcoded defaults on error.
func (a *GeminiAdapter) Capabilities(ctx context.Context) models.ModelCapabilities {
	caps := capabilities.DefaultCapabilities("google", a.modelID)

	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: a.apiKey})
	if err != nil {
		log.Printf("capabilities: gemini client creation failed for %s: %v", a.modelID, err)
		return caps
	}

	return queryGeminiCapabilities(ctx, client, a.modelID, caps)
}

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

	return runGeminiAgentLoop(ctx, geminiRunConfig{
		client:      client,
		modelID:     a.modelID,
		apiModelID:  a.modelID,
		qualifiedID: "google/" + a.modelID,
	}, history, prompt, onEvent)
}

func init() {
	var _ models.ModelAdapter = (*GeminiAdapter)(nil)
}
