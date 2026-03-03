package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// newGeminiMockServer returns an httptest server that handles
// POST /v1beta/models/{model}:generateContent.
func newGeminiMockServer(t *testing.T, responses []string) *httptest.Server {
	t.Helper()
	var idx atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, ":generateContent") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		i := int(idx.Add(1)) - 1
		if i >= len(responses) {
			http.Error(w, "no more responses", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, responses[i])
	}))
}

func testGeminiConfig(t *testing.T, ts *httptest.Server) geminiRunConfig {
	t.Helper()
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: ts.URL,
		},
	})
	require.NoError(t, err)
	return geminiRunConfig{
		client:      client,
		modelID:     "gemini-2.0-flash",
		apiModelID:  "gemini-2.0-flash",
		qualifiedID: "google/gemini-2.0-flash",
	}
}

func geminiToolCtx() context.Context {
	return tools.WithActiveTools(context.Background(), tools.Definitions)
}

// geminiTextResponse builds a Gemini API response with text content.
func geminiTextResponse(text string, promptTokens, candidatesTokens int32) string {
	return fmt.Sprintf(`{
		"candidates": [{
			"content": {
				"parts": [{"text": %s}],
				"role": "model"
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": %d,
			"candidatesTokenCount": %d,
			"totalTokenCount": %d
		}
	}`, mustJSON(text), promptTokens, candidatesTokens, promptTokens+candidatesTokens)
}

// geminiFunctionCallResponse builds a response with a functionCall part.
func geminiFunctionCallResponse(name string, args map[string]any, promptTokens, candidatesTokens int32) string {
	argsJSON, _ := json.Marshal(args)
	return fmt.Sprintf(`{
		"candidates": [{
			"content": {
				"parts": [{"functionCall": {"name": %s, "args": %s}}],
				"role": "model"
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": %d,
			"candidatesTokenCount": %d,
			"totalTokenCount": %d
		}
	}`, mustJSON(name), string(argsJSON), promptTokens, candidatesTokens, promptTokens+candidatesTokens)
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestGeminiLoop_TextOnly(t *testing.T) {
	ts := newGeminiMockServer(t, []string{
		geminiTextResponse("Hello from Gemini!", 120, 30),
	})
	defer ts.Close()

	ctx := geminiToolCtx()
	cfg := testGeminiConfig(t, ts)
	var events []models.AgentEvent
	resp, err := runGeminiAgentLoop(ctx, cfg, nil, "say hello",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "Hello from Gemini!", resp.Text)
	assert.Equal(t, int64(120), resp.InputTokens)
	assert.Equal(t, int64(30), resp.OutputTokens)
	assert.Empty(t, resp.ProposedWrites)
	assert.True(t, resp.OK())

	var textEvents int
	for _, e := range events {
		if e.Type == models.EventText {
			textEvents++
		}
	}
	assert.Equal(t, 1, textEvents)
}

func TestGeminiLoop_SingleToolCall(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("info.txt", []byte("gemini data"), 0o600))

	ts := newGeminiMockServer(t, []string{
		// Turn 1: function call
		geminiFunctionCallResponse("read_file", map[string]any{"path": "info.txt"}, 100, 25),
		// Turn 2: text
		geminiTextResponse("Read complete.", 200, 20),
	})
	defer ts.Close()

	ctx := geminiToolCtx()
	cfg := testGeminiConfig(t, ts)
	var events []models.AgentEvent
	resp, err := runGeminiAgentLoop(ctx, cfg, nil, "read info.txt",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "Read complete.", resp.Text)
	assert.Empty(t, resp.ProposedWrites)

	var readEvents int
	for _, e := range events {
		if e.Type == models.EventReading {
			readEvents++
		}
	}
	assert.GreaterOrEqual(t, readEvents, 1)
}

func TestGeminiLoop_WriteFileIntercepted(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ts := newGeminiMockServer(t, []string{
		geminiFunctionCallResponse("write_file", map[string]any{"path": "gen.go", "content": "package gen"}, 100, 20),
		geminiTextResponse("Written.", 200, 10),
	})
	defer ts.Close()

	ctx := geminiToolCtx()
	cfg := testGeminiConfig(t, ts)
	resp, err := runGeminiAgentLoop(ctx, cfg, nil, "write a file",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	require.Len(t, resp.ProposedWrites, 1)
	assert.Equal(t, "gen.go", resp.ProposedWrites[0].Path)
	assert.Equal(t, "package gen", resp.ProposedWrites[0].Content)

	_, err = os.Stat("gen.go")
	assert.True(t, os.IsNotExist(err), "write_file must not write to disk")
}

func TestGeminiLoop_TokenAccumulation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("tok.txt", []byte("t"), 0o600))

	ts := newGeminiMockServer(t, []string{
		geminiFunctionCallResponse("read_file", map[string]any{"path": "tok.txt"}, 90, 20),
		geminiTextResponse("done", 210, 15),
	})
	defer ts.Close()

	ctx := geminiToolCtx()
	cfg := testGeminiConfig(t, ts)
	resp, err := runGeminiAgentLoop(ctx, cfg, nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Equal(t, int64(300), resp.InputTokens)  // 90 + 210
	assert.Equal(t, int64(35), resp.OutputTokens)   // 20 + 15
}

func TestGeminiLoop_ContextCancelled(t *testing.T) {
	ts := newGeminiMockServer(t, nil)
	defer ts.Close()

	ctx, cancel := context.WithCancel(geminiToolCtx())
	cancel()

	cfg := testGeminiConfig(t, ts)
	resp, err := runGeminiAgentLoop(ctx, cfg, nil, "test",
		func(models.AgentEvent) {})

	require.Error(t, err)
	assert.True(t, resp.Interrupted)
}

func TestGeminiLoop_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"code":500,"message":"internal error","status":"INTERNAL"}}`)
	}))
	defer ts.Close()

	ctx := geminiToolCtx()
	cfg := testGeminiConfig(t, ts)
	resp, err := runGeminiAgentLoop(ctx, cfg, nil, "test",
		func(models.AgentEvent) {})

	require.Error(t, err)
	assert.NotEmpty(t, resp.Error)
	assert.False(t, resp.OK())
}

func TestGeminiLoop_EmptyCandidates(t *testing.T) {
	ts := newGeminiMockServer(t, []string{
		`{"candidates":[],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":0,"totalTokenCount":50}}`,
	})
	defer ts.Close()

	ctx := geminiToolCtx()
	cfg := testGeminiConfig(t, ts)
	resp, err := runGeminiAgentLoop(ctx, cfg, nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Empty(t, resp.Text)
	assert.Empty(t, resp.ProposedWrites)
}
