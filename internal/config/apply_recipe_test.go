package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/pkg/recipe"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func writeRecipe(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "recipe-*.md")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

func v1(content string) string {
	return "version: 1\n" + content
}

func defaultCfg() config.Config {
	return config.Config{
		ActiveModels:      nil,
		SystemPromptExtra: "",
		MCPServers:        "",
		SubagentModel:     "",
		SubagentMaxDepth:  1,
		MaxHistoryTurns:   20,
		AgentTimeout:      0,
		CompactThreshold:  0,
	}
}

// ─── ApplyRecipe tests ───────────────────────────────────────────────────────

func TestApplyRecipe_Models(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n- openai/gpt-4o\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, []string{"claude-sonnet-4-6", "openai/gpt-4o"}, cfg.ActiveModels)
}

func TestApplyRecipe_NilModels_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Constraints\ntimeout: 5m\n")))
	cfg := defaultCfg()
	cfg.ActiveModels = []string{"existing-model"}
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, []string{"existing-model"}, cfg.ActiveModels, "absent ## Models must not clear existing config")
}

func TestApplyRecipe_SystemPrompt(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## System Prompt\nYou are a Go expert.\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, "You are a Go expert.", cfg.SystemPromptExtra)
}

func TestApplyRecipe_MCPServers(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## MCP Servers\n- exa: npx @exa-ai/exa-mcp-server\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	assert.Contains(t, cfg.MCPServers, "exa:")
	assert.Contains(t, cfg.MCPServers, "npx @exa-ai/exa-mcp-server")
}

func TestApplyRecipe_SubagentDepthExplicitZero(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Sub-Agent\nmax_depth: 0\n")))
	cfg := defaultCfg()
	cfg.SubagentMaxDepth = 1
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 0, cfg.SubagentMaxDepth, "max_depth: 0 must disable spawn_agent")
}

func TestApplyRecipe_SubagentDepthNotSet_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n")))
	cfg := defaultCfg()
	cfg.SubagentMaxDepth = 3
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 3, cfg.SubagentMaxDepth, "absent max_depth must not override existing value")
}

func TestApplyRecipe_Timeout(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Constraints\ntimeout: 3m\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 3*time.Minute, cfg.AgentTimeout)
}

func TestApplyRecipe_MaxHistoryTurns(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Context\nmax_history_turns: 5\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 5, cfg.MaxHistoryTurns)
}

func TestApplyRecipe_CompactThreshold(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Context\ncompact_threshold: 0.60\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	assert.InDelta(t, 0.60, cfg.CompactThreshold, 0.001)
}

func TestApplyRecipe_Seed(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Model Parameters\nseed: 42\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	require.NotNil(t, cfg.Seed)
	assert.Equal(t, int64(42), *cfg.Seed)
}

func TestApplyRecipe_SeedNil_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n")))
	cfg := defaultCfg()
	existing := int64(99)
	cfg.Seed = &existing
	config.ApplyRecipe(r, &cfg)
	require.NotNil(t, cfg.Seed)
	assert.Equal(t, int64(99), *cfg.Seed, "absent seed must not clear existing config")
}

func TestApplyRecipe_MaxSteps(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Constraints\nmax_steps: 25\n")))
	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 25, cfg.MaxSteps)
}

func TestApplyRecipe_MaxStepsZero_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n")))
	cfg := defaultCfg()
	cfg.MaxSteps = 10
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 10, cfg.MaxSteps, "absent max_steps must not clear existing config")
}

// ─── ApplyRecipe regression tests ────────────────────────────────────────────

func TestApplyRecipe_AllFields_Simultaneous(t *testing.T) {
	// Construct a recipe with ALL ApplyRecipe-mapped fields set simultaneously.
	// Pins field-interaction behavior — setting all at once must not interfere.
	seed := int64(42)
	r := &recipe.Recipe{
		Models:       []string{"m1", "m2"},
		SystemPrompt: "Custom system prompt",
		MCPServers: []recipe.MCPServerEntry{
			{Name: "exa", Command: "npx exa-server"},
		},
		SubAgent: recipe.SubAgentConfig{
			Model:    "gpt-4o",
			MaxDepth: 3,
		},
		Context: recipe.ContextConfig{
			MaxHistoryTurns:  30,
			CompactThreshold: 0.65,
		},
		Constraints: recipe.ConstraintsConfig{
			Timeout:  10 * time.Minute,
			MaxSteps: 50,
		},
		ModelParams: recipe.ModelParamsConfig{
			Seed: &seed,
		},
	}

	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)

	assert.Equal(t, []string{"m1", "m2"}, cfg.ActiveModels)
	assert.Equal(t, "Custom system prompt", cfg.SystemPromptExtra)
	assert.Contains(t, cfg.MCPServers, "exa")
	assert.Contains(t, cfg.MCPServers, "npx exa-server")
	assert.Equal(t, "gpt-4o", cfg.SubagentModel)
	assert.Equal(t, 3, cfg.SubagentMaxDepth)
	assert.Equal(t, 30, cfg.MaxHistoryTurns)
	assert.Equal(t, 10*time.Minute, cfg.AgentTimeout)
	assert.Equal(t, 50, cfg.MaxSteps)
	assert.InDelta(t, 0.65, cfg.CompactThreshold, 1e-9)
	require.NotNil(t, cfg.Seed)
	assert.Equal(t, int64(42), *cfg.Seed)
}

// ─── Atomic section tests ─────────────────────────────────────────────────────

func TestApplyRecipe_AtomicConstraints(t *testing.T) {
	// User sets max_steps only in ## Constraints → timeout must zero out.
	r, _ := recipe.Parse(writeRecipe(t, v1("## Constraints\nmax_steps: 50\n")))
	cfg := defaultCfg()
	cfg.AgentTimeout = 5 * time.Minute // pre-existing default
	cfg.MaxSteps = 100
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 50, cfg.MaxSteps)
	assert.Equal(t, time.Duration(0), cfg.AgentTimeout, "timeout must be zeroed when section is present but timeout not declared")
}

func TestApplyRecipe_AtomicContext(t *testing.T) {
	// User sets max_history_turns only → compact_threshold must zero out.
	r, _ := recipe.Parse(writeRecipe(t, v1("## Context\nmax_history_turns: 10\n")))
	cfg := defaultCfg()
	cfg.CompactThreshold = 0.8
	cfg.MaxHistoryTurns = 20
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 10, cfg.MaxHistoryTurns)
	assert.InDelta(t, 0.0, cfg.CompactThreshold, 1e-9, "compact_threshold must be zeroed when section present but field not declared")
}

func TestApplyRecipe_AtomicModelParams(t *testing.T) {
	// User sets temperature only → seed must nil out.
	r, _ := recipe.Parse(writeRecipe(t, v1("## Model Parameters\ntemperature: 0.5\n")))
	cfg := defaultCfg()
	existing := int64(42)
	cfg.Seed = &existing
	config.ApplyRecipe(r, &cfg)
	assert.Nil(t, cfg.Seed, "seed must be nil when section present but seed not declared")
}

func TestApplyRecipe_AtomicSubAgent(t *testing.T) {
	// User sets model only → max_depth must zero out (disabled).
	r, _ := recipe.Parse(writeRecipe(t, v1("## Sub-Agent\nmodel: claude-haiku-3\n")))
	cfg := defaultCfg()
	cfg.SubagentMaxDepth = 3
	cfg.SubagentModel = "old-model"
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, "claude-haiku-3", cfg.SubagentModel)
	assert.Equal(t, 0, cfg.SubagentMaxDepth, "max_depth must be 0 (disabled) when section present but max_depth not mentioned")
}

func TestApplyRecipe_AbsentSection_PreservesDefaults(t *testing.T) {
	// No ## Constraints in recipe → existing timeout preserved.
	r, _ := recipe.Parse(writeRecipe(t, v1("## Models\n- m1\n")))
	cfg := defaultCfg()
	cfg.AgentTimeout = 5 * time.Minute
	cfg.MaxSteps = 100
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 5*time.Minute, cfg.AgentTimeout, "absent section must preserve existing timeout")
	assert.Equal(t, 100, cfg.MaxSteps, "absent section must preserve existing max_steps")
}

func TestApplyRecipe_UnsetFields_PreserveAll(t *testing.T) {
	// Pre-populate Config with non-default values for all non-Models fields.
	// Parse a recipe with only ## Models set.
	// Assert cfg.ActiveModels changed, all others retained pre-populated values.
	existingSeed := int64(99)
	cfg := config.Config{
		ActiveModels:      []string{"old-model"},
		SystemPromptExtra: "existing prompt",
		MCPServers:        "existing:server",
		SubagentModel:     "existing-subagent",
		SubagentMaxDepth:  5,
		MaxSteps:          15,
		MaxHistoryTurns:   40,
		AgentTimeout:      7 * time.Minute,
		CompactThreshold:  0.90,
		Seed:              &existingSeed,
	}

	// Recipe with only Models set; all other fields at zero values.
	r := &recipe.Recipe{
		Models:   []string{"new-model-1", "new-model-2"},
		SubAgent: recipe.SubAgentConfig{MaxDepth: -1}, // sentinel: not set
	}

	config.ApplyRecipe(r, &cfg)

	// Models should have changed.
	assert.Equal(t, []string{"new-model-1", "new-model-2"}, cfg.ActiveModels)

	// All other fields should be preserved.
	assert.Equal(t, "existing prompt", cfg.SystemPromptExtra)
	assert.Equal(t, "existing:server", cfg.MCPServers)
	assert.Equal(t, "existing-subagent", cfg.SubagentModel)
	assert.Equal(t, 5, cfg.SubagentMaxDepth)
	assert.Equal(t, 40, cfg.MaxHistoryTurns)
	assert.Equal(t, 7*time.Minute, cfg.AgentTimeout)
	assert.InDelta(t, 0.90, cfg.CompactThreshold, 1e-9)
	require.NotNil(t, cfg.Seed)
	assert.Equal(t, int64(99), *cfg.Seed)
	assert.Equal(t, 15, cfg.MaxSteps)
}
