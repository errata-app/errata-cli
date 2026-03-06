package recipe_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/pkg/recipe"
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

// v1 prepends "version: 1\n" to recipe content for use in tests.
func v1(content string) string {
	return "version: 1\n" + content
}

// ─── Version tests ──────────────────────────────────────────────────────────

func TestParse_MissingVersion(t *testing.T) {
	_, err := recipe.Parse(writeRecipe(t, "## Models\n- claude-sonnet-4-6\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required version")
}

func TestParse_MissingVersion_Empty(t *testing.T) {
	_, err := recipe.Parse(writeRecipe(t, ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required version")
}

func TestParse_UnsupportedVersion(t *testing.T) {
	_, err := recipe.Parse(writeRecipe(t, "version: 99\n## Models\n- m\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
	assert.Contains(t, err.Error(), "99")
}

func TestParse_VersionZero(t *testing.T) {
	_, err := recipe.Parse(writeRecipe(t, "version: 0\n## Models\n- m\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ">= 1")
}

func TestParse_VersionNegative(t *testing.T) {
	_, err := recipe.Parse(writeRecipe(t, "version: -1\n## Models\n- m\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ">= 1")
}

func TestParse_VersionNonInteger(t *testing.T) {
	_, err := recipe.Parse(writeRecipe(t, "version: 1.5\n## Models\n- m\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an integer")
}

func TestParse_VersionString(t *testing.T) {
	_, err := recipe.Parse(writeRecipe(t, "version: latest\n## Models\n- m\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an integer")
}

func TestParse_Version1(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n")))
	require.NoError(t, err)
	assert.Equal(t, 1, r.Version)
	require.Len(t, r.Models, 1)
	assert.Equal(t, "claude-sonnet-4-6", r.Models[0])
}

func TestParse_VersionAfterTitle(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "# My Recipe\nversion: 1\n\n## Models\n- m\n"))
	require.NoError(t, err)
	assert.Equal(t, 1, r.Version)
	assert.Equal(t, "My Recipe", r.Name)
}

func TestParse_VersionBeforeTitle(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "version: 1\n# My Recipe\n\n## Models\n- m\n"))
	require.NoError(t, err)
	assert.Equal(t, 1, r.Version)
	assert.Equal(t, "My Recipe", r.Name)
}

// ─── BuildRunner tests ──────────────────────────────────────────────────────

func TestBuildRunner_V1(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Models\n- m\n")))
	require.NoError(t, err)

	runner, err := r.BuildRunner()
	require.NoError(t, err)
	assert.Equal(t, 1, runner.Version())
	assert.Equal(t, r, runner.Recipe())
}

func TestBuildRunner_UnsupportedVersion(t *testing.T) {
	// Construct a recipe directly with an unsupported version.
	r := &recipe.Recipe{Version: 99}
	_, err := r.BuildRunner()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported recipe version 99")
}

func TestBuildRunner_ZeroVersion(t *testing.T) {
	r := &recipe.Recipe{}
	_, err := r.BuildRunner()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported recipe version 0")
}

func TestNewV1Runner_WrongVersion(t *testing.T) {
	r := &recipe.Recipe{Version: 2}
	_, err := recipe.NewV1Runner(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected version 1")
}

func TestMaxVersion(t *testing.T) {
	assert.Equal(t, 1, recipe.MaxVersion)
}

// ─── Parse tests ─────────────────────────────────────────────────────────────

func TestParse_Title(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, "# My Recipe\nversion: 1\n\n## Models\n- claude-sonnet-4-6\n"))
	require.NoError(t, err)
	assert.Equal(t, "My Recipe", r.Name)
}

func TestParse_Models(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Models
- claude-sonnet-4-6
- openai/gpt-4o
- gemini-2.5-pro
`)))
	require.NoError(t, err)
	require.Len(t, r.Models, 3)
	assert.Equal(t, "claude-sonnet-4-6", r.Models[0])
	assert.Equal(t, "openai/gpt-4o", r.Models[1])
	assert.Equal(t, "gemini-2.5-pro", r.Models[2])
}

func TestParse_SystemPrompt(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## System Prompt
You are a senior Go engineer.
Follow standard library conventions.

No external dependencies unless necessary.
`)))
	require.NoError(t, err)
	assert.Contains(t, r.SystemPrompt, "senior Go engineer")
	assert.Contains(t, r.SystemPrompt, "No external dependencies")
}

func TestParse_ToolsAllowlist(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tools
- read_file
- search_files
- list_directory
`)))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Equal(t, []string{"read_file", "search_files", "list_directory"}, r.Tools.Allowlist)
	assert.Nil(t, r.Tools.BashPrefixes)
}

func TestParse_ToolsBashRestriction(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tools
- read_file
- bash(go test *, go build *, go vet *)
`)))
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
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tools
- read_file
- bash
`)))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Contains(t, r.Tools.Allowlist, "bash")
	assert.Nil(t, r.Tools.BashPrefixes, "bare bash should not set prefix restrictions")
}

func TestParse_NoToolsSection(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n")))
	require.NoError(t, err)
	assert.Nil(t, r.Tools, "absent ## Tools section should leave Tools nil (all tools enabled)")
}

func TestParse_ToolsEmptySection(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Tools\n")))
	require.NoError(t, err)
	require.NotNil(t, r.Tools, "present but empty ## Tools should yield non-nil ToolsConfig")
	assert.Empty(t, r.Tools.Allowlist, "empty ## Tools should have zero allowlist entries")
}

func TestMarshalMarkdown_ToolsEmpty(t *testing.T) {
	r := &recipe.Recipe{
		Version: 1,
		Tools:   &recipe.ToolsConfig{Allowlist: []string{}},
	}
	md := r.MarshalMarkdown()
	assert.Contains(t, md, "## Tools\n", "non-nil empty tools should emit ## Tools section")

	// Round-trip: parse the emitted markdown back.
	rt, err := recipe.Parse(writeRecipe(t, md))
	require.NoError(t, err)
	require.NotNil(t, rt.Tools, "round-tripped empty tools should be non-nil")
	assert.Empty(t, rt.Tools.Allowlist, "round-tripped empty tools should have zero entries")
}

func TestMarshalMarkdown_ToolsNil(t *testing.T) {
	r := &recipe.Recipe{
		Version: 1,
		Tools:   nil,
	}
	md := r.MarshalMarkdown()
	assert.NotContains(t, md, "## Tools", "nil tools should not emit ## Tools section")
}

func TestParse_MCPServers(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## MCP Servers
- exa: npx @exa-ai/exa-mcp-server
- fs: npx @modelcontextprotocol/server-filesystem /tmp
`)))
	require.NoError(t, err)
	require.Len(t, r.MCPServers, 2)
	assert.Equal(t, "exa", r.MCPServers[0].Name)
	assert.Equal(t, "npx @exa-ai/exa-mcp-server", r.MCPServers[0].Command)
	assert.Equal(t, "fs", r.MCPServers[1].Name)
	assert.Contains(t, r.MCPServers[1].Command, "server-filesystem")
}

func TestParse_Constraints(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Constraints
timeout: 5m
max_steps: 30
`)))
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, r.Constraints.Timeout)
	assert.Equal(t, 30, r.Constraints.MaxSteps)
}

func TestParse_ConstraintsTimeoutSeconds(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Constraints\ntimeout: 300s\n")))
	require.NoError(t, err)
	assert.Equal(t, 300*time.Second, r.Constraints.Timeout)
}

func TestParse_Context(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Context
max_history_turns: 10
strategy: auto_compact
compact_threshold: 0.75
`)))
	require.NoError(t, err)
	assert.Equal(t, 10, r.Context.MaxHistoryTurns)
	assert.Equal(t, "auto_compact", r.Context.Strategy)
	assert.InDelta(t, 0.75, r.Context.CompactThreshold, 0.001)
}

func TestParse_ContextManual(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Context\nstrategy: manual\n")))
	require.NoError(t, err)
	assert.Equal(t, "manual", r.Context.Strategy)
}

func TestParse_ContextOff(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Context\nstrategy: off\n")))
	require.NoError(t, err)
	assert.Equal(t, "off", r.Context.Strategy)
}

func TestParse_SubAgent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Sub-Agent
model: claude-haiku-3
max_depth: 2
tools: read_file, search_files
`)))
	require.NoError(t, err)
	assert.Equal(t, "claude-haiku-3", r.SubAgent.Model)
	assert.Equal(t, 2, r.SubAgent.MaxDepth)
	assert.Equal(t, "read_file, search_files", r.SubAgent.Tools)
}

func TestParse_SubAgentMaxDepthZero(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Sub-Agent\nmax_depth: 0\n")))
	require.NoError(t, err)
	assert.Equal(t, 0, r.SubAgent.MaxDepth, "max_depth: 0 should disable sub-agents")
}

func TestParse_SubAgentNotSet(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n")))
	require.NoError(t, err)
	assert.Equal(t, -1, r.SubAgent.MaxDepth, "unset max_depth should be -1 sentinel")
}

func TestParse_Sandbox(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Sandbox
filesystem: project_only
network: none
`)))
	require.NoError(t, err)
	assert.Equal(t, "project_only", r.Sandbox.Filesystem)
	assert.Equal(t, "none", r.Sandbox.Network)
}

func TestParse_Tasks(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tasks
- Refactor the HTTP handler
- Add table-driven tests
- Fix the race condition
`)))
	require.NoError(t, err)
	require.Len(t, r.Tasks, 3)
	assert.Equal(t, "Refactor the HTTP handler", r.Tasks[0])
}

func TestParse_UnknownSection(t *testing.T) {
	// Unknown sections should not cause an error.
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Models
- claude-sonnet-4-6

## Totally Unknown Section
some content here
`)))
	require.NoError(t, err)
	assert.Len(t, r.Models, 1)
}

func TestParse_Metadata(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Metadata
name: Go Refactoring Suite
description: Tests Go refactoring tasks
tags: go, refactoring
author: charlessuarez
version: 1.0
contribute: true
`)))
	require.NoError(t, err)
	assert.Equal(t, "Go Refactoring Suite", r.Metadata.Name)
	assert.Equal(t, "charlessuarez", r.Metadata.Author)
	assert.True(t, r.Metadata.Contribute)
	require.Len(t, r.Metadata.Tags, 2)
	assert.Equal(t, "go", r.Metadata.Tags[0])
}

// ─── Discover tests ───────────────────────────────────────────────────────────

func TestDiscover_ExplicitPath(t *testing.T) {
	path := writeRecipe(t, v1("## Models\n- test-model\n"))
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
	assert.Equal(t, 1, r.Version)
	// Default recipe sets context strategy (sub-agent depth gated by SubagentEnabled).
	assert.Equal(t, "auto_compact", r.Context.Strategy)
	assert.Equal(t, -1, r.SubAgent.MaxDepth, "default recipe no longer sets sub-agent depth (feature gated)")
}

func TestDiscover_CwdRecipeMd(t *testing.T) {
	orig, err := os.Getwd()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	require.NoError(t, os.WriteFile("recipe.md", []byte("version: 1\n## Models\n- local-model\n"), 0o600))

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

	require.NoError(t, os.MkdirAll(".errata", 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(".errata", "recipe.md"),
		[]byte("version: 1\n## Models\n- errata-model\n"), 0o600))

	r, err := recipe.Discover("")
	require.NoError(t, err)
	require.Len(t, r.Models, 1)
	assert.Equal(t, "errata-model", r.Models[0])
}


func TestParse_ModelParamsSeed(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Model Parameters
seed: 42
`)))
	require.NoError(t, err)
	require.NotNil(t, r.ModelParams.Seed)
	assert.Equal(t, int64(42), *r.ModelParams.Seed)
}

func TestParse_ModelParamsSeedZero(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Model Parameters
seed: 0
`)))
	require.NoError(t, err)
	require.NotNil(t, r.ModelParams.Seed, "seed: 0 should be set, not nil")
	assert.Equal(t, int64(0), *r.ModelParams.Seed)
}

func TestParse_ModelParamsSeedNegative(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Model Parameters
seed: -1
`)))
	require.NoError(t, err)
	require.NotNil(t, r.ModelParams.Seed)
	assert.Equal(t, int64(-1), *r.ModelParams.Seed)
}

func TestParse_ModelParamsSeedAbsent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Model Parameters
temperature: 0.5
`)))
	require.NoError(t, err)
	assert.Nil(t, r.ModelParams.Seed, "absent seed should be nil")
}


func TestParse_SuccessCriteria(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Success Criteria
- no_errors
- has_writes
- contains: all tests pass
`)))
	require.NoError(t, err)
	require.Len(t, r.SuccessCriteria, 3)
	assert.Equal(t, "no_errors", r.SuccessCriteria[0])
	assert.Equal(t, "has_writes", r.SuccessCriteria[1])
	assert.Equal(t, "contains: all tests pass", r.SuccessCriteria[2])
}

func TestParse_SuccessCriteriaAbsent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n")))
	require.NoError(t, err)
	assert.Nil(t, r.SuccessCriteria)
}

func TestParse_ContextTaskMode(t *testing.T) {
	for _, mode := range []string{"independent", "sequential"} {
		t.Run(mode, func(t *testing.T) {
			r, err := recipe.Parse(writeRecipe(t, v1("## Context\ntask_mode: "+mode+"\n")))
			require.NoError(t, err)
			assert.Equal(t, mode, r.Context.TaskMode)
		})
	}
}

func TestParse_ContextTaskModeAbsent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Context\nstrategy: auto_compact\n")))
	require.NoError(t, err)
	assert.Empty(t, r.Context.TaskMode)
}

func TestParse_ContextTaskModeInvalid(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Context\ntask_mode: invalid\n")))
	require.NoError(t, err)
	assert.Empty(t, r.Context.TaskMode, "unknown task_mode should be ignored")
}

func TestDefault_IsNonNil(t *testing.T) {
	r := recipe.Default()
	require.NotNil(t, r)
	assert.Equal(t, 1, r.Version)
	assert.Equal(t, "auto_compact", r.Context.Strategy)
	assert.Equal(t, -1, r.SubAgent.MaxDepth, "default recipe no longer sets sub-agent depth (feature gated)")
	assert.Equal(t, 5*time.Minute, r.Constraints.Timeout)
}

// ─── SectionsPresent tests ────────────────────────────────────────────────────

func TestParse_SectionsPresent(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Constraints
max_steps: 50
`)))
	require.NoError(t, err)
	assert.True(t, r.HasSection("constraints"), "declared section should be tracked")
	assert.False(t, r.HasSection("context"), "undeclared section should not be tracked")
	assert.False(t, r.HasSection("model parameters"), "undeclared section should not be tracked")
}

func TestParse_SectionsPresent_Multiple(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Models
- m1

## Constraints
max_steps: 50

## Context
max_history_turns: 10
`)))
	require.NoError(t, err)
	assert.True(t, r.HasSection("models"))
	assert.True(t, r.HasSection("constraints"))
	assert.True(t, r.HasSection("context"))
	assert.False(t, r.HasSection("model parameters"))
	assert.False(t, r.HasSection("sub-agent"))
}

func TestHasSection_NilMap(t *testing.T) {
	r := &recipe.Recipe{}
	assert.False(t, r.HasSection("constraints"), "programmatic recipe with nil map should return false")
	assert.False(t, r.HasSection("context"))
}

// ─── Gap 2: Tool Descriptions ────────────────────────────────────────────────

func TestParse_ToolDescriptions(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tool Descriptions
### bash
Use bash for tests and builds.
Always check exit codes.

### read_file
Read files to understand code.
`)))
	require.NoError(t, err)
	require.Len(t, r.ToolDescriptions, 2)
	assert.Contains(t, r.ToolDescriptions["bash"], "Always check exit codes")
	assert.Contains(t, r.ToolDescriptions["read_file"], "Read files")
}

// ─── Gap 3: Sub-Agent Modes ─────────────────────────────────────────────────

func TestParse_SubAgentModes(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Sub-Agent Modes
### explore
You are a codebase exploration specialist. READ-ONLY mode.

### plan
You are a planning specialist. Do NOT make changes.
`)))
	require.NoError(t, err)
	require.Len(t, r.SubAgentModes, 2)
	assert.Contains(t, r.SubAgentModes["explore"], "READ-ONLY")
	assert.Contains(t, r.SubAgentModes["plan"], "planning specialist")
}

// ─── Gap 4: System Reminders ─────────────────────────────────────────────────

func TestParse_SystemReminders(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## System Reminders
### context_warning
trigger: context_usage > 0.75

Approaching context limit. Be concise.

### tool_failure
trigger: last_tool_call_failed

Analyze the error before retrying.
`)))
	require.NoError(t, err)
	require.Len(t, r.SystemReminders, 2)

	assert.Equal(t, "context_warning", r.SystemReminders[0].Name)
	assert.Equal(t, "context_usage > 0.75", r.SystemReminders[0].Trigger)
	assert.Contains(t, r.SystemReminders[0].Content, "Approaching context limit")

	assert.Equal(t, "tool_failure", r.SystemReminders[1].Name)
	assert.Equal(t, "last_tool_call_failed", r.SystemReminders[1].Trigger)
	assert.Contains(t, r.SystemReminders[1].Content, "Analyze the error")
}

// ─── Gap 5: Hooks ────────────────────────────────────────────────────────────

func TestParse_Hooks(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Hooks
### post_edit_vet
event: post_tool_use
matcher: edit_file
command: go vet ./... 2>&1 | head -20
timeout: 30s
inject_output: true

### response_logger
event: post_response
command: echo 'done' >> /tmp/log.txt
timeout: 5s
`)))
	require.NoError(t, err)
	require.Len(t, r.Hooks, 2)

	h0 := r.Hooks[0]
	assert.Equal(t, "post_edit_vet", h0.Name)
	assert.Equal(t, "post_tool_use", h0.Event)
	assert.Equal(t, "edit_file", h0.Matcher)
	assert.Equal(t, "go vet ./... 2>&1 | head -20", h0.Command)
	assert.Equal(t, "30s", h0.Timeout)
	assert.True(t, h0.InjectOutput)
	assert.Equal(t, "command", h0.Action) // default action

	h1 := r.Hooks[1]
	assert.Equal(t, "response_logger", h1.Name)
	assert.Equal(t, "post_response", h1.Event)
	assert.Empty(t, h1.Matcher)
	assert.False(t, h1.InjectOutput)
}

// ─── Gap 6: Summarization Prompt ─────────────────────────────────────────────

func TestParse_SummarizationPrompt(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Context Summarization Prompt
Summarize for context continuity. Preserve: file paths, decisions.
`)))
	require.NoError(t, err)
	assert.Contains(t, r.SummarizationPrompt, "context continuity")
}

// ─── Gap 7: Output Processing ────────────────────────────────────────────────

func TestParse_OutputProcessing(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Output Processing
### bash
max_lines: 200
truncation: tail
truncation_message: [Truncated to last 200 lines. Full output: {line_count} lines]

### web_fetch
max_tokens: 5000
truncation: head
`)))
	require.NoError(t, err)
	require.Len(t, r.OutputProcessing, 2)

	bash := r.OutputProcessing["bash"]
	assert.Equal(t, 200, bash.MaxLines)
	assert.Equal(t, "tail", bash.Truncation)
	assert.Contains(t, bash.TruncationMessage, "{line_count}")

	wf := r.OutputProcessing["web_fetch"]
	assert.Equal(t, 5000, wf.MaxTokens)
	assert.Equal(t, "head", wf.Truncation)
}

// ─── Model Profiles ──────────────────────────────────────────────────────────

func TestParse_ModelProfiles(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Model Profiles
### gpt-4o
context_budget: 32000
tool_format: function_calling
mid_convo_system: false

### gemini-2.0-flash
context_budget: 1000000
`)))
	require.NoError(t, err)
	require.Len(t, r.ModelProfiles, 2)

	gpt := r.ModelProfiles["gpt-4o"]
	assert.Equal(t, 32000, gpt.ContextBudget)
	assert.Equal(t, "function_calling", gpt.ToolFormat)
	require.NotNil(t, gpt.MidConvoSystem)
	assert.False(t, *gpt.MidConvoSystem)

	gemini := r.ModelProfiles["gemini-2.0-flash"]
	assert.Equal(t, 1000000, gemini.ContextBudget)
}

// ─── Backward Compatibility ──────────────────────────────────────────────────

func TestParse_FullRecipe_BackwardCompat(t *testing.T) {
	// All new sections alongside existing sections — nothing should break.
	r, err := recipe.Parse(writeRecipe(t, v1(`
# Full Test Recipe

## Models
- claude-sonnet-4-6
- gpt-4o
- gemini-2.0-flash

## System Prompt
You are working on a Go monorepo.

## Tools
- read_file
- bash(go test *, go vet *)

## Tool Descriptions
### bash
Use bash for tests.

## Model Profiles
### gpt-4o
context_budget: 32000

## Constraints
timeout: 10m
max_steps: 50

## Context
max_history_turns: 30
strategy: auto_compact
compact_threshold: 0.75

## Sub-Agent
model: claude-sonnet-4-6
max_depth: 2

## Sub-Agent Modes
### explore
Read-only exploration.

## System Reminders
### context_warning
trigger: context_usage > 0.75

Be concise.

## Hooks
### vet_check
event: post_tool_use
matcher: edit_file
command: go vet ./...
timeout: 30s

## Context Summarization Prompt
Keep file paths and decisions.

## Output Processing
### bash
max_lines: 200
truncation: tail

## Sandbox
filesystem: project_only
network: full

## Model Parameters
seed: 42
`)))
	require.NoError(t, err)

	assert.Equal(t, 1, r.Version)

	// Existing sections still work.
	assert.Len(t, r.Models, 3)
	assert.Contains(t, r.SystemPrompt, "Go monorepo")
	require.NotNil(t, r.Tools)
	assert.Contains(t, r.Tools.Allowlist, "bash")
	assert.Equal(t, 10*time.Minute, r.Constraints.Timeout)
	assert.Equal(t, 50, r.Constraints.MaxSteps)
	assert.Equal(t, 30, r.Context.MaxHistoryTurns)
	assert.Equal(t, "auto_compact", r.Context.Strategy)
	assert.Equal(t, "claude-sonnet-4-6", r.SubAgent.Model)
	assert.Equal(t, 2, r.SubAgent.MaxDepth)
	assert.Equal(t, "project_only", r.Sandbox.Filesystem)
	require.NotNil(t, r.ModelParams.Seed)
	assert.Equal(t, int64(42), *r.ModelParams.Seed)

	// New sections also parsed.
	assert.Len(t, r.ToolDescriptions, 1)
	assert.Len(t, r.ModelProfiles, 1)
	assert.Len(t, r.SubAgentModes, 1)
	assert.Len(t, r.SystemReminders, 1)
	assert.Len(t, r.Hooks, 1)
	assert.NotEmpty(t, r.SummarizationPrompt)
	assert.Len(t, r.OutputProcessing, 1)
}

// ─── Empty new sections ─────────────────────────────────────────────────────

func TestParse_ExampleRecipe_AllNewSections(t *testing.T) {
	// Parse the full example recipe and verify every new section is populated.
	examplePath := filepath.Join("..", "..", "recipe.example.md")
	if _, err := os.Stat(examplePath); err != nil {
		t.Skip("recipe.example.md not found at expected relative path")
	}
	r, err := recipe.Parse(examplePath)
	require.NoError(t, err)

	assert.Equal(t, 1, r.Version)
	assert.Equal(t, "My Project Recipe", r.Name)
	assert.Len(t, r.Models, 3)

	// Tool Descriptions
	require.NotNil(t, r.ToolDescriptions)
	assert.Contains(t, r.ToolDescriptions, "bash")
	assert.Contains(t, r.ToolDescriptions, "read_file")
	assert.Contains(t, r.ToolDescriptions, "search_code")
	assert.Contains(t, r.ToolDescriptions["bash"], "exit codes")

	// Sub-Agent Modes removed from example recipe (feature gated).
	assert.Empty(t, r.SubAgentModes)

	// Context Summarization Prompt
	assert.Contains(t, r.SummarizationPrompt, "Summarize this conversation")

	// System Reminders
	require.Len(t, r.SystemReminders, 4)
	assert.Equal(t, "context_warning", r.SystemReminders[0].Name)
	assert.Equal(t, "context_usage > 0.75", r.SystemReminders[0].Trigger)
	assert.Contains(t, r.SystemReminders[0].Content, "context limit")
	assert.Equal(t, "many_turns", r.SystemReminders[1].Name)
	assert.Equal(t, "tool_failure", r.SystemReminders[2].Name)
	assert.Equal(t, "focus_reminder", r.SystemReminders[3].Name)
	assert.Equal(t, "manual", r.SystemReminders[3].Trigger)
	assert.Contains(t, r.SystemReminders[3].Content, "focus on the specific task")

	// Hooks
	require.Len(t, r.Hooks, 3)
	assert.Equal(t, "post_edit_vet", r.Hooks[0].Name)
	assert.Equal(t, "post_tool_use", r.Hooks[0].Event)
	assert.Equal(t, "edit_file", r.Hooks[0].Matcher)
	assert.Contains(t, r.Hooks[0].Command, "go vet")
	assert.Equal(t, "30s", r.Hooks[0].Timeout)
	assert.True(t, r.Hooks[0].InjectOutput)

	assert.Equal(t, "post_edit_test", r.Hooks[1].Name)
	assert.Equal(t, "session_start_check", r.Hooks[2].Name)
	assert.Equal(t, "session_start", r.Hooks[2].Event)

	// Output Processing
	require.NotNil(t, r.OutputProcessing)
	assert.Equal(t, 200, r.OutputProcessing["bash"].MaxLines)
	assert.Equal(t, "tail", r.OutputProcessing["bash"].Truncation)
	assert.Contains(t, r.OutputProcessing["bash"].TruncationMessage, "{max_lines}")
	assert.Equal(t, 100, r.OutputProcessing["search_code"].MaxLines)
	assert.Equal(t, "head_tail", r.OutputProcessing["search_code"].Truncation)
	assert.Equal(t, 500, r.OutputProcessing["read_file"].MaxLines)

	// Model Profiles
	require.NotNil(t, r.ModelProfiles)
	gpt := r.ModelProfiles["gpt-4o"]
	assert.Equal(t, 128000, gpt.ContextBudget)
	assert.Equal(t, "function_calling", gpt.ToolFormat)

	gemini := r.ModelProfiles["gemini-2.0-flash"]
	assert.Equal(t, 1000000, gemini.ContextBudget)

	llama := r.ModelProfiles["local-llama"]
	assert.Equal(t, 8192, llama.ContextBudget)
	assert.Equal(t, "text_in_prompt", llama.ToolFormat)
	require.NotNil(t, llama.MidConvoSystem)
	assert.False(t, *llama.MidConvoSystem)
}

// ─── Parse edge cases ───────────────────────────────────────────────────────

func TestParse_MCPServers_NoColon(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## MCP Servers\n- no_colon_here\n")))
	require.NoError(t, err)
	assert.Empty(t, r.MCPServers)
}

func TestParse_MCPServers_EmptyNameOrCommand(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## MCP Servers\n- :\n- name:\n- :cmd\n")))
	require.NoError(t, err)
	assert.Empty(t, r.MCPServers, "empty name or command should be skipped")
}

func TestParse_ModelParams_InvalidValues(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Model Parameters
temperature: not_a_number
max_tokens: abc
seed: xyz
`)))
	require.NoError(t, err)
	assert.Nil(t, r.ModelParams.Temperature)
	assert.Nil(t, r.ModelParams.MaxTokens)
	assert.Nil(t, r.ModelParams.Seed)
}

func TestParse_ModelParams_MaxTokensZero(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Model Parameters\nmax_tokens: 0\n")))
	require.NoError(t, err)
	assert.Nil(t, r.ModelParams.MaxTokens, "max_tokens: 0 should be ignored (must be > 0)")
}

func TestParse_Sandbox_UnknownValues(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Sandbox
filesystem: invalid_value
network: unknown
`)))
	require.NoError(t, err)
	assert.Empty(t, r.Sandbox.Filesystem)
	assert.Empty(t, r.Sandbox.Network)
}

func TestParse_Constraints_IntegerTimeout(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Constraints\ntimeout: 120\n")))
	require.NoError(t, err)
	assert.Equal(t, 120*time.Second, r.Constraints.Timeout, "integer timeout should be treated as seconds")
}

func TestParse_Constraints_InvalidTimeout(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Constraints\ntimeout: abc\n")))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), r.Constraints.Timeout)
}

func TestParse_Constraints_InvalidMaxSteps(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Constraints\nmax_steps: not_a_number\n")))
	require.NoError(t, err)
	assert.Equal(t, 0, r.Constraints.MaxSteps)
}

// ─── Empty new sections ─────────────────────────────────────────────────────

func TestParse_EmptyNewSections(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tool Descriptions
## Model Profiles
## System Reminders
## Hooks
`)))
	require.NoError(t, err)
	assert.Nil(t, r.ToolDescriptions)
	assert.Nil(t, r.ModelProfiles)
	assert.Empty(t, r.SystemReminders)
	assert.Empty(t, r.Hooks)
}

// ─── MarshalMarkdown tests ───────────────────────────────────────────────────

func TestMarshalMarkdown_RoundTrip(t *testing.T) {
	seed := int64(42)
	temp := 0.7
	maxTok := 4096
	orig := &recipe.Recipe{
		Version:      1,
		Name:         "Test Recipe",
		Models:       []string{"claude-sonnet-4-6", "gpt-4o"},
		SystemPrompt: "You are a helpful assistant.",
		Tools: &recipe.ToolsConfig{
			Allowlist:    []string{"read_file", "bash"},
			BashPrefixes: []string{"go test", "go build"},
		},
		MCPServers: []recipe.MCPServerEntry{
			{Name: "exa", Command: "npx @exa-ai/exa-mcp-server"},
		},
		ModelParams: recipe.ModelParamsConfig{
			Temperature: &temp,
			MaxTokens:   &maxTok,
			Seed:        &seed,
		},
		Constraints: recipe.ConstraintsConfig{
			Timeout:     10 * time.Minute,
			BashTimeout: 30 * time.Second,
			MaxSteps:    50,
		},
		Context: recipe.ContextConfig{
			Strategy:         "auto_compact",
			MaxHistoryTurns:  30,
			CompactThreshold: 0.75,
		},
		SubAgent: recipe.SubAgentConfig{
			Model:    "gpt-4o",
			MaxDepth: 2,
			Tools:    "inherit",
		},
		Sandbox: recipe.SandboxConfig{
			Filesystem:      "project_only",
			Network:         "full",
			AllowLocalFetch: true,
		},
	}

	md := orig.MarshalMarkdown()
	assert.Contains(t, md, "version: 1")

	// Write and re-parse.
	path := writeRecipe(t, md)
	parsed, err := recipe.Parse(path)
	require.NoError(t, err)

	assert.Equal(t, 1, parsed.Version)
	assert.Equal(t, "Test Recipe", parsed.Name)
	assert.Equal(t, orig.Models, parsed.Models)
	assert.Equal(t, orig.SystemPrompt, parsed.SystemPrompt)
	require.NotNil(t, parsed.Tools)
	assert.Contains(t, parsed.Tools.Allowlist, "read_file")
	assert.Contains(t, parsed.Tools.Allowlist, "bash")
	assert.Equal(t, orig.Tools.BashPrefixes, parsed.Tools.BashPrefixes)
	assert.Len(t, parsed.MCPServers, 1)
	assert.Equal(t, "exa", parsed.MCPServers[0].Name)
	assert.Equal(t, *orig.ModelParams.Seed, *parsed.ModelParams.Seed)
	assert.InDelta(t, *orig.ModelParams.Temperature, *parsed.ModelParams.Temperature, 1e-9)
	assert.Equal(t, *orig.ModelParams.MaxTokens, *parsed.ModelParams.MaxTokens)
	assert.Equal(t, orig.Constraints.Timeout, parsed.Constraints.Timeout)
	assert.Equal(t, orig.Constraints.BashTimeout, parsed.Constraints.BashTimeout)
	assert.Equal(t, orig.Constraints.MaxSteps, parsed.Constraints.MaxSteps)
	assert.Equal(t, orig.Context.Strategy, parsed.Context.Strategy)
	assert.Equal(t, orig.Context.MaxHistoryTurns, parsed.Context.MaxHistoryTurns)
	assert.InDelta(t, orig.Context.CompactThreshold, parsed.Context.CompactThreshold, 1e-9)
	assert.Equal(t, orig.SubAgent.Model, parsed.SubAgent.Model)
	assert.Equal(t, orig.SubAgent.MaxDepth, parsed.SubAgent.MaxDepth)
	assert.Equal(t, orig.SubAgent.Tools, parsed.SubAgent.Tools)
	assert.Equal(t, orig.Sandbox.Filesystem, parsed.Sandbox.Filesystem)
	assert.Equal(t, orig.Sandbox.Network, parsed.Sandbox.Network)
	assert.Equal(t, orig.Sandbox.AllowLocalFetch, parsed.Sandbox.AllowLocalFetch)
}

func TestMarshalMarkdown_DefaultRecipe(t *testing.T) {
	r := recipe.Default()
	md := r.MarshalMarkdown()
	assert.Contains(t, md, "# Errata Default")
	assert.Contains(t, md, "version: 1")
	assert.Contains(t, md, "## Context")
}

func TestMarshalMarkdown_EmptyRecipe(t *testing.T) {
	r := &recipe.Recipe{}
	md := r.MarshalMarkdown()
	assert.Contains(t, md, "# Errata Recipe") // default name
	// Should not contain section headers for empty fields.
	assert.NotContains(t, md, "## Models")
	assert.NotContains(t, md, "## Constraints")
	// Version 0 should not be emitted.
	assert.NotContains(t, md, "version:")
}

func TestMarshalMarkdown_Version_RoundTrip(t *testing.T) {
	orig := &recipe.Recipe{
		Version: 1,
		Name:    "Version Test",
		Models:  []string{"m"},
	}
	md := orig.MarshalMarkdown()
	assert.Contains(t, md, "version: 1")

	path := writeRecipe(t, md)
	parsed, err := recipe.Parse(path)
	require.NoError(t, err)
	assert.Equal(t, 1, parsed.Version)
}

// ─── bash_timeout in Constraints ─────────────────────────────────────────────

func TestParse_Constraints_BashTimeout(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Constraints
timeout: 10m
bash_timeout: 30s
max_steps: 50
`)))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Minute, r.Constraints.Timeout)
	assert.Equal(t, 30*time.Second, r.Constraints.BashTimeout)
	assert.Equal(t, 50, r.Constraints.MaxSteps)
}

func TestParse_Constraints_BashTimeoutIntegerSeconds(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Constraints\nbash_timeout: 120\n")))
	require.NoError(t, err)
	assert.Equal(t, 120*time.Second, r.Constraints.BashTimeout)
}

func TestParse_Constraints_BashTimeoutInvalid(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1("## Constraints\nbash_timeout: abc\n")))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), r.Constraints.BashTimeout)
}

// ─── allow_local_fetch in Sandbox ────────────────────────────────────────────

func TestParse_Sandbox_AllowLocalFetch(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Sandbox
filesystem: project_only
allow_local_fetch: true
`)))
	require.NoError(t, err)
	assert.Equal(t, "project_only", r.Sandbox.Filesystem)
	assert.True(t, r.Sandbox.AllowLocalFetch)
}

func TestParse_Sandbox_AllowLocalFetchFalse(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Sandbox
allow_local_fetch: false
`)))
	require.NoError(t, err)
	assert.False(t, r.Sandbox.AllowLocalFetch)
}

func TestParse_Sandbox_AllowLocalFetchDefault(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Sandbox
filesystem: project_only
`)))
	require.NoError(t, err)
	assert.False(t, r.Sandbox.AllowLocalFetch)
}

// ─── Per-Tool Guidance ───────────────────────────────────────────────────────

func TestParse_ToolsGuidance(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tools
- read_file: Always check file size first.
- bash: Run tests before committing.
- search_code
`)))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Equal(t, []string{"read_file", "bash", "search_code"}, r.Tools.Allowlist)
	require.NotNil(t, r.Tools.Guidance)
	assert.Equal(t, "Always check file size first.", r.Tools.Guidance["read_file"])
	assert.Equal(t, "Run tests before committing.", r.Tools.Guidance["bash"])
	_, hasSearchCode := r.Tools.Guidance["search_code"]
	assert.False(t, hasSearchCode, "tool without guidance text should not appear in map")
}

func TestParse_ToolsGuidance_BashWithPrefixesAndGuidance(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tools
- bash(go test *, go build *): Only run go commands.
- read_file
`)))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Contains(t, r.Tools.Allowlist, "bash")
	assert.Equal(t, []string{"go test *", "go build *"}, r.Tools.BashPrefixes)
	require.NotNil(t, r.Tools.Guidance)
	assert.Equal(t, "Only run go commands.", r.Tools.Guidance["bash"])
}

func TestParse_ToolsGuidance_NoGuidance(t *testing.T) {
	r, err := recipe.Parse(writeRecipe(t, v1(`
## Tools
- read_file
- bash
`)))
	require.NoError(t, err)
	require.NotNil(t, r.Tools)
	assert.Nil(t, r.Tools.Guidance, "no per-tool guidance should leave Guidance nil")
}

func TestMarshalMarkdown_ToolsGuidance_RoundTrip(t *testing.T) {
	orig := &recipe.Recipe{
		Version: 1,
		Name:    "Test",
		Tools: &recipe.ToolsConfig{
			Allowlist: []string{"read_file", "bash"},
			Guidance: map[string]string{
				"read_file": "Check size first.",
				"bash":      "Run tests only.",
			},
		},
	}
	md := orig.MarshalMarkdown()
	assert.Contains(t, md, "- read_file: Check size first.")
	assert.Contains(t, md, "- bash: Run tests only.")

	path := writeRecipe(t, md)
	parsed, err := recipe.Parse(path)
	require.NoError(t, err)
	require.NotNil(t, parsed.Tools)
	assert.Equal(t, orig.Tools.Guidance, parsed.Tools.Guidance)
}

func TestMarshalMarkdown_ToolsGuidance_MixedWithAndWithout(t *testing.T) {
	r := &recipe.Recipe{
		Version: 1,
		Name:    "Test",
		Tools: &recipe.ToolsConfig{
			Allowlist: []string{"read_file", "bash"},
			Guidance:  map[string]string{"read_file": "Custom guidance."},
		},
	}
	md := r.MarshalMarkdown()
	assert.Contains(t, md, "- read_file: Custom guidance.")
	assert.Contains(t, md, "- bash\n")
}

