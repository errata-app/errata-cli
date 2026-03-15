// Package paths provides a single source of truth for all data directory paths.
// All persistent file locations are derived from a single root directory.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// RecipesDir returns the path to the shared recipes directory (~/.errata/recipes/).
func RecipesDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ".errata/recipes"
	}
	return filepath.Join(home, ".errata", "recipes")
}

// NextAvailable returns dir/name if it doesn't exist, otherwise tries
// name with incrementing numeric suffixes: slug1.md, slug2.md, etc.
func NextAvailable(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 100; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(dir, name) // fallback
}
