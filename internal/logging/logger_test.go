package logging_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/logging"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/tools"
)

// stubAdapter is a minimal ModelAdapter for testing.
type stubAdapter struct {
	id       string
	events   []models.AgentEvent
	response models.ModelResponse
}

func (s *stubAdapter) ID() string { return s.id }
func (s *stubAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s *stubAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	for _, e := range s.events {
		onEvent(e)
	}
	return s.response, nil
}

// readEntry reads the single JSONL entry written to path.
func readEntry(t *testing.T, path string) logging.Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	line := strings.TrimRight(string(data), "\n")
	var entry logging.Entry
	require.NoError(t, json.Unmarshal([]byte(line), &entry))
	return entry
}

// ─── NewLogger ───────────────────────────────────────────────────────────────

func TestNewLogger_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	l, err := logging.NewLogger(path)
	require.NoError(t, err)
	require.NoError(t, l.Close())
	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "log file should exist after NewLogger")
}

func TestNewLogger_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "test.jsonl")
	l, err := logging.NewLogger(path)
	require.NoError(t, err)
	require.NoError(t, l.Close())
	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "NewLogger should create intermediate directories")
}

// ─── Wrap / WrapAll ──────────────────────────────────────────────────────────

func TestWrap_NilLogger_ReturnsOriginalAdapter(t *testing.T) {
	inner := &stubAdapter{id: "m"}
	result := logging.Wrap(inner, "session", nil)
	assert.Equal(t, inner, result, "Wrap with nil logger must return the original adapter")
}

func TestWrapAll_NilLogger_ReturnsOriginalSlice(t *testing.T) {
	a1 := &stubAdapter{id: "m1"}
	a2 := &stubAdapter{id: "m2"}
	adapters := []models.ModelAdapter{a1, a2}
	result := logging.WrapAll(adapters, "s", nil)
	assert.Equal(t, adapters, result)
}

// TestWrap_ForwardsEventsToOnEvent verifies that the logging wrapper does not
// suppress events — they must still reach the upstream onEvent callback.
func TestWrap_ForwardsEventsToOnEvent(t *testing.T) {
	l, err := logging.NewLogger(filepath.Join(t.TempDir(), "test.jsonl"))
	require.NoError(t, err)
	defer l.Close()

	inner := &stubAdapter{
		id:       "m",
		events:   []models.AgentEvent{{Type: models.EventReading, Data: "main.go"}},
		response: models.ModelResponse{ModelID: "m"},
	}
	wrapped := logging.Wrap(inner, "s", l)

	var received []models.AgentEvent
	_, err = wrapped.RunAgent(context.Background(), nil, "p", func(e models.AgentEvent) {
		received = append(received, e)
	})
	require.NoError(t, err)
	require.Len(t, received, 1)
	assert.Equal(t, models.EventReading, received[0].Type)
	assert.Equal(t, "main.go", received[0].Data)
}

// TestWrap_LogsEntryWithAllFields is the comprehensive field-presence test.
// It verifies that every field in Entry / ResponseRecord is correctly
// serialized and deserialized, catching any future unexported-field regressions
// similar to the modelPricing bug in pricing.go.
func TestWrap_LogsEntryWithAllFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	l, err := logging.NewLogger(path)
	require.NoError(t, err)

	inner := &stubAdapter{
		id: "test-model",
		events: []models.AgentEvent{
			{Type: models.EventReading, Data: "main.go"},
			{Type: models.EventWriting, Data: "out.go"},
		},
		response: models.ModelResponse{
			ModelID:      "test-model",
			Text:         "all done",
			InputTokens:  100,
			OutputTokens: 50,
			CostUSD:      0.0015,
			LatencyMS:    250,
			ProposedWrites: []tools.FileWrite{
				{Path: "out.go", Content: "package main"},
			},
		},
	}

	wrapped := logging.Wrap(inner, "test-session-id", l)
	resp, err := wrapped.RunAgent(context.Background(), nil, "fix the bug", func(models.AgentEvent) {})
	require.NoError(t, err)
	assert.Equal(t, "all done", resp.Text, "response must be passed through unchanged")
	require.NoError(t, l.Close())

	entry := readEntry(t, path)

	assert.Equal(t, "test-session-id", entry.SessionID)
	assert.NotEmpty(t, entry.RunID)
	assert.False(t, entry.TS.IsZero())
	assert.Equal(t, "test-model", entry.ModelID)
	assert.Equal(t, "fix the bug", entry.Prompt)

	require.Len(t, entry.Events, 2)
	assert.Equal(t, models.EventReading, entry.Events[0].Type)
	assert.Equal(t, "main.go", entry.Events[0].Data)
	assert.Equal(t, models.EventWriting, entry.Events[1].Type)
	assert.Equal(t, "out.go", entry.Events[1].Data)

	assert.Equal(t, "all done", entry.Response.Text)
	assert.Equal(t, int64(100), entry.Response.InputTokens)
	assert.Equal(t, int64(50), entry.Response.OutputTokens)
	assert.InDelta(t, 0.0015, entry.Response.CostUSD, 0.00001)
	assert.Equal(t, int64(250), entry.Response.LatencyMS)
	require.Len(t, entry.Response.ProposedFiles, 1)
	assert.Equal(t, "out.go", entry.Response.ProposedFiles[0])
	assert.Empty(t, entry.Response.Error)
}

// TestWrap_LogsReasoningTokens verifies that ReasoningTokens are included in the log entry.
func TestWrap_LogsReasoningTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	l, err := logging.NewLogger(path)
	require.NoError(t, err)

	inner := &stubAdapter{
		id: "o3",
		response: models.ModelResponse{
			ModelID:         "o3",
			Text:            "done",
			InputTokens:     529000,
			OutputTokens:    15000,
			ReasoningTokens: 14500,
			CostUSD:         1.18,
			LatencyMS:       5000,
		},
	}

	wrapped := logging.Wrap(inner, "s", l)
	_, err = wrapped.RunAgent(context.Background(), nil, "p", func(models.AgentEvent) {})
	require.NoError(t, err)
	require.NoError(t, l.Close())

	entry := readEntry(t, path)
	assert.Equal(t, int64(14500), entry.Response.ReasoningTokens)
}

// TestWrapAll_WithRealLogger_WrapsAllAdapters verifies that WrapAll with a non-nil
// logger returns a slice of the same length with wrapped adapters.
func TestWrapAll_WithRealLogger_WrapsAllAdapters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	l, err := logging.NewLogger(path)
	require.NoError(t, err)
	defer l.Close()

	a1 := &stubAdapter{id: "m1", response: models.ModelResponse{ModelID: "m1"}}
	a2 := &stubAdapter{id: "m2", response: models.ModelResponse{ModelID: "m2"}}
	wrapped := logging.WrapAll([]models.ModelAdapter{a1, a2}, "sess", l)

	require.Len(t, wrapped, 2)
	assert.Equal(t, "m1", wrapped[0].ID())
	assert.Equal(t, "m2", wrapped[1].ID())

	// Each wrapped adapter should log an entry when run.
	wrapped[0].RunAgent(context.Background(), nil, "p1", func(models.AgentEvent) {}) //nolint:errcheck
	wrapped[1].RunAgent(context.Background(), nil, "p2", func(models.AgentEvent) {}) //nolint:errcheck
	require.NoError(t, l.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, 2)
}

// TestLoggingAdapter_ID verifies that the ID() method delegates to the inner adapter.
func TestLoggingAdapter_ID(t *testing.T) {
	l, err := logging.NewLogger(filepath.Join(t.TempDir(), "test.jsonl"))
	require.NoError(t, err)
	defer l.Close()

	inner := &stubAdapter{id: "my-model"}
	wrapped := logging.Wrap(inner, "s", l)
	assert.Equal(t, "my-model", wrapped.ID())
}

// TestNewLogger_OpenFileError verifies that NewLogger returns an error when the
// path is a directory (can't be opened as a file).
func TestNewLogger_OpenFileError(t *testing.T) {
	dir := t.TempDir()
	// Try to open a directory as a JSONL file → OpenFile should fail.
	_, err := logging.NewLogger(dir)
	assert.Error(t, err)
}

// TestNewLogger_MkdirAllError verifies that NewLogger returns an error when
// the parent directory cannot be created.
func TestNewLogger_MkdirAllError(t *testing.T) {
	// /dev/null is a file, not a directory — MkdirAll will fail trying to create a child.
	_, err := logging.NewLogger("/dev/null/sub/test.jsonl")
	assert.Error(t, err)
}

// TestLoggingAdapter_Capabilities verifies that the Capabilities method
// delegates to the inner adapter.
func TestLoggingAdapter_Capabilities(t *testing.T) {
	l, err := logging.NewLogger(filepath.Join(t.TempDir(), "test.jsonl"))
	require.NoError(t, err)
	defer l.Close()

	inner := &stubAdapter{id: "m"}
	wrapped := logging.Wrap(inner, "s", l)
	caps := wrapped.Capabilities(context.Background())
	assert.Equal(t, models.ModelCapabilities{}, caps)
}

// TestRunID_Format verifies that logged RunIDs use the run_ UUID v7 format.
func TestRunID_Format(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	l, err := logging.NewLogger(path)
	require.NoError(t, err)

	inner := &stubAdapter{id: "m", response: models.ModelResponse{ModelID: "m"}}
	wrapped := logging.Wrap(inner, "s", l)
	_, err = wrapped.RunAgent(context.Background(), nil, "p", func(models.AgentEvent) {})
	require.NoError(t, err)
	require.NoError(t, l.Close())

	entry := readEntry(t, path)
	assert.Regexp(t, `^run_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, entry.RunID)
}

// TestWrap_AppendsTwoEntries verifies that successive runs append new JSONL
// lines and do not truncate or overwrite previous entries.
func TestWrap_AppendsTwoEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	l, err := logging.NewLogger(path)
	require.NoError(t, err)

	a1 := &stubAdapter{id: "m1", response: models.ModelResponse{ModelID: "m1"}}
	a2 := &stubAdapter{id: "m2", response: models.ModelResponse{ModelID: "m2"}}
	logging.Wrap(a1, "s", l).RunAgent(context.Background(), nil, "prompt-one", func(models.AgentEvent) {}) //nolint:errcheck
	logging.Wrap(a2, "s", l).RunAgent(context.Background(), nil, "prompt-two", func(models.AgentEvent) {}) //nolint:errcheck
	require.NoError(t, l.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 2, "both entries should be appended as separate JSONL lines")

	var e1, e2 logging.Entry
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &e1))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &e2))
	assert.Equal(t, "prompt-one", e1.Prompt)
	assert.Equal(t, "prompt-two", e2.Prompt)
}
