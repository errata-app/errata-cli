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
	a.store.SetHistories(map[string][]models.ConversationTurn{
		"m1": {{Role: "user", Content: "hello"}},
	})
	a.feed = []feedItem{{kind: "msg", text: "old message"}}

	result, cmd := a.handleClearCmd()
	assert.Nil(t, cmd)
	app := result.(App)

	// Display feed should be cleared.
	assert.Nil(t, app.feed)
	// Conversation histories should be preserved.
	assert.NotNil(t, app.store.Histories())
	assert.Len(t, app.store.Histories()["m1"], 1)
}

func TestHandleWipeCmd_ClearsConversationHistories(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	a.store.SetHistories(map[string][]models.ConversationTurn{
		"m1": {{Role: "user", Content: "hello"}},
	})
	a.feed = []feedItem{{kind: "msg", text: "old message"}}

	result, cmd := a.handleWipeCmd()
	assert.Nil(t, cmd)
	app := result.(App)

	// Both display feed and conversation histories should be cleared.
	assert.Nil(t, app.feed)
	assert.Nil(t, app.store.Histories())
}

// ── /save command tests ──────────────────────────────────────────────────────

func TestHandleSaveCommand_DefaultPath(t *testing.T) {
	a := newAppForTestWithRecipe(t, nil, &recipe.Recipe{Name: "test-recipe", Models: []string{"m1"}})
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	result, _ := a.handleSaveCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Recipe saved to recipe.md")

	data, err := os.ReadFile(filepath.Join(dir, "recipe.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "test-recipe")
	assert.Contains(t, string(data), "m1")
}

func TestHandleSaveCommand_DefaultPathNoOverwrite(t *testing.T) {
	a := newAppForTestWithRecipe(t, nil, &recipe.Recipe{Name: "second-save", Models: []string{"m2"}})
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	// Create an existing recipe.md so the default path should increment.
	require.NoError(t, os.WriteFile("recipe.md", []byte("existing"), 0o600))

	result, _ := a.handleSaveCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Recipe saved to recipe_1.md")

	// Original file should be untouched.
	orig1, err := os.ReadFile(filepath.Join(dir, "recipe.md"))
	require.NoError(t, err)
	assert.Equal(t, "existing", string(orig1))

	// New file should have the recipe content.
	data, err := os.ReadFile(filepath.Join(dir, "recipe_1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "second-save")

	// A third save should go to recipe_2.md.
	result2, _ := app.handleSaveCommand("")
	app2 := result2.(App)
	last2 := app2.feed[len(app2.feed)-1].text
	assert.Contains(t, last2, "Recipe saved to recipe_2.md")
}

func TestHandleSaveCommand_CustomPath(t *testing.T) {
	a := newAppForTestWithRecipe(t, nil, &recipe.Recipe{Name: "my-recipe"})
	path := filepath.Join(t.TempDir(), "custom.md")

	result, _ := a.handleSaveCommand(path)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "my-recipe")
}

func TestHandleSaveCommand_SessionRecipeTakesPrecedence(t *testing.T) {
	a := newAppForTestWithRecipe(t, nil, &recipe.Recipe{Name: "base"})
	a.store.SetSessionRecipe(&recipe.Recipe{Name: "session-modified"})
	path := filepath.Join(t.TempDir(), "out.md")

	result, _ := a.handleSaveCommand(path)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "saved")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "session-modified")
}

func TestHandleSaveCommand_NoRecipe(t *testing.T) {
	a := newAppForTest(t, nil)
	// No recipe set (nil base and session).
	result, _ := a.handleSaveCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "No recipe to save")
}

// ── /load command tests ──────────────────────────────────────────────────────

func TestHandleLoadCommand_MissingPathShowsUsage(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleLoadCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

func TestHandleLoadCommand_LoadsRecipe(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)

	recipeContent := "# Loaded Recipe\nversion: 1\n\n## Models\n- alpha\n- beta\n\n## System Prompt\nYou are helpful.\n"
	path := filepath.Join(t.TempDir(), "recipe.md")
	require.NoError(t, os.WriteFile(path, []byte(recipeContent), 0o644))

	result, _ := a.handleLoadCommand(path)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Loaded recipe")
	assert.Contains(t, last, "Loaded Recipe")
	assert.True(t, app.store.RecipeModified())
	require.NotNil(t, app.store.SessionRecipe())
	assert.Equal(t, []string{"alpha", "beta"}, app.store.SessionRecipe().Models)
	assert.Equal(t, "You are helpful.", app.store.SessionRecipe().SystemPrompt)
}

func TestHandleLoadCommand_BadPathShowsError(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleLoadCommand("/nonexistent/path/recipe.md")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Load failed")
}

// ── /export command tests ────────────────────────────────────────────────────

func TestHandleExportCommand_NoReport(t *testing.T) {
	a := newAppForTest(t, nil)
	// No report path set — should show error.
	result, _ := a.handleExportCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "No run output to export")
}

func TestHandleExportCommand_WithReport(t *testing.T) {
	a := newAppForTest(t, nil)

	// Create a report on disk and tell the store about it.
	report := &output.Report{
		ID:     "abc123",
		Prompt: "test prompt",
		Recipe: output.RecipeSnapshot{Name: "test"},
	}
	tmpDir := t.TempDir()
	reportPath, err := output.Save(tmpDir, report)
	require.NoError(t, err)
	a.store.SetLastReportInfo(reportPath, nil)

	exportDir := filepath.Join(t.TempDir(), "exports")
	result, _ := a.handleExportCommand(exportDir)
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Output exported to")
	assert.Contains(t, last, exportDir)
}

// ── nextAvailablePath tests ──────────────────────────────────────────────────

func TestNextAvailablePath_NoConflict(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	assert.Equal(t, "recipe.md", nextAvailablePath("recipe.md"))
}

func TestNextAvailablePath_Increments(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	require.NoError(t, os.WriteFile("recipe.md", []byte("x"), 0o600))
	assert.Equal(t, "recipe_1.md", nextAvailablePath("recipe.md"))

	require.NoError(t, os.WriteFile("recipe_1.md", []byte("x"), 0o600))
	assert.Equal(t, "recipe_2.md", nextAvailablePath("recipe.md"))
}

func TestNextAvailablePath_NoExtension(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	require.NoError(t, os.WriteFile("Makefile", []byte("x"), 0o600))
	assert.Equal(t, "Makefile_1", nextAvailablePath("Makefile"))
}
