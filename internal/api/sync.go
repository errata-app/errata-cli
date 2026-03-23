package api

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SyncPath returns the path to the last-sync watermark file.
func SyncPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".errata/last_sync"
	}
	return filepath.Join(home, ".errata", "last_sync")
}

// LoadLastSync reads the last-sync timestamp from disk.
// Returns zero time if the file does not exist or is unreadable.
func LoadLastSync() time.Time {
	data, err := os.ReadFile(SyncPath())
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// SaveLastSync writes the sync timestamp to disk.
func SaveLastSync(t time.Time) error {
	p := SyncPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(t.Format(time.RFC3339)+"\n"), 0o600)
}
