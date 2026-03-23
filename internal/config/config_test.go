package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/config"
	"github.com/errata-app/errata-cli/pkg/recipe"
)

func TestLoad_Defaults(t *testing.T) {
	// Ensure env vars don't leak from the test environment.
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")

	cfg := config.Load()
	assert.Equal(t, "claude-sonnet-4-6", cfg.DefaultAnthropicModel)
	assert.Equal(t, "gpt-4o", cfg.DefaultOpenAIModel)
	assert.Equal(t, "gemini-2.5-flash", cfg.DefaultGeminiModel)
	assert.Equal(t, "data", cfg.DataDir)
	assert.Equal(t, 20, cfg.MaxHistoryTurns)
	assert.Empty(t, cfg.MCPServers)
	assert.Empty(t, cfg.SystemPromptExtra)
}

func TestLoad_DefaultsFromRecipe(t *testing.T) {
	// Verify that behavioral defaults are sourced from the embedded default
	// recipe (pkg/recipe/default.recipe.md), not hardcoded in Load().
	cfg := config.Load()
	assert.Equal(t, 20, cfg.MaxHistoryTurns, "should come from default recipe ## Context")
	assert.Equal(t, 5*time.Minute, cfg.AgentTimeout, "should come from default recipe ## Constraints")
	assert.InDelta(t, 0.80, cfg.CompactThreshold, 1e-9, "should come from default recipe ## Context")
}

func TestDefaultRecipe_HasToolsAndGuidance(t *testing.T) {
	r := recipe.Default()
	require.NotNil(t, r.Tools, "default recipe must have ## Tools section")
	assert.Len(t, r.Tools.Allowlist, 9, "default recipe should list all 9 built-in tools")
	require.NotNil(t, r.Tools.Guidance, "default recipe should have per-tool guidance")
	assert.Len(t, r.Tools.Guidance, 9, "each tool should have guidance text")
	assert.NotEmpty(t, r.SummarizationPrompt, "default recipe should have summarization prompt")
}

func TestResolvedActiveModels_FromKeys(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	cfg := config.Load()
	models := cfg.ResolvedActiveModels()
	assert.Equal(t, []string{"claude-sonnet-4-6"}, models)
}

func TestResolvedActiveModels_NoKeys(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")

	cfg := config.Load()
	assert.Empty(t, cfg.ResolvedActiveModels())
}

func TestResolvedActiveModels_AllProviders(t *testing.T) {
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
	assert.Contains(t, resolved, "gemini-2.5-flash")
}

func TestResolvedActiveModels_ExplicitModels(t *testing.T) {
	cfg := config.Load()
	cfg.ActiveModels = []string{"claude-opus-4-6", "gpt-4o"}
	models := cfg.ResolvedActiveModels()
	assert.Equal(t, []string{"claude-opus-4-6", "gpt-4o"}, models)
}

func TestLoad_DefaultModelNames(t *testing.T) {
	cfg := config.Load()
	assert.Equal(t, "claude-sonnet-4-6", cfg.DefaultAnthropicModel)
	assert.Equal(t, "gpt-4o", cfg.DefaultOpenAIModel)
	assert.Equal(t, "gemini-2.5-flash", cfg.DefaultGeminiModel)
}

func TestLoad_DataDirEnvOverride(t *testing.T) {
	os.Setenv("ERRATA_DATA_DIR", "/custom/data")
	defer os.Unsetenv("ERRATA_DATA_DIR")

	cfg := config.Load()
	assert.Equal(t, "/custom/data", cfg.DataDir)
}

func TestLoad_OpenRouterAPIKey(t *testing.T) {
	os.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	defer os.Unsetenv("OPENROUTER_API_KEY")
	assert.Equal(t, "sk-or-test", config.Load().OpenRouterAPIKey)
}

func TestLoad_LiteLLM(t *testing.T) {
	os.Setenv("LITELLM_BASE_URL", "http://localhost:4000/v1")
	os.Setenv("LITELLM_API_KEY", "test-key")
	defer os.Unsetenv("LITELLM_BASE_URL")
	defer os.Unsetenv("LITELLM_API_KEY")
	cfg := config.Load()
	assert.Equal(t, "http://localhost:4000/v1", cfg.LiteLLMBaseURL)
	assert.Equal(t, "test-key", cfg.LiteLLMAPIKey)
}

// ─── Bedrock config ──────────────────────────────────────────────────────────

func TestLoad_BedrockRegion(t *testing.T) {
	os.Setenv("AWS_REGION", "us-west-2")
	defer os.Unsetenv("AWS_REGION")
	assert.Equal(t, "us-west-2", config.Load().BedrockRegion)
}

func TestLoad_BedrockRegionFallsBackToDefault(t *testing.T) {
	os.Unsetenv("AWS_REGION")
	os.Setenv("AWS_DEFAULT_REGION", "eu-west-1")
	defer os.Unsetenv("AWS_DEFAULT_REGION")
	assert.Equal(t, "eu-west-1", config.Load().BedrockRegion)
}

func TestLoad_BedrockDefaultModel(t *testing.T) {
	cfg := config.Load()
	assert.Equal(t, "anthropic.claude-sonnet-4-20250514-v1:0", cfg.DefaultBedrockModel)
}

// ─── Azure OpenAI config ─────────────────────────────────────────────────────

func TestLoad_AzureOpenAI(t *testing.T) {
	os.Setenv("AZURE_OPENAI_API_KEY", "test-key")
	os.Setenv("AZURE_OPENAI_ENDPOINT", "https://myresource.openai.azure.com")
	os.Setenv("AZURE_OPENAI_API_VERSION", "2025-01-01")
	defer func() {
		os.Unsetenv("AZURE_OPENAI_API_KEY")
		os.Unsetenv("AZURE_OPENAI_ENDPOINT")
		os.Unsetenv("AZURE_OPENAI_API_VERSION")
	}()
	cfg := config.Load()
	assert.Equal(t, "test-key", cfg.AzureOpenAIAPIKey)
	assert.Equal(t, "https://myresource.openai.azure.com", cfg.AzureOpenAIEndpoint)
	assert.Equal(t, "2025-01-01", cfg.AzureOpenAIAPIVersion)
}

func TestLoad_AzureOpenAIAPIVersionDefault(t *testing.T) {
	os.Unsetenv("AZURE_OPENAI_API_VERSION")
	cfg := config.Load()
	assert.Equal(t, "2024-10-21", cfg.AzureOpenAIAPIVersion)
}

// ─── Vertex AI config ────────────────────────────────────────────────────────

func TestLoad_VertexAI(t *testing.T) {
	os.Setenv("VERTEX_AI_PROJECT", "my-gcp-project")
	os.Setenv("VERTEX_AI_LOCATION", "us-central1")
	defer func() {
		os.Unsetenv("VERTEX_AI_PROJECT")
		os.Unsetenv("VERTEX_AI_LOCATION")
	}()
	cfg := config.Load()
	assert.Equal(t, "my-gcp-project", cfg.VertexAIProject)
	assert.Equal(t, "us-central1", cfg.VertexAILocation)
}

func TestLoad_VertexDefaultModel(t *testing.T) {
	cfg := config.Load()
	assert.Equal(t, "gemini-2.5-flash", cfg.DefaultVertexModel)
}

// ─── ResolvedActiveModels with new providers ─────────────────────────────────

func TestResolvedActiveModels_BedrockAutoDetection(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	os.Setenv("AWS_REGION", "us-east-1")
	defer os.Unsetenv("AWS_REGION")

	cfg := config.Load()
	resolved := cfg.ResolvedActiveModels()
	assert.Contains(t, resolved, "bedrock/"+cfg.DefaultBedrockModel)
}

func TestResolvedActiveModels_AzureAutoDetection(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	os.Unsetenv("AWS_REGION")
	os.Unsetenv("AWS_DEFAULT_REGION")
	os.Setenv("AZURE_OPENAI_API_KEY", "test-key")
	os.Setenv("AZURE_OPENAI_ENDPOINT", "https://myresource.openai.azure.com")
	defer func() {
		os.Unsetenv("AZURE_OPENAI_API_KEY")
		os.Unsetenv("AZURE_OPENAI_ENDPOINT")
	}()

	cfg := config.Load()
	resolved := cfg.ResolvedActiveModels()
	assert.Contains(t, resolved, "azure/"+cfg.DefaultAzureModel)
}

func TestResolvedActiveModels_VertexAutoDetection(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	os.Unsetenv("AWS_REGION")
	os.Unsetenv("AWS_DEFAULT_REGION")
	os.Unsetenv("AZURE_OPENAI_API_KEY")
	os.Unsetenv("AZURE_OPENAI_ENDPOINT")
	os.Setenv("VERTEX_AI_PROJECT", "my-project")
	os.Setenv("VERTEX_AI_LOCATION", "us-central1")
	defer func() {
		os.Unsetenv("VERTEX_AI_PROJECT")
		os.Unsetenv("VERTEX_AI_LOCATION")
	}()

	cfg := config.Load()
	resolved := cfg.ResolvedActiveModels()
	assert.Contains(t, resolved, "vertex/"+cfg.DefaultVertexModel)
}

// ─── MaskKey ─────────────────────────────────────────────────────────────────

func TestMaskKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"empty", "", "****"},
		{"short", "abc", "****"},
		{"exactly 11", "12345678901", "****"},
		{"exactly 12", "123456789012", "12345...9012"},
		{"normal API key", "sk-ant-api-realkey-value4", "sk-an...lue4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, config.MaskKey(tt.key))
		})
	}
}

// ─── ProviderConfigured ──────────────────────────────────────────────────────

func TestProviderConfigured(t *testing.T) {
	// Clear all provider keys.
	for _, p := range config.ProviderEnvInfo() {
		for _, v := range p.EnvVars {
			os.Unsetenv(v)
		}
	}

	t.Run("anthropic not configured", func(t *testing.T) {
		assert.False(t, config.ProviderConfigured("anthropic"))
	})

	t.Run("anthropic configured", func(t *testing.T) {
		os.Setenv("ANTHROPIC_API_KEY", "sk-test")
		defer os.Unsetenv("ANTHROPIC_API_KEY")
		assert.True(t, config.ProviderConfigured("anthropic"))
	})

	t.Run("azure needs both vars", func(t *testing.T) {
		os.Setenv("AZURE_OPENAI_API_KEY", "test")
		defer os.Unsetenv("AZURE_OPENAI_API_KEY")
		assert.False(t, config.ProviderConfigured("azure"))

		os.Setenv("AZURE_OPENAI_ENDPOINT", "https://test.openai.azure.com")
		defer os.Unsetenv("AZURE_OPENAI_ENDPOINT")
		assert.True(t, config.ProviderConfigured("azure"))
	})

	t.Run("case insensitive", func(t *testing.T) {
		os.Setenv("OPENAI_API_KEY", "sk-test")
		defer os.Unsetenv("OPENAI_API_KEY")
		assert.True(t, config.ProviderConfigured("OpenAI"))
	})

	t.Run("unknown provider", func(t *testing.T) {
		assert.False(t, config.ProviderConfigured("nonexistent"))
	})
}

// ─── SetEnvKey ───────────────────────────────────────────────────────────────

func TestSetEnvKey_NewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, config.SetEnvKey(path, "TEST_KEY", "test-value"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "TEST_KEY=test-value")
}

func TestSetEnvKey_UpdateExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("TEST_KEY=old-value\n"), 0o600))
	require.NoError(t, config.SetEnvKey(path, "TEST_KEY", "new-value"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "TEST_KEY=new-value")
	assert.NotContains(t, content, "old-value")
}

func TestSetEnvKey_PreservesComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original := "# This is a comment\nEXISTING=foo\n\n# Another comment\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
	require.NoError(t, config.SetEnvKey(path, "NEW_KEY", "bar"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "# This is a comment")
	assert.Contains(t, content, "# Another comment")
	assert.Contains(t, content, "EXISTING=foo")
	assert.Contains(t, content, "NEW_KEY=bar")
}

func TestSetEnvKey_AppendsNewKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("A=1\nB=2\n"), 0o600))
	require.NoError(t, config.SetEnvKey(path, "C", "3"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "A=1")
	assert.Contains(t, content, "B=2")
	assert.Contains(t, content, "C=3")
}

func TestSetEnvKey_SetsOsEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	key := "ERRATA_TEST_SET_ENV_KEY_" + t.Name()
	defer os.Unsetenv(key)

	require.NoError(t, config.SetEnvKey(path, key, "hello"))
	assert.Equal(t, "hello", os.Getenv(key))
}

func TestSetEnvKey_FilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, config.SetEnvKey(path, "KEY", "val"))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
