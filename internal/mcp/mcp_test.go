package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/suarezc/errata/internal/tools"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// fakeWrite writes one Content-Length-framed JSON-RPC message to w.
func fakeWrite(w io.Writer, v any) {
	body, _ := json.Marshal(v)
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body))
	w.Write(body) //nolint:errcheck
}

// fakeRead reads one Content-Length-framed message from r and returns
// the JSON body as a map (method, id, params extracted as needed).
func fakeRead(r *bufio.Reader) (map[string]any, error) {
	contentLen := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		const prefix = "Content-Length:"
		if strings.HasPrefix(line, prefix) {
			v := strings.TrimSpace(line[len(prefix):])
			n := 0
			fmt.Sscanf(v, "%d", &n)
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("no Content-Length header")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(body, &out)
}

// runFakeServer runs a minimal MCP server that handles a sequence of exchanges.
// Each exchange is a (method, responsePayload) pair. For notifications (no id
// expected) pass responsePayload as nil and the server will consume but not reply.
// Runs in a goroutine; signals done via the returned channel.
func runFakeServer(serverR io.Reader, serverW io.Writer, exchanges []fakeExchange) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := bufio.NewReader(serverR)
		for _, ex := range exchanges {
			msg, err := fakeRead(r)
			if err != nil {
				return
			}
			_ = msg // we trust the client sends the right thing in tests
			if ex.response != nil {
				fakeWrite(serverW, ex.response)
			}
		}
	}()
	return done
}

type fakeExchange struct {
	response any // nil = notification, no reply
}

// ─── ParseServerConfigs ───────────────────────────────────────────────────────

func TestParseServerConfigs_Empty(t *testing.T) {
	if got := ParseServerConfigs(""); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
	if got := ParseServerConfigs("   "); got != nil {
		t.Fatalf("expected nil for whitespace input, got %v", got)
	}
}

func TestParseServerConfigs_Single(t *testing.T) {
	got := ParseServerConfigs("exa:npx @exa-ai/exa-mcp-server")
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Name != "exa" {
		t.Errorf("name = %q, want %q", got[0].Name, "exa")
	}
	if len(got[0].Args) != 2 || got[0].Args[0] != "npx" || got[0].Args[1] != "@exa-ai/exa-mcp-server" {
		t.Errorf("args = %v, want [npx @exa-ai/exa-mcp-server]", got[0].Args)
	}
}

func TestParseServerConfigs_Multiple(t *testing.T) {
	got := ParseServerConfigs("exa:npx foo,search:npx bar baz")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Name != "exa" {
		t.Errorf("first name = %q, want exa", got[0].Name)
	}
	if got[1].Name != "search" {
		t.Errorf("second name = %q, want search", got[1].Name)
	}
	if len(got[1].Args) != 3 {
		t.Errorf("second args = %v, want 3 elements", got[1].Args)
	}
}

func TestParseServerConfigs_MalformedSkipped(t *testing.T) {
	// No colon → skipped; valid entry still parsed.
	got := ParseServerConfigs("badentry,valid:cmd")
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Name != "valid" {
		t.Errorf("name = %q, want valid", got[0].Name)
	}
}

func TestParseServerConfigs_EmptyPartsSkipped(t *testing.T) {
	got := ParseServerConfigs(",,exa:npx foo,,")
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
}

// ─── Schema translation ───────────────────────────────────────────────────────

func TestExtractProperties_Basic(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max results",
			},
		},
	}
	props := extractProperties(schema)
	if len(props) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(props))
	}
	if props["query"].Type != "string" {
		t.Errorf("query type = %q, want string", props["query"].Type)
	}
	if props["query"].Description != "Search query" {
		t.Errorf("query description = %q, want 'Search query'", props["query"].Description)
	}
	if props["limit"].Type != "integer" {
		t.Errorf("limit type = %q, want integer", props["limit"].Type)
	}
}

func TestExtractProperties_NoProperties(t *testing.T) {
	props := extractProperties(map[string]any{})
	if len(props) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(props))
	}
}

func TestExtractRequired_Basic(t *testing.T) {
	schema := map[string]any{
		"required": []any{"query", "url"},
	}
	req := extractRequired(schema)
	if len(req) != 2 {
		t.Fatalf("expected 2 required fields, got %d", len(req))
	}
	if req[0] != "query" || req[1] != "url" {
		t.Errorf("required = %v, want [query url]", req)
	}
}

func TestExtractRequired_Missing(t *testing.T) {
	req := extractRequired(map[string]any{})
	if req != nil {
		t.Fatalf("expected nil, got %v", req)
	}
}

func TestTranslateTool(t *testing.T) {
	mt := MCPTool{
		Name:        "web_search",
		Description: "Search the web",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query",
				},
			},
			"required": []any{"query"},
		},
	}
	def := translateTool(mt)
	if def.Name != "web_search" {
		t.Errorf("name = %q, want web_search", def.Name)
	}
	if def.Description != "Search the web" {
		t.Errorf("description = %q, want 'Search the web'", def.Description)
	}
	if len(def.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(def.Properties))
	}
	if def.Properties["query"].Type != "string" {
		t.Errorf("query type = %q, want string", def.Properties["query"].Type)
	}
	if len(def.Required) != 1 || def.Required[0] != "query" {
		t.Errorf("required = %v, want [query]", def.Required)
	}
}

// ─── Conn: Handshake ─────────────────────────────────────────────────────────

func TestConn_Handshake(t *testing.T) {
	// serverR ← client writes; serverW → client reads
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer serverR.Close()
	defer clientW.Close()
	defer clientR.Close()
	defer serverW.Close()

	// Fake server: respond to initialize; consume the notifications/initialized notification.
	done := runFakeServer(serverR, serverW, []fakeExchange{
		{response: map[string]any{
			"jsonrpc": "2.0",
			"id":      float64(1),
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test-server", "version": "0.1"},
			},
		}},
		{response: nil}, // notifications/initialized — no reply
	})

	conn := NewConn(clientR, clientW)
	if err := conn.Handshake(); err != nil {
		t.Fatalf("Handshake() error: %v", err)
	}

	// Close pipes so the fake server goroutine can exit.
	clientW.Close()
	<-done
}

// ─── Conn: ListTools ─────────────────────────────────────────────────────────

func TestConn_ListTools(t *testing.T) {
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer serverR.Close()
	defer clientW.Close()
	defer clientR.Close()
	defer serverW.Close()

	toolsPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      float64(1),
		"result": map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "search",
					"description": "Search the web",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "string",
								"description": "Search query",
							},
						},
						"required": []any{"query"},
					},
				},
			},
		},
	}

	done := runFakeServer(serverR, serverW, []fakeExchange{
		{response: toolsPayload},
	})

	conn := NewConn(clientR, clientW)
	mcpTools, err := conn.ListTools()
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}
	if len(mcpTools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(mcpTools))
	}
	if mcpTools[0].Name != "search" {
		t.Errorf("tool name = %q, want search", mcpTools[0].Name)
	}
	if mcpTools[0].Description != "Search the web" {
		t.Errorf("tool description = %q, want 'Search the web'", mcpTools[0].Description)
	}

	clientW.Close()
	<-done
}

// ─── Conn: CallTool ──────────────────────────────────────────────────────────

func TestConn_CallTool(t *testing.T) {
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer serverR.Close()
	defer clientW.Close()
	defer clientR.Close()
	defer serverW.Close()

	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      float64(1),
		"result": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "result text"},
			},
		},
	}

	done := runFakeServer(serverR, serverW, []fakeExchange{
		{response: response},
	})

	conn := NewConn(clientR, clientW)
	result, err := conn.CallTool("search", map[string]any{"query": "golang"})
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result != "result text" {
		t.Errorf("result = %q, want 'result text'", result)
	}

	clientW.Close()
	<-done
}

func TestConn_CallTool_Error(t *testing.T) {
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer serverR.Close()
	defer clientW.Close()
	defer clientR.Close()
	defer serverW.Close()

	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      float64(1),
		"result": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "something went wrong"},
			},
			"isError": true,
		},
	}

	done := runFakeServer(serverR, serverW, []fakeExchange{
		{response: response},
	})

	conn := NewConn(clientR, clientW)
	_, err := conn.CallTool("search", map[string]any{"query": "golang"})
	if err == nil {
		t.Fatal("expected error for isError response, got nil")
	}

	clientW.Close()
	<-done
}

// ─── StartServers: non-existent binary ───────────────────────────────────────

func TestStartServers_FailedServer(t *testing.T) {
	configs := []ServerConfig{
		{Name: "nonexistent", Args: []string{"/nonexistent-binary-that-does-not-exist"}},
	}
	defs, dispatchers, warnings, mgr := StartServers(configs, nil)
	if len(defs) != 0 {
		t.Errorf("expected 0 tool defs for failed server, got %d", len(defs))
	}
	if len(dispatchers) != 0 {
		t.Errorf("expected 0 dispatchers for failed server, got %d", len(dispatchers))
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning for failed server, got %d: %v", len(warnings), warnings)
	}
	if mgr == nil {
		t.Fatal("mgr should not be nil even when all servers fail")
	}
	if len(mgr.servers) != 0 {
		t.Errorf("expected 0 running servers, got %d", len(mgr.servers))
	}
	// Shutdown should be safe to call on an empty manager.
	mgr.Shutdown()
}

// ─── Dispatcher integration via MCPDispatchersFromContext ─────────────────────

func TestDispatcher_TypeAlias(t *testing.T) {
	// Verify Dispatcher is the same type as tools.MCPDispatcher so they can
	// be stored in the same map without a type assertion.
	var d Dispatcher = func(args map[string]string) string {
		return "ok"
	}
	dispatchers := map[string]tools.MCPDispatcher{
		"test": d,
	}
	if got := dispatchers["test"](nil); got != "ok" {
		t.Errorf("dispatcher returned %q, want ok", got)
	}
}

// ─── Conn.Call: RPC error response ───────────────────────────────────────────

func TestConn_Call_RPCError(t *testing.T) {
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer serverR.Close()
	defer clientW.Close()
	defer clientR.Close()
	defer serverW.Close()

	done := runFakeServer(serverR, serverW, []fakeExchange{
		{response: map[string]any{
			"jsonrpc": "2.0",
			"id":      float64(1),
			"error":   map[string]any{"code": float64(-32600), "message": "Invalid Request"},
		}},
	})

	conn := NewConn(clientR, clientW)
	err := conn.Call("test", nil, nil)
	if err == nil {
		t.Fatal("expected RPC error, got nil")
	}
	if !strings.Contains(err.Error(), "Invalid Request") {
		t.Errorf("error %q should contain 'Invalid Request'", err.Error())
	}

	clientW.Close()
	<-done
}

// ─── readResponse: missing Content-Length ─────────────────────────────────────

func TestConn_ReadResponse_NoContentLength(t *testing.T) {
	// Simulate a response with no Content-Length header.
	raw := "Some-Header: value\r\n\r\n{}"
	conn := NewConn(strings.NewReader(raw), io.Discard)
	_, err := conn.readResponse()
	if err == nil {
		t.Fatal("expected error for missing Content-Length")
	}
	if !strings.Contains(err.Error(), "no Content-Length") {
		t.Errorf("error %q should mention Content-Length", err.Error())
	}
}

// ─── readResponse: invalid Content-Length ─────────────────────────────────────

func TestConn_ReadResponse_InvalidContentLength(t *testing.T) {
	raw := "Content-Length: notanumber\r\n\r\n"
	conn := NewConn(strings.NewReader(raw), io.Discard)
	_, err := conn.readResponse()
	if err == nil {
		t.Fatal("expected error for invalid Content-Length")
	}
	if !strings.Contains(err.Error(), "invalid Content-Length") {
		t.Errorf("error %q should mention invalid Content-Length", err.Error())
	}
}

// ─── readResponse: read header error ─────────────────────────────────────────

func TestConn_ReadResponse_HeaderReadError(t *testing.T) {
	// Empty reader → immediate EOF.
	conn := NewConn(strings.NewReader(""), io.Discard)
	_, err := conn.readResponse()
	if err == nil {
		t.Fatal("expected error for EOF during header read")
	}
}

// ─── CallTool: multiple content items ────────────────────────────────────────

func TestConn_CallTool_MultipleContent(t *testing.T) {
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer serverR.Close()
	defer clientW.Close()
	defer clientR.Close()
	defer serverW.Close()

	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      float64(1),
		"result": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "hello "},
				map[string]any{"type": "image", "data": "..."},
				map[string]any{"type": "text", "text": "world"},
			},
		},
	}

	done := runFakeServer(serverR, serverW, []fakeExchange{
		{response: response},
	})

	conn := NewConn(clientR, clientW)
	result, err := conn.CallTool("multi", nil)
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("result = %q, want 'hello world'", result)
	}

	clientW.Close()
	<-done
}

// ─── CallTool: empty content ─────────────────────────────────────────────────

func TestConn_CallTool_EmptyContent(t *testing.T) {
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer serverR.Close()
	defer clientW.Close()
	defer clientR.Close()
	defer serverW.Close()

	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      float64(1),
		"result": map[string]any{
			"content": []any{},
		},
	}

	done := runFakeServer(serverR, serverW, []fakeExchange{
		{response: response},
	})

	conn := NewConn(clientR, clientW)
	result, err := conn.CallTool("empty", nil)
	if err != nil {
		t.Fatalf("CallTool() error: %v", err)
	}
	if result != "" {
		t.Errorf("result = %q, want empty string", result)
	}

	clientW.Close()
	<-done
}

// ─── Schema edge cases ───────────────────────────────────────────────────────

func TestExtractProperties_NonMapProperty(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"bad": "string", // not a map
		},
	}
	props := extractProperties(schema)
	if len(props) != 0 {
		t.Fatalf("expected 0 properties for non-map value, got %d", len(props))
	}
}

func TestExtractRequired_NonStringElement(t *testing.T) {
	schema := map[string]any{
		"required": []any{"query", float64(123), "url"},
	}
	req := extractRequired(schema)
	if len(req) != 2 || req[0] != "query" || req[1] != "url" {
		t.Errorf("expected [query url], got %v", req)
	}
}

func TestExtractProperties_PropertiesNotMap(t *testing.T) {
	schema := map[string]any{
		"properties": "not-a-map",
	}
	props := extractProperties(schema)
	if len(props) != 0 {
		t.Fatalf("expected 0 properties when properties is not a map, got %d", len(props))
	}
}

func TestExtractRequired_RequiredNotArray(t *testing.T) {
	schema := map[string]any{
		"required": "not-an-array",
	}
	req := extractRequired(schema)
	if req != nil {
		t.Fatalf("expected nil when required is not an array, got %v", req)
	}
}

func TestTranslateTool_NilSchema(t *testing.T) {
	mt := MCPTool{
		Name:        "minimal",
		Description: "A tool",
		InputSchema: nil,
	}
	def := translateTool(mt)
	if def.Name != "minimal" {
		t.Errorf("name = %q, want minimal", def.Name)
	}
	if len(def.Properties) != 0 {
		t.Errorf("expected empty properties, got %d", len(def.Properties))
	}
}

// ─── ParseServerConfigs edge cases ───────────────────────────────────────────

func TestParseServerConfigs_EmptyName(t *testing.T) {
	got := ParseServerConfigs(":cmd arg1")
	if len(got) != 0 {
		t.Errorf("expected 0 entries for empty name, got %d", len(got))
	}
}

func TestParseServerConfigs_EmptyCommand(t *testing.T) {
	got := ParseServerConfigs("name:")
	if len(got) != 0 {
		t.Errorf("expected 0 entries for empty command, got %d", len(got))
	}
}

// ─── Manager.Shutdown idempotent ─────────────────────────────────────────────

func TestManager_Shutdown_NilSafe(t *testing.T) {
	var mgr *Manager
	mgr.Shutdown() // should not panic
}

func TestManager_Shutdown_Idempotent(t *testing.T) {
	mgr := &Manager{}
	mgr.Shutdown()
	mgr.Shutdown() // should not panic on second call
}
