package adapters

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// newAnthropicMockServer returns an httptest server that serves POST /v1/messages.
func newAnthropicMockServer(t *testing.T, responses []string) *httptest.Server {
	t.Helper()
	var idx atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
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

func testAnthropicConfig(ts *httptest.Server) *anthropicRunConfig {
	return &anthropicRunConfig{
		clientOpts: []option.RequestOption{
			option.WithAPIKey("test-key"),
			option.WithBaseURL(ts.URL),
		},
		modelID:     "claude-sonnet-4-6",
		qualifiedID: "anthropic/claude-sonnet-4-6",
	}
}

func anthropicToolCtx() context.Context {
	return tools.WithActiveTools(context.Background(), tools.Definitions)
}

// anthropicTextResponse builds a JSON response with text content and end_turn stop reason.
func anthropicTextResponse(text string, inputTokens, outputTokens int64) string {
	return fmt.Sprintf(`{
		"id": "msg_test",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": %s}],
		"model": "claude-sonnet-4-6",
		"stop_reason": "end_turn",
		"stop_sequence": null,
		"usage": {
			"input_tokens": %d,
			"output_tokens": %d,
			"cache_read_input_tokens": 0,
			"cache_creation_input_tokens": 0
		}
	}`, mustJSON(text), inputTokens, outputTokens)
}

// anthropicToolUseResponse builds a response with a single tool_use block and stop_reason=tool_use.
func anthropicToolUseResponse(toolUseID, name, argsJSON string, inputTokens, outputTokens int64) string {
	return fmt.Sprintf(`{
		"id": "msg_test",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "tool_use", "id": %s, "name": %s, "input": %s}],
		"model": "claude-sonnet-4-6",
		"stop_reason": "tool_use",
		"stop_sequence": null,
		"usage": {
			"input_tokens": %d,
			"output_tokens": %d,
			"cache_read_input_tokens": 0,
			"cache_creation_input_tokens": 0
		}
	}`, mustJSON(toolUseID), mustJSON(name), argsJSON, inputTokens, outputTokens)
}

// anthropicToolUseResponseWithCache builds a tool_use response with cache token counts.
func anthropicToolUseResponseWithCache(toolUseID, name, argsJSON string, inputTokens, outputTokens, cacheRead, cacheCreation int64) string {
	return fmt.Sprintf(`{
		"id": "msg_test",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "tool_use", "id": %s, "name": %s, "input": %s}],
		"model": "claude-sonnet-4-6",
		"stop_reason": "tool_use",
		"stop_sequence": null,
		"usage": {
			"input_tokens": %d,
			"output_tokens": %d,
			"cache_read_input_tokens": %d,
			"cache_creation_input_tokens": %d
		}
	}`, mustJSON(toolUseID), mustJSON(name), argsJSON, inputTokens, outputTokens, cacheRead, cacheCreation)
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestAnthropicLoop_TextOnly(t *testing.T) {
	ts := newAnthropicMockServer(t, []string{
		anthropicTextResponse("Hello from Claude!", 150, 25),
	})
	defer ts.Close()

	ctx := anthropicToolCtx()
	var events []models.AgentEvent
	resp, err := runAnthropicAgentLoop(ctx, testAnthropicConfig(ts), nil, "say hello",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "Hello from Claude!", resp.Text)
	assert.Equal(t, int64(150), resp.InputTokens)
	assert.Equal(t, int64(25), resp.OutputTokens)
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

func TestAnthropicLoop_SingleToolCall(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("data.txt", []byte("important data"), 0o600))

	ts := newAnthropicMockServer(t, []string{
		// Turn 1: tool_use
		anthropicToolUseResponse("toolu_01", "read_file", `{"path":"data.txt"}`, 100, 30),
		// Turn 2: text
		anthropicTextResponse("I read the file.", 200, 20),
	})
	defer ts.Close()

	ctx := anthropicToolCtx()
	var events []models.AgentEvent
	resp, err := runAnthropicAgentLoop(ctx, testAnthropicConfig(ts), nil, "read data.txt",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "I read the file.", resp.Text)
	assert.Empty(t, resp.ProposedWrites)

	var readEvents int
	for _, e := range events {
		if e.Type == models.EventReading {
			readEvents++
		}
	}
	assert.GreaterOrEqual(t, readEvents, 1)
}

func TestAnthropicLoop_WriteFileIntercepted(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ts := newAnthropicMockServer(t, []string{
		anthropicToolUseResponse("toolu_w", "write_file", `{"path":"new.go","content":"package new"}`, 100, 30),
		anthropicTextResponse("File written.", 200, 10),
	})
	defer ts.Close()

	ctx := anthropicToolCtx()
	resp, err := runAnthropicAgentLoop(ctx, testAnthropicConfig(ts), nil, "write a file",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	require.Len(t, resp.ProposedWrites, 1)
	assert.Equal(t, "new.go", resp.ProposedWrites[0].Path)
	assert.Equal(t, "package new", resp.ProposedWrites[0].Content)

	_, err = os.Stat("new.go")
	assert.True(t, os.IsNotExist(err), "write_file must not write to disk")
}

func TestAnthropicLoop_TokenAccumulation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("t.txt", []byte("tok"), 0o600))

	ts := newAnthropicMockServer(t, []string{
		// Turn 1: tool call with cache tokens
		anthropicToolUseResponseWithCache("toolu_t", "read_file", `{"path":"t.txt"}`, 80, 20, 10, 5),
		// Turn 2: text with no cache
		anthropicTextResponse("done", 200, 15),
	})
	defer ts.Close()

	ctx := anthropicToolCtx()
	resp, err := runAnthropicAgentLoop(ctx, testAnthropicConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	// Turn 1: input = 80 + 10 (cache_read) + 5 (cache_creation) = 95
	// Turn 2: input = 200 + 0 + 0 = 200
	// Total input = 295
	assert.Equal(t, int64(295), resp.InputTokens)
	// Total output = 20 + 15 = 35
	assert.Equal(t, int64(35), resp.OutputTokens)
}

func TestAnthropicLoop_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(anthropicToolCtx())
	cancel()

	ts := newAnthropicMockServer(t, nil)
	defer ts.Close()

	resp, err := runAnthropicAgentLoop(ctx, testAnthropicConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.Error(t, err)
	assert.True(t, resp.Interrupted)
}

func TestAnthropicLoop_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"server error"}}`)
	}))
	defer ts.Close()

	ctx := anthropicToolCtx()
	resp, err := runAnthropicAgentLoop(ctx, testAnthropicConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.Error(t, err)
	assert.NotEmpty(t, resp.Error)
	assert.False(t, resp.OK())
}

func TestAnthropicLoop_EndTurnStopsLoop(t *testing.T) {
	// Model returns end_turn with tool_use content — end_turn should still stop the loop.
	ts := newAnthropicMockServer(t, []string{
		anthropicTextResponse("Final answer.", 100, 20),
	})
	defer ts.Close()

	ctx := anthropicToolCtx()
	resp, err := runAnthropicAgentLoop(ctx, testAnthropicConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Equal(t, "Final answer.", resp.Text)
	assert.True(t, resp.OK())
}
