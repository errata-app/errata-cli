// Package tools defines the canonical tool schemas and file I/O executors.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ReadToolName  = "read_file"
	WriteToolName = "write_file"
)

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
