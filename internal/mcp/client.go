// Package mcp implements a minimal MCP (Model Context Protocol) client.
// It supports stdio transport only — the most common MCP deployment pattern.
// Only the tools capability is implemented: tools/list and tools/call.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
)

// rpcRequest is a JSON-RPC 2.0 request or notification.
// Notifications omit the ID field (id == nil after marshal).
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Conn is a synchronous JSON-RPC 2.0 connection over a stdio transport.
// It is NOT safe for concurrent use — Errata calls tools sequentially.
type Conn struct {
	w   io.Writer
	r   *bufio.Reader
	seq atomic.Int64
}

// NewConn wraps the given reader/writer as a JSON-RPC 2.0 connection.
// The caller is responsible for providing the subprocess stdin (w) and
// stdout (r) from exec.Cmd.
func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{w: w, r: bufio.NewReader(r)}
}

// Call sends a request with the given method and params, reads the response,
// and unmarshals result into out (if non-nil).
func (c *Conn) Call(method string, params, out any) error {
	id := c.seq.Add(1)
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}
	if err := c.writeMessage(req); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	resp, err := c.readResponse()
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out != nil && resp.Result != nil {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

// Notify sends a notification (no ID, no response expected).
func (c *Conn) Notify(method string, params any) error {
	n := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.writeMessage(n)
}

// writeMessage serialises msg as JSON and sends it with LSP-style framing:
//
//	Content-Length: N\r\n\r\n<N bytes of JSON>
func (c *Conn) writeMessage(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(c.w, header); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// readResponse reads one LSP-framed message and parses it as an rpcResponse.
func (c *Conn) readResponse() (*rpcResponse, error) {
	// Read headers until blank line.
	contentLen := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		const prefix = "Content-Length:"
		if strings.HasPrefix(line, prefix) {
			v := strings.TrimSpace(line[len(prefix):])
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length %q: %w", v, err)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("no Content-Length header found")
	}

	body := make([]byte, contentLen)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &resp, nil
}

// ---- MCP protocol types ----

// initParams are the parameters sent with the initialize request.
type initParams struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilties  `json:"capabilities"`
	ClientInfo      clientInfo   `json:"clientInfo"`
}

type capabilties struct {
	Tools map[string]any `json:"tools"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MCPTool is one entry from a tools/list response.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// toolsListResult is the result of a tools/list call.
type toolsListResult struct {
	Tools []MCPTool `json:"tools"`
}

// toolCallParams are the parameters of a tools/call request.
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// toolCallContent is one item in a tools/call result.
type toolCallContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolCallResult is the result of a tools/call request.
type toolCallResult struct {
	Content []toolCallContent `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// Handshake performs the MCP initialize / initialized exchange.
func (c *Conn) Handshake() error {
	params := initParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    capabilties{Tools: map[string]any{}},
		ClientInfo:      clientInfo{Name: "errata", Version: "1.0"},
	}
	if err := c.Call("initialize", params, nil); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	// Notify the server that initialization is complete.
	return c.Notify("notifications/initialized", nil)
}

// ListTools calls tools/list and returns the server's tool definitions.
func (c *Conn) ListTools() ([]MCPTool, error) {
	var result toolsListResult
	if err := c.Call("tools/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool calls tools/call with the given tool name and arguments.
// Returns the concatenated text from all content items.
func (c *Conn) CallTool(name string, args map[string]any) (string, error) {
	params := toolCallParams{Name: name, Arguments: args}
	var result toolCallResult
	if err := c.Call("tools/call", params, &result); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	text := sb.String()
	if result.IsError {
		return text, fmt.Errorf("tool returned error: %s", text)
	}
	return text, nil
}
