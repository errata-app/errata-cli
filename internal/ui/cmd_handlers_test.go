package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/api"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/pkg/recipe"
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
	assert.NotNil(t, cmd, "/clear should return tea.ClearScreen cmd")
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
	assert.NotNil(t, cmd, "/wipe should return tea.ClearScreen cmd")
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

func TestHandleExportCommand_WithRunContent(t *testing.T) {
	a := newAppForTest(t, nil)

	// Persist a run so there's content to export.
	a.store.PersistRunState("test prompt", []models.ModelResponse{
		{ModelID: "m1", Text: "response text", LatencyMS: 100, CostUSD: 0.01},
	}, nil, nil)

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

// ── /publish command tests ───────────────────────────────────────────────────

// newClientForTest creates an api.Client pointing at the given test server URL.
func newClientForTest(srvURL, token string) *api.Client {
	c := api.NewClientWithToken(token)
	c.SetBaseURL(srvURL)
	return c
}

func TestHandlePublishCommand_NotLoggedIn(t *testing.T) {
	a := newAppForTestWithRecipe(t, nil, &recipe.Recipe{Name: "test"})
	// Set an apiClient with no token (not logged in).
	a.apiClient = newClientForTest("http://unused", "")

	result, _ := a.handlePublishCommand()
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Not logged in")
}

func TestHandlePublishCommand_NoRecipe(t *testing.T) {
	a := newAppForTest(t, nil)
	a.apiClient = newClientForTest("http://unused", "test-token")

	result, _ := a.handlePublishCommand()
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "No recipe to publish")
}

func TestHandlePublishCommand_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(api.RecipeEntry{
			ID:             "r1",
			Name:           "My Recipe",
			Slug:           "my-recipe",
			AuthorUsername: "alice",
		})
	}))
	defer srv.Close()

	a := newAppForTestWithRecipe(t, nil, &recipe.Recipe{Name: "My Recipe"})
	a.apiClient = newClientForTest(srv.URL, "test-token")

	result, cmd := a.handlePublishCommand()
	app := result.(App)
	// Synchronous part shows "Publishing…"
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Publishing")
	// cmd is non-nil (batched: print + async HTTP call)
	assert.NotNil(t, cmd)

	// Execute the async function to verify it produces the correct message.
	entry, err := a.apiClient.CreateRecipe("# My Recipe\nversion: 1\n")
	require.NoError(t, err)
	assert.Equal(t, "alice/my-recipe", entry.Ref())
}

func TestHandlePullCommand_NoArgs(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handlePullCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

func TestHandlePullCommand_Success(t *testing.T) {
	recipeContent := "# Pulled Recipe\nversion: 1\n\n## Models\n- alpha\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/recipes/alice/cool-recipe/raw", r.URL.Path)
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(recipeContent))
	}))
	defer srv.Close()

	a := newAppForTest(t, nil)
	a.apiClient = newClientForTest(srv.URL, "test-token")

	result, cmd := a.handlePullCommand("alice/cool-recipe")
	app := result.(App)
	// Synchronous part shows "Pulling…"
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Pulling alice/cool-recipe")
	assert.NotNil(t, cmd)

	// Simulate the async completion by calling handlePullComplete directly.
	raw, err := a.apiClient.GetRecipeRaw("alice/cool-recipe")
	require.NoError(t, err)
	result2, _ := app.handlePullComplete(raw, "alice/cool-recipe")
	app2 := result2.(App)
	last2 := app2.feed[len(app2.feed)-1].text
	assert.Contains(t, last2, "Pulled")
	assert.Contains(t, last2, "Pulled Recipe")
	require.NotNil(t, app2.store.SessionRecipe())
	assert.Equal(t, []string{"alpha"}, app2.store.SessionRecipe().Models)
}

// ── /sync command tests ──────────────────────────────────────────────────────

func TestHandleSyncCommand_NotLoggedIn(t *testing.T) {
	a := newAppForTest(t, nil)
	a.apiClient = newClientForTest("http://unused", "")

	result, _ := a.handleSyncCommand()
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Not logged in")
}

func TestHandleSyncCommand_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v1/preferences", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"accepted": 3})
	}))
	defer srv.Close()

	a := newAppForTest(t, nil)
	a.apiClient = newClientForTest(srv.URL, "test-token")

	result, cmd := a.handleSyncCommand()
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Syncing")
	assert.NotNil(t, cmd)
}

// ── captureRunParams tests ───────────────────────────────────────────────────

func TestCaptureRunParams_ReadsFromActiveRecipe(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)

	sessionRec := &recipe.Recipe{
		Tools: &recipe.ToolsConfig{
			Allowlist:    []string{"read_file", "bash"},
			BashPrefixes: []string{"go test"},
			Guidance:     map[string]string{"bash": "run tests only"},
		},
		Context: recipe.ContextConfig{
			Strategy:            "manual",
			SummarizationPrompt: "summarize",
			MaxHistoryTurns:     30,
			CompactThreshold:    0.75,
		},
		Sandbox: recipe.SandboxConfig{
			Filesystem:      "project_only",
			Network:         "none",
			AllowLocalFetch: true,
		},
		Constraints: recipe.ConstraintsConfig{
			Timeout:     10 * time.Minute,
			BashTimeout: 30 * time.Second,
			MaxSteps:    50,
			ProjectRoot: "/opt/project",
		},
		SystemPrompt: "Be helpful",
	}
	a.store.SetSessionRecipe(sessionRec)

	params := a.captureRunParams()

	assert.Equal(t, []string{"go test"}, params.bashPrefixes)
	assert.Equal(t, "manual", params.contextStrategy)
	assert.Equal(t, "summarize", params.sumPrompt)
	assert.Equal(t, "Be helpful", params.systemPrompt)
	assert.Equal(t, 30*time.Second, params.bashTimeout)
	assert.Equal(t, 10*time.Minute, params.agentTimeout)
	assert.True(t, params.allowLocalFetch)
	assert.InDelta(t, 0.75, params.compactThreshold, 0.001)
	assert.Equal(t, 30, params.maxHistoryTurns)
	assert.Equal(t, 50, params.maxSteps)
	assert.Equal(t, "project_only", params.sandboxFilesystem)
	assert.Equal(t, "none", params.sandboxNetwork)
	assert.Equal(t, "/opt/project", params.projectRoot)
	assert.NotEmpty(t, params.activeDefs)
	assert.Equal(t, map[string]string{"bash": "run tests only"}, params.toolGuidanceMap)
}

func TestCaptureRunParams_NilRecipeUsesDefaults(t *testing.T) {
	a := newAppForTest(t, nil)

	params := a.captureRunParams()

	assert.Empty(t, params.bashPrefixes)
	assert.Empty(t, params.contextStrategy)
	assert.Empty(t, params.sumPrompt)
	assert.Empty(t, params.systemPrompt)
	assert.Zero(t, params.bashTimeout)
	assert.Zero(t, params.agentTimeout)
	assert.False(t, params.allowLocalFetch)
	assert.Zero(t, params.compactThreshold)
	assert.Zero(t, params.maxHistoryTurns)
	assert.Zero(t, params.maxSteps)
	assert.Empty(t, params.sandboxFilesystem)
	assert.Empty(t, params.sandboxNetwork)
	assert.Empty(t, params.projectRoot)
	// activeDefs should have the full set of built-in tools.
	assert.NotEmpty(t, params.activeDefs)
}
