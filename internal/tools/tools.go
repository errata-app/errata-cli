// Package tools defines the canonical tool schemas and file I/O executors.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/sync/singleflight"

	"github.com/suarezc/errata/internal/sandbox"
)

// webFetchGroup deduplicates concurrent web_fetch calls for the same URL.
// When two models request the same URL simultaneously, only one HTTP request
// is made and both receive the identical result, preventing rate-limiting and
// ensuring consistent content across models.
var webFetchGroup singleflight.Group

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

// SubagentEnabled gates all user-facing sub-agent functionality.
// When false, spawn_agent is excluded from Definitions, the config panel
// omits the sub-agent section, and dispatcher wiring is skipped.
// Set to true to re-enable the feature.
const SubagentEnabled = false

func init() {
	if !SubagentEnabled {
		filtered := Definitions[:0]
		for _, d := range Definitions {
			if d.Name != SpawnAgentToolName {
				filtered = append(filtered, d)
			}
		}
		Definitions = filtered
	}
}

// maxReadLines is the hard cap on lines returned by ExecuteRead.
const maxReadLines = 2000

// searchCommandTimeout is the maximum time allowed for search_code subprocess execution.
const searchCommandTimeout = 30 * time.Second

// defaultBashTimeout is the production timeout for bash tool execution.
const defaultBashTimeout = 2 * time.Minute

// bashTimeoutOverride, when > 0, overrides the default bash timeout.
// Set via SetBashTimeout (recipe ## Constraints bash_timeout:).
var bashTimeoutOverride time.Duration

// SetBashTimeout overrides the default bash tool timeout.
// Pass 0 to reset to the default.
func SetBashTimeout(d time.Duration) { bashTimeoutOverride = d }

// allowLocalFetch controls whether web_fetch may target localhost URLs.
// Set via SetAllowLocalFetch (recipe ## Sandbox allow_local_fetch:).
var allowLocalFetch bool

// SetAllowLocalFetch enables or disables web_fetch for localhost URLs.
func SetAllowLocalFetch(b bool) { allowLocalFetch = b }

// bashTimeout returns the effective bash timeout.
func bashTimeout() time.Duration {
	if bashTimeoutOverride > 0 {
		return bashTimeoutOverride
	}
	return defaultBashTimeout
}

// bashOutputLimit is the maximum bytes returned from a bash tool call.
const bashOutputLimit = 10_000

// FileWrite is a proposed file write intercepted from an agent's tool call.
// It lives here (not in models) to break the import cycle:
// tools → (stdlib only), models → tools.
type FileWrite struct {
	Path    string `json:"path"`
	Content string `json:"content"`
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
			"model_id": {Type: "string", Description: "Model to use. Defaults to ERRATA_SUBAGENT_MODEL or the current model."},
		},
		Required: []string{"task"},
	},
}

// activeToolsKey is the context key for the active tool set.
type activeToolsKey struct{}

// WithActiveTools returns a context carrying the given tool definitions.
// Adapters call ActiveToolsFromContext to retrieve the set for this run.
func WithActiveTools(ctx context.Context, defs []ToolDef) context.Context {
	return context.WithValue(ctx, activeToolsKey{}, defs)
}

// ActiveToolsFromContext returns the tool definitions stored in ctx, or Definitions
// if no active set was provided.
func ActiveToolsFromContext(ctx context.Context) []ToolDef {
	if v, ok := ctx.Value(activeToolsKey{}).([]ToolDef); ok && len(v) > 0 {
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

// matchBashPrefix reports whether command matches the given prefix pattern.
// A trailing "*" (with optional spaces) is stripped from pattern; the remaining
// text must be a prefix of command (case-sensitive, no leading-space trim).
func matchBashPrefix(command, pattern string) bool {
	prefix := strings.TrimRight(pattern, "* ")
	return strings.HasPrefix(command, prefix)
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

// ─── Seed support ─────────────────────────────────────────────────────────────

// seedKey is the context key for the pseudorandom seed.
type seedKey struct{}

// WithSeed returns a context carrying the given seed value.
// Adapters call SeedFromContext to retrieve it for API calls.
func WithSeed(ctx context.Context, seed int64) context.Context {
	return context.WithValue(ctx, seedKey{}, seed)
}

// SeedFromContext returns the seed stored in ctx and true,
// or (0, false) if no seed was set.
func SeedFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(seedKey{}).(int64)
	return v, ok
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
// and minus any disabled tools. If allowlist is nil, all Definitions are candidates.
// This combines recipe-level tool allowlist filtering with session-level disabling.
func DefinitionsAllowed(allowlist []string, disabled map[string]bool) []ToolDef {
	candidates := Definitions
	if len(allowlist) > 0 {
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

// systemPromptExtra holds optional user-supplied text appended after the
// built-in tool guidance. Set once at startup via SetSystemPromptExtra.
var systemPromptExtra string

// SetSystemPromptExtra stores additional text to be appended to every adapter's
// system prompt. Call once at startup (e.g. from ERRATA_SYSTEM_PROMPT).
// Subsequent calls overwrite the previous value.
func SetSystemPromptExtra(s string) { systemPromptExtra = s }

// toolUseGuidance is the fixed guidance text that teaches models how to use tools.
const toolUseGuidance = `
Tool use guidance:
- Use list_directory to explore the project structure before reading specific files.
- Use search_files to find files by name pattern (e.g. search_files("**/*.go")).
- Use search_code to find where a function, type, or string is defined or used.
- Use read_file only after you know which file you need. For large files, use offset and limit to page through content.
- Use edit_file for targeted changes to existing files (replaces an exact string). Use write_file only for new files or complete rewrites.
- Use bash to run tests, builds, or any shell command; always provide a clear description.
- Use web_fetch to read documentation, GitHub issues, package READMEs, or any public URL.
- Use web_search for quick factual lookups (definitions, Wikipedia summaries). For specific URLs, use web_fetch directly.
- write_file and edit_file proposals are NOT written to disk immediately — they are queued and applied only if the user selects your response.
- Use spawn_agent to delegate a focused sub-task to another agent. Specify a role ('explorer' for read-only, 'planner' for read+bash, 'coder' for full tools). Sub-agent writes bubble up automatically.
`

// SystemPromptGuidance returns the fixed tool-use guidance text.
// This is the same guidance as in SystemPromptSuffix but without the
// user-authored extra. Used by adapters that read their system prompt
// from a PromptPayload instead.
func SystemPromptGuidance() string {
	return toolUseGuidance
}

// SystemPromptSuffix returns guidance text appended to each adapter's system prompt
// so models understand how to use the available tool set effectively.
// If SetSystemPromptExtra has been called, that text is appended after the
// built-in guidance so project-specific context reaches every model.
func SystemPromptSuffix() string {
	if systemPromptExtra == "" {
		return toolUseGuidance
	}
	return toolUseGuidance + "\n" + systemPromptExtra
}

// validatePath resolves path relative to cwd and rejects paths that escape it.
// Returns (absolutePath, cwd, "") on success or ("", "", errorMessage) on failure.
func validatePath(path string) (abs, cwd, errMsg string) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Sprintf("[error: cannot determine working directory: %v]", err)
	}
	abs, err = filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Sprintf("[error: invalid path %q: %v]", path, err)
	}
	cwdClean := filepath.Clean(cwd) + string(filepath.Separator)
	absClean := filepath.Clean(abs)
	if !strings.HasPrefix(absClean+string(filepath.Separator), cwdClean) {
		return "", "", fmt.Sprintf("[error: path %q is outside the working directory]", path)
	}
	return abs, cwd, ""
}

// ExecuteRead reads a file relative to cwd.
// offset is 1-indexed (0 or 1 both mean "start at line 1").
// limit is the max lines to return (0 means use maxReadLines).
// Returns the file content, or an error string the model can see.
// Refuses paths that escape the working directory.
func ExecuteRead(path string, offset, limit int) string {
	abs, _, errMsg := validatePath(path)
	if errMsg != "" {
		return errMsg
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[error: file not found: %q]", path)
		}
		return fmt.Sprintf("[error: %v]", err)
	}

	// Normalize offset and limit.
	if offset <= 0 {
		offset = 1
	}
	if limit <= 0 || limit > maxReadLines {
		limit = maxReadLines
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)

	start := offset - 1 // convert to 0-indexed
	if start >= total {
		return fmt.Sprintf("[error: offset %d exceeds file length (%d lines)]", offset, total)
	}

	end := min(start+limit, total)

	result := strings.Join(lines[start:end], "\n")

	// Count remaining real lines (ignore the trailing empty element produced by a
	// trailing newline when strings.Split is applied to it).
	remaining := total - end
	if remaining > 0 && lines[total-1] == "" {
		remaining--
	}
	if remaining > 0 {
		result += fmt.Sprintf("\n[... %d lines omitted. Use offset=%d to continue reading.]", remaining, end+1)
	}

	return result
}

// ExecuteEditFile reads path, replaces exactly one occurrence of oldString with newString,
// and returns (newContent, ""). Returns ("", errorMessage) on failure.
// The caller is responsible for queuing the result as a ProposedWrite.
// Refuses paths that escape the working directory.
func ExecuteEditFile(path, oldString, newString string) (string, string) {
	abs, _, errMsg := validatePath(path)
	if errMsg != "" {
		return "", errMsg
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Sprintf("[error: file not found: %q]", path)
		}
		return "", fmt.Sprintf("[error: %v]", err)
	}

	content := string(data)
	count := strings.Count(content, oldString)
	switch count {
	case 0:
		return "", fmt.Sprintf("[error: old_string not found in %q]", path)
	case 1:
		return strings.Replace(content, oldString, newString, 1), ""
	default:
		return "", fmt.Sprintf("[error: old_string is ambiguous (%d matches) in %q — add more surrounding context]", count, path)
	}
}

// ApplyWrites writes each FileWrite to disk, creating parent directories as needed.
// All paths are validated against the current working directory; writes that
// would escape it via ".." sequences are rejected with an error.
func ApplyWrites(writes []FileWrite) error {
	for _, fw := range writes {
		abs, _, errMsg := validatePath(fw.Path)
		if errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return fmt.Errorf("mkdir for %q: %w", fw.Path, err)
		}
		if err := os.WriteFile(abs, []byte(fw.Content), 0o644); err != nil { //nolint:gosec // G306: user code files should be world-readable
			return fmt.Errorf("write %q: %w", fw.Path, err)
		}
	}
	return nil
}

// ExecuteListDirectory lists a directory tree up to depth levels deep.
// path is relative to cwd. Returns an indented tree string, or an error message.
// Directories are suffixed with /. depth is clamped to [1, 5].
// File entries include a human-readable size hint (e.g. "handlers.go  (12 KB)").
//
// DERIVED: BFS indented-tree design from codex list_dir.rs
func ExecuteListDirectory(path string, depth int) string {
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}

	abs, _, errMsg := validatePath(path)
	if errMsg != "" {
		return errMsg
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[error: path not found: %q]", path)
		}
		return fmt.Sprintf("[error: %v]", err)
	}
	if !info.IsDir() {
		return fmt.Sprintf("[error: %q is not a directory]", path)
	}

	var lines []string
	collectDirEntries(abs, 0, depth, &lines)
	if len(lines) == 0 {
		return "(empty directory)"
	}
	return strings.Join(lines, "\n")
}

// collectDirEntries recursively collects directory entries into lines.
// File entries include a size hint; directory entries do not.
func collectDirEntries(dir string, currentDepth, maxDepth int, lines *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	indent := strings.Repeat("  ", currentDepth)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			*lines = append(*lines, indent+name+"/")
			if currentDepth+1 < maxDepth {
				collectDirEntries(filepath.Join(dir, name), currentDepth+1, maxDepth, lines)
			}
		} else {
			info, infoErr := entry.Info()
			if infoErr == nil {
				*lines = append(*lines, indent+name+"  ("+formatFileSize(info.Size())+")")
			} else {
				*lines = append(*lines, indent+name)
			}
		}
	}
}

// formatFileSize returns a compact human-readable file size string.
func formatFileSize(bytes int64) string {
	if bytes < 1024 {
		return "< 1 KB"
	}
	kb := (bytes + 512) / 1024 // round to nearest KB
	if kb < 1024 {
		return fmt.Sprintf("%d KB", kb)
	}
	mb := (bytes + 512*1024) / (1024 * 1024)
	return fmt.Sprintf("%d MB", mb)
}

// ExecuteSearchFiles finds files matching a glob pattern relative to basePath.
// basePath is relative to cwd. Returns newline-separated matching paths, or an error message.
func ExecuteSearchFiles(pattern, basePath string) string {
	if basePath == "" {
		basePath = "."
	}
	absBase, cwd, errMsg := validatePath(basePath)
	if errMsg != "" {
		return errMsg
	}

	info, err := os.Stat(absBase)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("[error: base_path %q is not a directory]", basePath)
	}

	var matches []string
	err = filepath.Walk(absBase, func(fullPath string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // intentional: skip unreadable entries, continue walking
		}
		rel, _ := filepath.Rel(absBase, fullPath)
		rel = filepath.ToSlash(rel)
		matched, matchErr := matchGlob(pattern, rel)
		if matchErr != nil {
			return matchErr
		}
		if matched && !fi.IsDir() {
			// Return path relative to cwd for consistent output
			cwdRel, _ := filepath.Rel(cwd, fullPath)
			matches = append(matches, cwdRel)
		}
		return nil
	})
	if err != nil {
		return fmt.Sprintf("[error: invalid pattern %q: %v]", pattern, err)
	}

	if len(matches) == 0 {
		return "(no matches)"
	}
	return strings.Join(matches, "\n")
}

// ExecuteBash runs command via the system shell (sh -c) with a 2-minute timeout.
// stdout and stderr are combined; output is capped at bashOutputLimit bytes.
// If ctx carries a bash prefix allowlist (via WithBashPrefixes), the command must
// match one of the allowed prefixes or an error string is returned instead.
// If ctx carries a sandbox Config (via sandbox.WithConfig), the subprocess is
// wrapped with OS-level sandboxing appropriate for the current platform.
func ExecuteBash(ctx context.Context, command string) string {
	if prefixes := BashPrefixesFromContext(ctx); len(prefixes) > 0 {
		allowed := false
		for _, p := range prefixes {
			if matchBashPrefix(command, p) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Sprintf("[bash: command not allowed by recipe tools restriction: %q]", command)
		}
	}

	timeout := bashTimeout()
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Build subprocess, wrapping with OS-level sandbox when configured.
	var cmd *exec.Cmd
	if sbCfg, ok := sandbox.ConfigFromContext(ctx); ok && sbCfg.Active() {
		if sbCfg.ProjectRoot == "" {
			sbCfg.ProjectRoot, _ = os.Getwd()
		}
		cmd = sandbox.BuildCmd(timeoutCtx, sbCfg, "sh", "-c", command)
	} else {
		cmd = exec.CommandContext(timeoutCtx, "sh", "-c", command)
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if runErr := cmd.Run(); runErr != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			output := out.String()
			if output == "" {
				return fmt.Sprintf("[error: command timed out after %s]", timeout)
			}
			return capOutput(output) + fmt.Sprintf("\n[error: command timed out after %s]", timeout)
		}
		// Non-zero exit is normal (e.g. test failures); return output + exit info.
		output := out.String()
		if output == "" {
			return fmt.Sprintf("[exit: %v]", runErr)
		}
		return capOutput(output) + fmt.Sprintf("\n[exit: %v]", runErr)
	}

	output := out.String()
	if output == "" {
		return "(no output)"
	}
	return capOutput(output)
}

// capOutput truncates output at bashOutputLimit bytes with a notice.
func capOutput(s string) string {
	if len(s) <= bashOutputLimit {
		return strings.TrimRight(s, "\n")
	}
	return strings.TrimRight(s[:bashOutputLimit], "\n") +
		fmt.Sprintf("\n[truncated: output exceeded %d bytes]", bashOutputLimit)
}

// webFetchOutputLimit is the maximum bytes returned from a web_fetch call.
const webFetchOutputLimit = 50_000

// webFetchTimeout is the HTTP request timeout for web_fetch.
const webFetchTimeout = 30 * time.Second

// webFetchUserAgent mimics a real browser to avoid bot-detection pages that
// serve stripped-down content to non-browser user agents.
const webFetchUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// ExecuteWebFetch fetches a URL and returns cleaned text content.
// HTML pages are stripped to plain text. Output is capped at webFetchOutputLimit bytes.
// Concurrent calls for the same URL are deduplicated via singleflight — only one
// HTTP request goes out, and all callers receive the identical result.
func ExecuteWebFetch(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Sprintf("[error: invalid URL: %v]", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Sprintf("[error: only http/https URLs are supported, got %q]", u.Scheme)
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		if !allowLocalFetch {
			return "[error: fetching localhost URLs is disabled; set allow_local_fetch: true in recipe ## Sandbox to enable]"
		}
	}

	result, _, _ := webFetchGroup.Do(rawURL, func() (any, error) {
		return doWebFetch(rawURL), nil
	})
	s, _ := result.(string)
	return s
}

// doWebFetch performs the actual HTTP fetch. Called via singleflight so
// only one in-flight request per URL exists at any given time.
func doWebFetch(rawURL string) string {
	client := &http.Client{Timeout: webFetchTimeout}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Sprintf("[error: could not create request: %v]", err)
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("[error: fetch failed: %v]", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Sprintf("[error: HTTP %d from %s]", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(webFetchOutputLimit*4)))
	if err != nil {
		return fmt.Sprintf("[error: reading response: %v]", err)
	}

	var text string
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		text = htmlToText(string(body))
	} else {
		text = string(body)
	}

	text = strings.TrimSpace(text)
	if len(text) > webFetchOutputLimit {
		text = text[:webFetchOutputLimit] + fmt.Sprintf("\n[truncated: output exceeded %d bytes]", webFetchOutputLimit)
	}
	if text == "" {
		return "(empty response)"
	}
	return text
}

// htmlToText converts HTML to plain text by stripping tags and skipping
// script/style/head elements. Consecutive whitespace is collapsed.
func htmlToText(htmlContent string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	var sb strings.Builder
	skip := false
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return sb.String()
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, _ := tokenizer.TagName()
			switch string(tn) {
			case "script", "style", "head":
				skip = true
			}
		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			switch string(tn) {
			case "script", "style", "head":
				skip = false
			}
		case html.TextToken:
			if !skip {
				text := strings.TrimSpace(string(tokenizer.Text()))
				if text != "" {
					sb.WriteString(text)
					sb.WriteByte('\n')
				}
			}
		}
	}
}

// LoadDisabledTools reads the disabled-tool set from path.
// Returns an empty map and nil error if path is empty or the file does not exist (all tools enabled).
func LoadDisabledTools(path string) (map[string]bool, error) {
	if path == "" {
		return map[string]bool{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var payload struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if len(payload.Disabled) == 0 {
		return map[string]bool{}, nil
	}
	m := make(map[string]bool, len(payload.Disabled))
	for _, name := range payload.Disabled {
		m[name] = true
	}
	return m, nil
}

// SaveDisabledTools persists the disabled-tool set to path.
// If path is empty, disabled is nil, or disabled is empty, any existing file is removed.
func SaveDisabledTools(path string, disabled map[string]bool) error {
	if path == "" {
		return nil
	}
	if len(disabled) == 0 {
		_ = os.Remove(path)
		return nil
	}
	names := make([]string, 0, len(disabled))
	for name := range disabled {
		names = append(names, name)
	}
	sort.Strings(names)
	type payload struct {
		Disabled []string `json:"disabled"`
	}
	data, err := json.Marshal(payload{Disabled: names})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// webSearchTimeout is the HTTP request timeout for web_search queries.
const webSearchTimeout = 10 * time.Second

// webSearchOutputLimit is the maximum bytes returned from a web_search call.
const webSearchOutputLimit = 8_000

// webSearchAPIBaseOverride, when non-empty, replaces the DuckDuckGo API URL.
// Used in tests to point at a local HTTP server.
var webSearchAPIBaseOverride string

// SetWebSearchAPIBase overrides the DuckDuckGo API base URL (for tests).
// Pass "" to reset to the default.
func SetWebSearchAPIBase(u string) { webSearchAPIBaseOverride = u }

// webSearchAPIBase returns the DuckDuckGo API base URL.
func webSearchAPIBase() string {
	if webSearchAPIBaseOverride != "" {
		return strings.TrimRight(webSearchAPIBaseOverride, "/") + "/"
	}
	return "https://api.duckduckgo.com/"
}

// ddgResponse is the top-level DuckDuckGo instant answers API response.
type ddgResponse struct {
	AbstractText   string       `json:"AbstractText"`
	AbstractURL    string       `json:"AbstractURL"`
	AbstractSource string       `json:"AbstractSource"`
	Answer         string       `json:"Answer"`
	Definition     string       `json:"Definition"`
	DefinitionURL  string       `json:"DefinitionURL"`
	RelatedTopics  []ddgTopic   `json:"RelatedTopics"`
	Results        []ddgResult  `json:"Results"`
}

// ddgTopic is either a direct topic entry or a named group of sub-topics.
// A group has Name and Topics; a direct entry has Text and FirstURL.
type ddgTopic struct {
	Text     string     `json:"Text"`
	FirstURL string     `json:"FirstURL"`
	Name     string     `json:"Name"`
	Topics   []ddgTopic `json:"Topics"`
}

type ddgResult struct {
	Text     string `json:"Text"`
	FirstURL string `json:"FirstURL"`
}

// ExecuteWebSearch queries the DuckDuckGo instant answers API and returns
// a formatted plain-text result. Best for factual/definition queries.
// Not a full web index — for specific URLs, callers should prefer ExecuteWebFetch.
func ExecuteWebSearch(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "[error: query must not be empty]"
	}

	params := url.Values{
		"q":             []string{query},
		"format":        []string{"json"},
		"no_redirect":   []string{"1"},
		"no_html":       []string{"1"},
		"skip_disambig": []string{"1"},
	}
	fullURL := webSearchAPIBase() + "?" + params.Encode()

	client := &http.Client{Timeout: webSearchTimeout}
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Sprintf("[error: could not create request: %v]", err)
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("[error: search failed: %v]", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Sprintf("[error: HTTP %d from DuckDuckGo]", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(webSearchOutputLimit*4)))
	if err != nil {
		return fmt.Sprintf("[error: reading response: %v]", err)
	}

	var ddg ddgResponse
	if err := json.Unmarshal(body, &ddg); err != nil {
		return fmt.Sprintf("[error: parsing response: %v]", err)
	}

	return formatWebSearchResult(query, ddg)
}

// formatWebSearchResult renders a DuckDuckGo API response as readable plain text.
func formatWebSearchResult(query string, ddg ddgResponse) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[DuckDuckGo: %q]\n", query)

	empty := true

	if ddg.Answer != "" {
		sb.WriteString("\n")
		sb.WriteString(ddg.Answer)
		sb.WriteString("\n")
		empty = false
	}

	if ddg.AbstractText != "" {
		sb.WriteString("\n")
		sb.WriteString(ddg.AbstractText)
		if ddg.AbstractSource != "" {
			sb.WriteString(" (via " + ddg.AbstractSource + ")")
		}
		sb.WriteString("\n")
		if ddg.AbstractURL != "" {
			sb.WriteString("Source: " + ddg.AbstractURL + "\n")
		}
		empty = false
	}

	if ddg.Definition != "" {
		sb.WriteString("\nDefinition: " + ddg.Definition + "\n")
		if ddg.DefinitionURL != "" {
			sb.WriteString("Source: " + ddg.DefinitionURL + "\n")
		}
		empty = false
	}

	// Collect related topic lines, flattening groups.
	const maxTopics = 10
	var topicLines []string
	for _, t := range ddg.RelatedTopics {
		if len(topicLines) >= maxTopics {
			break
		}
		if t.Name != "" && len(t.Topics) > 0 {
			// Named group: emit a header then subtopics.
			topicLines = append(topicLines, "["+t.Name+"]")
			for _, sub := range t.Topics {
				if len(topicLines) >= maxTopics {
					break
				}
				if sub.Text != "" {
					line := "  • " + sub.Text
					if sub.FirstURL != "" {
						line += "  " + sub.FirstURL
					}
					topicLines = append(topicLines, line)
				}
			}
		} else if t.Text != "" {
			line := "• " + t.Text
			if t.FirstURL != "" {
				line += "  " + t.FirstURL
			}
			topicLines = append(topicLines, line)
		}
	}

	if len(topicLines) > 0 {
		sb.WriteString("\nRelated:\n")
		for _, line := range topicLines {
			sb.WriteString(line + "\n")
		}
		empty = false
	}

	if empty {
		return fmt.Sprintf(
			"(no instant answer found for %q)\n\n"+
				"DuckDuckGo instant answers cover factual/definition queries.\n"+
				"For code documentation, use web_fetch with the URL directly.",
			query,
		)
	}

	result := strings.TrimRight(sb.String(), "\n")
	if len(result) > webSearchOutputLimit {
		result = result[:webSearchOutputLimit] + "\n[truncated]"
	}
	return result
}

// matchGlob matches a slash-separated path against a glob pattern that may
// contain ** to match zero or more path segments. Single-segment wildcards
// (*, ?, [...]) use filepath.Match rules. ** must occupy a full path segment.
func matchGlob(pattern, path string) (bool, error) {
	p := filepath.ToSlash(pattern)
	f := filepath.ToSlash(path)
	return matchParts(strings.Split(p, "/"), strings.Split(f, "/"))
}

// matchParts is the recursive core of matchGlob.
func matchParts(pat, fp []string) (bool, error) {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			// ** matches zero or more segments: try every possible split point.
			for i := 0; i <= len(fp); i++ {
				if ok, err := matchParts(rest, fp[i:]); err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
		if len(fp) == 0 {
			return false, nil
		}
		ok, err := filepath.Match(pat[0], fp[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		pat, fp = pat[1:], fp[1:]
	}
	return len(fp) == 0, nil
}

// ExecuteSearchCode searches file contents for pattern using grep.
// path and fileGlob are optional; path defaults to ".".
// contextLines adds N lines of context before and after each match (grep -C N).
// Returns grep output (path:line:content format) or an error message.
//
// DERIVED: subprocess + 30s timeout pattern from codex grep_files.rs
func ExecuteSearchCode(pattern, path, fileGlob string, contextLines int) string {
	if path == "" {
		path = "."
	}
	absPath, _, errMsg := validatePath(path)
	if errMsg != "" {
		return errMsg
	}

	args := []string{"-rn"}
	if fileGlob != "" {
		args = append(args, "--include="+fileGlob)
	}
	if contextLines > 0 {
		args = append(args, fmt.Sprintf("-C%d", contextLines))
	}
	args = append(args, "--", pattern, absPath)

	ctx, cancel := context.WithTimeout(context.Background(), searchCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "grep", args...)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if runErr := cmd.Run(); runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "[error: search timed out after 30s]"
		}
		// grep exit code 1 means no matches — not an error.
		if out.Len() == 0 {
			return "(no matches)"
		}
	}

	output := out.String()
	// Make paths relative to cwd for cleaner output
	output = strings.ReplaceAll(output, absPath+string(filepath.Separator), "")
	output = strings.ReplaceAll(output, absPath, "")

	if output == "" {
		return "(no matches)"
	}
	return strings.TrimRight(output, "\n")
}
