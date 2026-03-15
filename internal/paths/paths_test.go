package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/paths"
)

func TestDefault_RootIsData(t *testing.T) {
	l := paths.Default()
	assert.Equal(t, "data", l.Root)
	assert.Equal(t, "data/preferences.jsonl", l.Preferences)
	assert.Equal(t, "data/pricing_cache.json", l.PricingCache)
	assert.Equal(t, "data/prompt_history.jsonl", l.PromptHistory)
	assert.Equal(t, "data/configs.json", l.ConfigStore)
	assert.Equal(t, "data/outputs", l.Outputs)
	assert.Equal(t, "data/sessions", l.Sessions)
	assert.Equal(t, "data/checkpoint.json", l.Checkpoint)
}

func TestNew_CustomRoot(t *testing.T) {
	l := paths.New("/tmp/errata-test")
	assert.Equal(t, "/tmp/errata-test", l.Root)
	assert.Equal(t, "/tmp/errata-test/preferences.jsonl", l.Preferences)
	assert.Equal(t, "/tmp/errata-test/pricing_cache.json", l.PricingCache)
	assert.Equal(t, "/tmp/errata-test/prompt_history.jsonl", l.PromptHistory)
	assert.Equal(t, "/tmp/errata-test/configs.json", l.ConfigStore)
	assert.Equal(t, "/tmp/errata-test/outputs", l.Outputs)
	assert.Equal(t, "/tmp/errata-test/sessions", l.Sessions)
	assert.Equal(t, "/tmp/errata-test/checkpoint.json", l.Checkpoint)
}

func TestDefault_EqualsNewData(t *testing.T) {
	assert.Equal(t, paths.New("data"), paths.Default())
}

func TestRecipesDir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".errata", "recipes"), paths.RecipesDir())
}

func TestNextAvailable_NoConflict(t *testing.T) {
	dir := t.TempDir()
	got := paths.NextAvailable(dir, "slug.md")
	assert.Equal(t, filepath.Join(dir, "slug.md"), got)
}

func TestNextAvailable_Increments(t *testing.T) {
	dir := t.TempDir()

	// Create slug.md so it needs to increment.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slug.md"), []byte("x"), 0o600))
	got := paths.NextAvailable(dir, "slug.md")
	assert.Equal(t, filepath.Join(dir, "slug1.md"), got)

	// Create slug1.md too.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slug1.md"), []byte("x"), 0o600))
	got2 := paths.NextAvailable(dir, "slug.md")
	assert.Equal(t, filepath.Join(dir, "slug2.md"), got2)
}

func TestNextAvailable_NoExtension(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme"), []byte("x"), 0o600))
	got := paths.NextAvailable(dir, "readme")
	assert.Equal(t, filepath.Join(dir, "readme1"), got)
}
