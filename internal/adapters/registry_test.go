package adapters_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/adapters"
	"github.com/errata-app/errata-cli/internal/config"
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
	tests := []string{"gpt-4o", "gpt-3.5-turbo", "chatgpt-4o", "o1", "o2", "o3", "o3-mini", "o4", "o5"}
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

// --- Bedrock ---

func bedrockCfg() config.Config {
	return config.Config{
		BedrockRegion: "us-east-1",
	}
}

func TestNewAdapter_BedrockReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("bedrock/anthropic.claude-sonnet-4-20250514-v1:0", bedrockCfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.BedrockAdapter)
	assert.True(t, ok, "expected *BedrockAdapter")
	assert.Equal(t, "bedrock/anthropic.claude-sonnet-4-20250514-v1:0", a.ID())
}

func TestNewAdapter_BedrockMissingRegion(t *testing.T) {
	cfg := config.Config{}
	_, err := adapters.NewAdapter("bedrock/anthropic.claude-sonnet-4-20250514-v1:0", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AWS_REGION")
}

func TestNewAdapter_BedrockTakesPrecedenceOverOpenRouter(t *testing.T) {
	// A model starting with "bedrock/" must route to Bedrock even though it contains "/".
	cfg := config.Config{
		OpenRouterAPIKey: "sk-or-test",
		BedrockRegion:    "us-east-1",
	}
	a, err := adapters.NewAdapter("bedrock/anthropic.claude-sonnet-4-20250514-v1:0", cfg)
	require.NoError(t, err)
	_, ok := a.(*adapters.BedrockAdapter)
	assert.True(t, ok, "bedrock/ prefix must route to BedrockAdapter, not OpenRouterAdapter")
}

// --- Azure OpenAI ---

func azureOpenAICfg() config.Config {
	return config.Config{
		AzureOpenAIAPIKey:     "test-key",
		AzureOpenAIEndpoint:   "https://myresource.openai.azure.com",
		AzureOpenAIAPIVersion: "2024-10-21",
	}
}

func TestNewAdapter_AzureOpenAIReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("azure/gpt-4o", azureOpenAICfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.AzureOpenAIAdapter)
	assert.True(t, ok, "expected *AzureOpenAIAdapter")
	assert.Equal(t, "azure/gpt-4o", a.ID())
}

func TestNewAdapter_AzureOpenAIMissingKey(t *testing.T) {
	cfg := config.Config{AzureOpenAIEndpoint: "https://myresource.openai.azure.com"}
	_, err := adapters.NewAdapter("azure/gpt-4o", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_OPENAI_API_KEY")
}

func TestNewAdapter_AzureOpenAIMissingEndpoint(t *testing.T) {
	cfg := config.Config{AzureOpenAIAPIKey: "test-key"}
	_, err := adapters.NewAdapter("azure/gpt-4o", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AZURE_OPENAI_ENDPOINT")
}

func TestNewAdapter_AzureOpenAITakesPrecedenceOverOpenRouter(t *testing.T) {
	cfg := config.Config{
		OpenRouterAPIKey:      "sk-or-test",
		AzureOpenAIAPIKey:     "test-key",
		AzureOpenAIEndpoint:   "https://myresource.openai.azure.com",
		AzureOpenAIAPIVersion: "2024-10-21",
	}
	a, err := adapters.NewAdapter("azure/gpt-4o", cfg)
	require.NoError(t, err)
	_, ok := a.(*adapters.AzureOpenAIAdapter)
	assert.True(t, ok, "azure/ prefix must route to AzureOpenAIAdapter, not OpenRouterAdapter")
}

// --- Vertex AI ---

func vertexAICfg() config.Config {
	return config.Config{
		VertexAIProject:  "my-project",
		VertexAILocation: "us-central1",
	}
}

func TestNewAdapter_VertexAIReturnsCorrectType(t *testing.T) {
	a, err := adapters.NewAdapter("vertex/gemini-2.0-flash", vertexAICfg())
	require.NoError(t, err)
	_, ok := a.(*adapters.VertexAIAdapter)
	assert.True(t, ok, "expected *VertexAIAdapter")
	assert.Equal(t, "vertex/gemini-2.0-flash", a.ID())
}

func TestNewAdapter_VertexAIMissingProject(t *testing.T) {
	cfg := config.Config{VertexAILocation: "us-central1"}
	_, err := adapters.NewAdapter("vertex/gemini-2.0-flash", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VERTEX_AI_PROJECT")
}

func TestNewAdapter_VertexAIMissingLocation(t *testing.T) {
	cfg := config.Config{VertexAIProject: "my-project"}
	_, err := adapters.NewAdapter("vertex/gemini-2.0-flash", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VERTEX_AI_LOCATION")
}

func TestNewAdapter_VertexAITakesPrecedenceOverOpenRouter(t *testing.T) {
	cfg := config.Config{
		OpenRouterAPIKey: "sk-or-test",
		VertexAIProject:  "my-project",
		VertexAILocation: "us-central1",
	}
	a, err := adapters.NewAdapter("vertex/gemini-2.0-flash", cfg)
	require.NoError(t, err)
	_, ok := a.(*adapters.VertexAIAdapter)
	assert.True(t, ok, "vertex/ prefix must route to VertexAIAdapter, not OpenRouterAdapter")
}

// --- NewAdapterForProvider ---

func TestNewAdapterForProvider_KnownProviders(t *testing.T) {
	cfg := config.Config{
		AnthropicAPIKey:       "sk-ant-test",
		OpenAIAPIKey:          "sk-oai-test",
		GoogleAPIKey:          "AIza-test",
		BedrockRegion:         "us-east-1",
		AzureOpenAIAPIKey:     "test-key",
		AzureOpenAIEndpoint:   "https://myresource.openai.azure.com",
		AzureOpenAIAPIVersion: "2024-10-21",
		VertexAIProject:       "my-project",
		VertexAILocation:      "us-central1",
	}
	tests := []struct {
		provider string
		modelID  string
		wantType string
	}{
		{"Anthropic", "ricky", "*adapters.AnthropicAdapter"},
		{"OpenAI", "ricky", "*adapters.OpenAIAdapter"},
		{"Gemini", "ricky", "*adapters.GeminiAdapter"},
		{"Bedrock", "ricky", "*adapters.BedrockAdapter"},
		{"AzureOpenAI", "ricky", "*adapters.AzureOpenAIAdapter"},
		{"VertexAI", "ricky", "*adapters.VertexAIAdapter"},
	}
	for _, tc := range tests {
		t.Run(tc.provider+"/"+tc.modelID, func(t *testing.T) {
			a, err := adapters.NewAdapterForProvider(tc.modelID, tc.provider, cfg)
			require.NoError(t, err)
			assert.NotNil(t, a)
			assert.Equal(t, tc.modelID, a.ID())
		})
	}
}

func TestNewAdapterForProvider_UnknownProviderFallsBackToPrefix(t *testing.T) {
	cfg := openAICfg()
	// Empty provider string → falls back to NewAdapter prefix routing.
	a, err := adapters.NewAdapterForProvider("gpt-4o", "", cfg)
	require.NoError(t, err)
	_, ok := a.(*adapters.OpenAIAdapter)
	assert.True(t, ok, "expected *OpenAIAdapter via prefix fallback")
}

func TestNewAdapterForProvider_MissingKey(t *testing.T) {
	cfg := config.Config{} // no keys
	_, err := adapters.NewAdapterForProvider("ricky", "OpenAI", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OPENAI_API_KEY")
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
