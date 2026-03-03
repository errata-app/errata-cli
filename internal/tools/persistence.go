package tools

import (
	"encoding/json"
	"os"
	"sort"
)

// LoadDisabledTools reads the disabled-tool set from path.
// Returns an empty map and nil error if path is empty or the file does not exist (all tools enabled).
func LoadDisabledTools(path string) (map[string]bool, error) {
	if path == "" {
		return map[string]bool{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var payload struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if len(payload.Disabled) == 0 {
		return map[string]bool{}, nil
	}
	m := make(map[string]bool, len(payload.Disabled))
	for _, name := range payload.Disabled {
		m[name] = true
	}
	return m, nil
}

// SaveDisabledTools persists the disabled-tool set to path.
// If path is empty, disabled is nil, or disabled is empty, any existing file is removed.
func SaveDisabledTools(path string, disabled map[string]bool) error {
	if path == "" {
		return nil
	}
	if len(disabled) == 0 {
		_ = os.Remove(path)
		return nil
	}
	names := make([]string, 0, len(disabled))
	for name := range disabled {
		names = append(names, name)
	}
	sort.Strings(names)
	type payload struct {
		Disabled []string `json:"disabled"`
	}
	data, err := json.Marshal(payload{Disabled: names})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
