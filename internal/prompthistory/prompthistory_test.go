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

func TestLoad_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := prompthistory.Load(path)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestLoad_SinglePrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := prompthistory.Append(path, "only one"); err != nil {
		t.Fatal(err)
	}
	got, err := prompthistory.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "only one" {
		t.Errorf("expected [only one], got %v", got)
	}
}

func TestAppend_SpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	special := []string{
		"prompt with \"quotes\"",
		"prompt with\nnewline",
		"prompt with\ttab",
		"unicode: 日本語 🎉",
	}
	for _, s := range special {
		if err := prompthistory.Append(path, s); err != nil {
			t.Fatalf("Append(%q) error: %v", s, err)
		}
	}
	got, err := prompthistory.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(special) {
		t.Fatalf("expected %d entries, got %d", len(special), len(got))
	}
	// Newest-first: reverse of input order.
	for i, want := range special {
		if got[len(got)-1-i] != want {
			t.Errorf("got[%d] = %q, want %q", len(got)-1-i, got[len(got)-1-i], want)
		}
	}
}

func TestLoad_ReadError(t *testing.T) {
	// A directory cannot be read as a file.
	dir := t.TempDir()
	_, err := prompthistory.Load(dir)
	if err == nil {
		t.Fatal("expected error reading a directory as a file")
	}
}

func TestAppend_MkdirError(t *testing.T) {
	// /dev/null is not a directory — MkdirAll should fail.
	err := prompthistory.Append("/dev/null/sub/file.jsonl", "test")
	if err == nil {
		t.Fatal("expected error for invalid parent directory")
	}
}

func TestAppend_CreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "history.jsonl")
	if err := prompthistory.Append(path, "hello"); err != nil {
		t.Fatalf("Append through nested dirs failed: %v", err)
	}
	got, err := prompthistory.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("expected [hello], got %v", got)
	}
}
