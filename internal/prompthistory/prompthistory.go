// Package prompthistory persists the user's submitted prompts so they can be
// recalled across sessions (Up-arrow cycling, Ctrl-R search).
//
// Storage format: append-only JSONL at the configured path. Each line is a
// JSON-encoded string (the raw prompt text). Lines are written oldest-first on
// disk; Load returns them newest-first for convenient index-0 == most-recent
// navigation.
package prompthistory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Load reads all prompts from path and returns them newest-first.
// Returns nil (not an error) if the file does not exist.
// Corrupt lines are skipped silently so a bad entry never blocks the whole file.
func Load(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var prompts []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var s string
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue // skip corrupt line
		}
		prompts = append(prompts, s)
	}

	// Reverse so index 0 == most recent (newest-first).
	for i, j := 0, len(prompts)-1; i < j; i, j = i+1, j-1 {
		prompts[i], prompts[j] = prompts[j], prompts[i]
	}
	return prompts, nil
}

// Append adds a single prompt to the end of the file (oldest-first on disk).
func Append(path string, prompt string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(prompt)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", b)
	return err
}
