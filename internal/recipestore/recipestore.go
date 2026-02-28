// Package recipestore provides a content-addressed store for recipe/configuration
// snapshots. Each unique configuration is stored once, keyed by its SHA-256 hash.
// Preference entries emit only the hash, keeping the JSONL lean while enabling
// full config lookup.
package recipestore

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RecipeSnapshot captures the active recipe/configuration at the time of a
// preference recording. It mirrors the fields relevant for comparing
// experimental setups.
type RecipeSnapshot struct {
	Name         string             `json:"name"`
	SystemPrompt string             `json:"system_prompt,omitempty"`
	Tools        []string           `json:"tools,omitempty"`
	Constraints  *ConstraintsConfig `json:"constraints,omitempty"`
	ModelParams  *ModelParamsConfig `json:"model_params,omitempty"`
}

// ConstraintsConfig captures constraint settings relevant to preference comparison.
type ConstraintsConfig struct {
	MaxSteps int    `json:"max_steps,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
}

// ModelParamsConfig captures sampling parameters relevant to preference comparison.
type ModelParamsConfig struct {
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
}

// Hash returns the content-addressed key for a RecipeSnapshot.
// The hash is SHA-256 of the canonical JSON representation, prefixed with "sha256:".
// Name is included because it is part of the recipe's identity from the user's
// perspective — distinct recipes should produce distinct hashes even if their
// settings happen to overlap.
func Hash(cfg *RecipeSnapshot) string {
	data, _ := json.Marshal(cfg)
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", h)
}

// Store is a content-addressed store for RecipeSnapshot values.
// It is safe for concurrent use.
type Store struct {
	path    string
	configs map[string]*RecipeSnapshot // hash → snapshot
	mu      sync.Mutex
}

// New creates a Store backed by the given file path.
// If the file does not exist, the store starts empty.
func New(path string) *Store {
	s := &Store{
		path:    path,
		configs: make(map[string]*RecipeSnapshot),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s // missing file → empty store
	}
	var m map[string]*RecipeSnapshot
	if err := json.Unmarshal(data, &m); err != nil {
		return s // corrupt file → empty store
	}
	s.configs = m
	return s
}

// Put stores a RecipeSnapshot and returns its hash.
// If the snapshot already exists (same hash), it is not re-written to disk.
func (s *Store) Put(cfg *RecipeSnapshot) string {
	h := Hash(cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.configs[h]; exists {
		return h
	}
	s.configs[h] = cfg
	s.save()
	return h
}

// Get retrieves a RecipeSnapshot by its hash. Returns nil if not found.
func (s *Store) Get(hash string) *RecipeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configs[hash]
}

// List returns a copy of all stored snapshots keyed by hash.
func (s *Store) List() map[string]*RecipeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*RecipeSnapshot, len(s.configs))
	for k, v := range s.configs {
		out[k] = v
	}
	return out
}

// HashesForName returns all hashes whose snapshot has the given recipe name.
func (s *Store) HashesForName(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var hashes []string
	for h, cfg := range s.configs {
		if cfg.Name == name {
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
	data, err := json.MarshalIndent(s.configs, "", "  ")
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
