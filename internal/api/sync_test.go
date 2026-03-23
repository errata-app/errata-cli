package api

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadLastSync_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	now := time.Now().Truncate(time.Second)
	require.NoError(t, SaveLastSync(now))

	loaded := LoadLastSync()
	assert.True(t, loaded.Equal(now))
}

func TestLoadLastSync_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	loaded := LoadLastSync()
	assert.True(t, loaded.IsZero())
}

func TestLoadLastSync_CorruptFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	p := SyncPath()
	require.NoError(t, os.MkdirAll(tmp+"/.errata", 0o750))
	require.NoError(t, os.WriteFile(p, []byte("not a timestamp\n"), 0o600))

	loaded := LoadLastSync()
	assert.True(t, loaded.IsZero())
}
