package mcp

import (
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/suarezc/errata/internal/tools"
)

// ServerConfig holds the configuration for a single MCP server.
type ServerConfig struct {
	// Name is a human-readable label used in logging.
	Name string
	// Args is the command and its arguments to launch the server subprocess.
	// e.g. ["npx", "@exa-ai/exa-mcp-server"]
	Args []string
}

// ParseServerConfigs parses the ERRATA_MCP_SERVERS env-var format:
//
//	name1:cmd1 arg1 arg2,name2:cmd2
//
// Each entry is "name:command args..." separated by commas.
// Returns an empty slice for an empty input string.
func ParseServerConfigs(raw string) []ServerConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []ServerConfig
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		idx := strings.IndexByte(entry, ':')
		if idx < 0 {
			log.Printf("mcp: ignoring malformed entry (no ':') %q", entry)
			continue
		}
		name := strings.TrimSpace(entry[:idx])
		cmdStr := strings.TrimSpace(entry[idx+1:])
		if name == "" || cmdStr == "" {
			log.Printf("mcp: ignoring empty name or command in entry %q", entry)
			continue
		}
		args := strings.Fields(cmdStr)
		out = append(out, ServerConfig{Name: name, Args: args})
	}
	return out
}

// Dispatcher is an alias for tools.MCPDispatcher — a function that executes an MCP tool call.
type Dispatcher = tools.MCPDispatcher

// server holds a running MCP subprocess and its connection.
type server struct {
	name string
	cmd  *exec.Cmd
	conn *Conn
}

// Manager owns all running MCP server subprocesses.
type Manager struct {
	servers []*server
}

// StartServers launches the given MCP servers, performs the MCP handshake,
// and discovers their tools. Returns:
//   - extra []tools.ToolDef: tool definitions to append to Errata's active set
//   - dispatchers map[toolName]Dispatcher: call-time dispatch table
//
// On per-server failure the error is logged and that server is skipped;
// the function never returns a fatal error so Errata can start without MCP.
func StartServers(configs []ServerConfig, env []string) (
	extra []tools.ToolDef,
	dispatchers map[string]Dispatcher,
	mgr *Manager,
) {
	dispatchers = make(map[string]Dispatcher)
	mgr = &Manager{}

	for _, cfg := range configs {
		defs, disp, srv, err := startOne(cfg, env)
		if err != nil {
			log.Printf("mcp: server %q failed to start: %v (skipped)", cfg.Name, err)
			continue
		}
		extra = append(extra, defs...)
		for name, d := range disp {
			dispatchers[name] = d
		}
		mgr.servers = append(mgr.servers, srv)
	}
	return extra, dispatchers, mgr
}

// startOne launches one MCP server and returns its tools and dispatchers.
func startOne(cfg ServerConfig, env []string) (
	[]tools.ToolDef, map[string]Dispatcher, *server, error,
) {
	if len(cfg.Args) == 0 {
		return nil, nil, nil, fmt.Errorf("empty command")
	}

	cmd := exec.Command(cfg.Args[0], cfg.Args[1:]...) //nolint:gosec // user-controlled config
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Stderr is inherited so the user sees server-side errors in their terminal.

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("start: %w", err)
	}

	conn := NewConn(stdout, stdin)

	if err := conn.Handshake(); err != nil {
		_ = cmd.Process.Kill()
		return nil, nil, nil, fmt.Errorf("handshake: %w", err)
	}

	mcpTools, err := conn.ListTools()
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, nil, nil, fmt.Errorf("tools/list: %w", err)
	}

	defs := make([]tools.ToolDef, 0, len(mcpTools))
	disp := make(map[string]Dispatcher, len(mcpTools))

	for _, mt := range mcpTools {
		def := translateTool(mt)
		defs = append(defs, def)

		// Capture conn and tool name for the closure.
		toolConn := conn
		toolName := mt.Name
		disp[toolName] = func(args map[string]string) string {
			anyArgs := make(map[string]any, len(args))
			for k, v := range args {
				anyArgs[k] = v
			}
			result, err := toolConn.CallTool(toolName, anyArgs)
			if err != nil {
				return fmt.Sprintf("[mcp error: %v]", err)
			}
			return result
		}
	}

	srv := &server{name: cfg.Name, cmd: cmd, conn: conn}
	log.Printf("mcp: server %q started with %d tool(s)", cfg.Name, len(mcpTools))
	return defs, disp, srv, nil
}

// translateTool converts an MCP tool definition into an Errata ToolDef.
func translateTool(mt MCPTool) tools.ToolDef {
	props := extractProperties(mt.InputSchema)
	required := extractRequired(mt.InputSchema)
	return tools.ToolDef{
		Name:        mt.Name,
		Description: mt.Description,
		Properties:  props,
		Required:    required,
	}
}

// extractProperties translates an MCP JSON Schema "properties" map to
// Errata's map[string]tools.ToolParam.
func extractProperties(schema map[string]any) map[string]tools.ToolParam {
	out := map[string]tools.ToolParam{}
	rawProps, ok := schema["properties"]
	if !ok {
		return out
	}
	props, ok := rawProps.(map[string]any)
	if !ok {
		return out
	}
	for name, rawParam := range props {
		p, ok := rawParam.(map[string]any)
		if !ok {
			continue
		}
		out[name] = tools.ToolParam{
			Type:        strField(p, "type"),
			Description: strField(p, "description"),
		}
	}
	return out
}

// extractRequired pulls the "required" []string from a JSON Schema object.
func extractRequired(schema map[string]any) []string {
	rawReq, ok := schema["required"]
	if !ok {
		return nil
	}
	rawSlice, ok := rawReq.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range rawSlice {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// strField safely extracts a string field from a map[string]any.
func strField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Shutdown terminates all MCP server subprocesses.
func (m *Manager) Shutdown() {
	if m == nil {
		return
	}
	for _, srv := range m.servers {
		if srv.cmd != nil && srv.cmd.Process != nil {
			_ = srv.cmd.Process.Kill()
		}
	}
}
