package adapters

import (
	"context"
	"log"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/suarezc/errata/internal/capabilities"
	"github.com/suarezc/errata/internal/models"
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

	return queryGeminiCapabilities(ctx, client, a.bareModelID, caps)
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

	return runGeminiAgentLoop(ctx, geminiRunConfig{
		client:      client,
		modelID:     a.modelID,
		apiModelID:  a.bareModelID,
		qualifiedID: qualifiedID,
	}, history, prompt, onEvent)
}

func init() {
	var _ models.ModelAdapter = (*VertexAIAdapter)(nil)
}
