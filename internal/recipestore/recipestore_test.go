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

func TestHash_DifferentConfigs(t *testing.T) {
	cfg1 := &recipestore.RecipeSnapshot{Name: "recipe-a"}
	cfg2 := &recipestore.RecipeSnapshot{Name: "recipe-b"}
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

func TestRecipeSnapshot_RoundTrip(t *testing.T) {
	temp := 0.5
	maxTok := 1024
	seed := int64(42)
	cfg := &recipestore.RecipeSnapshot{
		Name:         "full-config",
		SystemPrompt: "system prompt text",
		Tools:        []string{"read_file", "write_file", "bash"},
		Constraints: &recipestore.ConstraintsConfig{
			MaxSteps: 10,
			Timeout:  "5m0s",
		},
		ModelParams: &recipestore.ModelParamsConfig{
			Temperature: &temp,
			MaxTokens:   &maxTok,
			Seed:        &seed,
		},
	}

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var got recipestore.RecipeSnapshot
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, cfg.Name, got.Name)
	assert.Equal(t, cfg.SystemPrompt, got.SystemPrompt)
	assert.Equal(t, cfg.Tools, got.Tools)
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
}

func TestList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configs.json")
	s := recipestore.New(path)

	s.Put(&recipestore.RecipeSnapshot{Name: "a"})
	s.Put(&recipestore.RecipeSnapshot{Name: "b"})

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
