package recipe_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/recipe"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func writeRecipe(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "recipe-*.md")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// ─── Parse tests ─────────────────────────────────────────────────────────────

func TestParse_Empty(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, ""))
	require.NoError(t, err)
	assert.Nil(t, r.Models)
	assert.Equal(t, "", r.SystemPrompt)
	assert.Nil(t, r.Tools)
	assert.Empty(t, r.Tasks)
}

func TestParse_Title(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "# My Recipe\n\n## Models\n- claude-sonnet-4-6\n"))
	require.NoError(t, err)
	assert.Equal(t, "My Recipe", r.Name)
}

func TestParse_Models(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Models
- claude-sonnet-4-6
- openai/gpt-4o
- gemini-2.5-pro
`))
	require.NoError(t, err)
	require.Len(t, r.Models, 3)
	assert.Equal(t, "claude-sonnet-4-6", r.Models[0])
	assert.Equal(t, "openai/gpt-4o", r.Models[1])
	assert.Equal(t, "gemini-2.5-pro", r.Models[2])
}

func TestParse_SystemPrompt(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## System Prompt
You are a senior Go engineer.
Follow standard library conventions.

No external dependencies unless necessary.
`))
	require.NoError(t, err)
	assert.Contains(t, r.SystemPrompt, "senior Go engineer")
	assert.Contains(t, r.SystemPrompt, "No external dependencies")
}

func TestParse_ToolsAllowlist(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Tools
- read_file
- search_files
- list_directory
`))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Equal(t, []string{"read_file", "search_files", "list_directory"}, r.Tools.Allowlist)
	assert.Nil(t, r.Tools.BashPrefixes)
}

func TestParse_ToolsBashRestriction(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Tools
- read_file
- bash(go test *, go build *, go vet *)
`))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Contains(t, r.Tools.Allowlist, "bash")
	assert.Contains(t, r.Tools.Allowlist, "read_file")
	require.Len(t, r.Tools.BashPrefixes, 3)
	assert.Equal(t, "go test *", r.Tools.BashPrefixes[0])
	assert.Equal(t, "go build *", r.Tools.BashPrefixes[1])
	assert.Equal(t, "go vet *", r.Tools.BashPrefixes[2])
}

func TestParse_ToolsBashBare(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Tools
- read_file
- bash
`))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Contains(t, r.Tools.Allowlist, "bash")
	assert.Nil(t, r.Tools.BashPrefixes, "bare bash should not set prefix restrictions")
}

func TestParse_NoToolsSection(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Models\n- claude-sonnet-4-6\n"))
	require.NoError(t, err)
	assert.Nil(t, r.Tools, "absent ## Tools section should leave Tools nil (all tools enabled)")
}

func TestParse_MCPServers(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## MCP Servers
- exa: npx @exa-ai/exa-mcp-server
- fs: npx @modelcontextprotocol/server-filesystem /tmp
`))
	require.NoError(t, err)
	require.Len(t, r.MCPServers, 2)
	assert.Equal(t, "exa", r.MCPServers[0].Name)
	assert.Equal(t, "npx @exa-ai/exa-mcp-server", r.MCPServers[0].Command)
	assert.Equal(t, "fs", r.MCPServers[1].Name)
	assert.Contains(t, r.MCPServers[1].Command, "server-filesystem")
}

func TestParse_Constraints(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Constraints
timeout: 5m
max_steps: 30
`))
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, r.Constraints.Timeout)
	assert.Equal(t, 30, r.Constraints.MaxSteps)
}

func TestParse_ConstraintsTimeoutSeconds(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Constraints\ntimeout: 300s\n"))
	require.NoError(t, err)
	assert.Equal(t, 300*time.Second, r.Constraints.Timeout)
}

func TestParse_Context(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Context
max_history_turns: 10
strategy: auto_compact
compact_threshold: 0.75
`))
	require.NoError(t, err)
	assert.Equal(t, 10, r.Context.MaxHistoryTurns)
	assert.Equal(t, "auto_compact", r.Context.Strategy)
	assert.Equal(t, 0.75, r.Context.CompactThreshold)
}

func TestParse_ContextManual(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Context\nstrategy: manual\n"))
	require.NoError(t, err)
	assert.Equal(t, "manual", r.Context.Strategy)
}

func TestParse_ContextOff(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Context\nstrategy: off\n"))
	require.NoError(t, err)
	assert.Equal(t, "off", r.Context.Strategy)
}

func TestParse_SubAgent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Sub-Agent
model: claude-haiku-3
max_depth: 2
tools: read_file, search_files
`))
	require.NoError(t, err)
	assert.Equal(t, "claude-haiku-3", r.SubAgent.Model)
	assert.Equal(t, 2, r.SubAgent.MaxDepth)
	assert.Equal(t, "read_file, search_files", r.SubAgent.Tools)
}

func TestParse_SubAgentMaxDepthZero(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Sub-Agent\nmax_depth: 0\n"))
	require.NoError(t, err)
	assert.Equal(t, 0, r.SubAgent.MaxDepth, "max_depth: 0 should disable sub-agents")
}

func TestParse_SubAgentNotSet(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Models\n- claude-sonnet-4-6\n"))
	require.NoError(t, err)
	assert.Equal(t, -1, r.SubAgent.MaxDepth, "unset max_depth should be -1 sentinel")
}

func TestParse_Sandbox(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Sandbox
filesystem: project_only
network: none
`))
	require.NoError(t, err)
	assert.Equal(t, "project_only", r.Sandbox.Filesystem)
	assert.Equal(t, "none", r.Sandbox.Network)
}

func TestParse_Tasks(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Tasks
- Refactor the HTTP handler
- Add table-driven tests
- Fix the race condition
`))
	require.NoError(t, err)
	require.Len(t, r.Tasks, 3)
	assert.Equal(t, "Refactor the HTTP handler", r.Tasks[0])
}

func TestParse_UnknownSection(t *testing.T) {
	// Unknown sections should not cause an error.
	r, err := recipe.Parse(writeRecipe(t, `
## Models
- claude-sonnet-4-6

## Totally Unknown Section
some content here
`))
	require.NoError(t, err)
	assert.Len(t, r.Models, 1)
}

func TestParse_Metadata(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Metadata
name: Go Refactoring Suite
description: Tests Go refactoring tasks
tags: go, refactoring
author: charlessuarez
version: 1.0
contribute: true
`))
	require.NoError(t, err)
	assert.Equal(t, "Go Refactoring Suite", r.Metadata.Name)
	assert.Equal(t, "charlessuarez", r.Metadata.Author)
	assert.True(t, r.Metadata.Contribute)
	require.Len(t, r.Metadata.Tags, 2)
	assert.Equal(t, "go", r.Metadata.Tags[0])
}

// ─── Discover tests ───────────────────────────────────────────────────────────

func TestDiscover_ExplicitPath(t *testing.T) {
	path := writeRecipe(t, "## Models\n- test-model\n")
	r, err := recipe.Discover(path)
	require.NoError(t, err)
	require.Len(t, r.Models, 1)
	assert.Equal(t, "test-model", r.Models[0])
}

func TestDiscover_ExplicitPathNotFound(t *testing.T) {
	_, err := recipe.Discover("/nonexistent/path/recipe.md")
	assert.Error(t, err)
}

func TestDiscover_FallsBackToEmbeddedDefault(t *testing.T) {
	// Run in a temp dir with no recipe.md to trigger fallback to embedded defaults.
	orig, err := os.Getwd()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	r, err := recipe.Discover("")
	require.NoError(t, err)
	require.NotNil(t, r)
	// Default recipe sets context strategy and sub-agent depth.
	assert.Equal(t, "auto_compact", r.Context.Strategy)
	assert.Equal(t, 1, r.SubAgent.MaxDepth)
}

func TestDiscover_CwdRecipeMd(t *testing.T) {
	orig, err := os.Getwd()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	require.NoError(t, os.WriteFile("recipe.md", []byte("## Models\n- local-model\n"), 0o644))

	r, err := recipe.Discover("")
	require.NoError(t, err)
	require.Len(t, r.Models, 1)
	assert.Equal(t, "local-model", r.Models[0])
}

func TestDiscover_DotErrataRecipeMd(t *testing.T) {
	orig, err := os.Getwd()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	require.NoError(t, os.MkdirAll(".errata", 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(".errata", "recipe.md"),
		[]byte("## Models\n- errata-model\n"), 0o644))

	r, err := recipe.Discover("")
	require.NoError(t, err)
	require.Len(t, r.Models, 1)
	assert.Equal(t, "errata-model", r.Models[0])
}

// ─── ApplyTo tests ────────────────────────────────────────────────────────────

func defaultCfg() config.Config {
	return config.Config{
		ActiveModels:     nil,
		SystemPromptExtra: "",
		MCPServers:       "",
		SubagentModel:    "",
		SubagentMaxDepth: 1,
		MaxHistoryTurns:  20,
		AgentTimeout:     0,
		CompactThreshold: 0,
	}
}

func TestApplyTo_Models(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Models\n- claude-sonnet-4-6\n- openai/gpt-4o\n"))
	cfg := defaultCfg()
	r.ApplyTo(&cfg)
	assert.Equal(t, []string{"claude-sonnet-4-6", "openai/gpt-4o"}, cfg.ActiveModels)
}

func TestApplyTo_NilModels_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Constraints\ntimeout: 5m\n"))
	cfg := defaultCfg()
	cfg.ActiveModels = []string{"existing-model"}
	r.ApplyTo(&cfg)
	assert.Equal(t, []string{"existing-model"}, cfg.ActiveModels, "absent ## Models must not clear existing config")
}

func TestApplyTo_SystemPrompt(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## System Prompt\nYou are a Go expert.\n"))
	cfg := defaultCfg()
	r.ApplyTo(&cfg)
	assert.Equal(t, "You are a Go expert.", cfg.SystemPromptExtra)
}

func TestApplyTo_MCPServers(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## MCP Servers\n- exa: npx @exa-ai/exa-mcp-server\n"))
	cfg := defaultCfg()
	r.ApplyTo(&cfg)
	assert.Contains(t, cfg.MCPServers, "exa:")
	assert.Contains(t, cfg.MCPServers, "npx @exa-ai/exa-mcp-server")
}

func TestApplyTo_SubagentDepthExplicitZero(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Sub-Agent\nmax_depth: 0\n"))
	cfg := defaultCfg()
	cfg.SubagentMaxDepth = 1
	r.ApplyTo(&cfg)
	assert.Equal(t, 0, cfg.SubagentMaxDepth, "max_depth: 0 must disable spawn_agent")
}

func TestApplyTo_SubagentDepthNotSet_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Models\n- claude-sonnet-4-6\n"))
	cfg := defaultCfg()
	cfg.SubagentMaxDepth = 3
	r.ApplyTo(&cfg)
	assert.Equal(t, 3, cfg.SubagentMaxDepth, "absent max_depth must not override existing value")
}

func TestApplyTo_Timeout(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Constraints\ntimeout: 3m\n"))
	cfg := defaultCfg()
	r.ApplyTo(&cfg)
	assert.Equal(t, 3*time.Minute, cfg.AgentTimeout)
}

func TestApplyTo_MaxHistoryTurns(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Context\nmax_history_turns: 5\n"))
	cfg := defaultCfg()
	r.ApplyTo(&cfg)
	assert.Equal(t, 5, cfg.MaxHistoryTurns)
}

func TestApplyTo_CompactThreshold(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Context\ncompact_threshold: 0.60\n"))
	cfg := defaultCfg()
	r.ApplyTo(&cfg)
	assert.Equal(t, 0.60, cfg.CompactThreshold)
}

func TestParse_ModelParamsSeed(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Model Parameters
seed: 42
`))
	require.NoError(t, err)
	require.NotNil(t, r.ModelParams.Seed)
	assert.Equal(t, int64(42), *r.ModelParams.Seed)
}

func TestParse_ModelParamsSeedZero(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Model Parameters
seed: 0
`))
	require.NoError(t, err)
	require.NotNil(t, r.ModelParams.Seed, "seed: 0 should be set, not nil")
	assert.Equal(t, int64(0), *r.ModelParams.Seed)
}

func TestParse_ModelParamsSeedNegative(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Model Parameters
seed: -1
`))
	require.NoError(t, err)
	require.NotNil(t, r.ModelParams.Seed)
	assert.Equal(t, int64(-1), *r.ModelParams.Seed)
}

func TestParse_ModelParamsSeedAbsent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Model Parameters
temperature: 0.5
`))
	require.NoError(t, err)
	assert.Nil(t, r.ModelParams.Seed, "absent seed should be nil")
}

func TestApplyTo_Seed(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Model Parameters\nseed: 42\n"))
	cfg := defaultCfg()
	r.ApplyTo(&cfg)
	require.NotNil(t, cfg.Seed)
	assert.Equal(t, int64(42), *cfg.Seed)
}

func TestApplyTo_SeedNil_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, "## Models\n- claude-sonnet-4-6\n"))
	cfg := defaultCfg()
	existing := int64(99)
	cfg.Seed = &existing
	r.ApplyTo(&cfg)
	require.NotNil(t, cfg.Seed)
	assert.Equal(t, int64(99), *cfg.Seed, "absent seed must not clear existing config")
}

func TestParse_SuccessCriteria(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, `
## Success Criteria
- no_errors
- has_writes
- contains: all tests pass
`))
	require.NoError(t, err)
	require.Len(t, r.SuccessCriteria, 3)
	assert.Equal(t, "no_errors", r.SuccessCriteria[0])
	assert.Equal(t, "has_writes", r.SuccessCriteria[1])
	assert.Equal(t, "contains: all tests pass", r.SuccessCriteria[2])
}

func TestParse_SuccessCriteriaAbsent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Models\n- claude-sonnet-4-6\n"))
	require.NoError(t, err)
	assert.Nil(t, r.SuccessCriteria)
}

func TestParse_ContextTaskMode(t *testing.T) {
	for _, mode := range []string{"independent", "sequential"} {
		t.Run(mode, func(t *testing.T) {
			r, err := recipe.Parse(writeRecipe(t, "## Context\ntask_mode: "+mode+"\n"))
			require.NoError(t, err)
			assert.Equal(t, mode, r.Context.TaskMode)
		})
	}
}

func TestParse_ContextTaskModeAbsent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Context\nstrategy: auto_compact\n"))
	require.NoError(t, err)
	assert.Equal(t, "", r.Context.TaskMode)
}

func TestParse_ContextTaskModeInvalid(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "## Context\ntask_mode: invalid\n"))
	require.NoError(t, err)
	assert.Equal(t, "", r.Context.TaskMode, "unknown task_mode should be ignored")
}

func TestDefault_IsNonNil(t *testing.T) {
	r := recipe.Default()
	require.NotNil(t, r)
	assert.Equal(t, "auto_compact", r.Context.Strategy)
	assert.Equal(t, 1, r.SubAgent.MaxDepth)
	assert.Equal(t, 5*time.Minute, r.Constraints.Timeout)
}
