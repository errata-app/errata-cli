package models

import (
	"fmt"
	"strings"

	"github.com/suarezc/errata/internal/config"
)

// NewAdapter returns a ModelAdapter for the given model ID using cfg for API keys.
func NewAdapter(modelID string, cfg config.Config) (ModelAdapter, error) {
	switch {
	case strings.HasPrefix(modelID, "claude"):
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		return NewAnthropicAdapter(modelID, cfg.AnthropicAPIKey), nil

	case strings.HasPrefix(modelID, "gpt-"),
		strings.HasPrefix(modelID, "o1"),
		strings.HasPrefix(modelID, "o3"):
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

// ListAdapters builds adapters for all resolved active models.
// Returns the adapter slice and a list of warning strings for skipped models.
func ListAdapters(cfg config.Config) ([]ModelAdapter, []string) {
	models := cfg.ResolvedActiveModels()
	var adapters []ModelAdapter
	var warnings []string

	for _, id := range models {
		a, err := NewAdapter(id, cfg)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", id, err))
			continue
		}
		adapters = append(adapters, a)
	}
	return adapters, warnings
}
