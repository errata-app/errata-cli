// Package config loads Errata settings from environment variables and .env.
package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all runtime settings.
type Config struct {
	AnthropicAPIKey string
	OpenAIAPIKey    string
	GoogleAPIKey    string

	// ActiveModels is the explicit list from ERRATA_ACTIVE_MODELS (comma-separated).
	// Empty means auto-detect one model per available provider.
	ActiveModels []string

	DefaultAnthropicModel string
	DefaultOpenAIModel    string
	DefaultGeminiModel    string

	PreferencesPath string
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
	}

	cfg.AnthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")
	cfg.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
	cfg.GoogleAPIKey = os.Getenv("GOOGLE_API_KEY")

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
