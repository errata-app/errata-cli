// Package config loads Errata settings from environment variables and .env.
package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all runtime settings.
type Config struct {
	AnthropicAPIKey  string
	OpenAIAPIKey     string
	GoogleAPIKey     string
	OpenRouterAPIKey string

	// LiteLLMBaseURL is the base URL for a LiteLLM proxy (e.g. "http://localhost:4000/v1").
	// Empty disables the LiteLLM adapter.
	LiteLLMBaseURL string
	// LiteLLMAPIKey is optional; many local LiteLLM deployments don't require auth.
	LiteLLMAPIKey string

	// ActiveModels is the explicit list from ERRATA_ACTIVE_MODELS (comma-separated).
	// Empty means auto-detect one model per available provider.
	// OpenRouter models use "provider/model" format (e.g. "anthropic/claude-sonnet-4-6").
	// LiteLLM models use "litellm/<model>" format (e.g. "litellm/claude-sonnet-4-6").
	ActiveModels []string

	DefaultAnthropicModel string
	DefaultOpenAIModel    string
	DefaultGeminiModel    string

	PreferencesPath  string
	PricingCachePath string

	// DebugLogPath is the path for the append-only JSONL debug log.
	// Empty (default) disables debug logging entirely.
	DebugLogPath string
}

// Load reads .env (if present) then environment variables and returns a Config.
func Load() Config {
	// Best-effort .env load; ignore error if file is missing.
	_ = godotenv.Load(".env")

	cfg := Config{
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
		PreferencesPath:       "data/preferences.jsonl",
		PricingCachePath:      "data/pricing_cache.json",
	}

	cfg.DebugLogPath = os.Getenv("ERRATA_DEBUG_LOG")
	cfg.AnthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")
	cfg.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
	cfg.GoogleAPIKey = os.Getenv("GOOGLE_API_KEY")
	cfg.OpenRouterAPIKey = os.Getenv("OPENROUTER_API_KEY")
	cfg.LiteLLMBaseURL = os.Getenv("LITELLM_BASE_URL")
	cfg.LiteLLMAPIKey = os.Getenv("LITELLM_API_KEY")

	if v := os.Getenv("ERRATA_ACTIVE_MODELS"); v != "" {
		for _, m := range strings.Split(v, ",") {
			if m = strings.TrimSpace(m); m != "" {
				cfg.ActiveModels = append(cfg.ActiveModels, m)
			}
		}
	}

	return cfg
}

// ResolvedActiveModels returns the explicit model list, or one default per
// provider whose API key is present.
func (c Config) ResolvedActiveModels() []string {
	if len(c.ActiveModels) > 0 {
		return c.ActiveModels
	}
	var models []string
	if c.AnthropicAPIKey != "" {
		models = append(models, c.DefaultAnthropicModel)
	}
	if c.OpenAIAPIKey != "" {
		models = append(models, c.DefaultOpenAIModel)
	}
	if c.GoogleAPIKey != "" {
		models = append(models, c.DefaultGeminiModel)
	}
	return models
}
