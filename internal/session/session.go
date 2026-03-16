// Package session manages ephemeral session lifecycle: IDs, per-session
// directory paths, metadata persistence, and feed serialization for replay.
package session

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/errata-app/errata-cli/internal/uid"
)

// GenerateID returns a type-prefixed UUID v7 session ID (e.g. "ses_019505e2-...").
func GenerateID() string {
	return uid.New("ses_")
}

// Paths holds the per-session file paths.
type Paths struct {
	Dir            string
	MetadataPath   string
	ContentPath    string
	CheckpointPath string
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
		MetadataPath:   filepath.Join(dir, "session_metadata.json"),
		ContentPath:    filepath.Join(dir, "session_content.json"),
		CheckpointPath: filepath.Join(dir, "checkpoint.json"),
		RecipePath:     filepath.Join(dir, "recipe.md"),
	}
}

// Meta is an alias for SessionMetadata used by session listing.
type Meta = SessionMetadata

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
		metaPath := filepath.Join(baseDir, e.Name(), "session_metadata.json")
		m, err := LoadMetadata(metaPath)
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

