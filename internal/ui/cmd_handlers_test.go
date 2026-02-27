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

// ── /clear and /wipe command tests ───────────────────────────────────────────

func TestHandleClearCmd_PreservesConversationHistories(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	a.conversationHistories = map[string][]models.ConversationTurn{
		"m1": {{Role: "user", Content: "hello"}},
	}
	a.feed = []feedItem{{kind: "msg", text: "old message"}}

	result, cmd := a.handleClearCmd()
	assert.Nil(t, cmd)
	app := result.(App)

	// Display feed should be cleared.
	assert.Nil(t, app.feed)
	// Conversation histories should be preserved.
	assert.NotNil(t, app.conversationHistories)
	assert.Len(t, app.conversationHistories["m1"], 1)
}

func TestHandleWipeCmd_ClearsConversationHistories(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	a.conversationHistories = map[string][]models.ConversationTurn{
		"m1": {{Role: "user", Content: "hello"}},
	}
	a.feed = []feedItem{{kind: "msg", text: "old message"}}

	result, cmd := a.handleWipeCmd()
	assert.Nil(t, cmd)
	app := result.(App)

	// Both display feed and conversation histories should be cleared.
	assert.Nil(t, app.feed)
	assert.Nil(t, app.conversationHistories)
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
