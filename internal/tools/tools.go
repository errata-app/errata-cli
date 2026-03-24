// Package tools defines the canonical tool schemas and file I/O executors.
package tools

import (
	"context"
	"time"
)

const (
	ReadToolName       = "read_file"
	WriteToolName      = "write_file"
	EditToolName       = "edit_file"
	ListDirToolName    = "list_directory"
	SearchFilesName    = "search_files"
	SearchCodeName     = "search_code"
	BashToolName       = "bash"
	WebFetchToolName   = "web_fetch"
	WebSearchToolName  = "web_search"
	SpawnAgentToolName = "spawn_agent"
)

func init() {
	// Remove spawn_agent from Definitions (sub-agent feature not yet enabled).
	filtered := Definitions[:0]
	for _, d := range Definitions {
		if d.Name != SpawnAgentToolName {
			filtered = append(filtered, d)
		}
	}
	Definitions = filtered
}

// maxReadLines is the hard cap on lines returned by ExecuteRead.
const maxReadLines = 2000

// searchCommandTimeout is the maximum time allowed for search_code subprocess execution.
const searchCommandTimeout = 30 * time.Second

// defaultBashTimeout is the production timeout for bash tool execution.
const defaultBashTimeout = 2 * time.Minute

// bashOutputLimit is the maximum bytes returned from a bash tool call.
const bashOutputLimit = 10_000

// FileWrite is a proposed file write intercepted from an agent's tool call.
// It lives here (not in models) to break the import cycle:
// tools → (stdlib only), models → tools.
type FileWrite struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Delete  bool   `json:"delete,omitempty"`
}

// ToolParam is a provider-agnostic tool property description.
type ToolParam struct {
	Type        string
	Description string
}

// ToolDef is the canonical, provider-agnostic tool definition.
// Each adapter translates this into its own SDK format.
type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]ToolParam
	Required    []string
}

// JSONSchemaProps returns a JSON-Schema-compatible properties map and required
// list for this tool definition. Used by adapter tool-building functions to
// avoid duplicating the property-extraction loop.
func (d ToolDef) JSONSchemaProps() (props map[string]any, required []string) {
	props = make(map[string]any, len(d.Properties))
	for name, p := range d.Properties {
		props[name] = map[string]any{
			"type":        p.Type,
			"description": p.Description,
		}
	}
	required = make([]string, len(d.Required))
	copy(required, d.Required)
	return props, required
}

// Definitions is the canonical set of tools available to all agents.
var Definitions = []ToolDef{
	{
		Name: ReadToolName,
		Description: "Read the contents of a file relative to the current working directory. " +
			"Use offset and limit to read a specific line range for large files. " +
			"Returns a truncation notice when there are more lines to read.",
		Properties: map[string]ToolParam{
			"path":   {Type: "string", Description: "Relative path to the file"},
			"offset": {Type: "integer", Description: "First line to return, 1-indexed (default 1)"},
			"limit":  {Type: "integer", Description: "Maximum lines to return (default and max 2000)"},
		},
		Required: []string{"path"},
	},
	{
		Name: WriteToolName,
		Description: "Propose writing content to a file relative to the current working directory. " +
			"Use only for new files or complete rewrites — use edit_file for targeted changes to existing files. " +
			"The write will be applied only if the user selects this model's response.",
		Properties: map[string]ToolParam{
			"path":    {Type: "string", Description: "Relative path to the file"},
			"content": {Type: "string", Description: "Full file content to write"},
		},
		Required: []string{"path", "content"},
	},
	{
		Name: EditToolName,
		Description: "Propose a targeted edit to an existing file by replacing an exact string. " +
			"old_string must appear exactly once in the file — add enough surrounding context to make it unique. " +
			"More efficient than write_file for small changes. " +
			"The edit is queued and applied only if the user selects this model's response.",
		Properties: map[string]ToolParam{
			"path":       {Type: "string", Description: "Relative path to the file to edit"},
			"old_string": {Type: "string", Description: "The exact string to replace (must appear exactly once)"},
			"new_string": {Type: "string", Description: "The replacement string"},
		},
		Required: []string{"path", "old_string", "new_string"},
	},
	{
		Name: ListDirToolName,
		Description: "List files and directories recursively from a path relative to the current " +
			"working directory. Returns an indented tree; directories end with /. Use this to " +
			"explore the project structure before reading specific files.",
		Properties: map[string]ToolParam{
			"path":  {Type: "string", Description: "Relative path to the directory to list"},
			"depth": {Type: "integer", Description: "How many levels deep to recurse (default 2, max 5)"},
		},
		Required: []string{"path"},
	},
	{
		Name: SearchFilesName,
		Description: "Find files whose names match a glob pattern within the project. " +
			"Use ** for recursive matching (e.g. **/*.go). Returns matching paths relative to the base.",
		Properties: map[string]ToolParam{
			"pattern":   {Type: "string", Description: "Glob pattern, e.g. '**/*.go' or 'internal/**/*.go'"},
			"base_path": {Type: "string", Description: "Directory to search from, relative to cwd (default '.')"},
		},
		Required: []string{"pattern"},
	},
	{
		Name: SearchCodeName,
		Description: "Search file contents for a regex pattern. Returns matching file paths, " +
			"line numbers, and the matching lines. Use file_glob to filter by file type. " +
			"Use context_lines to include surrounding lines for context.",
		Properties: map[string]ToolParam{
			"pattern":       {Type: "string", Description: "Regex pattern to search for"},
			"path":          {Type: "string", Description: "File or directory to search, relative to cwd (default '.')"},
			"file_glob":     {Type: "string", Description: "Optional filename filter, e.g. '*.go'"},
			"context_lines": {Type: "integer", Description: "Lines of context before and after each match (default 0)"},
		},
		Required: []string{"pattern"},
	},
	{
		Name: BashToolName,
		Description: "Execute a shell command and return its combined stdout+stderr output. " +
			"Use for running tests, builds, linters, git commands, or any shell operation. " +
			"Commands run with a 2-minute timeout. Provide a brief description of what the command does.",
		Properties: map[string]ToolParam{
			"command":     {Type: "string", Description: "The shell command to execute"},
			"description": {Type: "string", Description: "One-line summary of what this command does"},
		},
		Required: []string{"command", "description"},
	},
	{
		Name: WebFetchToolName,
		Description: "Fetch the content of a public URL and return its text. " +
			"HTML pages are stripped to plain text. Use for reading documentation, " +
			"GitHub issues, READMEs, or any publicly accessible web page.",
		Properties: map[string]ToolParam{
			"url": {Type: "string", Description: "The http:// or https:// URL to fetch"},
		},
		Required: []string{"url"},
	},
	{
		Name: WebSearchToolName,
		Description: "Search DuckDuckGo for factual information and return instant answers. " +
			"Best for definitions, Wikipedia-style summaries, and quick facts. " +
			"Limited to knowledge-panel results — not a full web index. " +
			"For specific documentation pages or URLs, use web_fetch instead.",
		Properties: map[string]ToolParam{
			"query": {Type: "string", Description: "The search query"},
		},
		Required: []string{"query"},
	},
	{
		Name: SpawnAgentToolName,
		Description: "Spawn a sub-agent to complete a specific task. The sub-agent runs its own " +
			"agentic loop and returns its final response. Any file writes the sub-agent proposes " +
			"are automatically included in the current run's proposals and shown in the diff view.",
		Properties: map[string]ToolParam{
			"task":     {Type: "string", Description: "The specific task for the sub-agent to complete."},
			"role":     {Type: "string", Description: "Tool access role: 'explorer' (read-only search/file/web), 'planner' (explorer + bash), 'coder' (full tools, can propose writes). Default: coder"},
			"model_id": {Type: "string", Description: "Model to use. Defaults to the current model."},
		},
		Required: []string{"task"},
	},
}

// activeToolsKey is the context key for the active tool set.
type activeToolsKey struct{}

// WithActiveTools returns a context carrying the given tool definitions.
// Adapters call ActiveToolsFromContext to retrieve the set for this run.
// Passing nil is a no-op (nil means "not set"); pass an empty slice to
// explicitly indicate zero active tools.
func WithActiveTools(ctx context.Context, defs []ToolDef) context.Context {
	if defs == nil {
		return ctx
	}
	return context.WithValue(ctx, activeToolsKey{}, defs)
}

// ActiveToolsFromContext returns the tool definitions stored in ctx, or Definitions
// if no active set was provided. An empty slice means zero tools are active.
func ActiveToolsFromContext(ctx context.Context) []ToolDef {
	if v, ok := ctx.Value(activeToolsKey{}).([]ToolDef); ok {
		return v
	}
	return Definitions
}

// bashPrefixesKey is the context key for the bash command prefix allowlist.
type bashPrefixesKey struct{}

// WithBashPrefixes returns a context carrying the given bash prefix allowlist.
// When set, ExecuteBash will only run commands whose prefix matches one of these
// patterns (e.g. "go test *", "go build *"). nil or empty means unrestricted.
func WithBashPrefixes(ctx context.Context, prefixes []string) context.Context {
	return context.WithValue(ctx, bashPrefixesKey{}, prefixes)
}

// BashPrefixesFromContext returns the bash prefix allowlist stored in ctx,
// or nil if no restriction was set.
func BashPrefixesFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(bashPrefixesKey{}).([]string)
	return v
}

// MCPDispatcher is a function that executes one MCP tool call.
// args values are strings to match Errata's DispatchTool convention.
type MCPDispatcher func(args map[string]string) string

// mcpDispatchersKey is the context key for the MCP dispatcher table.
type mcpDispatchersKey struct{}

// WithMCPDispatchers returns a context carrying the MCP dispatcher table.
// Called at startup after MCP servers are launched.
func WithMCPDispatchers(ctx context.Context, dispatchers map[string]MCPDispatcher) context.Context {
	return context.WithValue(ctx, mcpDispatchersKey{}, dispatchers)
}

// MCPDispatchersFromContext returns the MCP dispatcher table stored in ctx,
// or nil if no dispatchers were registered (MCP not configured).
func MCPDispatchersFromContext(ctx context.Context) map[string]MCPDispatcher {
	v, _ := ctx.Value(mcpDispatchersKey{}).(map[string]MCPDispatcher)
	return v
}

// ─── WorkDir support (per-model filesystem isolation) ─────────────────────────

// workDirKey is the context key for the per-model working directory override.
type workDirKey struct{}

// WithWorkDir returns a context carrying the given working directory override.
// Executor functions resolve paths relative to this directory instead of os.Getwd().
func WithWorkDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, workDirKey{}, dir)
}

// WorkDirFromContext returns the working directory override stored in ctx,
// or "" if no override was set (fall back to os.Getwd()).
func WorkDirFromContext(ctx context.Context) string {
	v, _ := ctx.Value(workDirKey{}).(string)
	return v
}

// directWritesKey is the context key for direct-write mode.
type directWritesKey struct{}

// WithDirectWrites returns a context that enables direct-write mode.
// When enabled, write_file and edit_file write to disk immediately instead of
// queuing proposals. Used in headless mode with per-model worktrees.
func WithDirectWrites(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, directWritesKey{}, enabled)
}

// DirectWriteFromContext reports whether direct-write mode is enabled in ctx.
// Returns false if not set (queue writes as proposals — TUI default).
func DirectWriteFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(directWritesKey{}).(bool)
	return v
}

// ─── Max steps support ────────────────────────────────────────────────────────

// maxStepsKey is the context key for the maximum agentic loop iterations.
type maxStepsKey struct{}

// WithMaxSteps returns a context carrying the given max steps limit.
// Adapter loops call MaxStepsFromContext to enforce the limit.
func WithMaxSteps(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, maxStepsKey{}, n)
}

// MaxStepsFromContext returns the max steps limit stored in ctx.
// Returns 0 if no limit was set (unlimited).
func MaxStepsFromContext(ctx context.Context) int {
	v, _ := ctx.Value(maxStepsKey{}).(int)
	return v
}

// ─── Sub-agent support ────────────────────────────────────────────────────────

// Sub-agent role names control which tools a spawned sub-agent can access.
const (
	// RoleExplorer provides read-only search, file, and web tools.
	// Use for tasks that only need to gather information.
	RoleExplorer = "explorer"
	// RolePlanner provides explorer tools plus bash.
	// Use for tasks that need to explore and run commands but not write files.
	RolePlanner = "planner"
	// RoleCoder provides all active parent tools (default role).
	// The sub-agent can propose file writes that bubble up to the parent.
	RoleCoder = "coder"
	// RoleFull is an alias for RoleCoder.
	RoleFull = "full"
)

// explorerToolNames is the allowlist for the explorer role.
var explorerToolNames = map[string]bool{
	ListDirToolName:   true,
	SearchFilesName:   true,
	SearchCodeName:    true,
	ReadToolName:      true,
	WebFetchToolName:  true,
	WebSearchToolName: true,
}

// ToolsForRole returns the subset of tool definitions allowed for the given role.
// parentDefs is the active tool set from the parent context (returned as-is for
// coder/full or unknown roles). Explorer and planner roles filter from the
// canonical Definitions list so they always get the read-only or read+bash sets,
// regardless of what the parent has enabled.
func ToolsForRole(role string, parentDefs []ToolDef) []ToolDef {
	switch role {
	case RoleExplorer:
		var out []ToolDef
		for _, d := range Definitions {
			if explorerToolNames[d.Name] {
				out = append(out, d)
			}
		}
		return out
	case RolePlanner:
		var out []ToolDef
		for _, d := range Definitions {
			if explorerToolNames[d.Name] || d.Name == BashToolName {
				out = append(out, d)
			}
		}
		return out
	default: // RoleCoder, RoleFull, or unknown — inherit all parent tools
		return parentDefs
	}
}

// SubagentDispatcher is a function that spawns a sub-agent to complete a task.
// args contains the spawn_agent tool arguments (task, role, model_id).
// It returns the sub-agent's text response, any proposed writes to bubble up,
// and an error message string (empty on success).
type SubagentDispatcher func(ctx context.Context, args map[string]string) (text string, writes []FileWrite, errMsg string)

// subagentDispatcherKey is the context key for the sub-agent dispatcher.
type subagentDispatcherKey struct{}

// WithSubagentDispatcher returns a context carrying the given SubagentDispatcher.
// Called when building the run context before passing to runner.RunAll.
func WithSubagentDispatcher(ctx context.Context, d SubagentDispatcher) context.Context {
	return context.WithValue(ctx, subagentDispatcherKey{}, d)
}

// SubagentDispatcherFromContext returns the SubagentDispatcher stored in ctx,
// or nil if no dispatcher was registered.
func SubagentDispatcherFromContext(ctx context.Context) SubagentDispatcher {
	v, _ := ctx.Value(subagentDispatcherKey{}).(SubagentDispatcher)
	return v
}

// subagentDepthKey is the context key for the current sub-agent recursion depth.
type subagentDepthKey struct{}

// WithSubagentDepth returns a context with the given sub-agent recursion depth.
// The top-level run always starts at depth 0.
func WithSubagentDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, subagentDepthKey{}, depth)
}

// SubagentDepthFromContext returns the current sub-agent recursion depth from ctx.
// Returns 0 if not set (top-level run).
func SubagentDepthFromContext(ctx context.Context) int {
	v, _ := ctx.Value(subagentDepthKey{}).(int)
	return v
}

// systemPromptExtraKey is the context key for per-run system prompt extra text.
type systemPromptExtraKey struct{}

// WithSystemPromptExtra returns a context carrying the given system prompt extra text.
// Adapters read this via SystemPromptSuffix.
func WithSystemPromptExtra(ctx context.Context, s string) context.Context {
	return context.WithValue(ctx, systemPromptExtraKey{}, s)
}

// SystemPromptExtraFromContext returns the system prompt extra text stored in ctx.
// The bool is false if no value was set via WithSystemPromptExtra.
func SystemPromptExtraFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(systemPromptExtraKey{}).(string)
	return v, ok
}

// toolGuidanceMapKey is the context key for per-tool guidance overrides.
type toolGuidanceMapKey struct{}

// WithToolGuidanceMap returns a context carrying the per-tool guidance map.
// effectiveGuidanceForCtx reads this to override code-default guidance per tool.
// A nil map means "use code defaults for all tools."
func WithToolGuidanceMap(ctx context.Context, m map[string]string) context.Context {
	if m == nil {
		return ctx
	}
	return context.WithValue(ctx, toolGuidanceMapKey{}, m)
}

// ToolGuidanceMapFromContext returns the per-tool guidance map stored in ctx,
// or nil if no map was set via WithToolGuidanceMap.
func ToolGuidanceMapFromContext(ctx context.Context) map[string]string {
	v, _ := ctx.Value(toolGuidanceMapKey{}).(map[string]string)
	return v
}

// ActiveDefinitions returns the subset of Definitions not in disabled.
// An empty or nil disabled map returns all Definitions unchanged.
func ActiveDefinitions(disabled map[string]bool) []ToolDef {
	if len(disabled) == 0 {
		return Definitions
	}
	out := make([]ToolDef, 0, len(Definitions))
	for _, d := range Definitions {
		if !disabled[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// DefinitionsAllowed returns tool definitions filtered by allowlist (if non-nil)
// and minus any disabled tools. A nil allowlist means all Definitions are candidates;
// a non-nil empty allowlist means zero tools (the section was present but empty).
// This combines recipe-level tool allowlist filtering with session-level disabling.
func DefinitionsAllowed(allowlist []string, disabled map[string]bool) []ToolDef {
	candidates := Definitions
	if allowlist != nil {
		set := make(map[string]bool, len(allowlist))
		for _, n := range allowlist {
			set[n] = true
		}
		out := make([]ToolDef, 0, len(allowlist))
		for _, d := range Definitions {
			if set[d.Name] {
				out = append(out, d)
			}
		}
		candidates = out
	}
	if len(disabled) == 0 {
		return candidates
	}
	out := make([]ToolDef, 0, len(candidates))
	for _, d := range candidates {
		if !disabled[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// FilterDefs returns the subset of defs not in disabled.
// Used to apply the same disabled-tool filter to MCP or other dynamic tool sets.
func FilterDefs(defs []ToolDef, disabled map[string]bool) []ToolDef {
	if len(disabled) == 0 {
		return defs
	}
	out := make([]ToolDef, 0, len(defs))
	for _, d := range defs {
		if !disabled[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// ApplyDescriptions returns a copy of defs with descriptions replaced from descs.
// Tool names not present in descs, or with empty values, are left unchanged.
func ApplyDescriptions(defs []ToolDef, descs map[string]string) []ToolDef {
	if len(descs) == 0 {
		return defs
	}
	out := make([]ToolDef, len(defs))
	copy(out, defs)
	for i := range out {
		if d, ok := descs[out[i].Name]; ok && d != "" {
			out[i].Description = d
		}
	}
	return out
}
