package recipestore_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/pkg/recipestore"
)

const recipeA = "# Recipe A\nversion: 1\n\n## Models\n- model-a\n"
const recipeB = "# Recipe B\nversion: 1\n\n## Models\n- model-b\n"
const recipeAVariant = "# Recipe A\nversion: 1\n\n## Models\n- model-a\n- model-c\n"

func TestPutAndGet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	h := s.Put(recipeA)
	assert.Contains(t, h, "rcp_")

	got := s.Get(h)
	assert.Equal(t, recipeA, got)
}

func TestPut_Deduplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	h1 := s.Put(recipeA)
	h2 := s.Put(recipeA)
	assert.Equal(t, h1, h2)

	all := s.List()
	assert.Len(t, all, 1)
}

func TestGet_NotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)
	assert.Empty(t, s.Get("rcp_0000"))
}

func TestNew_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "recipes.json")
	s := recipestore.New(path)
	assert.Empty(t, s.List())
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s1 := recipestore.New(path)
	h := s1.Put(recipeA)

	// Reload from disk.
	s2 := recipestore.New(path)
	got := s2.Get(h)
	assert.Equal(t, recipeA, got)
}

func TestList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	s.Put(recipeA)
	s.Put(recipeB)

	all := s.List()
	assert.Len(t, all, 2)
}

func TestHashesForName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	s.Put(recipeA)
	// recipeAVariant differs only in models → same config hash, collides with recipeA.
	s.Put(recipeAVariant)
	s.Put(recipeB)

	hashes := s.HashesForName("Recipe A")
	assert.Len(t, hashes, 1, "model-only variants collide under the same config hash")

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
	s.Put(recipeA)

	// Verify file was created.
	_, err := os.Stat(path)
	require.NoError(t, err)
}

func TestPersistence_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s1 := recipestore.New(path)

	h1 := s1.Put(recipeA)
	h2 := s1.Put(recipeB)

	// Verify the on-disk JSON is valid.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var raw map[string]string
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Len(t, raw, 2)
	assert.Equal(t, recipeA, raw[h1])
	assert.Equal(t, recipeB, raw[h2])

	// Reload and verify.
	s2 := recipestore.New(path)
	assert.Equal(t, recipeA, s2.Get(h1))
	assert.Equal(t, recipeB, s2.Get(h2))
}

func TestHashesForName_DefaultName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	// Recipe with no name header defaults to "default".
	noName := "version: 1\n\n## Models\n- m1\n"
	s.Put(noName)

	hashes := s.HashesForName("default")
	assert.Len(t, hashes, 1)
}

func TestPut_CollisionDifferentModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recipes.json")
	s := recipestore.New(path)

	// Two recipes with the same config but different models should produce
	// the same hash. The first-stored markdown wins.
	h1 := s.Put(recipeA)
	h2 := s.Put(recipeAVariant) // same name, different models
	assert.Equal(t, h1, h2, "same config with different models should collide")

	got := s.Get(h1)
	assert.Equal(t, recipeA, got, "first-stored markdown should be kept on collision")

	all := s.List()
	assert.Len(t, all, 1)
}
