package tools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/tools"
)

func TestExecuteRead_FileFound(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello world"), 0o644))

	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("hello.txt")
	assert.Equal(t, "hello world", got)
}

func TestExecuteRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("nonexistent.txt")
	assert.Contains(t, got, "[error:")
}

func TestExecuteRead_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("../../etc/passwd")
	assert.Contains(t, got, "[error:")
	assert.Contains(t, got, "outside the working directory")
}

func TestExecuteRead_Nested(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	file := filepath.Join(sub, "data.txt")
	require.NoError(t, os.WriteFile(file, []byte("nested"), 0o644))

	require.NoError(t, os.Chdir(dir))
	got := tools.ExecuteRead("sub/data.txt")
	assert.Equal(t, "nested", got)
}

func TestApplyWrites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	writes := []tools.FileWrite{
		{Path: "out.txt", Content: "content A"},
		{Path: "sub/out.txt", Content: "content B"},
	}
	require.NoError(t, tools.ApplyWrites(writes))

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content A", string(got))

	got2, err := os.ReadFile(filepath.Join(dir, "sub", "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content B", string(got2))
}
