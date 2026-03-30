package recipestore_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/pkg/recipestore"
)

func TestHash_Deterministic(t *testing.T) {
	cfg := &recipestore.RecipeSnapshot{
		Name:         "test-recipe",
		SystemPrompt: "You are helpful.",
		Tools:        []string{"read_file", "write_file"},
	}
	h1 := recipestore.Hash(cfg)
	h2 := recipestore.Hash(cfg)
	assert.Equal(t, h1, h2)
	assert.Contains(t, h1, "rcp_v")
}

func TestHash_IncludesName(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Name: "recipe-a", SystemPrompt: "same"}
	cfg2 := &recipestore.RecipeSnapshot{Name: "recipe-b", SystemPrompt: "same"}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2),
		"different names should produce different hashes")
}

func TestHash_IncludesVersion(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Version: 1, SystemPrompt: "same"}
	cfg2 := &recipestore.RecipeSnapshot{Version: 2, SystemPrompt: "same"}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2),
		"different versions should produce different hashes")
	assert.Contains(t, recipestore.Hash(cfg1), "rcp_v1_")
	assert.Contains(t, recipestore.Hash(cfg2), "rcp_v2_")
}

func TestHash_DifferentSystemPrompts(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{SystemPrompt: "prompt-a"}
	cfg2 := &recipestore.RecipeSnapshot{SystemPrompt: "prompt-b"}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestHash_DifferentTools(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Name: "r", Tools: []string{"read_file"}}
	cfg2 := &recipestore.RecipeSnapshot{Name: "r", Tools: []string{"read_file", "write_file"}}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestPutAndGet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	cfg := &recipestore.RecipeSnapshot{Name: "my-recipe", Tools: []string{"bash"}}
	h := s.Put(cfg)
	assert.Contains(t, h, "rcp_v")

	got := s.Get(h)
	require.NotNil(t, got)
	assert.Equal(t, "my-recipe", got.Name)
	assert.Equal(t, []string{"bash"}, got.Tools)
}

func TestPut_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	cfg := &recipestore.RecipeSnapshot{Name: "dup"}
	h1 := s.Put(cfg)
	h2 := s.Put(cfg)
	assert.Equal(t, h1, h2)

	all := s.List()
	assert.Len(t, all, 1)
}

func TestGet_NotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)
	assert.Nil(t, s.Get("rcp_v0_0000"))
}

func TestNew_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "recipes.json")
	s := recipestore.New(path)
	assert.Empty(t, s.List())
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s1 := recipestore.New(path)

	cfg := &recipestore.RecipeSnapshot{
		Name:         "persisted",
		SystemPrompt: "Be brief.",
		Tools:        []string{"read_file", "bash"},
	}
	h := s1.Put(cfg)

	// Reload from disk.
	s2 := recipestore.New(path)
	got := s2.Get(h)
	require.NotNil(t, got)
	assert.Equal(t, "persisted", got.Name)
	assert.Equal(t, "Be brief.", got.SystemPrompt)
	assert.Equal(t, []string{"read_file", "bash"}, got.Tools)
}

func TestHash_DifferentToolGuidance(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{ToolGuidance: map[string]string{"bash": "guidance-a"}}
	cfg2 := &recipestore.RecipeSnapshot{ToolGuidance: map[string]string{"bash": "guidance-b"}}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestHash_DifferentBashPrefixes(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Tools: []string{"bash"}, BashPrefixes: []string{"go test"}}
	cfg2 := &recipestore.RecipeSnapshot{Tools: []string{"bash"}}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestHash_DifferentContext(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Context: &recipestore.ContextConfig{Strategy: "auto_compact"}}
	cfg2 := &recipestore.RecipeSnapshot{Context: &recipestore.ContextConfig{Strategy: "manual"}}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestRecipeSnapshot_RoundTrip(t *testing.T) {
	cfg := &recipestore.RecipeSnapshot{
		Version:             1,
		Name:                "full-config",
		SystemPrompt:        "system prompt text",
		ToolGuidance:        map[string]string{"bash": "custom tool guidance"},
		Tools:               []string{"read_file", "write_file", "bash"},
		BashPrefixes:        []string{"go test", "go vet"},
		ToolDescriptions:    map[string]string{"read_file": "reads a file"},
		Tasks:               []string{"fix the bug", "add tests"},
		SuccessCriteria:     []string{"tests pass", "no lint errors"},
		MCPServers:          []recipestore.MCPServerSnapshot{{Name: "db", Command: "npx db-server"}},
		Sandbox:             &recipestore.SandboxConfig{Filesystem: "project_only", Network: "none", AllowLocalFetch: true},
		SummarizationPrompt: "summarize the conversation",
		Constraints: &recipestore.ConstraintsConfig{
			MaxSteps:    10,
			Timeout:     "5m0s",
			BashTimeout: "30s",
			ProjectRoot: "/tmp/proj",
		},
		Context: &recipestore.ContextConfig{
			MaxHistoryTurns:  20,
			Strategy:         "auto_compact",
			CompactThreshold: 0.8,
			TaskMode:         "sequential",
		},
		OutputProcessing: map[string]recipestore.OutputRuleConfig{
			"bash": {MaxLines: 50, Truncation: "tail", TruncationMessage: "truncated"},
		},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var got recipestore.RecipeSnapshot
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, 1, got.Version)
	assert.Equal(t, "full-config", got.Name)
	assert.Equal(t, "system prompt text", got.SystemPrompt)
	assert.Equal(t, map[string]string{"bash": "custom tool guidance"}, got.ToolGuidance)
	assert.Equal(t, []string{"read_file", "write_file", "bash"}, got.Tools)
	assert.Equal(t, []string{"go test", "go vet"}, got.BashPrefixes)
	assert.Equal(t, map[string]string{"read_file": "reads a file"}, got.ToolDescriptions)
	assert.Equal(t, "summarize the conversation", got.SummarizationPrompt)

	assert.Equal(t, []string{"fix the bug", "add tests"}, got.Tasks)
	assert.Equal(t, []string{"tests pass", "no lint errors"}, got.SuccessCriteria)
	require.Len(t, got.MCPServers, 1)
	assert.Equal(t, "db", got.MCPServers[0].Name)
	assert.Equal(t, "npx db-server", got.MCPServers[0].Command)

	require.NotNil(t, got.Sandbox)
	assert.Equal(t, "project_only", got.Sandbox.Filesystem)
	assert.Equal(t, "none", got.Sandbox.Network)
	assert.True(t, got.Sandbox.AllowLocalFetch)

	require.NotNil(t, got.Constraints)
	assert.Equal(t, 10, got.Constraints.MaxSteps)
	assert.Equal(t, "5m0s", got.Constraints.Timeout)
	assert.Equal(t, "30s", got.Constraints.BashTimeout)
	assert.Equal(t, "/tmp/proj", got.Constraints.ProjectRoot)

	require.NotNil(t, got.Context)
	assert.Equal(t, 20, got.Context.MaxHistoryTurns)
	assert.Equal(t, "auto_compact", got.Context.Strategy)
	assert.InDelta(t, 0.8, got.Context.CompactThreshold, 1e-9)
	assert.Equal(t, "sequential", got.Context.TaskMode)

	require.Contains(t, got.OutputProcessing, "bash")
	assert.Equal(t, 50, got.OutputProcessing["bash"].MaxLines)
	assert.Equal(t, "tail", got.OutputProcessing["bash"].Truncation)
}

func TestList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	s.Put(&recipestore.RecipeSnapshot{Name: "a", SystemPrompt: "prompt-a"})
	s.Put(&recipestore.RecipeSnapshot{Name: "b", SystemPrompt: "prompt-b"})

	all := s.List()
	assert.Len(t, all, 2)
}

func TestHashesForName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	s.Put(&recipestore.RecipeSnapshot{Name: "target", Tools: []string{"a"}})
	s.Put(&recipestore.RecipeSnapshot{Name: "target", Tools: []string{"b"}})
	s.Put(&recipestore.RecipeSnapshot{Name: "other"})

	hashes := s.HashesForName("target")
	assert.Len(t, hashes, 2)

	hashes = s.HashesForName("nonexistent")
	assert.Empty(t, hashes)
}

func TestNew_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	require.NoError(t, os.WriteFile(path, []byte("{bad json"), 0o600))

	s := recipestore.New(path)
	assert.Empty(t, s.List())
}

func TestPut_CreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "recipes.json")
	s := recipestore.New(path)
	s.Put(&recipestore.RecipeSnapshot{Name: "nested"})

	// Verify file was created.
	_, err := os.Stat(path)
	require.NoError(t, err)
}

// ── SnapshotFromRecipe ──────────────────────────────────────────────────────

func TestSnapshotFromRecipe_AllFields(t *testing.T) {
	rec := &recipe.Recipe{
		Version:      1,
		Name:         "full-recipe",
		SystemPrompt: "be helpful",
		Tools: &recipe.ToolsConfig{
			Allowlist:    []string{"bash", "read_file"},
			BashPrefixes: []string{"go test"},
			Guidance:     map[string]string{"bash": "run tests only"},
		},
		MCPServers: []recipe.MCPServerEntry{
			{Name: "db", Command: "npx db-server"},
		},
		Constraints: recipe.ConstraintsConfig{
			MaxSteps:    5,
			Timeout:     3 * time.Minute,
			BashTimeout: 30 * time.Second,
			ProjectRoot: "/tmp/proj",
		},
		Context: recipe.ContextConfig{
			MaxHistoryTurns:     10,
			Strategy:            "auto_compact",
			CompactThreshold:    0.8,
			TaskMode:            "sequential",
			SummarizationPrompt: "summarize it",
		},
		Sandbox: recipe.SandboxConfig{
			Filesystem:      "project_only",
			Network:         "none",
			AllowLocalFetch: true,
		},
		Tasks:           []string{"fix the bug", "add tests"},
		SuccessCriteria: []string{"tests pass"},
		OutputProcessing: map[string]recipe.OutputRuleConfig{
			"bash": {MaxLines: 50, Truncation: "tail"},
		},
	}
	activeTools := []string{"bash", "read_file", "write_file"}

	snap := recipestore.SnapshotFromRecipe(rec, activeTools)

	assert.Equal(t, 1, snap.Version)
	assert.Equal(t, "full-recipe", snap.Name)
	assert.Equal(t, "be helpful", snap.SystemPrompt)
	assert.Equal(t, activeTools, snap.Tools)
	assert.Equal(t, []string{"go test"}, snap.BashPrefixes)
	assert.Equal(t, map[string]string{"bash": "run tests only"}, snap.ToolGuidance)
	assert.Equal(t, map[string]string{"bash": "run tests only"}, snap.ToolDescriptions)
	assert.Equal(t, "summarize it", snap.SummarizationPrompt)

	assert.Equal(t, []string{"fix the bug", "add tests"}, snap.Tasks)
	assert.Equal(t, []string{"tests pass"}, snap.SuccessCriteria)

	require.Len(t, snap.MCPServers, 1)
	assert.Equal(t, "db", snap.MCPServers[0].Name)
	assert.Equal(t, "npx db-server", snap.MCPServers[0].Command)

	require.NotNil(t, snap.Sandbox)
	assert.Equal(t, "project_only", snap.Sandbox.Filesystem)
	assert.Equal(t, "none", snap.Sandbox.Network)
	assert.True(t, snap.Sandbox.AllowLocalFetch)

	require.NotNil(t, snap.Constraints)
	assert.Equal(t, 5, snap.Constraints.MaxSteps)
	assert.Equal(t, "3m0s", snap.Constraints.Timeout)
	assert.Equal(t, "30s", snap.Constraints.BashTimeout)
	assert.Equal(t, "/tmp/proj", snap.Constraints.ProjectRoot)

	require.NotNil(t, snap.Context)
	assert.Equal(t, 10, snap.Context.MaxHistoryTurns)
	assert.Equal(t, "auto_compact", snap.Context.Strategy)
	assert.InDelta(t, 0.8, snap.Context.CompactThreshold, 1e-9)
	assert.Equal(t, "sequential", snap.Context.TaskMode)

	require.Contains(t, snap.OutputProcessing, "bash")
	assert.Equal(t, 50, snap.OutputProcessing["bash"].MaxLines)
}

func TestSnapshotFromRecipe_MinimalRecipe(t *testing.T) {
	rec := &recipe.Recipe{Version: 1}
	snap := recipestore.SnapshotFromRecipe(rec, nil)

	assert.Equal(t, 1, snap.Version)
	assert.Equal(t, "default", snap.Name)
	assert.Empty(t, snap.SystemPrompt)
	assert.Nil(t, snap.Tools)
	assert.Nil(t, snap.Tasks)
	assert.Nil(t, snap.SuccessCriteria)
	assert.Nil(t, snap.MCPServers)
	assert.Nil(t, snap.Sandbox)
	assert.Nil(t, snap.Constraints)
	assert.Nil(t, snap.Context)
	assert.Nil(t, snap.OutputProcessing)
}

func TestHash_DifferentTasks(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Name: "r", Tasks: []string{"task-a"}}
	cfg2 := &recipestore.RecipeSnapshot{Name: "r", Tasks: []string{"task-b"}}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestHash_DifferentSandbox(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Name: "r", Sandbox: &recipestore.SandboxConfig{Filesystem: "project_only"}}
	cfg2 := &recipestore.RecipeSnapshot{Name: "r", Sandbox: &recipestore.SandboxConfig{Filesystem: "unrestricted"}}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestHash_DifferentMCPServers(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Name: "r", MCPServers: []recipestore.MCPServerSnapshot{{Name: "a", Command: "cmd-a"}}}
	cfg2 := &recipestore.RecipeSnapshot{Name: "r", MCPServers: []recipestore.MCPServerSnapshot{{Name: "b", Command: "cmd-b"}}}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}
