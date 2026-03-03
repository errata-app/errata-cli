package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// newOpenAIMockServer returns an httptest server that serves POST /chat/completions.
// Each request pops the next JSON response body from the responses slice.
func newOpenAIMockServer(t *testing.T, responses []string) *httptest.Server {
	t.Helper()
	var idx atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
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

func testOpenAIConfig(ts *httptest.Server) *openaiRunConfig {
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(ts.URL),
	)
	return &openaiRunConfig{
		client:      client,
		modelID:     "gpt-4o",
		apiModelID:  "gpt-4o",
		qualifiedID: "openai/gpt-4o",
	}
}

func openaiToolCtx() context.Context {
	return tools.WithActiveTools(context.Background(), tools.Definitions)
}

// openaiTextResponse builds a JSON response with text content and no tool calls.
func openaiTextResponse(text string, promptTokens, completionTokens int64) string {
	return fmt.Sprintf(`{
		"id": "chatcmpl-test",
		"object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": %s
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": %d,
			"completion_tokens": %d,
			"total_tokens": %d
		}
	}`, mustJSON(text), promptTokens, completionTokens, promptTokens+completionTokens)
}

// openaiToolCallResponse builds a JSON response with a single tool call.
func openaiToolCallResponse(toolCallID, funcName, argsJSON string, promptTokens, completionTokens int64) string {
	return fmt.Sprintf(`{
		"id": "chatcmpl-test",
		"object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [{
					"id": %s,
					"type": "function",
					"function": {
						"name": %s,
						"arguments": %s
					}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": %d,
			"completion_tokens": %d,
			"total_tokens": %d
		}
	}`, mustJSON(toolCallID), mustJSON(funcName), mustJSON(argsJSON), promptTokens, completionTokens, promptTokens+completionTokens)
}

// openaiMultiToolCallResponse builds a response with two tool calls.
func openaiMultiToolCallResponse(tc1ID, tc1Name, tc1Args, tc2ID, tc2Name, tc2Args string, promptTokens, completionTokens int64) string {
	return fmt.Sprintf(`{
		"id": "chatcmpl-test",
		"object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [
					{"id": %s, "type": "function", "function": {"name": %s, "arguments": %s}},
					{"id": %s, "type": "function", "function": {"name": %s, "arguments": %s}}
				]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": %d,
			"completion_tokens": %d,
			"total_tokens": %d
		}
	}`, mustJSON(tc1ID), mustJSON(tc1Name), mustJSON(tc1Args),
		mustJSON(tc2ID), mustJSON(tc2Name), mustJSON(tc2Args),
		promptTokens, completionTokens, promptTokens+completionTokens)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestOpenAILoop_NoTools(t *testing.T) {
	ts := newOpenAIMockServer(t, []string{
		openaiTextResponse("No tools here.", 100, 20),
	})
	defer ts.Close()

	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{})
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "hello",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Equal(t, "No tools here.", resp.Text)
	assert.Empty(t, resp.ProposedWrites)
	assert.True(t, resp.OK())
}

func TestOpenAILoop_TextOnly(t *testing.T) {
	ts := newOpenAIMockServer(t, []string{
		openaiTextResponse("Hello, world!", 100, 20),
	})
	defer ts.Close()

	ctx := openaiToolCtx()
	var events []models.AgentEvent
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "say hello",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "Hello, world!", resp.Text)
	assert.Equal(t, int64(100), resp.InputTokens)
	assert.Equal(t, int64(20), resp.OutputTokens)
	assert.Empty(t, resp.ProposedWrites)
	assert.True(t, resp.OK())

	// Should have emitted a text event.
	var textEvents int
	for _, e := range events {
		if e.Type == models.EventText {
			textEvents++
		}
	}
	assert.Equal(t, 1, textEvents)
}

func TestOpenAILoop_SingleToolCall(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("hello.txt", []byte("file content here"), 0o600))

	ts := newOpenAIMockServer(t, []string{
		// Turn 1: model calls read_file
		openaiToolCallResponse("call_1", "read_file", `{"path":"hello.txt"}`, 100, 30),
		// Turn 2: model responds with text
		openaiTextResponse("I read the file.", 200, 40),
	})
	defer ts.Close()

	ctx := openaiToolCtx()
	var events []models.AgentEvent
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "read hello.txt",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "I read the file.", resp.Text)
	assert.Empty(t, resp.ProposedWrites)

	// Should have emitted a reading event for the read_file call.
	var readEvents int
	for _, e := range events {
		if e.Type == models.EventReading {
			readEvents++
		}
	}
	assert.GreaterOrEqual(t, readEvents, 1)
}

func TestOpenAILoop_WriteFileIntercepted(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ts := newOpenAIMockServer(t, []string{
		// Turn 1: model calls write_file
		openaiToolCallResponse("call_w", "write_file", `{"path":"out.go","content":"package main"}`, 100, 30),
		// Turn 2: model responds with text
		openaiTextResponse("Done.", 200, 10),
	})
	defer ts.Close()

	ctx := openaiToolCtx()
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "write a file",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	require.Len(t, resp.ProposedWrites, 1)
	assert.Equal(t, "out.go", resp.ProposedWrites[0].Path)
	assert.Equal(t, "package main", resp.ProposedWrites[0].Content)

	// File must NOT exist on disk.
	_, err = os.Stat("out.go")
	assert.True(t, os.IsNotExist(err), "write_file must not write to disk")
}

func TestOpenAILoop_MultiToolSameTurn(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("a.txt", []byte("aaa"), 0o600))
	require.NoError(t, os.WriteFile("b.txt", []byte("bbb"), 0o600))

	ts := newOpenAIMockServer(t, []string{
		// Turn 1: two read_file calls
		openaiMultiToolCallResponse(
			"call_a", "read_file", `{"path":"a.txt"}`,
			"call_b", "read_file", `{"path":"b.txt"}`,
			100, 30,
		),
		// Turn 2: final text
		openaiTextResponse("Both read.", 200, 20),
	})
	defer ts.Close()

	ctx := openaiToolCtx()
	var events []models.AgentEvent
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "read both",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "Both read.", resp.Text)

	// Should have emitted two reading events.
	var readCount int
	for _, e := range events {
		if e.Type == models.EventReading {
			readCount++
		}
	}
	assert.Equal(t, 2, readCount)
}

func TestOpenAILoop_TokenAccumulation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("x.txt", []byte("x"), 0o600))

	ts := newOpenAIMockServer(t, []string{
		openaiToolCallResponse("call_1", "read_file", `{"path":"x.txt"}`, 100, 30),
		openaiTextResponse("done", 250, 15),
	})
	defer ts.Close()

	ctx := openaiToolCtx()
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	// Tokens should be summed across both turns.
	assert.Equal(t, int64(350), resp.InputTokens)  // 100 + 250
	assert.Equal(t, int64(45), resp.OutputTokens)   // 30 + 15
}

func TestOpenAILoop_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(openaiToolCtx())
	cancel() // cancel immediately

	ts := newOpenAIMockServer(t, nil) // no responses needed
	defer ts.Close()

	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.Error(t, err)
	assert.True(t, resp.Interrupted)
}

func TestOpenAILoop_APIError(t *testing.T) {
	// Server returns 500 for all requests.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"server error","type":"server_error"}}`)
	}))
	defer ts.Close()

	ctx := openaiToolCtx()
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.Error(t, err)
	assert.NotEmpty(t, resp.Error)
	assert.False(t, resp.OK())
}

func TestOpenAILoop_EmptyChoices(t *testing.T) {
	ts := newOpenAIMockServer(t, []string{
		`{"id":"chatcmpl-test","object":"chat.completion","choices":[],"usage":{"prompt_tokens":50,"completion_tokens":0,"total_tokens":50}}`,
	})
	defer ts.Close()

	ctx := openaiToolCtx()
	resp, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Empty(t, resp.Text)
	assert.Empty(t, resp.ProposedWrites)
}

func TestOpenAILoop_SnapshotEmitted(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("s.txt", []byte("snap"), 0o600))

	ts := newOpenAIMockServer(t, []string{
		openaiToolCallResponse("call_s", "read_file", `{"path":"s.txt"}`, 100, 20),
		openaiTextResponse("done", 200, 10),
	})
	defer ts.Close()

	ctx := openaiToolCtx()
	var events []models.AgentEvent
	_, err := runOpenAIAgentLoop(ctx, testOpenAIConfig(ts), nil, "test",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)

	// A snapshot event should have been emitted between the two turns.
	var snapshotCount int
	for _, e := range events {
		if e.Type == models.EventSnapshot {
			snapshotCount++
		}
	}
	assert.GreaterOrEqual(t, snapshotCount, 1)
}
