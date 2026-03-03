package tools

import (
	"context"
	"strings"
)

// DefaultToolGuidance returns the built-in tool-use guidance text.
// Useful for documentation and as a starting point for customization.
func DefaultToolGuidance() string { return toolUseGuidance }

// guidanceLine is a single guidance instruction tagged with the tool names it
// applies to. An empty tools slice means the line is always included.
type guidanceLine struct {
	tools []string // tool names this line requires (empty = always include)
	text  string
}

// defaultGuidanceLines is the structured source of truth for tool-use guidance.
// Each entry is tagged with the tool name(s) that must be active for the line
// to appear in the system prompt.
var defaultGuidanceLines = []guidanceLine{
	{[]string{ListDirToolName}, "- Use list_directory to explore the project structure before reading specific files."},
	{[]string{SearchFilesName}, "- Use search_files to find files by name pattern (e.g. search_files(\"**/*.go\"))."},
	{[]string{SearchCodeName}, "- Use search_code to find where a function, type, or string is defined or used."},
	{[]string{ReadToolName}, "- Use read_file only after you know which file you need. For large files, use offset and limit to page through content."},
	{[]string{EditToolName, WriteToolName}, "- Use edit_file for targeted changes to existing files (replaces an exact string). Use write_file only for new files or complete rewrites."},
	{[]string{BashToolName}, "- Use bash to run tests, builds, or any shell command; always provide a clear description."},
	{[]string{WebFetchToolName}, "- Use web_fetch to read documentation, GitHub issues, package READMEs, or any public URL."},
	{[]string{WebSearchToolName, WebFetchToolName}, "- Use web_search for quick factual lookups (definitions, Wikipedia summaries). For specific URLs, use web_fetch directly."},
	{[]string{WriteToolName, EditToolName}, "- write_file and edit_file proposals are NOT written to disk immediately — they are queued and applied only if the user selects your response."},
}

// toolUseGuidance is the full unfiltered guidance text, computed from
// defaultGuidanceLines at init time. Preserved for DefaultToolGuidance() and
// backward compatibility with callers that don't have a context.
var toolUseGuidance string

func init() {
	lines := make([]string, len(defaultGuidanceLines))
	for i, g := range defaultGuidanceLines {
		lines[i] = g.text
	}
	toolUseGuidance = "\nTool use guidance:\n" + strings.Join(lines, "\n") + "\n"
}

// buildGuidance returns the guidance text filtered to only include lines whose
// tagged tools overlap with activeNames. An empty map means "zero tools active"
// and returns no guidance.
func buildGuidance(activeNames map[string]bool) string {
	if len(activeNames) == 0 {
		return ""
	}
	var lines []string
	for _, g := range defaultGuidanceLines {
		if len(g.tools) == 0 {
			lines = append(lines, g.text)
			continue
		}
		for _, t := range g.tools {
			if activeNames[t] {
				lines = append(lines, g.text)
				break
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\nTool use guidance:\n" + strings.Join(lines, "\n") + "\n"
}

// spawnAgentGuidance is appended to toolUseGuidance only when SubagentEnabled is true.
const spawnAgentGuidance = `- Use spawn_agent to delegate a focused sub-task to another agent. Specify a role ('explorer' for read-only, 'planner' for read+bash, 'coder' for full tools). Sub-agent writes bubble up automatically.`

// effectiveGuidance returns toolUseGuidance with spawn_agent line
// included only when enabled. Used when no context is available (e.g. tests).
func effectiveGuidance() string {
	base := toolUseGuidance
	if SubagentEnabled {
		return base + spawnAgentGuidance + "\n"
	}
	return base
}

// effectiveGuidanceForCtx returns guidance filtered to active tools from ctx.
// Custom guidance from context is never filtered — the user wrote it
// intentionally and it may reference tools by any name.
func effectiveGuidanceForCtx(ctx context.Context) string {
	if override, ok := ToolGuidanceFromContext(ctx); ok && override != "" {
		base := override
		if SubagentEnabled {
			return base + spawnAgentGuidance + "\n"
		}
		return base
	}

	active := ActiveToolsFromContext(ctx)
	nameSet := make(map[string]bool, len(active))
	for _, d := range active {
		nameSet[d.Name] = true
	}
	base := buildGuidance(nameSet)
	if SubagentEnabled {
		if nameSet[SpawnAgentToolName] {
			return base + spawnAgentGuidance + "\n"
		}
	}
	return base
}

// SystemPromptGuidance returns the fixed tool-use guidance text.
// This is the same guidance as in SystemPromptSuffix but without the
// user-authored extra.
func SystemPromptGuidance() string {
	return effectiveGuidance()
}

// SystemPromptSuffix returns guidance text appended to each adapter's system prompt
// so models understand how to use the available tool set effectively.
// When ctx carries an active tool set (via WithActiveTools), only guidance lines
// relevant to those tools are included. If no active tools are in context, the
// full unfiltered guidance is returned for backward compatibility.
// If WithSystemPromptExtra was called on ctx, that text is appended after the
// built-in guidance so project-specific context reaches every model.
func SystemPromptSuffix(ctx context.Context) string {
	g := effectiveGuidanceForCtx(ctx)
	if extra, ok := SystemPromptExtraFromContext(ctx); ok && extra != "" {
		return g + "\n" + extra
	}
	return g
}
