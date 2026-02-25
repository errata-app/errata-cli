// Package recipe parses and resolves Errata recipe.md configuration files.
//
// A recipe is a human-readable Markdown file that configures a reproducible
// comparison environment: which models to run, what tools they can use, how
// the agentic loop is constrained, and (in headless mode) what tasks to run.
//
// Discovery order (first match wins):
//  1. Explicit path passed to Discover
//  2. recipe.md in cwd
//  3. .errata/recipe.md in cwd
//  4. ~/.errata/default.recipe.md
//  5. Built-in compiled-in defaults
package recipe

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/suarezc/errata/internal/config"
)

//go:embed default.recipe.md
var defaultFS embed.FS

// ─── Data types ───────────────────────────────────────────────────────────────

// Recipe holds all settings parsed from a recipe.md file.
// Nil/zero values mean "not set" — ApplyTo leaves the corresponding
// config.Config field unchanged when a field is unset.
type Recipe struct {
	Name         string
	Models       []string        // nil = not set
	SystemPrompt string          // "" = not set
	Tools        *ToolsConfig    // nil = all tools enabled
	MCPServers   []MCPServerEntry
	ModelParams  ModelParamsConfig
	Constraints  ConstraintsConfig
	Context      ContextConfig
	SubAgent     SubAgentConfig
	Sandbox      SandboxConfig
	Tasks        []string
	Metadata     MetadataConfig
}

// ToolsConfig describes which tools are available in a recipe.
// When nil (section absent), all tools are enabled.
type ToolsConfig struct {
	// Allowlist is the explicit set of tool names available to models.
	// nil means all tools; non-nil means only these tools.
	Allowlist []string
	// BashPrefixes, if non-nil, restricts bash execution to commands whose
	// trimmed text starts with one of the listed prefix patterns.
	// nil (with bash in Allowlist) means all bash commands are allowed.
	BashPrefixes []string
}

// MCPServerEntry is one named MCP server subprocess.
type MCPServerEntry struct {
	Name    string // display name
	Command string // full command string including arguments
}

// ModelParamsConfig carries API sampling parameters.
// Pointer fields distinguish "not set" (nil) from "set to zero".
type ModelParamsConfig struct {
	Temperature *float64
	MaxTokens   *int
}

// ConstraintsConfig limits agentic loop execution.
type ConstraintsConfig struct {
	MaxSteps int           // 0 = not set (unlimited)
	Timeout  time.Duration // 0 = not set (use runner default)
}

// ContextConfig controls conversation history management.
type ContextConfig struct {
	MaxHistoryTurns  int     // 0 = not set
	Strategy         string  // "" | "auto_compact" | "manual" | "off"
	CompactThreshold float64 // 0 = not set
}

// SubAgentConfig configures spawn_agent sub-agent behaviour.
type SubAgentConfig struct {
	Model    string // "" = not set (inherit parent)
	MaxDepth int    // -1 = not set; 0 = disable; ≥1 = limit
	Tools    string // "inherit" or comma-separated tool names
}

// SandboxConfig restricts the execution environment.
type SandboxConfig struct {
	Filesystem string // "" | "unrestricted" | "project_only" | "read_only"
	Network    string // "" | "full" | "none"
}

// MetadataConfig carries recipe labels and sharing settings.
type MetadataConfig struct {
	Name        string
	Description string
	Tags        []string
	Author      string
	Version     string
	Extends     string
	Contribute  bool
	ProjectRoot string
}

// ─── Constructor ──────────────────────────────────────────────────────────────

// newRecipe returns a Recipe with sentinel values for fields that distinguish
// "not set" from "explicitly set to zero".
func newRecipe() *Recipe {
	return &Recipe{
		SubAgent: SubAgentConfig{MaxDepth: -1},
	}
}

// ─── Parse ────────────────────────────────────────────────────────────────────

// Parse reads and parses a recipe file at the given path.
func Parse(path string) (*Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseBytes(data)
}

// parseEmbedded parses the built-in default recipe from the embedded FS.
func parseEmbedded() *Recipe {
	data, err := fs.ReadFile(defaultFS, "default.recipe.md")
	if err != nil {
		// Should never happen — file is embedded at compile time.
		return newRecipe()
	}
	r, err := parseBytes(data)
	if err != nil {
		return newRecipe()
	}
	return r
}

// parseBytes parses recipe content from a byte slice.
func parseBytes(data []byte) (*Recipe, error) {
	r := newRecipe()
	lines := strings.Split(string(data), "\n")

	// Split into (header, body) pairs on "## " boundaries.
	type section struct {
		header string
		lines  []string
	}
	var sections []section
	var title string
	var cur *section

	for _, line := range lines {
		if strings.HasPrefix(line, "# ") && title == "" {
			title = strings.TrimSpace(line[2:])
			continue
		}
		if strings.HasPrefix(line, "## ") {
			header := strings.TrimSpace(line[3:])
			sections = append(sections, section{header: header})
			cur = &sections[len(sections)-1]
			continue
		}
		if cur != nil {
			cur.lines = append(cur.lines, line)
		}
	}

	if title != "" {
		r.Name = title
	}

	for _, s := range sections {
		body := strings.Join(s.lines, "\n")
		switch strings.ToLower(s.header) {
		case "models":
			r.Models = parseList(body)
		case "system prompt":
			r.SystemPrompt = parseProse(body)
		case "tools":
			r.Tools = parseTools(body)
		case "mcp servers":
			r.MCPServers = parseMCPServers(body)
		case "model parameters":
			r.ModelParams = parseModelParams(body)
		case "constraints":
			r.Constraints = parseConstraints(body)
		case "context":
			r.Context = parseContext(body)
		case "sub-agent":
			r.SubAgent = parseSubAgent(body)
		case "sandbox":
			r.Sandbox = parseSandbox(body)
		case "tasks":
			r.Tasks = parseList(body)
		case "success criteria":
			// Parsed but not used until Part 9 (headless execution).
		case "metadata":
			r.Metadata = parseMetadata(body)
		case "system reminders":
			// Parsed but not used until reminder injection is implemented.
		default:
			fmt.Fprintf(os.Stderr, "recipe: unknown section %q, skipping\n", s.header)
		}
	}

	return r, nil
}

// ─── Section parsers ──────────────────────────────────────────────────────────

// parseList extracts bullet list items from a section body.
// Items may be "- text" or "- key: value" form; returned as raw strings.
func parseList(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			item := strings.TrimSpace(line[2:])
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

// parseMap extracts key: value pairs from a section body.
// Accepts both "key: value" and "- key: value" line forms.
func parseMap(body string) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		val = strings.TrimSpace(val)
		if key != "" {
			m[key] = val
		}
	}
	return m
}

// parseProse returns the section body as trimmed raw text.
func parseProse(body string) string {
	return strings.TrimSpace(body)
}

// parseTools parses the ## Tools section into a ToolsConfig.
func parseTools(body string) *ToolsConfig {
	tc := &ToolsConfig{}
	for _, item := range parseList(body) {
		if strings.HasPrefix(item, "bash(") && strings.HasSuffix(item, ")") {
			// bash(prefix1, prefix2, ...)
			inner := item[5 : len(item)-1]
			var prefixes []string
			for _, p := range strings.Split(inner, ",") {
				if p = strings.TrimSpace(p); p != "" {
					prefixes = append(prefixes, p)
				}
			}
			tc.Allowlist = append(tc.Allowlist, "bash")
			tc.BashPrefixes = prefixes
		} else {
			tc.Allowlist = append(tc.Allowlist, item)
		}
	}
	if len(tc.Allowlist) == 0 {
		return nil
	}
	return tc
}

// parseMCPServers parses the ## MCP Servers section.
func parseMCPServers(body string) []MCPServerEntry {
	var out []MCPServerEntry
	for _, item := range parseList(body) {
		name, cmd, ok := strings.Cut(item, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		cmd = strings.TrimSpace(cmd)
		if name != "" && cmd != "" {
			out = append(out, MCPServerEntry{Name: name, Command: cmd})
		}
	}
	return out
}

// parseModelParams parses the ## Model Parameters section.
// ### subsections (per-model overrides) are noted but not yet applied by this parser;
// they are deferred to Part 9 when per-model adapter config is threaded through.
func parseModelParams(body string) ModelParamsConfig {
	m := parseMap(body)
	var cfg ModelParamsConfig
	if v, ok := m["temperature"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Temperature = &f
		}
	}
	if v, ok := m["max_tokens"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxTokens = &n
		}
	}
	return cfg
}

// parseConstraints parses the ## Constraints section.
func parseConstraints(body string) ConstraintsConfig {
	m := parseMap(body)
	var cfg ConstraintsConfig
	if v, ok := m["timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Timeout = d
		} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Timeout = time.Duration(n) * time.Second
		}
	}
	if v, ok := m["max_steps"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MaxSteps = n
		}
	}
	return cfg
}

// parseContext parses the ## Context section.
func parseContext(body string) ContextConfig {
	m := parseMap(body)
	var cfg ContextConfig
	if v, ok := m["max_history_turns"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MaxHistoryTurns = n
		}
	}
	if v, ok := m["strategy"]; ok {
		switch v {
		case "auto_compact", "manual", "off":
			cfg.Strategy = v
		default:
			fmt.Fprintf(os.Stderr, "recipe: unknown context strategy %q, ignoring\n", v)
		}
	}
	if v, ok := m["compact_threshold"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			cfg.CompactThreshold = f
		}
	}
	return cfg
}

// parseSubAgent parses the ## Sub-Agent section.
func parseSubAgent(body string) SubAgentConfig {
	m := parseMap(body)
	cfg := SubAgentConfig{MaxDepth: -1} // -1 = not set
	if v, ok := m["model"]; ok {
		cfg.Model = v
	}
	if v, ok := m["max_depth"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MaxDepth = n
		}
	}
	if v, ok := m["tools"]; ok {
		cfg.Tools = v
	}
	return cfg
}

// parseSandbox parses the ## Sandbox section.
func parseSandbox(body string) SandboxConfig {
	m := parseMap(body)
	var cfg SandboxConfig
	if v, ok := m["filesystem"]; ok {
		switch v {
		case "unrestricted", "project_only", "read_only":
			cfg.Filesystem = v
		default:
			fmt.Fprintf(os.Stderr, "recipe: unknown sandbox filesystem %q, ignoring\n", v)
		}
	}
	if v, ok := m["network"]; ok {
		switch v {
		case "full", "none":
			cfg.Network = v
		default:
			fmt.Fprintf(os.Stderr, "recipe: unknown sandbox network %q, ignoring\n", v)
		}
	}
	return cfg
}

// parseMetadata parses the ## Metadata section.
func parseMetadata(body string) MetadataConfig {
	m := parseMap(body)
	var cfg MetadataConfig
	cfg.Name = m["name"]
	cfg.Description = m["description"]
	cfg.Author = m["author"]
	cfg.Version = m["version"]
	cfg.Extends = m["extends"]
	cfg.ProjectRoot = m["project_root"]
	if v, ok := m["tags"]; ok {
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				cfg.Tags = append(cfg.Tags, t)
			}
		}
	}
	if v, ok := m["contribute"]; ok {
		cfg.Contribute = strings.ToLower(v) == "true"
	}
	return cfg
}

// ─── Discover ─────────────────────────────────────────────────────────────────

// Discover finds and parses the appropriate recipe using the discovery chain.
// explicitPath may be:
//   - An absolute or relative file path
//   - A short name (no path separators) resolved against ~/.errata/recipes/<name>.md
//   - Empty string to use auto-discovery
func Discover(explicitPath string) (*Recipe, error) {
	if explicitPath != "" {
		// Check if it looks like a short name (no directory separator).
		if !strings.ContainsAny(explicitPath, "/\\") && !strings.HasSuffix(explicitPath, ".md") {
			home, err := os.UserHomeDir()
			if err == nil {
				named := filepath.Join(home, ".errata", "recipes", explicitPath+".md")
				if _, err := os.Stat(named); err == nil {
					return Parse(named)
				}
			}
		}
		return Parse(explicitPath)
	}

	// 2. recipe.md in cwd
	if _, err := os.Stat("recipe.md"); err == nil {
		return Parse("recipe.md")
	}

	// 3. .errata/recipe.md in cwd
	if _, err := os.Stat(".errata/recipe.md"); err == nil {
		return Parse(".errata/recipe.md")
	}

	// 4. ~/.errata/default.recipe.md
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".errata", "default.recipe.md")
		if _, err := os.Stat(p); err == nil {
			return Parse(p)
		}
	}

	// 5. Built-in embedded defaults.
	return parseEmbedded(), nil
}

// Default returns the built-in default Recipe (embedded default.recipe.md).
// Always returns a non-nil Recipe.
func Default() *Recipe {
	return parseEmbedded()
}

// ─── ApplyTo ──────────────────────────────────────────────────────────────────

// ApplyTo overlays recipe settings onto cfg.
// Only fields that are explicitly set in the recipe override cfg values;
// unset recipe fields leave cfg unchanged (preserving env-var-sourced defaults).
func (r *Recipe) ApplyTo(cfg *config.Config) {
	if len(r.Models) > 0 {
		cfg.ActiveModels = r.Models
	}
	if r.SystemPrompt != "" {
		cfg.SystemPromptExtra = r.SystemPrompt
	}
	if len(r.MCPServers) > 0 {
		parts := make([]string, len(r.MCPServers))
		for i, s := range r.MCPServers {
			parts[i] = s.Name + ":" + s.Command
		}
		cfg.MCPServers = strings.Join(parts, ",")
	}
	if r.SubAgent.Model != "" {
		cfg.SubagentModel = r.SubAgent.Model
	}
	// MaxDepth: -1 = not set, 0 = disable, ≥1 = explicit limit
	if r.SubAgent.MaxDepth >= 0 {
		cfg.SubagentMaxDepth = r.SubAgent.MaxDepth
	}
	if r.Context.MaxHistoryTurns > 0 {
		cfg.MaxHistoryTurns = r.Context.MaxHistoryTurns
	}
	if r.Constraints.Timeout > 0 {
		cfg.AgentTimeout = r.Constraints.Timeout
	}
	if r.Context.CompactThreshold > 0 {
		cfg.CompactThreshold = r.Context.CompactThreshold
	}
}
