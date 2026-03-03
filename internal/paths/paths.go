// Package paths provides a single source of truth for all data directory paths.
// All persistent file locations are derived from a single root directory.
package paths

import "path/filepath"

// Layout holds all data file paths derived from a single root directory.
type Layout struct {
	Root          string // e.g. "data"
	Preferences   string // e.g. "data/preferences.jsonl"
	PricingCache  string // e.g. "data/pricing_cache.json"
	PromptHistory string // e.g. "data/prompt_history.jsonl"
	ConfigStore   string // e.g. "data/configs.json"
	Outputs       string // e.g. "data/outputs"
	Sessions      string // e.g. "data/sessions"
	Checkpoint    string // e.g. "data/checkpoint.json"
}

// New creates a Layout with all paths derived from the given root directory.
func New(root string) Layout {
	return Layout{
		Root:          root,
		Preferences:   filepath.Join(root, "preferences.jsonl"),
		PricingCache:  filepath.Join(root, "pricing_cache.json"),
		PromptHistory: filepath.Join(root, "prompt_history.jsonl"),
		ConfigStore:   filepath.Join(root, "configs.json"),
		Outputs:       filepath.Join(root, "outputs"),
		Sessions:      filepath.Join(root, "sessions"),
		Checkpoint:    filepath.Join(root, "checkpoint.json"),
	}
}

// Default returns a Layout rooted at "data".
func Default() Layout {
	return New("data")
}
