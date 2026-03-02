package recipestore_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/recipestore"
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
	assert.Contains(t, h1, "sha256:")
}

func TestHash_ExcludesName(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Name: "recipe-a", SystemPrompt: "same"}
	cfg2 := &recipestore.RecipeSnapshot{Name: "recipe-b", SystemPrompt: "same"}
	assert.Equal(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2),
		"name should not affect the hash")
}

func TestHash_ExcludesVersion(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Version: 1, SystemPrompt: "same"}
	cfg2 := &recipestore.RecipeSnapshot{Version: 2, SystemPrompt: "same"}
	assert.Equal(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2),
		"version should not affect the hash")
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
	path := filepath.Join(t.TempDir(), "configs.json")
	s := recipestore.New(path)

	cfg := &recipestore.RecipeSnapshot{Name: "my-recipe", Tools: []string{"bash"}}
	h := s.Put(cfg)
	assert.Contains(t, h, "sha256:")

	got := s.Get(h)
	require.NotNil(t, got)
	assert.Equal(t, "my-recipe", got.Name)
	assert.Equal(t, []string{"bash"}, got.Tools)
}

func TestPut_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configs.json")
	s := recipestore.New(path)

	cfg := &recipestore.RecipeSnapshot{Name: "dup"}
	h1 := s.Put(cfg)
	h2 := s.Put(cfg)
	assert.Equal(t, h1, h2)

	all := s.List()
	assert.Len(t, all, 1)
}

func TestGet_NotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configs.json")
	s := recipestore.New(path)
	assert.Nil(t, s.Get("sha256:0000"))
}

func TestNew_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "configs.json")
	s := recipestore.New(path)
	assert.Empty(t, s.List())
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configs.json")
	s1 := recipestore.New(path)

	temp := 0.7
	cfg := &recipestore.RecipeSnapshot{
		Name:         "persisted",
		SystemPrompt: "Be brief.",
		Tools:        []string{"read_file", "bash"},
		ModelParams:  &recipestore.ModelParamsConfig{Temperature: &temp},
	}
	h := s1.Put(cfg)

	// Reload from disk.
	s2 := recipestore.New(path)
	got := s2.Get(h)
	require.NotNil(t, got)
	assert.Equal(t, "persisted", got.Name)
	assert.Equal(t, "Be brief.", got.SystemPrompt)
	assert.Equal(t, []string{"read_file", "bash"}, got.Tools)
	require.NotNil(t, got.ModelParams)
	require.NotNil(t, got.ModelParams.Temperature)
	assert.InDelta(t, 0.7, *got.ModelParams.Temperature, 1e-9)
}

func TestHash_DifferentToolGuidance(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{ToolGuidance: "guidance-a"}
	cfg2 := &recipestore.RecipeSnapshot{ToolGuidance: "guidance-b"}
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

func TestHash_DifferentSystemReminders(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{
		SystemReminders: []recipestore.SystemReminderConfig{{Name: "a", Content: "reminder"}},
	}
	cfg2 := &recipestore.RecipeSnapshot{}
	assert.NotEqual(t, recipestore.Hash(cfg1), recipestore.Hash(cfg2))
}

func TestRecipeSnapshot_RoundTrip(t *testing.T) {
	temp := 0.5
	maxTok := 1024
	seed := int64(42)
	sysRole := true
	midConvo := false
	cfg := &recipestore.RecipeSnapshot{
		Version:             1,
		Name:                "full-config",
		SystemPrompt:        "system prompt text",
		ToolGuidance:        "custom tool guidance",
		Tools:               []string{"read_file", "write_file", "bash"},
		BashPrefixes:        []string{"go test", "go vet"},
		ToolDescriptions:    map[string]string{"read_file": "reads a file"},
		SummarizationPrompt: "summarize the conversation",
		Constraints: &recipestore.ConstraintsConfig{
			MaxSteps: 10,
			Timeout:  "5m0s",
		},
		ModelParams: &recipestore.ModelParamsConfig{
			Temperature: &temp,
			MaxTokens:   &maxTok,
			Seed:        &seed,
		},
		Context: &recipestore.ContextConfig{
			MaxHistoryTurns:  20,
			Strategy:         "auto_compact",
			CompactThreshold: 0.8,
			TaskMode:         "sequential",
		},
		SystemReminders: []recipestore.SystemReminderConfig{
			{Name: "ctx-warn", Trigger: "context_usage > 0.75", Content: "Context is filling up."},
		},
		OutputProcessing: map[string]recipestore.OutputRuleConfig{
			"bash": {MaxLines: 50, Truncation: "tail", TruncationMessage: "truncated"},
		},
		ModelProfiles: map[string]recipestore.ModelProfileConfig{
			"claude-sonnet-4-6": {
				ContextBudget:  100000,
				ToolFormat:     "native",
				SystemRole:     &sysRole,
				MidConvoSystem: &midConvo,
			},
		},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var got recipestore.RecipeSnapshot
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, 1, got.Version)
	assert.Equal(t, "full-config", got.Name)
	assert.Equal(t, "system prompt text", got.SystemPrompt)
	assert.Equal(t, "custom tool guidance", got.ToolGuidance)
	assert.Equal(t, []string{"read_file", "write_file", "bash"}, got.Tools)
	assert.Equal(t, []string{"go test", "go vet"}, got.BashPrefixes)
	assert.Equal(t, map[string]string{"read_file": "reads a file"}, got.ToolDescriptions)
	assert.Equal(t, "summarize the conversation", got.SummarizationPrompt)

	require.NotNil(t, got.Constraints)
	assert.Equal(t, 10, got.Constraints.MaxSteps)
	assert.Equal(t, "5m0s", got.Constraints.Timeout)

	require.NotNil(t, got.ModelParams)
	require.NotNil(t, got.ModelParams.Temperature)
	assert.InDelta(t, 0.5, *got.ModelParams.Temperature, 1e-9)
	require.NotNil(t, got.ModelParams.MaxTokens)
	assert.Equal(t, 1024, *got.ModelParams.MaxTokens)
	require.NotNil(t, got.ModelParams.Seed)
	assert.Equal(t, int64(42), *got.ModelParams.Seed)

	require.NotNil(t, got.Context)
	assert.Equal(t, 20, got.Context.MaxHistoryTurns)
	assert.Equal(t, "auto_compact", got.Context.Strategy)
	assert.InDelta(t, 0.8, got.Context.CompactThreshold, 1e-9)
	assert.Equal(t, "sequential", got.Context.TaskMode)

	require.Len(t, got.SystemReminders, 1)
	assert.Equal(t, "ctx-warn", got.SystemReminders[0].Name)
	assert.Equal(t, "context_usage > 0.75", got.SystemReminders[0].Trigger)
	assert.Equal(t, "Context is filling up.", got.SystemReminders[0].Content)

	require.Contains(t, got.OutputProcessing, "bash")
	assert.Equal(t, 50, got.OutputProcessing["bash"].MaxLines)
	assert.Equal(t, "tail", got.OutputProcessing["bash"].Truncation)

	require.Contains(t, got.ModelProfiles, "claude-sonnet-4-6")
	prof := got.ModelProfiles["claude-sonnet-4-6"]
	assert.Equal(t, 100000, prof.ContextBudget)
	assert.Equal(t, "native", prof.ToolFormat)
	require.NotNil(t, prof.SystemRole)
	assert.True(t, *prof.SystemRole)
	require.NotNil(t, prof.MidConvoSystem)
	assert.False(t, *prof.MidConvoSystem)
}

func TestList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configs.json")
	s := recipestore.New(path)

	s.Put(&recipestore.RecipeSnapshot{Name: "a", SystemPrompt: "prompt-a"})
	s.Put(&recipestore.RecipeSnapshot{Name: "b", SystemPrompt: "prompt-b"})

	all := s.List()
	assert.Len(t, all, 2)
}

func TestHashesForName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configs.json")
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
	path := filepath.Join(t.TempDir(), "configs.json")
	require.NoError(t, os.WriteFile(path, []byte("{bad json"), 0o600))

	s := recipestore.New(path)
	assert.Empty(t, s.List())
}

func TestPut_CreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "configs.json")
	s := recipestore.New(path)
	s.Put(&recipestore.RecipeSnapshot{Name: "nested"})

	// Verify file was created.
	_, err := os.Stat(path)
	require.NoError(t, err)
}
