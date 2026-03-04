package checkpoint

import (
	"os"
	"path/filepath"
	"sync"
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

// ─── IncrementalSaver tests ──────────────────────────────────────────────────

func TestIncrementalSaver_UpdateWritesValidCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	saver := NewIncrementalSaver(path, "test prompt", []string{"model-a", "model-b"}, false)

	saver.Update("model-a", ResponseSnapshot{
		ModelID:      "model-a",
		Text:         "partial text",
		InputTokens:  50,
		OutputTokens: 20,
		Interrupted:  true,
	})

	// Checkpoint file should exist and be valid.
	cp, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Update: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint file after Update")
	}
	if cp.Prompt != "test prompt" {
		t.Errorf("Prompt = %q, want %q", cp.Prompt, "test prompt")
	}
	if len(cp.Responses) != 1 {
		t.Fatalf("len(Responses) = %d, want 1", len(cp.Responses))
	}
	if cp.Responses[0].Text != "partial text" {
		t.Errorf("Response text = %q, want %q", cp.Responses[0].Text, "partial text")
	}
}

func TestIncrementalSaver_MarkCompleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	saver := NewIncrementalSaver(path, "prompt", []string{"model-a"}, false)

	saver.MarkCompleted("model-a", ResponseSnapshot{
		ModelID:     "model-a",
		Text:        "done",
		Interrupted: false,
	})

	cp, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cp.Responses[0].Completed {
		t.Error("expected Completed=true after MarkCompleted")
	}
}

func TestIncrementalSaver_ConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	ids := []string{"model-a", "model-b", "model-c"}
	saver := NewIncrementalSaver(path, "prompt", ids, false)

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() {
			for range 10 {
				saver.Update(id, ResponseSnapshot{
					ModelID:     id,
					Text:        "turn data",
					Interrupted: true,
				})
			}
		})
	}
	wg.Wait()

	// File should be valid JSON with all 3 models.
	cp, err := Load(path)
	if err != nil {
		t.Fatalf("Load after concurrent updates: %v", err)
	}
	if len(cp.Responses) != 3 {
		t.Errorf("len(Responses) = %d, want 3", len(cp.Responses))
	}
}

func TestIncrementalSaver_PreservesAdapterOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	saver := NewIncrementalSaver(path, "prompt", []string{"model-a", "model-b"}, true)

	// Update model-b first, then model-a.
	saver.Update("model-b", ResponseSnapshot{ModelID: "model-b", Text: "b", Interrupted: true})
	saver.Update("model-a", ResponseSnapshot{ModelID: "model-a", Text: "a", Interrupted: true})

	cp, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Responses should be in adapter ID order, not insertion order.
	if cp.Responses[0].ModelID != "model-a" || cp.Responses[1].ModelID != "model-b" {
		t.Errorf("order: got [%s, %s], want [model-a, model-b]",
			cp.Responses[0].ModelID, cp.Responses[1].ModelID)
	}
	if !cp.Verbose {
		t.Error("Verbose should be true")
	}
}

// ─── Save error paths ───────────────────────────────────────────────────────

func TestSave_WriteFileError(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o444)
	defer os.Chmod(dir, 0o755)

	cp := Checkpoint{Prompt: "test"}
	err := Save(filepath.Join(dir, "checkpoint.json"), cp)
	if err == nil {
		t.Error("expected error writing to read-only directory")
	}
}

func TestSave_MkdirAllError(t *testing.T) {
	// /dev/null is a file, not a directory — MkdirAll fails.
	cp := Checkpoint{Prompt: "test"}
	err := Save("/dev/null/sub/checkpoint.json", cp)
	if err == nil {
		t.Error("expected error for impossible directory creation")
	}
}

// ─── Load: ReadFile error that is not IsNotExist ────────────────────────────

func TestLoad_ReadFileError(t *testing.T) {
	// Passing a directory path to ReadFile returns a non-IsNotExist error.
	dir := t.TempDir()
	cp, err := Load(dir)
	if err == nil {
		t.Error("expected error reading a directory as a file")
	}
	if cp != nil {
		t.Error("expected nil checkpoint on read error")
	}
}

// ─── SnapshotFromPartial with nil writes ────────────────────────────────────

func TestSnapshotFromPartial_NilWrites(t *testing.T) {
	ps := models.PartialSnapshot{
		Text:        "hello",
		InputTokens: 100,
		Writes:      nil,
	}
	snap := SnapshotFromPartial("test-model", ps)
	if snap.ModelID != "test-model" {
		t.Errorf("ModelID = %q, want %q", snap.ModelID, "test-model")
	}
	if len(snap.ProposedWrites) != 0 {
		t.Errorf("expected empty ProposedWrites, got %d", len(snap.ProposedWrites))
	}
}

// ─── FromModelResponse with no proposed writes ──────────────────────────────

func TestFromModelResponse_NoWrites(t *testing.T) {
	r := models.ModelResponse{
		ModelID: "test",
		Text:    "hello",
	}
	snap := FromModelResponse(r)
	if len(snap.ProposedWrites) != 0 {
		t.Errorf("expected empty ProposedWrites, got %d", len(snap.ProposedWrites))
	}
	if !snap.Completed {
		t.Error("expected Completed=true for non-interrupted, no-error response")
	}
}

func TestSnapshotFromPartial(t *testing.T) {
	ps := models.PartialSnapshot{
		Text:         "hello",
		InputTokens:  100,
		OutputTokens: 50,
		CostUSD:      0.005,
		LatencyMS:    1234,
		Writes:       []tools.FileWrite{{Path: "a.go", Content: "package a"}},
	}
	snap := SnapshotFromPartial("test-model", ps)
	if snap.ModelID != "test-model" {
		t.Errorf("ModelID = %q, want %q", snap.ModelID, "test-model")
	}
	if snap.Text != "hello" {
		t.Errorf("Text = %q, want %q", snap.Text, "hello")
	}
	if snap.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", snap.InputTokens)
	}
	if !snap.Interrupted {
		t.Error("expected Interrupted=true for in-progress snapshot")
	}
	if len(snap.ProposedWrites) != 1 || snap.ProposedWrites[0].Path != "a.go" {
		t.Errorf("ProposedWrites = %v", snap.ProposedWrites)
	}
}

// ─── ToolCalls round-trip ────────────────────────────────────────────────────

func TestFromModelResponse_ToolCalls_RoundTrip(t *testing.T) {
	tc := map[string]int{"read_file": 3, "bash": 1}
	orig := models.ModelResponse{
		ModelID:      "test-model",
		Text:         "done",
		InputTokens:  200,
		OutputTokens: 100,
		ToolCalls:    tc,
	}

	snap := FromModelResponse(orig)
	if snap.ToolCalls == nil {
		t.Fatal("ToolCalls should not be nil in snapshot")
	}
	if snap.ToolCalls["read_file"] != 3 || snap.ToolCalls["bash"] != 1 {
		t.Errorf("snapshot ToolCalls = %v, want %v", snap.ToolCalls, tc)
	}

	restored := snap.ToModelResponse()
	if restored.ToolCalls == nil {
		t.Fatal("ToolCalls should not be nil after round-trip")
	}
	if restored.ToolCalls["read_file"] != 3 || restored.ToolCalls["bash"] != 1 {
		t.Errorf("restored ToolCalls = %v, want %v", restored.ToolCalls, tc)
	}
}

// ─── Delete flag round-trip ──────────────────────────────────────────────────

func TestWriteSnapshot_Delete_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	responses := []models.ModelResponse{
		{
			ModelID: "model-a",
			Text:    "deleted a file",
			ProposedWrites: []tools.FileWrite{
				{Path: "kept.go", Content: "package kept"},
				{Path: "removed.go", Delete: true},
			},
			Interrupted: true,
		},
	}

	cp := Build("prompt", []string{"model-a"}, responses, false)
	if cp == nil {
		t.Fatal("Build returned nil")
	}
	if err := Save(path, *cp); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	writes := loaded.Responses[0].ProposedWrites
	if len(writes) != 2 {
		t.Fatalf("ProposedWrites len = %d, want 2", len(writes))
	}
	if writes[0].Delete {
		t.Error("writes[0] should not be Delete")
	}
	if !writes[1].Delete {
		t.Error("writes[1] should be Delete")
	}
	if writes[1].Path != "removed.go" {
		t.Errorf("writes[1].Path = %q, want removed.go", writes[1].Path)
	}

	// Round-trip through ToModelResponse.
	mr := loaded.Responses[0].ToModelResponse()
	if len(mr.ProposedWrites) != 2 {
		t.Fatalf("ToModelResponse ProposedWrites len = %d", len(mr.ProposedWrites))
	}
	if !mr.ProposedWrites[1].Delete {
		t.Error("ToModelResponse: writes[1].Delete should be true")
	}
}

func TestSnapshotFromPartial_ToolCalls(t *testing.T) {
	tc := map[string]int{"write_file": 2}
	ps := models.PartialSnapshot{
		Text:      "partial",
		ToolCalls: tc,
	}
	snap := SnapshotFromPartial("m", ps)
	if snap.ToolCalls == nil {
		t.Fatal("ToolCalls should not be nil")
	}
	if snap.ToolCalls["write_file"] != 2 {
		t.Errorf("ToolCalls = %v, want %v", snap.ToolCalls, tc)
	}
}

func TestToolCalls_SaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	tc := map[string]int{"read_file": 5, "bash": 2}
	responses := []models.ModelResponse{
		{
			ModelID:     "model-a",
			Text:        "done",
			ToolCalls:   tc,
			Interrupted: true,
		},
	}
	cp := Build("prompt", []string{"model-a"}, responses, false)
	if cp == nil {
		t.Fatal("Build returned nil")
	}
	if err := Save(path, *cp); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Responses[0].ToolCalls["read_file"] != 5 {
		t.Errorf("persisted ToolCalls read_file = %d, want 5", loaded.Responses[0].ToolCalls["read_file"])
	}
	if loaded.Responses[0].ToolCalls["bash"] != 2 {
		t.Errorf("persisted ToolCalls bash = %d, want 2", loaded.Responses[0].ToolCalls["bash"])
	}
}
