package config_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/suarezc/errata/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	// Ensure env vars don't leak from the test environment.
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	os.Unsetenv("ERRATA_ACTIVE_MODELS")

	cfg := config.Load()
	assert.Equal(t, "claude-sonnet-4-6", cfg.DefaultAnthropicModel)
	assert.Equal(t, "gpt-4o", cfg.DefaultOpenAIModel)
	assert.Equal(t, "gemini-2.0-flash", cfg.DefaultGeminiModel)
	assert.Equal(t, "data/preferences.jsonl", cfg.PreferencesPath)
}

func TestResolvedActiveModels_ExplicitOverride(t *testing.T) {
	os.Setenv("ERRATA_ACTIVE_MODELS", "claude-opus-4-6,gpt-4o")
	defer os.Unsetenv("ERRATA_ACTIVE_MODELS")

	cfg := config.Load()
	models := cfg.ResolvedActiveModels()
	assert.Equal(t, []string{"claude-opus-4-6", "gpt-4o"}, models)
}

func TestResolvedActiveModels_FromKeys(t *testing.T) {
	os.Unsetenv("ERRATA_ACTIVE_MODELS")
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	cfg := config.Load()
	models := cfg.ResolvedActiveModels()
	assert.Equal(t, []string{"claude-sonnet-4-6"}, models)
}

func TestResolvedActiveModels_NoKeys(t *testing.T) {
	os.Unsetenv("ERRATA_ACTIVE_MODELS")
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")

	cfg := config.Load()
	assert.Empty(t, cfg.ResolvedActiveModels())
}

func TestResolvedActiveModels_AllProviders(t *testing.T) {
	os.Unsetenv("ERRATA_ACTIVE_MODELS")
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("OPENAI_API_KEY", "sk-oai-test")
	os.Setenv("GOOGLE_API_KEY", "AIza-test")
	defer func() {
		os.Unsetenv("ANTHROPIC_API_KEY")
		os.Unsetenv("OPENAI_API_KEY")
		os.Unsetenv("GOOGLE_API_KEY")
	}()

	cfg := config.Load()
	resolved := cfg.ResolvedActiveModels()
	assert.Contains(t, resolved, "claude-sonnet-4-6")
	assert.Contains(t, resolved, "gpt-4o")
	assert.Contains(t, resolved, "gemini-2.0-flash")
}

func TestLoad_DefaultModelNames(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	os.Unsetenv("ERRATA_ACTIVE_MODELS")

	cfg := config.Load()
	assert.Equal(t, "claude-sonnet-4-6", cfg.DefaultAnthropicModel)
	assert.Equal(t, "gpt-4o", cfg.DefaultOpenAIModel)
	assert.Equal(t, "gemini-2.0-flash", cfg.DefaultGeminiModel)
}
