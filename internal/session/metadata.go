package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// SessionMetadata is the lightweight per-session file (session_metadata.json).
// It holds stats, summaries, and run-level analytics — everything needed to
// list sessions, compute /stats, and decide what to share/upload.
type SessionMetadata struct {
	ID           string       `json:"id"`
	CreatedAt    time.Time    `json:"created_at"`
	LastActiveAt time.Time    `json:"last_active_at"`
	Models       []string     `json:"models,omitempty"`
	FirstPrompt  string       `json:"first_prompt,omitempty"`
	LastPrompt   string       `json:"last_prompt,omitempty"`
	PromptCount  int          `json:"prompt_count"`
	ConfigHash   string       `json:"config_hash,omitempty"`
	Runs         []RunSummary `json:"runs"`
}

// RunSummary captures per-run metadata without full response text.
type RunSummary struct {
	Timestamp           time.Time                  `json:"timestamp"`
	PromptHash          string                     `json:"prompt_hash"`
	PromptPreview       string                     `json:"prompt_preview"`
	Models              []string                   `json:"models"`
	Selected            string                     `json:"selected,omitempty"`
	Rating              string                     `json:"rating,omitempty"`
	Type                string                     `json:"type,omitempty"` // "" | "rewind"
	LatenciesMS         map[string]int64           `json:"latencies_ms,omitempty"`
	CostsUSD            map[string]float64         `json:"costs_usd,omitempty"`
	InputTokens         map[string]int64           `json:"input_tokens,omitempty"`
	OutputTokens        map[string]int64           `json:"output_tokens,omitempty"`
	ToolCalls           map[string]map[string]int  `json:"tool_calls,omitempty"`
	ProposedWritesCount map[string]int             `json:"proposed_writes_count,omitempty"`
	AppliedFiles        []string                   `json:"applied_files,omitempty"`
	ConfigHash          string                     `json:"config_hash,omitempty"`
	Note                string                     `json:"note,omitempty"`
}

// SaveMetadata atomically writes session metadata to path.
func SaveMetadata(path string, m SessionMetadata) error {
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

// LoadMetadata reads session metadata from path.
// Returns (nil, nil) if the file does not exist.
func LoadMetadata(path string) (*SessionMetadata, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil //nolint:nilnil // intentional: missing file is not an error
	}
	if err != nil {
		return nil, err
	}
	var m SessionMetadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
