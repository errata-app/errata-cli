// Package tools defines the canonical tool schemas and file I/O executors.
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	ReadToolName    = "read_file"
	WriteToolName   = "write_file"
	ListDirToolName = "list_directory"
	SearchFilesName = "search_files"
	SearchCodeName  = "search_code"
	BashToolName    = "bash"
)

// searchCommandTimeout is the maximum time allowed for search_code subprocess execution.
const searchCommandTimeout = 30 * time.Second

// defaultBashTimeout is the production timeout for bash tool execution.
const defaultBashTimeout = 2 * time.Minute

// bashTimeout returns the effective bash timeout, allowing ERRATA_BASH_TIMEOUT
// to override (e.g. "2s") in tests without modifying global state.
func bashTimeout() time.Duration {
	if v := os.Getenv("ERRATA_BASH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultBashTimeout
}

// bashOutputLimit is the maximum bytes returned from a bash tool call.
const bashOutputLimit = 10_000

// FileWrite is a proposed file write intercepted from an agent's tool call.
// It lives here (not in models) to break the import cycle:
// tools → (stdlib only), models → tools.
type FileWrite struct {
	Path    string
	Content string
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
		Name:        ReadToolName,
		Description: "Read the contents of a file relative to the current working directory.",
		Properties: map[string]ToolParam{
			"path": {Type: "string", Description: "Relative path to the file"},
		},
		Required: []string{"path"},
	},
	{
		Name: WriteToolName,
		Description: "Propose writing content to a file relative to the current working directory. " +
			"The write will be applied only if the user selects this model's response.",
		Properties: map[string]ToolParam{
			"path":    {Type: "string", Description: "Relative path to the file"},
			"content": {Type: "string", Description: "Full file content to write"},
		},
		Required: []string{"path", "content"},
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
			"line numbers, and the matching lines. Use file_glob to filter by file type.",
		Properties: map[string]ToolParam{
			"pattern":   {Type: "string", Description: "Regex pattern to search for"},
			"path":      {Type: "string", Description: "File or directory to search, relative to cwd (default '.')"},
			"file_glob": {Type: "string", Description: "Optional filename filter, e.g. '*.go'"},
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

// SystemPromptSuffix returns guidance text appended to each adapter's system prompt
// so models understand how to use the available tool set effectively.
func SystemPromptSuffix() string {
	return `
Tool use guidance:
- Use list_directory to explore the project structure before reading specific files.
- Use search_files to find files by name pattern (e.g. search_files("**/*.go")).
- Use search_code to find where a function, type, or string is defined or used.
- Use read_file only after you know which file you need.
- Use bash to run tests, builds, or any shell command; always provide a clear description.
- write_file proposals are NOT written to disk immediately — they are queued and applied only if the user selects your response.
`
}

// ExecuteRead reads a file relative to cwd.
// Returns the file content, or an error string the model can see.
// Refuses paths that escape the working directory.
func ExecuteRead(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Sprintf("[error: cannot determine working directory: %v]", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Sprintf("[error: invalid path %q: %v]", path, err)
	}

	// Enforce cwd boundary.
	cwdClean := filepath.Clean(cwd) + string(filepath.Separator)
	absClean := filepath.Clean(abs)
	if !strings.HasPrefix(absClean+string(filepath.Separator), cwdClean) {
		return fmt.Sprintf("[error: path %q is outside the working directory]", path)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[error: file not found: %q]", path)
		}
		return fmt.Sprintf("[error: %v]", err)
	}
	return string(data)
}

// ApplyWrites writes each FileWrite to disk, creating parent directories as needed.
func ApplyWrites(writes []FileWrite) error {
	for _, fw := range writes {
		if err := os.MkdirAll(filepath.Dir(fw.Path), 0o755); err != nil {
			return fmt.Errorf("mkdir for %q: %w", fw.Path, err)
		}
		if err := os.WriteFile(fw.Path, []byte(fw.Content), 0o644); err != nil {
			return fmt.Errorf("write %q: %w", fw.Path, err)
		}
	}
	return nil
}

// ExecuteListDirectory lists a directory tree up to depth levels deep.
// path is relative to cwd. Returns an indented tree string, or an error message.
// Directories are suffixed with /. depth is clamped to [1, 5].
//
// DERIVED: BFS indented-tree design from codex list_dir.rs
func ExecuteListDirectory(path string, depth int) string {
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Sprintf("[error: cannot determine working directory: %v]", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Sprintf("[error: invalid path %q: %v]", path, err)
	}

	cwdClean := filepath.Clean(cwd) + string(filepath.Separator)
	absClean := filepath.Clean(abs)
	if !strings.HasPrefix(absClean+string(filepath.Separator), cwdClean) {
		return fmt.Sprintf("[error: path %q is outside the working directory]", path)
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
			*lines = append(*lines, indent+name)
		}
	}
}

// ExecuteSearchFiles finds files matching a glob pattern relative to basePath.
// basePath is relative to cwd. Returns newline-separated matching paths, or an error message.
func ExecuteSearchFiles(pattern, basePath string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Sprintf("[error: cannot determine working directory: %v]", err)
	}

	if basePath == "" {
		basePath = "."
	}
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return fmt.Sprintf("[error: invalid base_path %q: %v]", basePath, err)
	}

	cwdClean := filepath.Clean(cwd) + string(filepath.Separator)
	absClean := filepath.Clean(absBase)
	if !strings.HasPrefix(absClean+string(filepath.Separator), cwdClean) {
		return fmt.Sprintf("[error: base_path %q is outside the working directory]", basePath)
	}

	info, err := os.Stat(absBase)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("[error: base_path %q is not a directory]", basePath)
	}

	var matches []string
	err = filepath.Walk(absBase, func(fullPath string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
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
func ExecuteBash(command string) string {
	timeout := bashTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if runErr := cmd.Run(); runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
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
// Returns grep output (path:line:content format) or an error message.
//
// DERIVED: subprocess + 30s timeout pattern from codex grep_files.rs
func ExecuteSearchCode(pattern, path, fileGlob string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Sprintf("[error: cannot determine working directory: %v]", err)
	}

	if path == "" {
		path = "."
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Sprintf("[error: invalid path %q: %v]", path, err)
	}

	cwdClean := filepath.Clean(cwd) + string(filepath.Separator)
	absClean := filepath.Clean(absPath)
	if !strings.HasPrefix(absClean+string(filepath.Separator), cwdClean) {
		return fmt.Sprintf("[error: path %q is outside the working directory]", path)
	}

	args := []string{"-rn", "--", pattern, absPath}
	if fileGlob != "" {
		args = []string{"-rn", "--include=" + fileGlob, "--", pattern, absPath}
	}

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
