// Package recipestore provides a content-addressed store for recipe markdown.
// Each unique recipe is stored once, keyed by its SHA-256 hash.
// Preference entries emit only the hash, keeping the JSONL lean while enabling
// full recipe lookup.
package recipestore

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/errata-app/errata-cli/pkg/recipe"
)

// Store is a content-addressed store for recipe markdown strings.
// It is safe for concurrent use.
type Store struct {
	path    string
	recipes map[string]string // hash → markdown
	mu      sync.Mutex
}

// New creates a Store backed by the given file path.
// If the file does not exist, the store starts empty.
func New(path string) *Store {
	s := &Store{
		path:    path,
		recipes: make(map[string]string),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s // missing file → empty store
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return s // corrupt file → empty store
	}
	s.recipes = m
	return s
}

// Put stores recipe markdown and returns its config hash.
// The hash excludes the Models section so that the same recipe configuration
// run with different model sets produces the same hash.
// If a recipe with the same hash already exists, the stored markdown is kept.
func (s *Store) Put(markdown string) string {
	rec, err := recipe.ParseContent([]byte(markdown))
	if err != nil {
		// Fallback for unparseable markdown: hash raw content.
		h := sha256.Sum256([]byte(markdown))
		hash := fmt.Sprintf("rcp_%x", h)
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, exists := s.recipes[hash]; !exists {
			s.recipes[hash] = markdown
			s.save()
		}
		return hash
	}
	hash := rec.ConfigHash()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.recipes[hash]; exists {
		return hash
	}
	s.recipes[hash] = markdown
	s.save()
	return hash
}

// Get retrieves recipe markdown by its hash. Returns "" if not found.
func (s *Store) Get(hash string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recipes[hash]
}

// List returns a copy of all stored recipes keyed by hash.
func (s *Store) List() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.recipes))
	maps.Copy(out, s.recipes)
	return out
}

// HashesForName returns all hashes whose recipe markdown parses to the given name.
func (s *Store) HashesForName(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var hashes []string
	for h, md := range s.recipes {
		rec, err := recipe.ParseContent([]byte(md))
		if err != nil {
			continue
		}
		parsedName := rec.Name
		if parsedName == "" {
			parsedName = "default"
		}
		if parsedName == name {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

// save writes the store to disk atomically (temp file + rename).
// Caller must hold s.mu.
func (s *Store) save() {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return
	}
	data, err := json.MarshalIndent(s.recipes, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = os.Rename(tmp, s.path)
}
