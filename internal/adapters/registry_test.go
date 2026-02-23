package adapters_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
)

// --- helpers ---

func anthropicCfg() config.Config {
	return config.Config{
		AnthropicAPIKey:       "sk-ant-test",
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
	}
}

func openAICfg() config.Config {
	return config.Config{
		OpenAIAPIKey:          "sk-oai-test",
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
	}
}

func geminiCfg() config.Config {
	return config.Config{
		GoogleAPIKey:          "AIza-test",
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
	}
}

func openRouterCfg() config.Config {
	return config.Config{
		OpenRouterAPIKey:      "sk-or-test",
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
	}
}

func liteLLMCfg() config.Config {
	return config.Config{
		LiteLLMBaseURL:        "http://localhost:4000/v1",
		LiteLLMAPIKey:         "test-key",
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
	}
}

// --- NewAdapter ---

func TestNewAdapter_AnthropicReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("claude-sonnet-4-6", anthropicCfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.AnthropicAdapter)
	assert.True(t, ok, "expected *AnthropicAdapter")
	assert.Equal(t, "claude-sonnet-4-6", a.ID())
}

func TestNewAdapter_AnthropicMissingKey(t *testing.T) {
	cfg := config.Config{AnthropicAPIKey: ""}
	_, err := adapters.NewAdapter("claude-opus-4-6", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY")
}

func TestNewAdapter_OpenAIReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("gpt-4o", openAICfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.OpenAIAdapter)
	assert.True(t, ok, "expected *OpenAIAdapter")
	assert.Equal(t, "gpt-4o", a.ID())
}

func TestNewAdapter_OpenAIMissingKey(t *testing.T) {
	cfg := config.Config{OpenAIAPIKey: ""}
	_, err := adapters.NewAdapter("gpt-4o", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENAI_API_KEY")
}

func TestNewAdapter_OpenAIPrefixes(t *testing.T) {
	cfg := openAICfg()
	tests := []string{"gpt-4o", "gpt-3.5-turbo", "o1", "o3"}
	for _, modelID := range tests {
		t.Run(modelID, func(t *testing.T) {
			a, err := adapters.NewAdapter(modelID, cfg)
			require.NoError(t, err)
			_, ok := a.(*adapters.OpenAIAdapter)
			assert.True(t, ok)
		})
	}
}

func TestNewAdapter_GeminiReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("gemini-2.0-flash", geminiCfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.GeminiAdapter)
	assert.True(t, ok, "expected *GeminiAdapter")
	assert.Equal(t, "gemini-2.0-flash", a.ID())
}

func TestNewAdapter_GeminiMissingKey(t *testing.T) {
	cfg := config.Config{GoogleAPIKey: ""}
	_, err := adapters.NewAdapter("gemini-2.0-flash", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GOOGLE_API_KEY")
}

func TestNewAdapter_UnknownPrefix(t *testing.T) {
	cfg := config.Config{}
	_, err := adapters.NewAdapter("llama-3-unknown", cfg)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "Unknown"))
}

func TestNewAdapter_OpenRouterReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("anthropic/claude-sonnet-4-6", openRouterCfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.OpenRouterAdapter)
	assert.True(t, ok, "expected *OpenRouterAdapter")
	assert.Equal(t, "anthropic/claude-sonnet-4-6", a.ID())
}

func TestNewAdapter_OpenRouterMissingKey(t *testing.T) {
	cfg := config.Config{}
	_, err := adapters.NewAdapter("openai/gpt-4o", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENROUTER_API_KEY")
}

func TestNewAdapter_OpenRouterVariousProviders(t *testing.T) {
	cfg := openRouterCfg()
	tests := []string{
		"anthropic/claude-sonnet-4-6",
		"openai/gpt-4o",
		"google/gemini-2.0-flash",
		"meta-llama/llama-3-70b-instruct",
	}
	for _, modelID := range tests {
		t.Run(modelID, func(t *testing.T) {
			a, err := adapters.NewAdapter(modelID, cfg)
			require.NoError(t, err)
			_, ok := a.(*adapters.OpenRouterAdapter)
			assert.True(t, ok)
		})
	}
}

func TestNewAdapter_LiteLLMReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("litellm/claude-sonnet-4-6", liteLLMCfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.LiteLLMAdapter)
	assert.True(t, ok, "expected *LiteLLMAdapter")
	assert.Equal(t, "litellm/claude-sonnet-4-6", a.ID())
}

func TestNewAdapter_LiteLLMMissingBaseURL(t *testing.T) {
	cfg := config.Config{}
	_, err := adapters.NewAdapter("litellm/gpt-4o", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LITELLM_BASE_URL")
}

func TestNewAdapter_LiteLLMTakesPrecedenceOverOpenRouter(t *testing.T) {
	// A model starting with "litellm/" must route to LiteLLM even though it contains "/".
	cfg := config.Config{
		OpenRouterAPIKey: "sk-or-test",
		LiteLLMBaseURL:   "http://localhost:4000/v1",
	}
	a, err := adapters.NewAdapter("litellm/claude-sonnet-4-6", cfg)
	require.NoError(t, err)
	_, ok := a.(*adapters.LiteLLMAdapter)
	assert.True(t, ok, "litellm/ prefix must route to LiteLLMAdapter, not OpenRouterAdapter")
}

// --- ListAdapters ---

func TestListAdapters_ReturnsAdapterForActiveModel(t *testing.T) {
	cfg := config.Config{
		AnthropicAPIKey:       "sk-ant-test",
		ActiveModels:          []string{"claude-sonnet-4-6"},
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
	}
	result, warnings := adapters.ListAdapters(cfg)
	assert.Len(t, result, 1)
	assert.Empty(t, warnings)
	assert.Equal(t, "claude-sonnet-4-6", result[0].ID())
}

func TestListAdapters_SkipsMissingKeyWithWarning(t *testing.T) {
	// Anthropic key present, OpenAI key absent.
	cfg := config.Config{
		AnthropicAPIKey:       "sk-ant-test",
		OpenAIAPIKey:          "",
		ActiveModels:          []string{"claude-sonnet-4-6", "gpt-4o"},
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
	}
	result, warnings := adapters.ListAdapters(cfg)
	assert.Len(t, result, 1)
	assert.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "gpt-4o")
}

func TestListAdapters_EmptyWhenNoKeys(t *testing.T) {
	cfg := config.Config{}
	result, warnings := adapters.ListAdapters(cfg)
	assert.Empty(t, result)
	assert.Empty(t, warnings)
}

func TestListAdapters_AllThreeProviders(t *testing.T) {
	cfg := config.Config{
		AnthropicAPIKey:       "sk-ant-test",
		OpenAIAPIKey:          "sk-oai-test",
		GoogleAPIKey:          "AIza-test",
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
	}
	result, warnings := adapters.ListAdapters(cfg)
	assert.Len(t, result, 3)
	assert.Empty(t, warnings)
}
