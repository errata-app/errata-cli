package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/recipe"
)

func TestFmtPrice(t *testing.T) {
	tests := []struct {
		name   string
		input  float64
		expect string
	}{
		{"whole dollars", 15.0, "$15.00"},
		{"dollars with cents", 2.50, "$2.50"},
		{"one dollar", 1.0, "$1.00"},
		{"sub-dollar round", 0.80, "$0.80"},
		{"sub-dollar exact", 0.15, "$0.15"},
		{"sub-dollar 60c", 0.60, "$0.60"},
		{"sub-dollar 50c", 0.50, "$0.50"},
		{"sub-cent", 0.075, "$0.075"},
		{"sub-cent small", 0.0075, "$0.0075"},
		{"one cent", 0.01, "$0.01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, fmtPrice(tt.input))
		})
	}
}

// ── /export command tests ────────────────────────────────────────────────────

func TestHandleExportCommand_BareShowsUsage(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleExportCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

func TestHandleExportCommand_InvalidSubShowsUsage(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleExportCommand("bogus")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

func TestHandleExportRecipe_DefaultPath(t *testing.T) {
	a := newAppForTest(t, nil)
	a.recipe = &recipe.Recipe{Name: "test-recipe", Models: []string{"m1"}}
	dir := t.TempDir()
	// Change to temp dir so default path writes there.
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	result, _ := a.handleExportCommand("recipe")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Recipe exported to recipe_export.md")

	data, err := os.ReadFile(filepath.Join(dir, "recipe_export.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "test-recipe")
	assert.Contains(t, string(data), "m1")
}

func TestHandleExportRecipe_CustomPath(t *testing.T) {
	a := newAppForTest(t, nil)
	a.recipe = &recipe.Recipe{Name: "my-recipe"}
	path := filepath.Join(t.TempDir(), "custom.md")

	result, _ := a.handleExportCommand("recipe " + path)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "my-recipe")
}

func TestHandleExportRecipe_SessionRecipeTakesPrecedence(t *testing.T) {
	a := newAppForTest(t, nil)
	a.recipe = &recipe.Recipe{Name: "base"}
	a.sessionRecipe = &recipe.Recipe{Name: "session-modified"}
	path := filepath.Join(t.TempDir(), "out.md")

	result, _ := a.handleExportCommand("recipe " + path)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "exported")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "session-modified")
}

func TestHandleExportRecipe_NoRecipe(t *testing.T) {
	a := newAppForTest(t, nil)
	a.recipe = nil
	a.sessionRecipe = nil
	result, _ := a.handleExportCommand("recipe")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "No recipe to export")
}

func TestHandleExportOutput_NoReport(t *testing.T) {
	a := newAppForTest(t, nil)
	a.lastReport = nil
	result, _ := a.handleExportCommand("output")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "No run output to export")
}

func TestHandleExportOutput_WithReport(t *testing.T) {
	a := newAppForTest(t, nil)
	a.lastReport = &output.Report{
		ID:     "abc123",
		Prompt: "test prompt",
		Recipe: output.RecipeSnapshot{Name: "test"},
	}
	dir := filepath.Join(t.TempDir(), "outputs")

	result, _ := a.handleExportCommand("output " + dir)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Output exported to")
	assert.Contains(t, last, dir)
}

// ── /import command tests ────────────────────────────────────────────────────

func TestHandleImportCommand_BareShowsUsage(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleImportCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

func TestHandleImportCommand_InvalidSubShowsUsage(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleImportCommand("bogus")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

func TestHandleImportCommand_RecipeMissingPathShowsUsage(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleImportCommand("recipe")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

func TestHandleImportRecipe_LoadsRecipe(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)

	// Write a valid recipe file.
	recipeContent := "# Imported Recipe\n\n## Models\n- alpha\n- beta\n\n## System Prompt\nYou are helpful.\n"
	path := filepath.Join(t.TempDir(), "recipe.md")
	require.NoError(t, os.WriteFile(path, []byte(recipeContent), 0o644))

	result, _ := a.handleImportCommand("recipe " + path)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Imported recipe")
	assert.Contains(t, last, "Imported Recipe")
	assert.True(t, app.recipeModified)
	require.NotNil(t, app.sessionRecipe)
	assert.Equal(t, []string{"alpha", "beta"}, app.sessionRecipe.Models)
	assert.Equal(t, "You are helpful.", app.sessionRecipe.SystemPrompt)
}

func TestHandleImportRecipe_BadPathShowsError(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleImportCommand("recipe /nonexistent/path/recipe.md")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Import failed")
}
