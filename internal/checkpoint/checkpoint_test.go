package checkpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	responses := []models.ModelResponse{
		{
			ModelID:      "model-a",
			Text:         "hello world",
			LatencyMS:    1234,
			InputTokens:  100,
			OutputTokens: 50,
			CostUSD:      0.005,
			ProposedWrites: []tools.FileWrite{
				{Path: "foo.go", Content: "package foo"},
			},
		},
		{
			ModelID:      "model-b",
			Text:         "partial",
			LatencyMS:    567,
			InputTokens:  80,
			OutputTokens: 30,
			CostUSD:      0.002,
			Error:        "context canceled",
			Interrupted:  true,
		},
	}

	cp := Build("test prompt", []string{"model-a", "model-b"}, responses, true)
	if cp == nil {
		t.Fatal("Build returned nil; expected checkpoint with interrupted response")
	}
	if cp.Prompt != "test prompt" {
		t.Errorf("Prompt = %q, want %q", cp.Prompt, "test prompt")
	}
	if !cp.Verbose {
		t.Error("Verbose = false, want true")
	}
	if len(cp.Responses) != 2 {
		t.Fatalf("len(Responses) = %d, want 2", len(cp.Responses))
	}

	if err := Save(path, *cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil")
	}
	if loaded.Prompt != "test prompt" {
		t.Errorf("loaded Prompt = %q, want %q", loaded.Prompt, "test prompt")
	}
	if !loaded.Verbose {
		t.Error("loaded Verbose = false, want true")
	}

	// Check completed response
	s0 := loaded.Responses[0]
	if s0.ModelID != "model-a" || !s0.Completed || s0.Interrupted {
		t.Errorf("Response 0: ModelID=%q Completed=%v Interrupted=%v", s0.ModelID, s0.Completed, s0.Interrupted)
	}
	if s0.Text != "hello world" {
		t.Errorf("Response 0 Text = %q, want %q", s0.Text, "hello world")
	}
	if len(s0.ProposedWrites) != 1 || s0.ProposedWrites[0].Path != "foo.go" {
		t.Errorf("Response 0 ProposedWrites = %v", s0.ProposedWrites)
	}

	// Check interrupted response
	s1 := loaded.Responses[1]
	if s1.ModelID != "model-b" || s1.Completed || !s1.Interrupted {
		t.Errorf("Response 1: ModelID=%q Completed=%v Interrupted=%v", s1.ModelID, s1.Completed, s1.Interrupted)
	}
	if s1.Text != "partial" {
		t.Errorf("Response 1 Text = %q, want %q", s1.Text, "partial")
	}
	if s1.Error != "context canceled" {
		t.Errorf("Response 1 Error = %q, want %q", s1.Error, "context canceled")
	}

	// Round-trip through ToModelResponse
	mr := s1.ToModelResponse()
	if mr.ModelID != "model-b" || !mr.Interrupted || mr.Text != "partial" {
		t.Errorf("ToModelResponse: ModelID=%q Interrupted=%v Text=%q", mr.ModelID, mr.Interrupted, mr.Text)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	cp, err := Load(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cp != nil {
		t.Error("expected nil checkpoint for missing file")
	}
}

func TestLoad_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp, err := Load(path)
	if err == nil {
		t.Error("expected error for corrupt JSON")
	}
	if cp != nil {
		t.Error("expected nil checkpoint for corrupt JSON")
	}
}

func TestBuild_NoInterrupted(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "model-a", Text: "done"},
		{ModelID: "model-b", Text: "also done"},
	}
	cp := Build("prompt", []string{"model-a", "model-b"}, responses, false)
	if cp != nil {
		t.Error("expected nil checkpoint when no responses are interrupted")
	}
}

func TestBuild_AllInterrupted(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "model-a", Error: "cancelled", Interrupted: true},
		{ModelID: "model-b", Error: "cancelled", Interrupted: true},
	}
	cp := Build("prompt", []string{"model-a", "model-b"}, responses, false)
	if cp == nil {
		t.Fatal("expected non-nil checkpoint when responses are interrupted")
	}
	for _, s := range cp.Responses {
		if s.Completed {
			t.Errorf("Response %q should not be Completed", s.ModelID)
		}
		if !s.Interrupted {
			t.Errorf("Response %q should be Interrupted", s.ModelID)
		}
	}
}

func TestClear_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Clear(path); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still exists after Clear")
	}
}

func TestClear_MissingFile(t *testing.T) {
	if err := Clear(filepath.Join(t.TempDir(), "nonexistent.json")); err != nil {
		t.Errorf("Clear on missing file: %v", err)
	}
}

func TestFromModelResponse_ToModelResponse_RoundTrip(t *testing.T) {
	orig := models.ModelResponse{
		ModelID:             "test-model",
		Text:                "some text",
		LatencyMS:           999,
		InputTokens:         200,
		OutputTokens:        100,
		CacheReadTokens:     50,
		CacheCreationTokens: 25,
		CostUSD:             0.01,
		ProposedWrites: []tools.FileWrite{
			{Path: "a.go", Content: "package a"},
			{Path: "b.go", Content: "package b"},
		},
		Error:       "some error",
		Interrupted: true,
	}

	snap := FromModelResponse(orig)
	restored := snap.ToModelResponse()

	if restored.ModelID != orig.ModelID {
		t.Errorf("ModelID: got %q, want %q", restored.ModelID, orig.ModelID)
	}
	if restored.Text != orig.Text {
		t.Errorf("Text: got %q, want %q", restored.Text, orig.Text)
	}
	if restored.LatencyMS != orig.LatencyMS {
		t.Errorf("LatencyMS: got %d, want %d", restored.LatencyMS, orig.LatencyMS)
	}
	if restored.InputTokens != orig.InputTokens {
		t.Errorf("InputTokens: got %d, want %d", restored.InputTokens, orig.InputTokens)
	}
	if restored.OutputTokens != orig.OutputTokens {
		t.Errorf("OutputTokens: got %d, want %d", restored.OutputTokens, orig.OutputTokens)
	}
	if restored.CacheReadTokens != orig.CacheReadTokens {
		t.Errorf("CacheReadTokens: got %d, want %d", restored.CacheReadTokens, orig.CacheReadTokens)
	}
	if restored.CacheCreationTokens != orig.CacheCreationTokens {
		t.Errorf("CacheCreationTokens: got %d, want %d", restored.CacheCreationTokens, orig.CacheCreationTokens)
	}
	if restored.CostUSD != orig.CostUSD {
		t.Errorf("CostUSD: got %f, want %f", restored.CostUSD, orig.CostUSD)
	}
	if restored.Error != orig.Error {
		t.Errorf("Error: got %q, want %q", restored.Error, orig.Error)
	}
	if restored.Interrupted != orig.Interrupted {
		t.Errorf("Interrupted: got %v, want %v", restored.Interrupted, orig.Interrupted)
	}
	if len(restored.ProposedWrites) != len(orig.ProposedWrites) {
		t.Fatalf("ProposedWrites len: got %d, want %d", len(restored.ProposedWrites), len(orig.ProposedWrites))
	}
	for i, w := range restored.ProposedWrites {
		if w.Path != orig.ProposedWrites[i].Path || w.Content != orig.ProposedWrites[i].Content {
			t.Errorf("ProposedWrite[%d]: got {%q, %q}, want {%q, %q}",
				i, w.Path, w.Content, orig.ProposedWrites[i].Path, orig.ProposedWrites[i].Content)
		}
	}
}
