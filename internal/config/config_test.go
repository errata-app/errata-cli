package config_test

import (
	"os"
	"testing"
	"time"

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

func TestLoad_SeedFromEnv(t *testing.T) {
	os.Setenv("ERRATA_SEED", "42")
	defer os.Unsetenv("ERRATA_SEED")

	cfg := config.Load()
	assert.NotNil(t, cfg.Seed)
	assert.Equal(t, int64(42), *cfg.Seed)
}

func TestLoad_SeedZeroFromEnv(t *testing.T) {
	os.Setenv("ERRATA_SEED", "0")
	defer os.Unsetenv("ERRATA_SEED")

	cfg := config.Load()
	assert.NotNil(t, cfg.Seed, "seed 0 should be distinguishable from not set")
	assert.Equal(t, int64(0), *cfg.Seed)
}

func TestLoad_SeedAbsent(t *testing.T) {
	os.Unsetenv("ERRATA_SEED")

	cfg := config.Load()
	assert.Nil(t, cfg.Seed)
}

func TestLoad_SeedInvalid(t *testing.T) {
	os.Setenv("ERRATA_SEED", "not-a-number")
	defer os.Unsetenv("ERRATA_SEED")

	cfg := config.Load()
	assert.Nil(t, cfg.Seed, "invalid ERRATA_SEED should be ignored")
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

// ─── SubagentMaxDepth parsing ────────────────────────────────────────────────

func TestLoad_SubagentMaxDepth_Valid(t *testing.T) {
	os.Setenv("ERRATA_SUBAGENT_MAX_DEPTH", "3")
	defer os.Unsetenv("ERRATA_SUBAGENT_MAX_DEPTH")
	assert.Equal(t, 3, config.Load().SubagentMaxDepth)
}

func TestLoad_SubagentMaxDepth_Zero(t *testing.T) {
	os.Setenv("ERRATA_SUBAGENT_MAX_DEPTH", "0")
	defer os.Unsetenv("ERRATA_SUBAGENT_MAX_DEPTH")
	assert.Equal(t, 0, config.Load().SubagentMaxDepth)
}

func TestLoad_SubagentMaxDepth_Negative(t *testing.T) {
	os.Setenv("ERRATA_SUBAGENT_MAX_DEPTH", "-1")
	defer os.Unsetenv("ERRATA_SUBAGENT_MAX_DEPTH")
	assert.Equal(t, 1, config.Load().SubagentMaxDepth, "negative should keep default")
}

func TestLoad_SubagentMaxDepth_Invalid(t *testing.T) {
	os.Setenv("ERRATA_SUBAGENT_MAX_DEPTH", "abc")
	defer os.Unsetenv("ERRATA_SUBAGENT_MAX_DEPTH")
	assert.Equal(t, 1, config.Load().SubagentMaxDepth, "invalid should keep default")
}

// ─── MaxHistoryTurns parsing ─────────────────────────────────────────────────

func TestLoad_MaxHistoryTurns_Valid(t *testing.T) {
	os.Setenv("ERRATA_MAX_HISTORY_TURNS", "50")
	defer os.Unsetenv("ERRATA_MAX_HISTORY_TURNS")
	assert.Equal(t, 50, config.Load().MaxHistoryTurns)
}

func TestLoad_MaxHistoryTurns_Zero(t *testing.T) {
	os.Setenv("ERRATA_MAX_HISTORY_TURNS", "0")
	defer os.Unsetenv("ERRATA_MAX_HISTORY_TURNS")
	assert.Equal(t, 20, config.Load().MaxHistoryTurns, "zero should keep default")
}

func TestLoad_MaxHistoryTurns_Negative(t *testing.T) {
	os.Setenv("ERRATA_MAX_HISTORY_TURNS", "-5")
	defer os.Unsetenv("ERRATA_MAX_HISTORY_TURNS")
	assert.Equal(t, 20, config.Load().MaxHistoryTurns, "negative should keep default")
}

func TestLoad_MaxHistoryTurns_Invalid(t *testing.T) {
	os.Setenv("ERRATA_MAX_HISTORY_TURNS", "abc")
	defer os.Unsetenv("ERRATA_MAX_HISTORY_TURNS")
	assert.Equal(t, 20, config.Load().MaxHistoryTurns, "invalid should keep default")
}

// ─── AgentTimeout parsing ────────────────────────────────────────────────────

func TestLoad_AgentTimeout_Valid(t *testing.T) {
	os.Setenv("ERRATA_AGENT_TIMEOUT", "10m")
	defer os.Unsetenv("ERRATA_AGENT_TIMEOUT")
	cfg := config.Load()
	assert.Equal(t, 10*time.Minute, cfg.AgentTimeout)
}

func TestLoad_AgentTimeout_ZeroDuration(t *testing.T) {
	os.Setenv("ERRATA_AGENT_TIMEOUT", "0s")
	defer os.Unsetenv("ERRATA_AGENT_TIMEOUT")
	assert.Equal(t, time.Duration(0), config.Load().AgentTimeout, "zero should keep default")
}

func TestLoad_AgentTimeout_Negative(t *testing.T) {
	os.Setenv("ERRATA_AGENT_TIMEOUT", "-1s")
	defer os.Unsetenv("ERRATA_AGENT_TIMEOUT")
	assert.Equal(t, time.Duration(0), config.Load().AgentTimeout, "negative should keep default")
}

func TestLoad_AgentTimeout_Invalid(t *testing.T) {
	os.Setenv("ERRATA_AGENT_TIMEOUT", "not-a-duration")
	defer os.Unsetenv("ERRATA_AGENT_TIMEOUT")
	assert.Equal(t, time.Duration(0), config.Load().AgentTimeout, "invalid should keep default")
}

// ─── Direct env var reads ────────────────────────────────────────────────────

func TestLoad_DebugLogPath(t *testing.T) {
	os.Setenv("ERRATA_DEBUG_LOG", "custom/log.jsonl")
	defer os.Unsetenv("ERRATA_DEBUG_LOG")
	assert.Equal(t, "custom/log.jsonl", config.Load().DebugLogPath)
}

func TestLoad_MCPServers(t *testing.T) {
	os.Setenv("ERRATA_MCP_SERVERS", "exa:npx @exa-ai/exa-mcp-server")
	defer os.Unsetenv("ERRATA_MCP_SERVERS")
	assert.Equal(t, "exa:npx @exa-ai/exa-mcp-server", config.Load().MCPServers)
}

func TestLoad_SystemPromptExtra(t *testing.T) {
	os.Setenv("ERRATA_SYSTEM_PROMPT", "Use Go idioms")
	defer os.Unsetenv("ERRATA_SYSTEM_PROMPT")
	assert.Equal(t, "Use Go idioms", config.Load().SystemPromptExtra)
}

func TestLoad_SubagentModel(t *testing.T) {
	os.Setenv("ERRATA_SUBAGENT_MODEL", "gpt-4o-mini")
	defer os.Unsetenv("ERRATA_SUBAGENT_MODEL")
	assert.Equal(t, "gpt-4o-mini", config.Load().SubagentModel)
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
	assert.Equal(t, "gemini-2.0-flash", cfg.DefaultVertexModel)
}

// ─── ResolvedActiveModels with new providers ─────────────────────────────────

func TestResolvedActiveModels_BedrockAutoDetection(t *testing.T) {
	os.Unsetenv("ERRATA_ACTIVE_MODELS")
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
	os.Unsetenv("ERRATA_ACTIVE_MODELS")
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
	os.Unsetenv("ERRATA_ACTIVE_MODELS")
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

// ─── ERRATA_ACTIVE_MODELS edge cases ─────────────────────────────────────────

func TestLoad_ActiveModels_WhitespaceHandling(t *testing.T) {
	os.Setenv("ERRATA_ACTIVE_MODELS", "model1 , model2 , model3")
	defer os.Unsetenv("ERRATA_ACTIVE_MODELS")
	cfg := config.Load()
	assert.Equal(t, []string{"model1", "model2", "model3"}, cfg.ActiveModels)
}

func TestLoad_ActiveModels_EmptyEntries(t *testing.T) {
	os.Setenv("ERRATA_ACTIVE_MODELS", "model1,,model2")
	defer os.Unsetenv("ERRATA_ACTIVE_MODELS")
	cfg := config.Load()
	assert.Equal(t, []string{"model1", "model2"}, cfg.ActiveModels)
}
