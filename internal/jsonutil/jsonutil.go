// Package jsonutil provides generic helpers for atomic JSON file I/O.
package jsonutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveJSON writes v as pretty-printed JSON to dir/filename using an
// atomic write (temp file + rename). Parent directories are created
// as needed. Returns the full path.
func SaveJSON(dir, filename string, v any) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	path := filepath.Join(dir, filename)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}
	return path, nil
}

// LoadJSON reads a JSON file at path and unmarshals it into *T.
func LoadJSON[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &v, nil
}
