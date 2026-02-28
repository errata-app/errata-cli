// Package session manages ephemeral session lifecycle: IDs, per-session
// directory paths, metadata persistence, and feed serialization for replay.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GenerateID returns a random 16-character hex session ID (8 bytes).
func GenerateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("session: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// Paths holds the per-session file paths.
type Paths struct {
	Dir            string
	HistoryPath    string
	CheckpointPath string
	MetaPath       string
	FeedPath       string
	RecipePath     string
}

// New creates a fresh session directory and returns the ID and paths.
func New(baseDir string) (string, Paths) {
	id := GenerateID()
	return id, PathsFor(baseDir, id)
}

// PathsFor returns paths for an existing session ID.
func PathsFor(baseDir, id string) Paths {
	dir := filepath.Join(baseDir, id)
	return Paths{
		Dir:            dir,
		HistoryPath:    filepath.Join(dir, "history.json"),
		CheckpointPath: filepath.Join(dir, "checkpoint.json"),
		MetaPath:       filepath.Join(dir, "meta.json"),
		FeedPath:       filepath.Join(dir, "feed.json"),
		RecipePath:     filepath.Join(dir, "recipe.md"),
	}
}

// Meta holds per-session metadata for listing.
type Meta struct {
	ID           string    `json:"id"`
	FirstPrompt  string    `json:"first_prompt,omitempty"`
	LastPrompt   string    `json:"last_prompt,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	PromptCount  int       `json:"prompt_count"`
	Models       []string  `json:"models,omitempty"`
}

// SaveMeta atomically writes session metadata to path.
func SaveMeta(path string, m Meta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadMeta reads session metadata from path.
// Returns (nil, nil) if the file does not exist.
func LoadMeta(path string) (*Meta, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil //nolint:nilnil // intentional: missing file is not an error
	}
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// LatestID returns the ID of the most recently active session in baseDir.
func LatestID(baseDir string) (string, error) {
	metas, err := List(baseDir)
	if err != nil {
		return "", err
	}
	if len(metas) == 0 {
		return "", fmt.Errorf("no sessions found")
	}
	return metas[0].ID, nil
}

// List returns all sessions sorted by LastActiveAt descending (newest first).
// Corrupt or unreadable session directories are skipped with a log warning.
func List(baseDir string) ([]Meta, error) {
	entries, err := os.ReadDir(baseDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var metas []Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(baseDir, e.Name(), "meta.json")
		m, err := LoadMeta(metaPath)
		if err != nil {
			log.Printf("session: skipping %q: %v", e.Name(), err)
			continue
		}
		if m == nil {
			continue
		}
		metas = append(metas, *m)
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].LastActiveAt.After(metas[j].LastActiveAt)
	})
	return metas, nil
}

// Resolve resolves a session ID by exact match or unique prefix.
// Returns an error if the prefix is ambiguous or no match is found.
func Resolve(baseDir, prefix string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("no sessions found")
	}
	if err != nil {
		return "", err
	}
	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == prefix {
			return name, nil // exact match
		}
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matching %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous prefix %q matches %d sessions: %s",
			prefix, len(matches), strings.Join(matches, ", "))
	}
}

// FeedEntry is a serializable record of one feed item for replay.
type FeedEntry struct {
	Kind   string       `json:"kind"`             // "message" | "run"
	Text   string       `json:"text,omitempty"`   // for messages
	Prompt string       `json:"prompt,omitempty"` // for runs
	Models []ModelEntry `json:"models,omitempty"` // per-model results
	Note   string       `json:"note,omitempty"`   // "Applied: foo.go" / "Skipped."
}

// ModelEntry is a per-model summary within a FeedEntry.
type ModelEntry struct {
	ID            string   `json:"id"`
	Text          string   `json:"text"`
	ProposedFiles []string `json:"proposed_files,omitempty"`
}

// SaveFeed atomically writes feed entries to path.
func SaveFeed(path string, entries []FeedEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadFeed reads feed entries from path.
// Returns (nil, nil) if the file does not exist.
func LoadFeed(path string) ([]FeedEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []FeedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}
