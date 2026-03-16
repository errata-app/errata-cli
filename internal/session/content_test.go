package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
)

func TestSaveContent_LoadContent_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_content.json")

	c := SessionContent{
		Runs: []RunContent{
			{
				Prompt: "fix the bug",
				Models: []ModelRunContent{
					{
						ModelID:    "claude-sonnet-4-6",
						Text:       "I fixed it by changing line 42.",
						StopReason: "complete",
						Steps:      3,
						ProposedWrites: []output.WriteEntry{
							{Path: "main.go", Content: "package main"},
						},
						Events: []output.EventEntry{
							{Type: models.EventReading, Data: "main.go"},
							{Type: models.EventText, Data: "I fixed it"},
						},
						ReasoningTokens: 50,
					},
					{
						ModelID:    "gpt-4o",
						Text:       "Done.",
						StopReason: "complete",
						Steps:      1,
						Events:     []output.EventEntry{},
					},
				},
			},
		},
		Histories: map[string][]models.ConversationTurn{
			"claude-sonnet-4-6": {
				{Role: "user", Content: "fix the bug"},
				{Role: "assistant", Content: "I fixed it."},
			},
			"gpt-4o": {
				{Role: "user", Content: "fix the bug"},
				{Role: "assistant", Content: "Done."},
			},
		},
	}

	require.NoError(t, SaveContent(path, c))

	loaded, err := LoadContent(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	require.Len(t, loaded.Runs, 1)
	run := loaded.Runs[0]
	assert.Equal(t, "fix the bug", run.Prompt)
	require.Len(t, run.Models, 2)

	m0 := run.Models[0]
	assert.Equal(t, "claude-sonnet-4-6", m0.ModelID)
	assert.Equal(t, "I fixed it by changing line 42.", m0.Text)
	assert.Equal(t, "complete", m0.StopReason)
	assert.Equal(t, 3, m0.Steps)
	assert.Equal(t, int64(50), m0.ReasoningTokens)
	require.Len(t, m0.ProposedWrites, 1)
	assert.Equal(t, "main.go", m0.ProposedWrites[0].Path)
	require.Len(t, m0.Events, 2)
	assert.Equal(t, models.EventReading, m0.Events[0].Type)

	m1 := run.Models[1]
	assert.Equal(t, "gpt-4o", m1.ModelID)
	assert.Empty(t, m1.ProposedWrites)

	require.Len(t, loaded.Histories, 2)
	assert.Len(t, loaded.Histories["claude-sonnet-4-6"], 2)
	assert.Equal(t, "user", loaded.Histories["claude-sonnet-4-6"][0].Role)
}

func TestLoadContent_MissingFile(t *testing.T) {
	c, err := LoadContent("/nonexistent/path/session_content.json")
	require.NoError(t, err)
	assert.Nil(t, c)
}

func TestLoadContent_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_content.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid json"), 0o600))

	c, err := LoadContent(path)
	require.Error(t, err)
	assert.Nil(t, c)
}

func TestSaveContent_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "session_content.json")

	c := SessionContent{Runs: []RunContent{{Prompt: "test"}}}
	require.NoError(t, SaveContent(path, c))

	loaded, err := LoadContent(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Runs, 1)
	assert.Equal(t, "test", loaded.Runs[0].Prompt)
}

func TestSaveContent_EmptyRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_content.json")

	c := SessionContent{}
	require.NoError(t, SaveContent(path, c))

	loaded, err := LoadContent(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Empty(t, loaded.Runs)
}

func TestSaveContent_NilHistories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_content.json")

	c := SessionContent{Runs: []RunContent{{Prompt: "hello"}}}
	require.NoError(t, SaveContent(path, c))

	loaded, err := LoadContent(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Nil(t, loaded.Histories)
}

func TestSaveContent_WriteEntryDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session_content.json")

	c := SessionContent{
		Runs: []RunContent{
			{
				Prompt: "delete file",
				Models: []ModelRunContent{
					{
						ModelID: "m1",
						ProposedWrites: []output.WriteEntry{
							{Path: "old.go", Delete: true},
						},
						Events: []output.EventEntry{},
					},
				},
			},
		},
	}

	require.NoError(t, SaveContent(path, c))

	loaded, err := LoadContent(path)
	require.NoError(t, err)
	require.Len(t, loaded.Runs[0].Models[0].ProposedWrites, 1)
	assert.True(t, loaded.Runs[0].Models[0].ProposedWrites[0].Delete)
}
