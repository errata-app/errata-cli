package headless_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/adapters"
	"github.com/errata-app/errata-cli/internal/headless"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/internal/tools"
)

// ─── Mock adapter ─────────────────────────────────────────────────────────────

type mockAdapter struct {
	id       string
	response models.ModelResponse
	err      error
	calls    int // tracks how many times RunAgent was called
}

func (m *mockAdapter) ID() string { return m.id }

func (m *mockAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}

func (m *mockAdapter) RunAgent(
	ctx context.Context,
	history []models.ConversationTurn,
	prompt string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	m.calls++
	if m.err != nil {
		return models.ModelResponse{}, m.err
	}
	resp := m.response
	resp.ModelID = m.id
	return resp, nil
}

var _ models.ModelAdapter = (*mockAdapter)(nil)

// dispatchingMockAdapter is a mock that writes files through the real
// adapters.DispatchTool path, exercising the full chain:
//   runner context (WorkDir+DirectWrites) → DispatchTool → WriteFileDirect
//
// This avoids coupling tests to internal implementation details.
type dispatchingMockAdapter struct {
	id       string
	response models.ModelResponse
	// toolCalls is a sequence of (toolName, args) to dispatch via DispatchTool.
	toolCalls []mockToolCall
}

type mockToolCall struct {
	name string
	args map[string]string
}

func (m *dispatchingMockAdapter) ID() string { return m.id }

func (m *dispatchingMockAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}

func (m *dispatchingMockAdapter) RunAgent(
	ctx context.Context,
	_ []models.ConversationTurn,
	_ string,
	_ func(models.AgentEvent),
) (models.ModelResponse, error) {
	var proposed []tools.FileWrite
	for _, tc := range m.toolCalls {
		result, ok := adapters.DispatchTool(ctx, tc.name, tc.args,
			func(models.AgentEvent) {}, &proposed, nil)
		if !ok {
			return models.ModelResponse{Error: "unknown tool: " + tc.name}, nil
		}
		if len(result) > 0 && result[0] == '[' {
			return models.ModelResponse{Error: result}, nil
		}
	}
	resp := m.response
	resp.ModelID = m.id
	resp.ProposedWrites = proposed // carry any queued writes (TUI mode)
	return resp, nil
}

var _ models.ModelAdapter = (*dispatchingMockAdapter)(nil)

// ─── Helpers ──────────────────────────────────────────────────────────────────

func testRecipe(tasks []string, criteriaItems []string) *recipe.Recipe {
	return &recipe.Recipe{
		Version:         1,
		Name:            "Test Recipe",
		Models:          []string{"model-a", "model-b"},
		Tasks:           tasks,
		SuccessCriteria: criteriaItems,
		Context:         recipe.ContextConfig{MaxHistoryTurns: 20},
	}
}

func testOpts(rec *recipe.Recipe, adapters []models.ModelAdapter, outputDir string) *headless.Options {
	return &headless.Options{
		Recipe:         rec,
		Adapters:       adapters,
		SessionID:      "test-session",
		OutputDir:      outputDir,
		CheckpointPath: filepath.Join(outputDir, "checkpoint.json"),
		Stderr:         &bytes.Buffer{},
	}
}

// ─── Run tests ────────────────────────────────────────────────────────────────

// ─── Metadata Report tests ────────────────────────────────────────────────────

func TestMetadataReport_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	meta := &headless.MetadataReport{
		ID:        "rpt_test123",
		SessionID: "sess1",
		TaskMode:  "independent",
		Recipe: headless.MetaRecipeSnapshot{
			Name:            "test",
			Version:         1,
			Models:          []string{"model-a"},
			Tasks:           []string{"task1"},
			SuccessCriteria: []string{"no_errors"},
		},
		Tasks: []headless.MetaTaskResult{
			{
				Index:      0,
				PromptHash: "abc123",
				Models: []headless.MetaModelResult{
					{
						ModelID:           "model-a",
						LatencyMS:         500,
						InputTokens:       1000,
						OutputTokens:      200,
						CostUSD:           0.01,
						Steps:             3,
						ToolCalls:         map[string]int{"read_file": 2},
						FilesChangedCount: 1,
					},
				},
			},
		},
		Summary: headless.Summary{TotalTasks: 1, CompletedTasks: 1},
	}

	path, err := headless.SaveMetadata(dir, meta)
	require.NoError(t, err)

	loaded, err := headless.LoadMetadata(path)
	require.NoError(t, err)

	assert.Equal(t, meta.ID, loaded.ID)
	assert.Equal(t, meta.SessionID, loaded.SessionID)
	assert.Equal(t, meta.TaskMode, loaded.TaskMode)
	assert.Equal(t, meta.Recipe.Name, loaded.Recipe.Name)
	require.Len(t, loaded.Tasks, 1)
	assert.Equal(t, "abc123", loaded.Tasks[0].PromptHash)
	require.Len(t, loaded.Tasks[0].Models, 1)
	assert.Equal(t, int64(500), loaded.Tasks[0].Models[0].LatencyMS)
	assert.Equal(t, 2, loaded.Tasks[0].Models[0].ToolCalls["read_file"])
	assert.Equal(t, 1, loaded.Tasks[0].Models[0].FilesChangedCount)
}

func TestMetadataReport_NoSensitiveContent(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"Write a secret program"}, []string{"no_errors"})
	rec.SystemPrompt = "You are a super secret system prompt"

	a1 := &mockAdapter{
		id: "model-a",
		response: models.ModelResponse{
			Text:      "Here is the secret response with file contents",
			LatencyMS: 100,
			Error:     "detailed error with sensitive info",
		},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	meta := headless.BuildMetadataReport(report)
	data, err := json.Marshal(meta)
	require.NoError(t, err)
	jsonStr := string(data)

	// Must NOT contain sensitive content (system prompt, model response text).
	// Note: recipe task strings appear in MetaRecipeSnapshot.Tasks as structural metadata.
	// Note: error strings appear in MetaModelResult.Error as operational metadata.
	assert.NotContains(t, jsonStr, "super secret system prompt")
	assert.NotContains(t, jsonStr, "Here is the secret response")

	// Must contain structural metadata.
	assert.Contains(t, jsonStr, "prompt_hash")
	assert.Contains(t, jsonStr, "model-a")
	assert.Contains(t, jsonStr, "no_errors")
}

func TestRun_BundledOutputDir(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"task"}, nil)
	a1 := &mockAdapter{
		id:       "model-a",
		response: models.ModelResponse{Text: "done", LatencyMS: 100},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	// Find the run directory.
	runDirName := headless.RunDirName(report.Recipe.Name, report.ID)
	runDir := filepath.Join(outDir, runDirName)

	// Verify bundled structure.
	assert.FileExists(t, filepath.Join(runDir, "report.json"))
	assert.FileExists(t, filepath.Join(runDir, "meta.json"))
	assert.DirExists(t, filepath.Join(runDir, "worktrees"))

	// Verify report.json loads correctly.
	loaded, err := headless.Load(filepath.Join(runDir, "report.json"))
	require.NoError(t, err)
	assert.Equal(t, report.ID, loaded.ID)

	// Verify meta.json loads correctly.
	meta, err := headless.LoadMetadata(filepath.Join(runDir, "meta.json"))
	require.NoError(t, err)
	assert.Equal(t, report.ID, meta.ID)
}

func TestRun_IndependentMode(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"task one", "task two"}, nil)

	a1 := &mockAdapter{id: "model-a", response: models.ModelResponse{Text: "done a", LatencyMS: 100}}
	a2 := &mockAdapter{id: "model-b", response: models.ModelResponse{Text: "done b", LatencyMS: 200}}

	opts := testOpts(rec, []models.ModelAdapter{a1, a2}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	assert.Equal(t, "independent", report.TaskMode)
	require.Len(t, report.Tasks, 2)
	assert.Equal(t, "task one", report.Tasks[0].Prompt)
	assert.Equal(t, "task two", report.Tasks[1].Prompt)
	assert.Equal(t, 2, report.Summary.TotalTasks)
	assert.Equal(t, 2, report.Summary.CompletedTasks)

	// Each adapter should be called once per task = 2 times total.
	assert.Equal(t, 2, a1.calls)
	assert.Equal(t, 2, a2.calls)

	// Worktrees should be preserved under the output directory.
	assert.DirExists(t, report.Setup.WorktreeBase)
	for _, dir := range report.Setup.ModelDirs {
		assert.DirExists(t, dir)
	}
}

func TestRun_SequentialMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"write something", "check it"}, nil)
	rec.Context.TaskMode = "sequential"

	a1 := &mockAdapter{
		id: "model-a",
		response: models.ModelResponse{
			Text:      "wrote file",
			LatencyMS: 100,
		},
	}
	a2 := &mockAdapter{
		id: "model-b",
		response: models.ModelResponse{
			Text:      "also wrote",
			LatencyMS: 300,
		},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1, a2}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	assert.Equal(t, "sequential", report.TaskMode)
	require.Len(t, report.Tasks, 2)

	// Both adapters called twice (sequential carries history).
	assert.Equal(t, 2, a1.calls)
	assert.Equal(t, 2, a2.calls)
}

func TestRun_WithCriteria(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"do it"}, []string{"no_errors", "has_writes"})

	// model-a writes a file through DispatchTool → diffWorktree picks it up → has_writes passes.
	a1 := &dispatchingMockAdapter{
		id:       "model-a",
		response: models.ModelResponse{Text: "done", LatencyMS: 100},
		toolCalls: []mockToolCall{
			{name: tools.WriteToolName, args: map[string]string{"path": "f.go", "content": "package main"}},
		},
	}
	// model-b produces no writes → has_writes fails.
	a2 := &mockAdapter{
		id:       "model-b",
		response: models.ModelResponse{Text: "done but no writes", LatencyMS: 200},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1, a2}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	require.Len(t, report.Tasks, 1)
	cr := report.Tasks[0].CriteriaResults

	// model-a: both criteria pass.
	require.Len(t, cr["model-a"], 2)
	assert.True(t, cr["model-a"][0].Passed)
	assert.True(t, cr["model-a"][1].Passed)

	// model-b: no_errors passes, has_writes fails.
	require.Len(t, cr["model-b"], 2)
	assert.True(t, cr["model-b"][0].Passed)
	assert.False(t, cr["model-b"][1].Passed)

	// Summary reflects criteria.
	assert.Equal(t, 2, report.Summary.PerModel["model-a"].CriteriaPassed)
	assert.Equal(t, 1, report.Summary.PerModel["model-b"].CriteriaPassed)
}

func TestRun_EmptyTasks(t *testing.T) {
	rec := testRecipe(nil, nil)
	opts := testOpts(rec, nil, t.TempDir())
	_, err := headless.Run(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tasks")
}

func TestRun_AllModelsError(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"do something"}, nil)
	a1 := &mockAdapter{
		id:       "model-a",
		response: models.ModelResponse{Error: "api error", LatencyMS: 50},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err, "all-error runs should still produce a report")
	require.Len(t, report.Tasks, 1)
	assert.Equal(t, 0, report.Summary.PerModel["model-a"].TasksSucceeded)
}


// ─── Run criterion integration test ──────────────────────────────────────────

func TestRun_WithRunCriterion(t *testing.T) {
	dir := setupGitDir(t)
	t.Chdir(dir)
	outDir := filepath.Join(t.TempDir(), "out")

	// The run criterion checks for a file that only model-a will create via DispatchTool.
	rec := testRecipe([]string{"create marker"}, []string{
		"no_errors",
		"run: test -f marker.txt",
	})

	// model-a writes marker.txt through DispatchTool → ends up in its worktree.
	a1 := &dispatchingMockAdapter{
		id:       "model-a",
		response: models.ModelResponse{Text: "done", LatencyMS: 100},
		toolCalls: []mockToolCall{
			{name: tools.WriteToolName, args: map[string]string{"path": "marker.txt", "content": "present"}},
		},
	}
	// model-b does nothing → marker.txt absent in its worktree.
	a2 := &mockAdapter{
		id:       "model-b",
		response: models.ModelResponse{Text: "done but no writes", LatencyMS: 200},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1, a2}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	require.Len(t, report.Tasks, 1)
	cr := report.Tasks[0].CriteriaResults

	// model-a: both no_errors and run: test -f marker.txt should pass.
	require.Len(t, cr["model-a"], 2)
	assert.True(t, cr["model-a"][0].Passed, "model-a: no_errors should pass")
	assert.True(t, cr["model-a"][1].Passed, "model-a: run criterion should pass (marker.txt written)")

	// model-b: no_errors passes, but run: test -f marker.txt fails.
	require.Len(t, cr["model-b"], 2)
	assert.True(t, cr["model-b"][0].Passed, "model-b: no_errors should pass")
	assert.False(t, cr["model-b"][1].Passed, "model-b: run criterion should fail (no marker.txt)")
}

// ─── Report round-trip test ──────────────────────────────────────────────────

func TestRunReport_RoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"task"}, []string{"no_errors"})
	a1 := &mockAdapter{
		id:       "model-a",
		response: models.ModelResponse{Text: "done", LatencyMS: 100},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	// Load from the bundled run directory.
	runDirName := headless.RunDirName(report.Recipe.Name, report.ID)
	reportPath := filepath.Join(outDir, runDirName, "report.json")
	require.FileExists(t, reportPath)

	loaded, err := headless.Load(reportPath)
	require.NoError(t, err)

	assert.Equal(t, report.ID, loaded.ID)
	assert.Equal(t, report.TaskMode, loaded.TaskMode)
	assert.Equal(t, report.Recipe.Name, loaded.Recipe.Name)
	require.Len(t, loaded.Tasks, 1)
	assert.Equal(t, "task", loaded.Tasks[0].Prompt)
	assert.Equal(t, 1, loaded.Summary.TotalTasks)
}

// ─── SetupInfo round-trip test ────────────────────────────────────────────────

func TestRunReport_SetupInfo_RoundTrip(t *testing.T) {
	report := &headless.RunReport{
		ID:       "rpt_test",
		TaskMode: "independent",
		Recipe:   headless.RecipeSnapshot{Name: "test", Tasks: []string{"t"}},
		Tasks:    []headless.TaskResult{{Index: 0, Prompt: "t"}},
		Summary:  headless.Summary{TotalTasks: 1},
		Setup: headless.SetupInfo{
			WorktreeBase: "/tmp/errata-workdirs-123",
			SetupMS:      42,
			GitMode:      true,
			ModelDirs:    map[string]string{"model-a": "/tmp/errata-workdirs-123/errata-model-a"},
		},
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)

	var loaded headless.RunReport
	require.NoError(t, json.Unmarshal(data, &loaded))

	assert.Equal(t, "/tmp/errata-workdirs-123", loaded.Setup.WorktreeBase)
	assert.Equal(t, int64(42), loaded.Setup.SetupMS)
	assert.True(t, loaded.Setup.GitMode)
	assert.Equal(t, "/tmp/errata-workdirs-123/errata-model-a", loaded.Setup.ModelDirs["model-a"])
}

// ─── recipeName ──────────────────────────────────────────────────────────────

func TestRecipeName_UsesName(t *testing.T) {
	rec := &recipe.Recipe{Name: "Explicit"}
	assert.Equal(t, "Explicit", headless.RecipeName(rec))
}

func TestRecipeName_FallsBackToDefault(t *testing.T) {
	rec := &recipe.Recipe{}
	assert.Equal(t, "default", headless.RecipeName(rec))
}

// ─── truncate ───────────────────────────────────────────────────────────────

func TestTruncate_Short(t *testing.T) {
	assert.Equal(t, "hi", headless.Truncate("hi", 10))
}

func TestTruncate_ExactLength(t *testing.T) {
	assert.Equal(t, "hello", headless.Truncate("hello", 5))
}

func TestTruncate_Long(t *testing.T) {
	assert.Equal(t, "hello ...", headless.Truncate("hello world", 9))
}

// ─── JSON report ─────────────────────────────────────────────────────────────

func TestRunReport_JSONOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"task"}, nil)
	a1 := &mockAdapter{
		id:       "model-a",
		response: models.ModelResponse{Text: "ok", LatencyMS: 50},
	}

	// Capture stdout to verify JSON output.
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	opts := testOpts(rec, []models.ModelAdapter{a1}, outDir)
	opts.JSON = true
	_, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	var parsed headless.RunReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))
	assert.Equal(t, "Test Recipe", parsed.Recipe.Name)
}

// ─── setupGitDir helper ─────────────────────────────────────────────────────

// setupGitDir creates a temp dir with a git repo containing hello.txt.
func setupGitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run())
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644))
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "baseline")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
	return dir
}

// ─── createModelWorkDirs tests ──────────────────────────────────────────────

func TestCreateModelWorkDirs(t *testing.T) {
	// Set up a minimal git repo.
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run())
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644))
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "commit", "-m", "init")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	adapters := []models.ModelAdapter{
		&mockAdapter{id: "model-a"},
		&mockAdapter{id: "provider/model-b"},
	}

	baseDir := filepath.Join(t.TempDir(), "worktrees")
	dirs, base, _, cleanup, err := headless.CreateModelWorkDirs(dir, baseDir, adapters)
	require.NoError(t, err)
	defer cleanup()

	require.Len(t, dirs, 2)
	assert.NotEmpty(t, base, "base directory should be non-empty")

	// Each worktree should have the file from the original repo and be under base.
	for _, d := range dirs {
		got, readErr := os.ReadFile(filepath.Join(d, "hello.txt"))
		require.NoError(t, readErr)
		assert.Equal(t, "hello", string(got))

		rel, relErr := filepath.Rel(base, d)
		require.NoError(t, relErr)
		assert.False(t, filepath.IsAbs(rel), "model dir should be under base")
	}

	// Sanitized: provider/model-b should not have "/" in dir name.
	dirB := dirs["provider/model-b"]
	assert.NotContains(t, filepath.Base(dirB), "/")
}

// ─── diffWorktree tests ─────────────────────────────────────────────────────

func TestDiffWorktree_NewFile(t *testing.T) {
	dir := setupGitDir(t)

	// Add a new file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new content"), 0o644))

	writes, err := headless.DiffWorktree(dir)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.Equal(t, "new.txt", writes[0].Path)
	assert.Equal(t, "new content", writes[0].Content)
}

func TestDiffWorktree_ModifiedFile(t *testing.T) {
	dir := setupGitDir(t)

	// Modify existing file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("modified"), 0o644))

	writes, err := headless.DiffWorktree(dir)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.Equal(t, "hello.txt", writes[0].Path)
	assert.Equal(t, "modified", writes[0].Content)
}

func TestDiffWorktree_DeletedFile(t *testing.T) {
	dir := setupGitDir(t)

	// Delete the committed file.
	require.NoError(t, os.Remove(filepath.Join(dir, "hello.txt")))

	writes, err := headless.DiffWorktree(dir)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.Equal(t, "hello.txt", writes[0].Path)
	assert.True(t, writes[0].Delete, "deleted file should have Delete: true")
	assert.Empty(t, writes[0].Content, "deleted file should have empty content")
}

func TestDiffWorktree_NoChanges(t *testing.T) {
	dir := setupGitDir(t)

	writes, err := headless.DiffWorktree(dir)
	require.NoError(t, err)
	assert.Empty(t, writes)
}

// ─── snapshotDir tests ──────────────────────────────────────────────────────

func TestSnapshotDir(t *testing.T) {
	dir := t.TempDir()

	// Create some files and a .git/ subdirectory that should be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("beta"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o600))

	snap, err := headless.SnapshotDir(dir)
	require.NoError(t, err)

	// Should contain the two real files but not .git/HEAD.
	assert.Contains(t, snap, "a.txt")
	assert.Contains(t, snap, filepath.Join("sub", "b.txt"))
	assert.NotContains(t, snap, filepath.Join(".git", "HEAD"))
	assert.Len(t, snap, 2)

	// Hashes should be non-empty hex strings.
	for _, h := range snap {
		assert.Len(t, h, 64, "SHA-256 hex should be 64 chars")
	}
}

// ─── diffSnapshot tests ────────────────────────────────────────────────────

func TestDiffSnapshot_NewFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("hello"), 0o600))

	baseline, err := headless.SnapshotDir(dir)
	require.NoError(t, err)

	// Add a new file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new content"), 0o600))

	writes, err := headless.DiffSnapshot(dir, baseline)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.Equal(t, "new.txt", writes[0].Path)
	assert.Equal(t, "new content", writes[0].Content)
}

func TestDiffSnapshot_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original"), 0o600))

	baseline, err := headless.SnapshotDir(dir)
	require.NoError(t, err)

	// Modify the file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("modified"), 0o600))

	writes, err := headless.DiffSnapshot(dir, baseline)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.Equal(t, "file.txt", writes[0].Path)
	assert.Equal(t, "modified", writes[0].Content)
}

func TestDiffSnapshot_NoChanges(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("stable"), 0o600))

	baseline, err := headless.SnapshotDir(dir)
	require.NoError(t, err)

	writes, err := headless.DiffSnapshot(dir, baseline)
	require.NoError(t, err)
	assert.Empty(t, writes)
}

func TestDiffSnapshot_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("gone soon"), 0o600))

	baseline, err := headless.SnapshotDir(dir)
	require.NoError(t, err)

	// Delete the file.
	require.NoError(t, os.Remove(filepath.Join(dir, "file.txt")))

	writes, err := headless.DiffSnapshot(dir, baseline)
	require.NoError(t, err)
	require.Len(t, writes, 1)
	assert.Equal(t, "file.txt", writes[0].Path)
	assert.True(t, writes[0].Delete, "deleted file should have Delete: true")
	assert.Empty(t, writes[0].Content, "deleted file should have empty content")
}

func TestGitAvailable(t *testing.T) {
	// On CI and dev machines git is expected to be available.
	// This test just verifies the function runs without panicking.
	_ = headless.GitAvailable()
}

// ─── copyDir tests ──────────────────────────────────────────────────────────

func TestCopyDir(t *testing.T) {
	src := t.TempDir()

	// Create files in a subdirectory.
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("alpha"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("beta"), 0o600))

	// Create a .git/ directory that should be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o600))

	// Create a symlink.
	require.NoError(t, os.Symlink("a.txt", filepath.Join(src, "link.txt")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, headless.CopyDir(src, dst))

	// Regular files should be copied with correct content.
	got, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(got))

	got, err = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "beta", string(got))

	// File permissions should be preserved.
	info, err := os.Lstat(filepath.Join(dst, "sub", "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// .git/ should NOT be copied.
	assert.NoDirExists(t, filepath.Join(dst, ".git"))

	// Symlink should be preserved as a symlink.
	linkTarget, err := os.Readlink(filepath.Join(dst, "link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "a.txt", linkTarget)
}

func TestCopyDir_EmptyDir(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "empty-copy")

	require.NoError(t, headless.CopyDir(src, dst))
	assert.DirExists(t, dst)
}

// ─── Snapshot-mode integration test ─────────────────────────────────────────

func TestRun_SnapshotMode(t *testing.T) {
	// Create a project directory (non-git) with an initial file.
	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "existing.txt"), []byte("original"), 0o644))
	t.Chdir(projectDir)

	// Hide git so we fall through to snapshot mode.
	t.Setenv("PATH", t.TempDir())

	outDir := filepath.Join(t.TempDir(), "out")
	rec := testRecipe([]string{"modify project"}, nil)

	// Use a dispatching mock that writes a new file and deletes an existing one
	// via the real DispatchTool path (direct writes in snapshot mode).
	a1 := &dispatchingMockAdapter{
		id:       "model-a",
		response: models.ModelResponse{Text: "done", LatencyMS: 100},
		toolCalls: []mockToolCall{
			{name: tools.WriteToolName, args: map[string]string{"path": "added.txt", "content": "new file"}},
		},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	require.Len(t, report.Tasks, 1)
	writes := report.Tasks[0].Report.Models[0].ProposedWrites

	// Should detect the added file.
	var foundAdd bool
	for _, w := range writes {
		if w.Path == "added.txt" {
			foundAdd = true
			assert.Equal(t, "new file", w.Content)
		}
	}
	assert.True(t, foundAdd, "expected added.txt in ProposedWrites")
}
