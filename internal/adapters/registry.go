package adapters

import (
	"fmt"
	"strings"

	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
)

// NewAdapter returns a ModelAdapter for the given model ID using cfg for API keys.
//
// Routing rules (checked in order):
//  1. "litellm/<model>"     → LiteLLMAdapter (requires LITELLM_BASE_URL)
//  2. "provider/model"      → OpenRouterAdapter (requires OPENROUTER_API_KEY)
//  3. "claude*"             → AnthropicAdapter
//  4. "gpt-*", "o1", "o3*" → OpenAIAdapter
//  5. "gemini*"             → GeminiAdapter
func NewAdapter(modelID string, cfg config.Config) (models.ModelAdapter, error) {
	switch {
	case strings.HasPrefix(modelID, "litellm/"):
		if cfg.LiteLLMBaseURL == "" {
			return nil, fmt.Errorf("LITELLM_BASE_URL not set")
		}
		return NewLiteLLMAdapter(modelID, cfg.LiteLLMAPIKey, cfg.LiteLLMBaseURL), nil

	case strings.Contains(modelID, "/"):
		if cfg.OpenRouterAPIKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not set")
		}
		return NewOpenRouterAdapter(modelID, cfg.OpenRouterAPIKey), nil

	case strings.HasPrefix(modelID, "claude"):
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		return NewAnthropicAdapter(modelID, cfg.AnthropicAPIKey), nil

	case strings.HasPrefix(modelID, "gpt-"),
		strings.HasPrefix(modelID, "chatgpt-"),
		len(modelID) >= 2 && modelID[0] == 'o' && modelID[1] >= '0' && modelID[1] <= '9':
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not set")
		}
		return NewOpenAIAdapter(modelID, cfg.OpenAIAPIKey), nil

	case strings.HasPrefix(modelID, "gemini"):
		if cfg.GoogleAPIKey == "" {
			return nil, fmt.Errorf("GOOGLE_API_KEY not set")
		}
		return NewGeminiAdapter(modelID, cfg.GoogleAPIKey), nil

	default:
		return nil, fmt.Errorf("unknown model prefix for %q", modelID)
	}
}

// NewAdapterForProvider creates an adapter using an explicit provider name
// (e.g. "Anthropic", "OpenAI", "Gemini") rather than inferring from the model ID prefix.
// Falls back to NewAdapter for unrecognised providers (OpenRouter, LiteLLM, custom).
func NewAdapterForProvider(modelID, provider string, cfg config.Config) (models.ModelAdapter, error) {
	switch provider {
	case "Anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		return NewAnthropicAdapter(modelID, cfg.AnthropicAPIKey), nil
	case "OpenAI":
		if cfg.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not set")
		}
		return NewOpenAIAdapter(modelID, cfg.OpenAIAPIKey), nil
	case "Gemini":
		if cfg.GoogleAPIKey == "" {
			return nil, fmt.Errorf("GOOGLE_API_KEY not set")
		}
		return NewGeminiAdapter(modelID, cfg.GoogleAPIKey), nil
	default: // OpenRouter, LiteLLM, unknown → prefix routing
		return NewAdapter(modelID, cfg)
	}
}

// ListAdapters builds adapters for all resolved active models.
// Returns the adapter slice and a list of warning strings for skipped models.
func ListAdapters(cfg config.Config) ([]models.ModelAdapter, []string) {
	modelIDs := cfg.ResolvedActiveModels()
	var result []models.ModelAdapter
	var warnings []string

	for _, id := range modelIDs {
		a, err := NewAdapter(id, cfg)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", id, err))
			continue
		}
		result = append(result, a)
	}
	return result, warnings
}
