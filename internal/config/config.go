// Package config loads Errata settings from environment variables and .env.
package config

import (
	"os"
	"strconv"
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
	HistoryPath      string
	PromptHistoryPath string

	// DebugLogPath is the path for the append-only JSONL debug log.
	// Empty (default) disables debug logging entirely.
	DebugLogPath string

	// MCPServers is the raw value of ERRATA_MCP_SERVERS.
	// Format: "name:command arg1 arg2,name2:command2"
	// Empty disables MCP entirely.
	MCPServers string

	// SystemPromptExtra is appended after the built-in tool guidance in every
	// adapter's system prompt. Use for project-specific context, coding conventions,
	// or domain knowledge that should influence all models.
	// Loaded from ERRATA_SYSTEM_PROMPT.
	SystemPromptExtra string

	// SubagentModel is the model ID used when spawning sub-agents via spawn_agent.
	// Empty means use the same model as the parent. Loaded from ERRATA_SUBAGENT_MODEL.
	SubagentModel string

	// SubagentMaxDepth is the maximum spawn_agent recursion depth.
	// 1 (default) means sub-agents cannot spawn further sub-agents.
	// 0 disables spawn_agent entirely. Loaded from ERRATA_SUBAGENT_MAX_DEPTH.
	SubagentMaxDepth int

	// AgentTimeout is the per-adapter wall-clock timeout for a single RunAgent call.
	// 0 means use the runner's built-in default (5 minutes).
	// Set via recipe ## Constraints timeout: or ERRATA_AGENT_TIMEOUT env var.
	AgentTimeout time.Duration

	// CompactThreshold is the context fill fraction that triggers auto-compact.
	// 0 means use the runner's built-in default (0.80).
	// Set via recipe ## Context compact_threshold:.
	CompactThreshold float64

	// MaxHistoryTurns is the maximum number of conversation turns kept per model.
	// Load() sets this to 20 by default; ERRATA_MAX_HISTORY_TURNS overrides it.
	// A recipe can further override via ## Context max_history_turns:.
	MaxHistoryTurns int

	// Seed is the pseudorandom seed passed to model APIs for reproducible sampling.
	// nil means not set (provider default); non-nil is passed through even if 0.
	// Set via ERRATA_SEED env var, recipe ## Model Parameters seed:, or /seed command.
	Seed *int64
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
		HistoryPath:           "data/history.json",
		PromptHistoryPath:     "data/prompt_history.jsonl",
	}

	cfg.DebugLogPath = os.Getenv("ERRATA_DEBUG_LOG")
	cfg.MCPServers = os.Getenv("ERRATA_MCP_SERVERS")
	cfg.SystemPromptExtra = os.Getenv("ERRATA_SYSTEM_PROMPT")
	cfg.SubagentModel = os.Getenv("ERRATA_SUBAGENT_MODEL")
	cfg.SubagentMaxDepth = 1 // default: sub-agents cannot spawn sub-agents
	if v := os.Getenv("ERRATA_SUBAGENT_MAX_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.SubagentMaxDepth = n
		}
	}
	cfg.MaxHistoryTurns = 20
	if v := os.Getenv("ERRATA_MAX_HISTORY_TURNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxHistoryTurns = n
		}
	}
	if v := os.Getenv("ERRATA_AGENT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.AgentTimeout = d
		}
	}
	if v := os.Getenv("ERRATA_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Seed = &n
		}
	}

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
