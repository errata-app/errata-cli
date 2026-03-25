package api

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PrivacyMode controls what data is included in preference uploads.
type PrivacyMode string

const (
	PrivacyMetadata PrivacyMode = "metadata"
	PrivacyFull     PrivacyMode = "full"
)

// PrivacySettings holds the user's upload privacy preferences.
type PrivacySettings struct {
	Mode PrivacyMode `json:"mode"`
}

// PrivacyPath returns the path to the privacy settings file.
func PrivacyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".errata/privacy.json"
	}
	return filepath.Join(home, ".errata", "privacy.json")
}

// LoadPrivacy reads privacy settings from disk.
// Returns default (metadata) on missing or corrupt file.
func LoadPrivacy() PrivacySettings {
	data, err := os.ReadFile(PrivacyPath())
	if err != nil {
		return PrivacySettings{Mode: PrivacyMetadata}
	}
	var s PrivacySettings
	if err := json.Unmarshal(data, &s); err != nil {
		return PrivacySettings{Mode: PrivacyMetadata}
	}
	if s.Mode != PrivacyMetadata && s.Mode != PrivacyFull {
		return PrivacySettings{Mode: PrivacyMetadata}
	}
	return s
}

// SavePrivacy writes privacy settings to disk atomically.
func SavePrivacy(s PrivacySettings) error {
	p := PrivacyPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
