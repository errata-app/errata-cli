package tools

import (
	"context"
	"strings"
)

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

// defaultGuidanceByTool maps each tool name to the code-default guidance text
// for that tool (first tagged line that mentions it). Used as the fallback when
// the per-tool guidance map is present but lacks an entry for a tool.
var defaultGuidanceByTool map[string]string

// toolUseGuidance is the full unfiltered guidance text, computed from
// defaultGuidanceLines at init time. Preserved for backward compatibility
// with callers that don't have a context.
var toolUseGuidance string

func init() {
	lines := make([]string, len(defaultGuidanceLines))
	for i, g := range defaultGuidanceLines {
		lines[i] = g.text
	}
	toolUseGuidance = "\nTool use guidance:\n" + strings.Join(lines, "\n") + "\n"

	// Build the per-tool default guidance map.
	defaultGuidanceByTool = make(map[string]string)
	for _, g := range defaultGuidanceLines {
		for _, t := range g.tools {
			if _, exists := defaultGuidanceByTool[t]; !exists {
				defaultGuidanceByTool[t] = g.text
			}
		}
	}
}

// spawnAgentGuidance is appended to toolUseGuidance only when SubagentEnabled is true.
const spawnAgentGuidance = `- Use spawn_agent to delegate a focused sub-task to another agent. Specify a role ('explorer' for read-only, 'planner' for read+bash, 'coder' for full tools). Sub-agent writes bubble up automatically.`

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

// buildGuidanceWithOverrides returns guidance text filtered by active tools,
// with per-tool overrides applied. For each default guidance line, if the
// override map has an entry for the first tagged tool, the override text is
// used (prefixed with "- "); otherwise the code default is used.
func buildGuidanceWithOverrides(activeNames map[string]bool, overrides map[string]string) string {
	if len(activeNames) == 0 {
		return ""
	}
	var lines []string
	seen := make(map[int]bool) // track which defaultGuidanceLines we've emitted
	for i, g := range defaultGuidanceLines {
		if len(g.tools) == 0 {
			lines = append(lines, g.text)
			seen[i] = true
			continue
		}
		// Check if any tagged tool is active.
		active := false
		for _, t := range g.tools {
			if activeNames[t] {
				active = true
				break
			}
		}
		if !active {
			continue
		}
		seen[i] = true
		// Check if the first tagged tool has an override.
		if override, ok := overrides[g.tools[0]]; ok {
			lines = append(lines, "- "+override)
		} else {
			lines = append(lines, g.text)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\nTool use guidance:\n" + strings.Join(lines, "\n") + "\n"
}

// effectiveGuidanceForCtx returns guidance filtered to active tools from ctx.
// When a per-tool guidance map is present in context, per-tool overrides are
// applied; tools without an override use code defaults. Filtering by active
// tools always applies.
func effectiveGuidanceForCtx(ctx context.Context) string {
	active := ActiveToolsFromContext(ctx)
	nameSet := make(map[string]bool, len(active))
	for _, d := range active {
		nameSet[d.Name] = true
	}

	perTool := ToolGuidanceMapFromContext(ctx)
	var base string
	if perTool != nil {
		base = buildGuidanceWithOverrides(nameSet, perTool)
	} else {
		base = buildGuidance(nameSet)
	}

	if SubagentEnabled {
		if nameSet[SpawnAgentToolName] {
			return base + spawnAgentGuidance + "\n"
		}
	}
	return base
}

// SystemPromptGuidance returns the fixed tool-use guidance text.
// This is the same guidance as in SystemPromptSuffix but without the
// user-authored extra. Returns full unfiltered guidance (no context).
func SystemPromptGuidance() string {
	base := toolUseGuidance
	if SubagentEnabled {
		return base + spawnAgentGuidance + "\n"
	}
	return base
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
