package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPrivacy_Default(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := LoadPrivacy()
	assert.Equal(t, PrivacyMetadata, s.Mode)
}

func TestLoadPrivacy_CorruptFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".errata")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "privacy.json"), []byte("{invalid json"), 0o600))

	s := LoadPrivacy()
	assert.Equal(t, PrivacyMetadata, s.Mode)
}

func TestSaveLoadPrivacy_RoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	require.NoError(t, SavePrivacy(PrivacySettings{Mode: PrivacyFull}))
	s := LoadPrivacy()
	assert.Equal(t, PrivacyFull, s.Mode)

	require.NoError(t, SavePrivacy(PrivacySettings{Mode: PrivacyMetadata}))
	s = LoadPrivacy()
	assert.Equal(t, PrivacyMetadata, s.Mode)
}

func TestSavePrivacy_Permissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	require.NoError(t, SavePrivacy(PrivacySettings{Mode: PrivacyFull}))

	info, err := os.Stat(filepath.Join(home, ".errata", "privacy.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadPrivacy_InvalidMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".errata")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "privacy.json"), []byte(`{"mode":"bogus"}`), 0o600))

	s := LoadPrivacy()
	assert.Equal(t, PrivacyMetadata, s.Mode)
}
