package config_test

import (
	"os"
	"testing"

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

// ─── ApplyRecipe tests ───────────────────────────────────────────────────────

func TestApplyRecipe_Models(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Models\n- claude-sonnet-4-6\n- openai/gpt-4o\n")))
	cfg := config.Config{}
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, []string{"claude-sonnet-4-6", "openai/gpt-4o"}, cfg.ActiveModels)
}

func TestApplyRecipe_NilModels_DoesNotOverwrite(t *testing.T) {
	r, _ := recipe.Parse(writeRecipe(t, v1("## Constraints\ntimeout: 5m\n")))
	cfg := config.Config{ActiveModels: []string{"existing-model"}}
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, []string{"existing-model"}, cfg.ActiveModels, "absent ## Models must not clear existing config")
}

func TestApplyRecipe_OnlyModels(t *testing.T) {
	// ApplyRecipe now only sets ActiveModels — all other recipe settings
	// are read directly from the recipe at run time.
	r := &recipe.Recipe{Models: []string{"m1", "m2"}}
	cfg := config.Config{}
	config.ApplyRecipe(r, &cfg)
	assert.Equal(t, []string{"m1", "m2"}, cfg.ActiveModels)
}
