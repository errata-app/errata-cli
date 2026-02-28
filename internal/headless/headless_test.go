package headless_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/headless"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/tools"
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

// ─── Helpers ──────────────────────────────────────────────────────────────────

func testRecipe(tasks []string, criteriaItems []string) *recipe.Recipe {
	return &recipe.Recipe{
		Version:         1,
		Name:            "Test Recipe",
		Models:          []string{"model-a", "model-b"},
		Tasks:           tasks,
		SuccessCriteria: criteriaItems,
	}
}

func testOpts(rec *recipe.Recipe, adapters []models.ModelAdapter, outputDir string) *headless.Options {
	return &headless.Options{
		Recipe:    rec,
		Adapters:  adapters,
		SessionID: "test-session",
		Cfg:       config.Config{MaxHistoryTurns: 20},
		OutputDir: outputDir,
		Stderr:    &bytes.Buffer{},
	}
}

// ─── Run tests ────────────────────────────────────────────────────────────────

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
}

func TestRun_SequentialMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"write something", "check it"}, nil)
	rec.Context.TaskMode = "sequential"

	writePath := filepath.Join(dir, "output.txt")

	a1 := &mockAdapter{
		id: "model-a",
		response: models.ModelResponse{
			Text:      "wrote file",
			LatencyMS: 100,
			ProposedWrites: []tools.FileWrite{
				{Path: writePath, Content: "hello from model-a"},
			},
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
	// model-a should be selected as winner (it has writes, model-b doesn't; same score otherwise).
	assert.Equal(t, "model-a", report.Tasks[0].SelectedModel)

	// Verify the file was written to disk.
	data, err := os.ReadFile(writePath)
	require.NoError(t, err)
	assert.Equal(t, "hello from model-a", string(data))
}

func TestRun_WithCriteria(t *testing.T) {
	t.Chdir(t.TempDir())
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"do it"}, []string{"no_errors", "has_writes"})

	a1 := &mockAdapter{
		id: "model-a",
		response: models.ModelResponse{
			Text:           "done",
			LatencyMS:      100,
			ProposedWrites: []tools.FileWrite{{Path: "f.go", Content: "x"}},
		},
	}
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

func TestSelectWinner_ByCriteria(t *testing.T) {
	// This tests the public behavior through the sequential mode path.
	dir := t.TempDir()
	t.Chdir(dir)
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"task"}, []string{"no_errors", "has_writes"})
	rec.Context.TaskMode = "sequential"

	// model-a: 2/2 criteria, slower
	a1 := &mockAdapter{
		id: "model-a",
		response: models.ModelResponse{
			Text:           "done",
			LatencyMS:      500,
			ProposedWrites: []tools.FileWrite{{Path: filepath.Join(dir, "f.go"), Content: "x"}},
		},
	}
	// model-b: 1/2 criteria (no writes), faster
	a2 := &mockAdapter{
		id:       "model-b",
		response: models.ModelResponse{Text: "done", LatencyMS: 100},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1, a2}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	// model-a should win because it passes more criteria despite being slower.
	assert.Equal(t, "model-a", report.Tasks[0].SelectedModel)
}

func TestSelectWinner_ByCost(t *testing.T) {
	// When criteria scores are tied, the cheaper model should win.
	dir := t.TempDir()
	t.Chdir(dir)
	outDir := filepath.Join(t.TempDir(), "out")

	rec := testRecipe([]string{"task"}, []string{"no_errors"})
	rec.Context.TaskMode = "sequential"

	// Both pass 1/1 criteria. model-a is cheaper but slower.
	a1 := &mockAdapter{
		id: "model-a",
		response: models.ModelResponse{
			Text:      "done",
			LatencyMS: 500,
			CostUSD:   0.001,
		},
	}
	a2 := &mockAdapter{
		id: "model-b",
		response: models.ModelResponse{
			Text:      "done",
			LatencyMS: 100,
			CostUSD:   0.010,
		},
	}

	opts := testOpts(rec, []models.ModelAdapter{a1, a2}, outDir)
	report, err := headless.Run(context.Background(), opts)
	require.NoError(t, err)

	// model-a wins: same criteria score, lower cost (despite higher latency).
	assert.Equal(t, "model-a", report.Tasks[0].SelectedModel)
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

	// Find the saved file.
	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	path := filepath.Join(outDir, entries[0].Name())
	loaded, err := headless.Load(path)
	require.NoError(t, err)

	assert.Equal(t, report.ID, loaded.ID)
	assert.Equal(t, report.TaskMode, loaded.TaskMode)
	assert.Equal(t, report.Recipe.Name, loaded.Recipe.Name)
	require.Len(t, loaded.Tasks, 1)
	assert.Equal(t, "task", loaded.Tasks[0].Prompt)
	assert.Equal(t, 1, loaded.Summary.TotalTasks)
}

// ─── recipeName ──────────────────────────────────────────────────────────────

func TestRecipeName_UsesName(t *testing.T) {
	rec := &recipe.Recipe{Name: "Explicit"}
	assert.Equal(t, "Explicit", headless.RecipeName(rec))
}

func TestRecipeName_FallsBackToMetadataName(t *testing.T) {
	rec := &recipe.Recipe{Metadata: recipe.MetadataConfig{Name: "MetaName"}}
	assert.Equal(t, "MetaName", headless.RecipeName(rec))
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

// ─── Save / Load error paths ────────────────────────────────────────────────

func TestSave_MkdirAllError(t *testing.T) {
	_, err := headless.Save("/dev/null/sub/out", &headless.RunReport{})
	assert.Error(t, err)
}

func TestLoad_NonexistentFile(t *testing.T) {
	_, err := headless.Load("/no/such/file.json")
	assert.Error(t, err)
}

func TestLoad_CorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{bad json!"), 0o644))
	_, err := headless.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
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
