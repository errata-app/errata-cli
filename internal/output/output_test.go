package output

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/tools"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "default"},
		{"My Cool Recipe", "my_cool_recipe"},
		{"recipe/with:bad*chars", "recipewithbadchars"},
		{"---", "---"},
		{"  spaces  ", "spaces"},
		{"UPPERCASE", "uppercase"},
		{"hello world", "hello_world"},
		{"a-b_c", "a-b_c"},
		{"***", "default"}, // all unsafe chars → empty → fallback
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReport_Filename(t *testing.T) {
	r := &Report{
		ID:     "a3f8b2c1d4e5f678",
		Recipe: RecipeSnapshot{Name: "My Test Recipe"},
	}
	want := "my_test_recipe_output_a3f8b2c1d4e5f678.json"
	got := r.Filename()
	if got != want {
		t.Errorf("Filename() = %q, want %q", got, want)
	}
}

func TestReport_Filename_Default(t *testing.T) {
	r := &Report{
		ID:     "abcd1234",
		Recipe: RecipeSnapshot{Name: ""},
	}
	want := "default_output_abcd1234.json"
	got := r.Filename()
	if got != want {
		t.Errorf("Filename() = %q, want %q", got, want)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	temp := 0.7
	maxTok := 4096
	seed := int64(42)

	original := &Report{
		ID:        "deadbeef01234567",
		Timestamp: time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC),
		SessionID: "sess123",
		Recipe: RecipeSnapshot{
			Name:         "test recipe",
			Models:       []string{"model-a", "model-b"},
			SystemPrompt: "Be helpful.",
			Tools:        []string{"read_file", "bash"},
			Constraints:  &ConstraintsSnapshot{MaxSteps: 10, Timeout: "5m0s"},
			ModelParams:  &ModelParamsSnapshot{Temperature: &temp, MaxTokens: &maxTok, Seed: &seed},
		},
		Prompt: "Write a hello world program",
		Models: []ModelResult{
			{
				ModelID:             "model-a",
				Text:                "Here is your code.",
				LatencyMS:           1500,
				InputTokens:         1000,
				OutputTokens:        500,
				CostUSD:             0.0042,
				StopReason:          "complete",
				ProposedWrites: []WriteEntry{
					{Path: "main.go", Content: "package main\n"},
				},
				Events: []EventEntry{
					{Type: "reading", Data: "read_file main.go"},
					{Type: "writing", Data: "write_file main.go"},
				},
			},
			{
				ModelID:      "model-b",
				Text:         "Error occurred.",
				LatencyMS:    2000,
				InputTokens:  800,
				OutputTokens: 100,
				CostUSD:      0.001,
				Error:        "context limit exceeded",
				StopReason:   "error",
				Events:       []EventEntry{},
			},
		},
		Aggregate: AggregateStats{
			TotalCostUSD:      0.0052,
			TotalInputTokens:  1800,
			TotalOutputTokens: 600,
			ModelCount:        2,
			SuccessCount:      1,
			FastestModel:      "model-a",
			FastestMS:         1500,
		},
		Selection: &SelectionOutcome{
			SelectedModel: "model-a",
			AppliedFiles:  []string{"main.go"},
			Timestamp:     time.Date(2026, 2, 25, 12, 1, 0, 0, time.UTC),
		},
	}

	path, err := Save(dir, original)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify top-level fields.
	if loaded.ID != original.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, original.ID)
	}
	if !loaded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", loaded.Timestamp, original.Timestamp)
	}
	if loaded.SessionID != original.SessionID {
		t.Errorf("SessionID = %q, want %q", loaded.SessionID, original.SessionID)
	}
	if loaded.Prompt != original.Prompt {
		t.Errorf("Prompt = %q, want %q", loaded.Prompt, original.Prompt)
	}

	// Recipe snapshot.
	if loaded.Recipe.Name != original.Recipe.Name {
		t.Errorf("Recipe.Name = %q, want %q", loaded.Recipe.Name, original.Recipe.Name)
	}
	if len(loaded.Recipe.Models) != 2 {
		t.Errorf("Recipe.Models len = %d, want 2", len(loaded.Recipe.Models))
	}
	if loaded.Recipe.SystemPrompt != "Be helpful." {
		t.Errorf("Recipe.SystemPrompt = %q, want %q", loaded.Recipe.SystemPrompt, "Be helpful.")
	}
	if loaded.Recipe.Constraints == nil {
		t.Fatal("Recipe.Constraints is nil")
	}
	if loaded.Recipe.Constraints.MaxSteps != 10 {
		t.Errorf("Constraints.MaxSteps = %d, want 10", loaded.Recipe.Constraints.MaxSteps)
	}
	if loaded.Recipe.Constraints.Timeout != "5m0s" {
		t.Errorf("Constraints.Timeout = %q, want %q", loaded.Recipe.Constraints.Timeout, "5m0s")
	}
	if loaded.Recipe.ModelParams == nil {
		t.Fatal("Recipe.ModelParams is nil")
	}
	if *loaded.Recipe.ModelParams.Temperature != 0.7 {
		t.Errorf("ModelParams.Temperature = %f, want 0.7", *loaded.Recipe.ModelParams.Temperature)
	}
	if *loaded.Recipe.ModelParams.MaxTokens != 4096 {
		t.Errorf("ModelParams.MaxTokens = %d, want 4096", *loaded.Recipe.ModelParams.MaxTokens)
	}
	if *loaded.Recipe.ModelParams.Seed != 42 {
		t.Errorf("ModelParams.Seed = %d, want 42", *loaded.Recipe.ModelParams.Seed)
	}

	// Models.
	if len(loaded.Models) != 2 {
		t.Fatalf("Models len = %d, want 2", len(loaded.Models))
	}
	m0 := loaded.Models[0]
	if m0.ModelID != "model-a" {
		t.Errorf("Models[0].ModelID = %q, want %q", m0.ModelID, "model-a")
	}
	if m0.Text != "Here is your code." {
		t.Errorf("Models[0].Text = %q", m0.Text)
	}
	if m0.LatencyMS != 1500 {
		t.Errorf("Models[0].LatencyMS = %d, want 1500", m0.LatencyMS)
	}
	if m0.InputTokens != 1000 {
		t.Errorf("Models[0].InputTokens = %d", m0.InputTokens)
	}
	if len(m0.ProposedWrites) != 1 || m0.ProposedWrites[0].Path != "main.go" {
		t.Errorf("Models[0].ProposedWrites unexpected: %v", m0.ProposedWrites)
	}
	if len(m0.Events) != 2 {
		t.Errorf("Models[0].Events len = %d, want 2", len(m0.Events))
	}

	m1 := loaded.Models[1]
	if m1.Error != "context limit exceeded" {
		t.Errorf("Models[1].Error = %q", m1.Error)
	}

	// StopReason round-trip.
	if m0.StopReason != "complete" {
		t.Errorf("Models[0].StopReason = %q, want %q", m0.StopReason, "complete")
	}
	if m1.StopReason != "error" {
		t.Errorf("Models[1].StopReason = %q, want %q", m1.StopReason, "error")
	}

	// Aggregate.
	if loaded.Aggregate.TotalCostUSD != original.Aggregate.TotalCostUSD {
		t.Errorf("Aggregate.TotalCostUSD = %f", loaded.Aggregate.TotalCostUSD)
	}
	if loaded.Aggregate.ModelCount != 2 {
		t.Errorf("Aggregate.ModelCount = %d", loaded.Aggregate.ModelCount)
	}
	if loaded.Aggregate.SuccessCount != 1 {
		t.Errorf("Aggregate.SuccessCount = %d", loaded.Aggregate.SuccessCount)
	}
	if loaded.Aggregate.FastestModel != "model-a" {
		t.Errorf("Aggregate.FastestModel = %q", loaded.Aggregate.FastestModel)
	}

	// Selection.
	if loaded.Selection == nil {
		t.Fatal("Selection is nil")
	}
	if loaded.Selection.SelectedModel != "model-a" {
		t.Errorf("Selection.SelectedModel = %q", loaded.Selection.SelectedModel)
	}
	if len(loaded.Selection.AppliedFiles) != 1 || loaded.Selection.AppliedFiles[0] != "main.go" {
		t.Errorf("Selection.AppliedFiles = %v", loaded.Selection.AppliedFiles)
	}
}

func TestCollector_ConcurrentEvents(t *testing.T) {
	c := NewCollector()
	var callCount atomic.Int64

	wrapped := c.WrapOnEvent(func(modelID string, event models.AgentEvent) {
		callCount.Add(1)
	})

	var wg sync.WaitGroup
	const goroutines = 10
	const eventsEach = 100
	for g := range goroutines {
		wg.Go(func() {
			modelID := "model-a"
			if g%2 == 1 {
				modelID = "model-b"
			}
			for range eventsEach {
				wrapped(modelID, models.AgentEvent{Type: models.EventReading, Data: "data"})
			}
		})
	}
	wg.Wait()

	total := callCount.Load()
	if total != goroutines*eventsEach {
		t.Errorf("original onEvent called %d times, want %d", total, goroutines*eventsEach)
	}

	aEvents := c.Events("model-a")
	bEvents := c.Events("model-b")
	if len(aEvents)+len(bEvents) != goroutines*eventsEach {
		t.Errorf("total collected events = %d, want %d", len(aEvents)+len(bEvents), goroutines*eventsEach)
	}
}

func TestCollector_Events_UnknownModel(t *testing.T) {
	c := NewCollector()
	if got := c.Events("nonexistent"); got != nil {
		t.Errorf("Events for unknown model = %v, want nil", got)
	}
}

func TestBuildReport_AggregateStats(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID:      "fast-model",
			Text:         "answer 1",
			LatencyMS:    500,
			InputTokens:  1000,
			OutputTokens: 200,
			CostUSD:      0.01,
			ProposedWrites: []tools.FileWrite{
				{Path: "a.go", Content: "package a"},
			},
		},
		{
			ModelID:      "slow-model",
			Text:         "answer 2",
			LatencyMS:    3000,
			InputTokens:  2000,
			OutputTokens: 400,
			CostUSD:      0.05,
		},
		{
			ModelID:      "error-model",
			LatencyMS:    100,
			InputTokens:  500,
			OutputTokens: 0,
			Error:        "timeout",
		},
	}

	rec := &recipe.Recipe{Name: "test"}
	collector := NewCollector()
	// Simulate some events.
	collector.WrapOnEvent(func(string, models.AgentEvent) {})(
		"fast-model", models.AgentEvent{Type: models.EventReading, Data: "read_file a.go"},
	)

	report := BuildReport("sess1", rec, "do stuff", responses, collector, []string{"read_file", "bash"})

	if diff := report.Aggregate.TotalCostUSD - 0.06; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("TotalCostUSD = %f, want 0.06", report.Aggregate.TotalCostUSD)
	}
	if report.Aggregate.TotalInputTokens != 3500 {
		t.Errorf("TotalInputTokens = %d, want 3500", report.Aggregate.TotalInputTokens)
	}
	if report.Aggregate.TotalOutputTokens != 600 {
		t.Errorf("TotalOutputTokens = %d, want 600", report.Aggregate.TotalOutputTokens)
	}
	if report.Aggregate.ModelCount != 3 {
		t.Errorf("ModelCount = %d, want 3", report.Aggregate.ModelCount)
	}
	if report.Aggregate.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", report.Aggregate.SuccessCount)
	}
	if report.Aggregate.FastestModel != "fast-model" {
		t.Errorf("FastestModel = %q, want fast-model", report.Aggregate.FastestModel)
	}
	if report.Aggregate.FastestMS != 500 {
		t.Errorf("FastestMS = %d, want 500", report.Aggregate.FastestMS)
	}

	// Verify events were captured for fast-model.
	if len(report.Models[0].Events) != 1 {
		t.Errorf("fast-model events len = %d, want 1", len(report.Models[0].Events))
	}
	// error-model should have empty (not nil) events.
	if report.Models[2].Events == nil {
		t.Error("error-model events should not be nil")
	}

	// Verify proposed writes.
	if len(report.Models[0].ProposedWrites) != 1 {
		t.Errorf("fast-model ProposedWrites len = %d, want 1", len(report.Models[0].ProposedWrites))
	}
	if report.Models[0].ProposedWrites[0].Path != "a.go" {
		t.Errorf("ProposedWrites[0].Path = %q", report.Models[0].ProposedWrites[0].Path)
	}

	// Recipe snapshot.
	if report.Recipe.Name != "test" {
		t.Errorf("Recipe.Name = %q", report.Recipe.Name)
	}
	if len(report.Recipe.Tools) != 2 {
		t.Errorf("Recipe.Tools len = %d, want 2", len(report.Recipe.Tools))
	}

	// Selection should be nil.
	if report.Selection != nil {
		t.Error("Selection should be nil before RecordSelection")
	}
}

func TestBuildReport_NilRecipe(t *testing.T) {
	report := BuildReport("sess", nil, "hello", nil, nil, nil)
	if report.Recipe.Name != "default" {
		t.Errorf("Recipe.Name = %q, want default", report.Recipe.Name)
	}
	if report.Aggregate.ModelCount != 0 {
		t.Errorf("ModelCount = %d, want 0", report.Aggregate.ModelCount)
	}
}

func TestRecordSelection_UpdatesFile(t *testing.T) {
	dir := t.TempDir()

	report := BuildReport("sess", nil, "hello", []models.ModelResponse{
		{ModelID: "model-a", Text: "hi", LatencyMS: 100},
	}, nil, nil)

	if _, err := Save(dir, report); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify no selection initially.
	loaded, err := Load(filepath.Join(dir, report.Filename()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Selection != nil {
		t.Fatal("Selection should be nil before RecordSelection")
	}

	// Record selection.
	if err := RecordSelection(dir, report, "model-a", []string{"main.go"}, ""); err != nil {
		t.Fatalf("RecordSelection: %v", err)
	}

	// Reload and verify.
	loaded, err = Load(filepath.Join(dir, report.Filename()))
	if err != nil {
		t.Fatalf("Load after selection: %v", err)
	}
	if loaded.Selection == nil {
		t.Fatal("Selection is nil after RecordSelection")
	}
	if loaded.Selection.SelectedModel != "model-a" {
		t.Errorf("SelectedModel = %q", loaded.Selection.SelectedModel)
	}
	if len(loaded.Selection.AppliedFiles) != 1 || loaded.Selection.AppliedFiles[0] != "main.go" {
		t.Errorf("AppliedFiles = %v", loaded.Selection.AppliedFiles)
	}
}

func TestSave_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep", "outputs")
	report := BuildReport("sess", nil, "hello", nil, nil, nil)

	path, err := Save(dir, report)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestSave_NoTmpLeftBehind(t *testing.T) {
	dir := t.TempDir()
	report := BuildReport("sess", nil, "hello", nil, nil, nil)

	path, err := Save(dir, report)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Check no .tmp file exists.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected .tmp file to not exist, got err: %v", err)
	}
}

func TestLoad_NonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/report.json")
	if err == nil {
		t.Error("Load should return error for nonexistent file")
	}
}

func TestBuildReport_NilRecipeNilCollector(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m", Text: "answer", LatencyMS: 100},
	}
	report := BuildReport("sess", nil, "hello", responses, nil, nil)
	if report.Recipe.Name != "default" {
		t.Errorf("Recipe.Name = %q, want default", report.Recipe.Name)
	}
	if report.Recipe.Constraints != nil {
		t.Error("nil recipe should leave Constraints nil")
	}
	if report.Recipe.ModelParams != nil {
		t.Error("nil recipe should leave ModelParams nil")
	}
	if len(report.Models) != 1 {
		t.Fatalf("Models len = %d, want 1", len(report.Models))
	}
	if report.Models[0].Events == nil {
		t.Error("Events should not be nil even without a collector")
	}
	if len(report.Models[0].Events) != 0 {
		t.Errorf("Events len = %d, want 0", len(report.Models[0].Events))
	}
}

func TestBuildReport_ZeroConstraints(t *testing.T) {
	rec := &recipe.Recipe{Name: "test"}
	report := BuildReport("sess", rec, "hello", nil, nil, nil)
	if report.Recipe.Constraints != nil {
		t.Error("zero constraints should leave Constraints nil")
	}
	if report.Recipe.ModelParams != nil {
		t.Error("zero model params should leave ModelParams nil")
	}
}

func TestBuildReport_PartialModelParams(t *testing.T) {
	temp := 0.5
	rec := &recipe.Recipe{
		Name:        "test",
		ModelParams: recipe.ModelParamsConfig{Temperature: &temp},
	}
	report := BuildReport("sess", rec, "hello", nil, nil, nil)
	if report.Recipe.ModelParams == nil {
		t.Fatal("ModelParams should not be nil when Temperature is set")
	}
	if report.Recipe.ModelParams.Temperature == nil || *report.Recipe.ModelParams.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", report.Recipe.ModelParams.Temperature)
	}
	if report.Recipe.ModelParams.MaxTokens != nil {
		t.Errorf("MaxTokens should be nil, got %v", report.Recipe.ModelParams.MaxTokens)
	}
	if report.Recipe.ModelParams.Seed != nil {
		t.Errorf("Seed should be nil, got %v", report.Recipe.ModelParams.Seed)
	}
}

func TestBuildReport_AllErrors(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Error: "fail1"},
		{ModelID: "m2", Error: "fail2"},
	}
	report := BuildReport("sess", nil, "hello", responses, nil, nil)
	if report.Aggregate.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", report.Aggregate.SuccessCount)
	}
	if report.Aggregate.FastestModel != "" {
		t.Errorf("FastestModel = %q, want empty", report.Aggregate.FastestModel)
	}
	if report.Aggregate.FastestMS != 0 {
		t.Errorf("FastestMS = %d, want 0", report.Aggregate.FastestMS)
	}
}

func TestSave_MkdirAllError(t *testing.T) {
	report := BuildReport("sess", nil, "hello", nil, nil, nil)
	_, err := Save("/dev/null/sub/outputs", report)
	if err == nil {
		t.Error("Save to impossible path should error")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("Load should return error for invalid JSON")
	}
}

// ─── Session Report ──────────────────────────────────────────────────────────

func TestBuildSessionReport_SingleTurn(t *testing.T) {
	r := &Report{
		ID:        "rpt_1",
		Timestamp: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		SessionID: "sess1",
		Prompt:    "fix bug",
		Recipe:    RecipeSnapshot{Name: "test"},
		Models: []ModelResult{
			{ModelID: "m1", InputTokens: 100, OutputTokens: 50, CostUSD: 0.01},
		},
		Aggregate: AggregateStats{
			TotalCostUSD:      0.01,
			TotalInputTokens:  100,
			TotalOutputTokens: 50,
			ModelCount:        1,
			SuccessCount:      1,
		},
	}

	sr := BuildSessionReport("sess1", []*Report{r})

	if sr.SessionID != "sess1" {
		t.Errorf("SessionID = %q, want sess1", sr.SessionID)
	}
	if len(sr.Turns) != 1 {
		t.Fatalf("Turns len = %d, want 1", len(sr.Turns))
	}
	if sr.Turns[0].TurnIndex != 0 {
		t.Errorf("TurnIndex = %d, want 0", sr.Turns[0].TurnIndex)
	}
	if sr.Turns[0].Prompt != "fix bug" {
		t.Errorf("Prompt = %q, want fix bug", sr.Turns[0].Prompt)
	}
	if sr.Aggregate.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", sr.Aggregate.TurnCount)
	}
	if sr.Aggregate.TotalCostUSD != 0.01 {
		t.Errorf("TotalCostUSD = %f, want 0.01", sr.Aggregate.TotalCostUSD)
	}
	if sr.Aggregate.TotalInputTokens != 100 {
		t.Errorf("TotalInputTokens = %d, want 100", sr.Aggregate.TotalInputTokens)
	}
	if sr.Aggregate.TotalOutputTokens != 50 {
		t.Errorf("TotalOutputTokens = %d, want 50", sr.Aggregate.TotalOutputTokens)
	}
}

func TestBuildSessionReport_MultiTurn(t *testing.T) {
	r1 := &Report{
		ID:        "rpt_1",
		Timestamp: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		Prompt:    "first prompt",
		Recipe:    RecipeSnapshot{Name: "test"},
		Models:    []ModelResult{{ModelID: "m1", InputTokens: 100, OutputTokens: 50, CostUSD: 0.01}},
		Aggregate: AggregateStats{TotalCostUSD: 0.01, TotalInputTokens: 100, TotalOutputTokens: 50},
	}
	r2 := &Report{
		ID:        "rpt_2",
		Timestamp: time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC),
		Prompt:    "second prompt",
		Recipe:    RecipeSnapshot{Name: "test"},
		Models:    []ModelResult{{ModelID: "m1", InputTokens: 200, OutputTokens: 100, CostUSD: 0.02}},
		Aggregate: AggregateStats{TotalCostUSD: 0.02, TotalInputTokens: 200, TotalOutputTokens: 100},
	}
	r3 := &Report{
		ID:        "rpt_3",
		Timestamp: time.Date(2026, 3, 1, 10, 10, 0, 0, time.UTC),
		Prompt:    "third prompt",
		Recipe:    RecipeSnapshot{Name: "test"},
		Models:    []ModelResult{{ModelID: "m1", InputTokens: 300, OutputTokens: 150, CostUSD: 0.03}},
		Aggregate: AggregateStats{TotalCostUSD: 0.03, TotalInputTokens: 300, TotalOutputTokens: 150},
	}

	sr := BuildSessionReport("sess1", []*Report{r1, r2, r3})

	if len(sr.Turns) != 3 {
		t.Fatalf("Turns len = %d, want 3", len(sr.Turns))
	}
	for i, turn := range sr.Turns {
		if turn.TurnIndex != i {
			t.Errorf("Turns[%d].TurnIndex = %d, want %d", i, turn.TurnIndex, i)
		}
	}
	if sr.Turns[0].Prompt != "first prompt" {
		t.Errorf("Turns[0].Prompt = %q", sr.Turns[0].Prompt)
	}
	if sr.Turns[2].Prompt != "third prompt" {
		t.Errorf("Turns[2].Prompt = %q", sr.Turns[2].Prompt)
	}
	if sr.Aggregate.TurnCount != 3 {
		t.Errorf("TurnCount = %d, want 3", sr.Aggregate.TurnCount)
	}
	if diff := sr.Aggregate.TotalCostUSD - 0.06; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("TotalCostUSD = %f, want 0.06", sr.Aggregate.TotalCostUSD)
	}
	if sr.Aggregate.TotalInputTokens != 600 {
		t.Errorf("TotalInputTokens = %d, want 600", sr.Aggregate.TotalInputTokens)
	}
	if sr.Aggregate.TotalOutputTokens != 300 {
		t.Errorf("TotalOutputTokens = %d, want 300", sr.Aggregate.TotalOutputTokens)
	}
}

func TestBuildSessionReport_WithSelection(t *testing.T) {
	sel := &SelectionOutcome{
		SelectedModel: "m1",
		AppliedFiles:  []string{"main.go"},
		Timestamp:     time.Date(2026, 3, 1, 10, 1, 0, 0, time.UTC),
	}
	r1 := &Report{
		ID:        "rpt_1",
		Prompt:    "no selection",
		Aggregate: AggregateStats{},
	}
	r2 := &Report{
		ID:        "rpt_2",
		Prompt:    "with selection",
		Selection: sel,
		Aggregate: AggregateStats{},
	}

	sr := BuildSessionReport("sess1", []*Report{r1, r2})

	if sr.Turns[0].Selection != nil {
		t.Error("Turns[0].Selection should be nil")
	}
	if sr.Turns[1].Selection == nil {
		t.Fatal("Turns[1].Selection should not be nil")
	}
	if sr.Turns[1].Selection.SelectedModel != "m1" {
		t.Errorf("Selection.SelectedModel = %q", sr.Turns[1].Selection.SelectedModel)
	}
}

func TestBuildSessionReport_Empty(t *testing.T) {
	sr := BuildSessionReport("sess1", nil)

	if sr.SessionID != "sess1" {
		t.Errorf("SessionID = %q", sr.SessionID)
	}
	if len(sr.Turns) != 0 {
		t.Errorf("Turns len = %d, want 0", len(sr.Turns))
	}
	if sr.Aggregate.TurnCount != 0 {
		t.Errorf("TurnCount = %d, want 0", sr.Aggregate.TurnCount)
	}
	if sr.Aggregate.TotalCostUSD != 0 {
		t.Errorf("TotalCostUSD = %f, want 0", sr.Aggregate.TotalCostUSD)
	}
}

func TestSaveSessionAndLoadSession_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	r := &Report{
		ID:        "rpt_1",
		Timestamp: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		SessionID: "sess1",
		Prompt:    "test prompt",
		Recipe:    RecipeSnapshot{Name: "test", Tools: []string{"bash"}},
		Models: []ModelResult{
			{
				ModelID:      "m1",
				Text:         "answer",
				LatencyMS:    500,
				InputTokens:  100,
				OutputTokens: 50,
				CostUSD:      0.01,
				Events:       []EventEntry{{Type: "reading", Data: "read_file x.go"}},
			},
		},
		Aggregate: AggregateStats{
			TotalCostUSD:      0.01,
			TotalInputTokens:  100,
			TotalOutputTokens: 50,
			ModelCount:        1,
			SuccessCount:      1,
			FastestModel:      "m1",
			FastestMS:         500,
		},
		Selection: &SelectionOutcome{
			SelectedModel: "m1",
			AppliedFiles:  []string{"x.go"},
			Timestamp:     time.Date(2026, 3, 1, 10, 1, 0, 0, time.UTC),
		},
	}

	original := BuildSessionReport("sess1", []*Report{r})

	path, err := SaveSession(dir, original)
	if err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if loaded.ID != original.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, original.ID)
	}
	if loaded.SessionID != "sess1" {
		t.Errorf("SessionID = %q", loaded.SessionID)
	}
	if !loaded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", loaded.Timestamp, original.Timestamp)
	}
	if len(loaded.Turns) != 1 {
		t.Fatalf("Turns len = %d, want 1", len(loaded.Turns))
	}
	if loaded.Turns[0].Prompt != "test prompt" {
		t.Errorf("Turns[0].Prompt = %q", loaded.Turns[0].Prompt)
	}
	if loaded.Turns[0].Selection == nil {
		t.Fatal("Turns[0].Selection should not be nil")
	}
	if loaded.Turns[0].Selection.SelectedModel != "m1" {
		t.Errorf("Selection.SelectedModel = %q", loaded.Turns[0].Selection.SelectedModel)
	}
	if loaded.Aggregate.TurnCount != 1 {
		t.Errorf("TurnCount = %d", loaded.Aggregate.TurnCount)
	}
	if loaded.Aggregate.TotalCostUSD != 0.01 {
		t.Errorf("TotalCostUSD = %f", loaded.Aggregate.TotalCostUSD)
	}
	if loaded.Aggregate.TotalInputTokens != 100 {
		t.Errorf("TotalInputTokens = %d", loaded.Aggregate.TotalInputTokens)
	}
}

func TestSessionReport_Filename(t *testing.T) {
	sr := &SessionReport{ID: "srpt_abc123"}
	want := "session_output_srpt_abc123.json"
	got := sr.Filename()
	if got != want {
		t.Errorf("Filename() = %q, want %q", got, want)
	}
}
