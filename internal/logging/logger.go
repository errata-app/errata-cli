// Package logging provides optional per-run logging for all model adapter calls.
//
// Usage:
//
//	logger, err := logging.NewLogger("data/runs.jsonl")
//	if err != nil { ... }
//	defer logger.Close()
//
//	adapters = logging.WrapAll(adapters, sessionID, logger)
//
// Pass a nil *Logger to WrapAll to disable logging with no overhead.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/suarezc/errata/internal/models"
)

// ─── Log entry types ─────────────────────────────────────────────────────────

// Entry is one complete agent-run record. One entry is appended to the JSONL
// log file each time a model adapter finishes a RunAgent call.
type Entry struct {
	TS        time.Time      `json:"ts"`
	SessionID string         `json:"session_id"`
	RunID     string         `json:"run_id"`
	ModelID   string         `json:"model_id"`
	Prompt    string         `json:"prompt"`
	Events    []EventRecord  `json:"events"`
	Response  ResponseRecord `json:"response"`
}

// EventRecord captures a single tool event emitted during the run.
type EventRecord struct {
	Type string `json:"type"` // "reading" | "writing" | "text" | "error"
	Data string `json:"data"`
}

// ResponseRecord captures the final outcome of a RunAgent call.
type ResponseRecord struct {
	Text          string        `json:"text"`
	InputTokens   int64         `json:"input_tokens"`
	OutputTokens  int64         `json:"output_tokens"`
	CostUSD       float64       `json:"cost_usd"`
	LatencyMS     int64         `json:"latency_ms"`
	ProposedFiles []string      `json:"proposed_files"`
	Writes        []WriteRecord `json:"writes,omitempty"`
	Error         string        `json:"error,omitempty"`
}

// WriteRecord captures one proposed file write (path + full content).
type WriteRecord struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ─── Logger ───────────────────────────────────────────────────────────────────

// Logger writes run entries to an append-only JSONL file.
// All methods are safe for concurrent use.
type Logger struct {
	mu   sync.Mutex
	file *os.File
}

// NewLogger opens (or creates) a JSONL file at path for append-only writes.
// The caller must call Close() when done.
func NewLogger(path string) (*Logger, error) {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{file: f}, nil
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// Write appends a single Entry as a JSON line. Errors are silently ignored so
// the logging path never crashes the main program.
func (l *Logger) Write(e Entry) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.file.Write(b)
	_, _ = l.file.Write([]byte("\n"))
}

// ─── Adapter wrapper ─────────────────────────────────────────────────────────

// Wrap returns a ModelAdapter that logs every RunAgent call to l.
// If l is nil the original adapter is returned unchanged (zero overhead).
func Wrap(adapter models.ModelAdapter, sessionID string, l *Logger) models.ModelAdapter {
	if l == nil {
		return adapter
	}
	return &loggingAdapter{inner: adapter, logger: l, sessionID: sessionID}
}

// WrapAll wraps a slice of adapters. Convenience helper for wrapping all at once.
func WrapAll(adapters []models.ModelAdapter, sessionID string, l *Logger) []models.ModelAdapter {
	if l == nil {
		return adapters
	}
	out := make([]models.ModelAdapter, len(adapters))
	for i, a := range adapters {
		out[i] = Wrap(a, sessionID, l)
	}
	return out
}

type loggingAdapter struct {
	inner     models.ModelAdapter
	logger    *Logger
	sessionID string
}

func (a *loggingAdapter) ID() string { return a.inner.ID() }

func (a *loggingAdapter) RunAgent(
	ctx     context.Context,
	history []models.ConversationTurn,
	prompt  string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	var (
		mu     sync.Mutex
		events []EventRecord
	)

	// Shadow onEvent to collect all tool events before forwarding upstream.
	wrappedOnEvent := func(e models.AgentEvent) {
		mu.Lock()
		events = append(events, EventRecord{Type: e.Type, Data: e.Data})
		mu.Unlock()
		onEvent(e)
	}

	resp, err := a.inner.RunAgent(ctx, history, prompt, wrappedOnEvent)

	var proposedFiles []string
	var writes []WriteRecord
	for _, fw := range resp.ProposedWrites {
		proposedFiles = append(proposedFiles, fw.Path)
		writes = append(writes, WriteRecord{Path: fw.Path, Content: fw.Content})
	}

	mu.Lock()
	captured := events
	mu.Unlock()

	a.logger.Write(Entry{
		TS:        time.Now().UTC(),
		SessionID: a.sessionID,
		RunID:     newRunID(),
		ModelID:   resp.ModelID,
		Prompt:    prompt,
		Events:    captured,
		Response: ResponseRecord{
			Text:          resp.Text,
			InputTokens:   resp.InputTokens,
			OutputTokens:  resp.OutputTokens,
			CostUSD:       resp.CostUSD,
			LatencyMS:     resp.LatencyMS,
			ProposedFiles: proposedFiles,
			Writes:        writes,
			Error:         resp.Error,
		},
	})

	return resp, err
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func newRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// dirOf returns the directory component of a file path.
func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
