// Package config loads Errata settings from environment variables and .env.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// BedrockRegion is the AWS region for Amazon Bedrock (e.g. "us-east-1").
	// Uses the AWS SDK default credential chain (AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY,
	// AWS_PROFILE, or IAM role). Empty disables the Bedrock adapter.
	BedrockRegion string

	// AzureOpenAIAPIKey is the API key for Azure OpenAI Service.
	AzureOpenAIAPIKey string
	// AzureOpenAIEndpoint is the Azure resource endpoint (e.g. "https://myresource.openai.azure.com").
	// Both key and endpoint must be set to enable the Azure OpenAI adapter.
	AzureOpenAIEndpoint string
	// AzureOpenAIAPIVersion is the Azure OpenAI API version (default "2024-10-21").
	AzureOpenAIAPIVersion string

	// VertexAIProject is the GCP project ID for Vertex AI.
	// Uses Application Default Credentials (gcloud auth or GOOGLE_APPLICATION_CREDENTIALS).
	VertexAIProject string
	// VertexAILocation is the GCP region for Vertex AI (e.g. "us-central1").
	// Both project and location must be set to enable the Vertex AI adapter.
	VertexAILocation string

	// ActiveModels is the explicit model list (set via recipe ## Models).
	// Empty means auto-detect one model per available provider.
	// OpenRouter models use "provider/model" format (e.g. "anthropic/claude-sonnet-4-6").
	// LiteLLM models use "litellm/<model>" format (e.g. "litellm/claude-sonnet-4-6").
	ActiveModels []string

	DefaultAnthropicModel string
	DefaultOpenAIModel    string
	DefaultGeminiModel    string
	DefaultBedrockModel   string
	DefaultAzureModel     string
	DefaultVertexModel    string

	PreferencesPath   string
	PricingCachePath  string
	HistoryPath       string
	PromptHistoryPath string

	// MCPServers is the serialised MCP server config (set via recipe ## MCP Servers).
	// Format: "name:command arg1 arg2,name2:command2"
	// Empty disables MCP entirely.
	MCPServers string

	// SystemPromptExtra is appended after the built-in tool guidance in every
	// adapter's system prompt. Use for project-specific context, coding conventions,
	// or domain knowledge that should influence all models.
	// Set via recipe ## System Prompt.
	SystemPromptExtra string

	// SubagentModel is the model ID used when spawning sub-agents via spawn_agent.
	// Empty means use the same model as the parent. Set via recipe ## Sub-Agent model:.
	SubagentModel string

	// SubagentMaxDepth is the maximum spawn_agent recursion depth.
	// 1 (default) means sub-agents cannot spawn further sub-agents.
	// 0 disables spawn_agent entirely. Set via recipe ## Sub-Agent max_depth:.
	SubagentMaxDepth int

	// AgentTimeout is the per-adapter wall-clock timeout for a single RunAgent call.
	// 0 means use the runner's built-in default (5 minutes).
	// Set via recipe ## Constraints timeout:.
	AgentTimeout time.Duration

	// CompactThreshold is the context fill fraction that triggers auto-compact.
	// 0 means use the runner's built-in default (0.80).
	// Set via recipe ## Context compact_threshold:.
	CompactThreshold float64

	// MaxHistoryTurns is the maximum number of conversation turns kept per model.
	// Default is 20. Set via recipe ## Context max_history_turns:.
	MaxHistoryTurns int

	// Seed is the pseudorandom seed passed to model APIs for reproducible sampling.
	// nil means not set (provider default); non-nil is passed through even if 0.
	// Set via recipe ## Model Parameters seed: or /seed command.
	Seed *int64

	// ToolGuidance replaces the built-in tool-use guidance in every adapter's
	// system prompt when set. Empty means use the default guidance.
	// Set via recipe ## Tool Guidance.
	ToolGuidance string
}

// Load reads .env (if present) then environment variables and returns a Config.
func Load() Config {
	// Best-effort .env load; ignore error if file is missing.
	_ = godotenv.Load(".env")

	cfg := Config{
		DefaultAnthropicModel: "claude-sonnet-4-6",
		DefaultOpenAIModel:    "gpt-4o",
		DefaultGeminiModel:    "gemini-2.0-flash",
		DefaultBedrockModel:   "anthropic.claude-sonnet-4-20250514-v1:0",
		DefaultAzureModel:     "gpt-4o",
		DefaultVertexModel:    "gemini-2.0-flash",
		PreferencesPath:       "data/preferences.jsonl",
		PricingCachePath:      "data/pricing_cache.json",
		HistoryPath:           "data/history.json",
		PromptHistoryPath:     "data/prompt_history.jsonl",
	}

	// SubagentMaxDepth default (1) comes from the default recipe's ## Sub-Agent section.
	// When tools.SubagentEnabled is false, the default recipe omits that section,
	// leaving SubagentMaxDepth at 0 (disabled). See internal/tools/tools.go.
	cfg.MaxHistoryTurns = 20

	cfg.AnthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")
	cfg.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
	cfg.GoogleAPIKey = os.Getenv("GOOGLE_API_KEY")
	cfg.OpenRouterAPIKey = os.Getenv("OPENROUTER_API_KEY")
	cfg.LiteLLMBaseURL = os.Getenv("LITELLM_BASE_URL")
	cfg.LiteLLMAPIKey = os.Getenv("LITELLM_API_KEY")

	cfg.BedrockRegion = os.Getenv("AWS_REGION")
	if cfg.BedrockRegion == "" {
		cfg.BedrockRegion = os.Getenv("AWS_DEFAULT_REGION")
	}

	cfg.AzureOpenAIAPIKey = os.Getenv("AZURE_OPENAI_API_KEY")
	cfg.AzureOpenAIEndpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
	cfg.AzureOpenAIAPIVersion = os.Getenv("AZURE_OPENAI_API_VERSION")
	if cfg.AzureOpenAIAPIVersion == "" {
		cfg.AzureOpenAIAPIVersion = "2024-10-21"
	}

	cfg.VertexAIProject = os.Getenv("VERTEX_AI_PROJECT")
	cfg.VertexAILocation = os.Getenv("VERTEX_AI_LOCATION")

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
	if c.BedrockRegion != "" {
		models = append(models, "bedrock/"+c.DefaultBedrockModel)
	}
	if c.AzureOpenAIAPIKey != "" && c.AzureOpenAIEndpoint != "" {
		models = append(models, "azure/"+c.DefaultAzureModel)
	}
	if c.VertexAIProject != "" && c.VertexAILocation != "" {
		models = append(models, "vertex/"+c.DefaultVertexModel)
	}
	return models
}

// ── Provider env helpers ─────────────────────────────────────────────────────

// ProviderEnv describes a provider's required environment variables.
type ProviderEnv struct {
	Name         string   // shorthand for /keys (e.g. "anthropic")
	EnvVars      []string // env vars (first is the primary key)
	DefaultModel string
}

// ProviderEnvInfo returns the static list of supported providers and their env vars.
func ProviderEnvInfo() []ProviderEnv {
	return []ProviderEnv{
		{"anthropic", []string{"ANTHROPIC_API_KEY"}, "claude-sonnet-4-6"},
		{"openai", []string{"OPENAI_API_KEY"}, "gpt-4o"},
		{"google", []string{"GOOGLE_API_KEY"}, "gemini-2.0-flash"},
		{"openrouter", []string{"OPENROUTER_API_KEY"}, ""},
		{"bedrock", []string{"AWS_REGION"}, ""},
		{"azure", []string{"AZURE_OPENAI_API_KEY", "AZURE_OPENAI_ENDPOINT"}, "gpt-4o"},
		{"vertex", []string{"VERTEX_AI_PROJECT", "VERTEX_AI_LOCATION"}, "gemini-2.0-flash"},
		{"litellm", []string{"LITELLM_BASE_URL"}, ""},
	}
}

// ProviderConfigured returns true if all required env vars for the named
// provider have non-empty values in the current process environment.
func ProviderConfigured(providerName string) bool {
	for _, p := range ProviderEnvInfo() {
		if strings.EqualFold(p.Name, providerName) {
			for _, v := range p.EnvVars {
				if os.Getenv(v) == "" {
					return false
				}
			}
			return true
		}
	}
	return false
}

// SetEnvKey performs a line-level upsert of key=value in the given .env file.
// If the file does not exist it is created. Comments and blank lines are preserved.
// The change is also applied to the current process via os.Setenv.
func SetEnvKey(path, key, value string) error {
	// Ensure parent directory exists.
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
	}

	var lines []string
	found := false

	if data, err := os.ReadFile(path); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			// Match KEY= at start of line (ignoring leading whitespace).
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "#") && strings.HasPrefix(trimmed, key+"=") {
				lines = append(lines, key+"="+value)
				found = true
				continue
			}
			lines = append(lines, line)
		}
	}

	if !found {
		lines = append(lines, key+"="+value)
	}

	content := strings.Join(lines, "\n") + "\n"

	// Atomic write via temp file + rename.
	cleanPath := filepath.Clean(path)
	tmp := cleanPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil { //nolint:gosec // path comes from caller, not user input
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmp, cleanPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return os.Setenv(key, value)
}

// MaskKey returns a masked version of an API key for display.
// Keys >= 12 chars show first 5 and last 4 chars; shorter keys become "****".
func MaskKey(key string) string {
	if len(key) < 12 {
		return "****"
	}
	return key[:5] + "..." + key[len(key)-4:]
}
