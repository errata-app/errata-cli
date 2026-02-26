// Package history provides atomic load/save/clear for per-model conversation histories.
package history

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/suarezc/errata/internal/models"
)

// Load reads the history file at path and returns the stored conversation turns.
// Returns (nil, nil) if the file does not exist.
// Returns (nil, err) and logs a warning if the file exists but cannot be decoded.
func Load(path string) (map[string][]models.ConversationTurn, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil //nolint:nilnil // intentional: missing file is not an error
	}
	if err != nil {
		return nil, err
	}
	var h map[string][]models.ConversationTurn
	if err := json.Unmarshal(data, &h); err != nil {
		log.Printf("history: corrupt file %q, starting fresh: %v", path, err)
		return nil, err
	}
	return h, nil
}

// Save atomically writes h to path. The parent directory is created if needed.
// A zero-length or nil map is a no-op.
func Save(path string, h map[string][]models.ConversationTurn) error {
	if len(h) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clear deletes the history file at path.
// A missing file is not an error.
func Clear(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
