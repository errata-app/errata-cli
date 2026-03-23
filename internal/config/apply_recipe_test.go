package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/config"
	"github.com/errata-app/errata-cli/pkg/recipe"
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
	r := &recipe.Recipe{
		Models:       []string{"m1", "m2"},
		SystemPrompt: "Custom system prompt",
		MCPServers: []recipe.MCPServerEntry{
			{Name: "exa", Command: "npx exa-server"},
		},
		Context: recipe.ContextConfig{
			MaxHistoryTurns:  30,
			CompactThreshold: 0.65,
		},
		Constraints: recipe.ConstraintsConfig{
			Timeout:  10 * time.Minute,
			MaxSteps: 50,
		},
	}

	cfg := defaultCfg()
	config.ApplyRecipe(r, &cfg)

	assert.Equal(t, []string{"m1", "m2"}, cfg.ActiveModels)
	assert.Equal(t, "Custom system prompt", cfg.SystemPromptExtra)
	assert.Contains(t, cfg.MCPServers, "exa")
	assert.Equal(t, 30, cfg.MaxHistoryTurns)
	assert.Equal(t, 10*time.Minute, cfg.AgentTimeout)
	assert.Equal(t, 50, cfg.MaxSteps)
	assert.InDelta(t, 0.65, cfg.CompactThreshold, 1e-9)
}

// ─── Atomic section tests ─────────────────────────────────────────────────────

func TestApplyRecipe_AtomicConstraints(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Constraints\nmax_steps: 50\n")))
	cfg := defaultCfg()
	cfg.AgentTimeout = 5 * time.Minute
	cfg.MaxSteps = 100
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 50, cfg.MaxSteps)
	assert.Equal(t, time.Duration(0), cfg.AgentTimeout, "timeout must be zeroed when section is present but timeout not declared")
}

func TestApplyRecipe_AtomicContext(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Context\nmax_history_turns: 10\n")))
	cfg := defaultCfg()
	cfg.CompactThreshold = 0.8
	cfg.MaxHistoryTurns = 20
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 10, cfg.MaxHistoryTurns)
	assert.InDelta(t, 0.0, cfg.CompactThreshold, 1e-9, "compact_threshold must be zeroed when section present but field not declared")
}

func TestApplyRecipe_AbsentSection_PreservesDefaults(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Models\n- m1\n")))
	cfg := defaultCfg()
	cfg.AgentTimeout = 5 * time.Minute
	cfg.MaxSteps = 100
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, 5*time.Minute, cfg.AgentTimeout, "absent section must preserve existing timeout")
	assert.Equal(t, 100, cfg.MaxSteps, "absent section must preserve existing max_steps")
}

func TestApplyRecipe_UnsetFields_PreserveAll(t *testing.T) {
	cfg := config.Config{
		ActiveModels:      []string{"old-model"},
		SystemPromptExtra: "existing prompt",
		MCPServers:        "existing:server",
		MaxSteps:          15,
		MaxHistoryTurns:   40,
		AgentTimeout:      7 * time.Minute,
		CompactThreshold:  0.90,
	}

	r := &recipe.Recipe{
		Models: []string{"new-model-1", "new-model-2"},
	}

	config.ApplyRecipe(r, &cfg)

	assert.Equal(t, []string{"new-model-1", "new-model-2"}, cfg.ActiveModels)
	assert.Equal(t, "existing prompt", cfg.SystemPromptExtra)
	assert.Equal(t, "existing:server", cfg.MCPServers)
	assert.Equal(t, 40, cfg.MaxHistoryTurns)
	assert.Equal(t, 7*time.Minute, cfg.AgentTimeout)
	assert.InDelta(t, 0.90, cfg.CompactThreshold, 1e-9)
	assert.Equal(t, 15, cfg.MaxSteps)
}
