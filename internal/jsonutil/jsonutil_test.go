package jsonutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sample struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestSaveJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := &sample{Name: "test", Value: 42}

	path, err := SaveJSON(dir, "data.json", original)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "data.json"), path)

	loaded, err := LoadJSON[sample](path)
	require.NoError(t, err)
	assert.Equal(t, original.Name, loaded.Name)
	assert.Equal(t, original.Value, loaded.Value)
}

func TestSaveJSON_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep")
	_, err := SaveJSON(dir, "out.json", &sample{Name: "x"})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "out.json"))
}

func TestSaveJSON_NoTmpLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveJSON(dir, "clean.json", &sample{})
	require.NoError(t, err)
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err))
}

func TestSaveJSON_MkdirError(t *testing.T) {
	_, err := SaveJSON("/dev/null/sub", "x.json", &sample{})
	assert.Error(t, err)
}

func TestLoadJSON_NotFound(t *testing.T) {
	_, err := LoadJSON[sample]("/no/such/file.json")
	assert.Error(t, err)
}

func TestLoadJSON_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))
	_, err := LoadJSON[sample](path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}
