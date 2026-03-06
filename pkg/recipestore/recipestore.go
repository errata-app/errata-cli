// Package recipestore provides a content-addressed store for recipe/configuration
// snapshots. Each unique configuration is stored once, keyed by its SHA-256 hash.
// Preference entries emit only the hash, keeping the JSONL lean while enabling
// full config lookup.
package recipestore

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// RecipeSnapshot captures the active recipe/configuration at the time of a
// preference recording. It mirrors the fields relevant for comparing
// experimental setups.
//
// Name and Version are stored for display and diagnostics but excluded from
// the content-addressed hash — they are metadata about the recipe, not
// settings that affect model behavior.
type RecipeSnapshot struct {
	Version             int                           `json:"version,omitempty"`
	Name                string                        `json:"name"`
	SystemPrompt        string                        `json:"system_prompt,omitempty"`
	ToolGuidance        map[string]string             `json:"tool_guidance,omitempty"`
	Tools               []string                      `json:"tools,omitempty"`
	BashPrefixes        []string                      `json:"bash_prefixes,omitempty"`
	ToolDescriptions    map[string]string             `json:"tool_descriptions,omitempty"`
	Constraints         *ConstraintsConfig            `json:"constraints,omitempty"`
	ModelParams         *ModelParamsConfig             `json:"model_params,omitempty"`
	Context             *ContextConfig                `json:"context,omitempty"`
	SystemReminders     []SystemReminderConfig        `json:"system_reminders,omitempty"`
	OutputProcessing    map[string]OutputRuleConfig   `json:"output_processing,omitempty"`
	SummarizationPrompt string                        `json:"summarization_prompt,omitempty"`
	ModelProfiles       map[string]ModelProfileConfig `json:"model_profiles,omitempty"`
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

// ContextConfig captures conversation history management settings.
type ContextConfig struct {
	MaxHistoryTurns  int     `json:"max_history_turns,omitempty"`
	Strategy         string  `json:"strategy,omitempty"`
	CompactThreshold float64 `json:"compact_threshold,omitempty"`
	TaskMode         string  `json:"task_mode,omitempty"`
}

// SystemReminderConfig captures a conditional mid-conversation injection.
type SystemReminderConfig struct {
	Name    string `json:"name"`
	Trigger string `json:"trigger,omitempty"`
	Content string `json:"content,omitempty"`
}

// OutputRuleConfig captures deterministic output processing for a tool.
type OutputRuleConfig struct {
	MaxLines          int    `json:"max_lines,omitempty"`
	MaxTokens         int    `json:"max_tokens,omitempty"`
	Truncation        string `json:"truncation,omitempty"`
	TruncationMessage string `json:"truncation_message,omitempty"`
}

// ModelProfileConfig captures capability overrides for a model.
type ModelProfileConfig struct {
	ContextBudget  int    `json:"context_budget,omitempty"`
	ToolFormat     string `json:"tool_format,omitempty"`
	SystemRole     *bool  `json:"system_role,omitempty"`
	MidConvoSystem *bool  `json:"mid_convo_system,omitempty"`
}

// Hash returns the content-addressed key for a RecipeSnapshot.
// The hash is SHA-256 of the canonical JSON representation, prefixed with "sha256:".
//
// Name and Version are excluded — they are metadata about the recipe format,
// not settings that affect model behavior. Two recipes with identical behavioral
// fields produce the same hash regardless of name or schema version.
func Hash(cfg *RecipeSnapshot) string {
	hashable := *cfg
	hashable.Name = ""
	hashable.Version = 0
	data, _ := json.Marshal(hashable)
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
	maps.Copy(out, s.configs)
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
