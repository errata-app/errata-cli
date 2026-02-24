package prompthistory_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/suarezc/errata/internal/prompthistory"
)

func TestLoad_MissingFile(t *testing.T) {
	prompts, err := prompthistory.Load(filepath.Join(t.TempDir(), "nonexistent.jsonl"))
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if prompts != nil {
		t.Fatalf("expected nil slice, got %v", prompts)
	}
}

func TestAppendAndLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")

	entries := []string{"first prompt", "second prompt", "third prompt"}
	for _, e := range entries {
		if err := prompthistory.Append(path, e); err != nil {
			t.Fatalf("Append(%q) error: %v", e, err)
		}
	}

	got, err := prompthistory.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("expected %d entries, got %d: %v", len(entries), len(got), got)
	}

	// Load returns newest-first, so reversed order.
	for i, want := range []string{"third prompt", "second prompt", "first prompt"} {
		if got[i] != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestLoad_CorruptLineSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")

	content := "\"good first\"\nnot valid json\n\"good second\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := prompthistory.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 valid entries, got %d: %v", len(got), got)
	}
	// newest-first: "good second" then "good first"
	if got[0] != "good second" || got[1] != "good first" {
		t.Errorf("unexpected order: %v", got)
	}
}

func TestLoad_NewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")

	for _, p := range []string{"a", "b", "c"} {
		if err := prompthistory.Append(path, p); err != nil {
			t.Fatal(err)
		}
	}

	got, err := prompthistory.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "c" || got[1] != "b" || got[2] != "a" {
		t.Errorf("expected [c b a], got %v", got)
	}
}
