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
	"strconv"
	"strings"
	"time"

)

//go:embed default.recipe.md
var defaultFS embed.FS

// ─── Data types ───────────────────────────────────────────────────────────────

// Recipe holds all settings parsed from a recipe.md file.
// Nil/zero values mean "not set"; callers check zero values before
// applying recipe fields to runtime configuration.
//
// The Version field is the recipe schema version (integer). Every recipe must
// declare its version explicitly; recipes without a version are rejected.
// The Recipe type only grows: new fields are added with zero-value defaults so
// that older-version parsers can migrate forward without breaking.
type Recipe struct {
	Version int // recipe schema version (required; currently only 1)

	Name         string
	Models       []string        // nil = not set
	SystemPrompt string          // "" = not set
	Tools        *ToolsConfig    // nil = all tools enabled
	MCPServers  []MCPServerEntry
	Constraints ConstraintsConfig
	Context     ContextConfig
	Sandbox     SandboxConfig
	Tasks           []string
	SuccessCriteria []string

	// Uniform per-tool description overrides (applied to all models).
	ToolDescriptions map[string]string // tool_name → description

	// Context summarization prompt (applied to all models).
	SummarizationPrompt string

	// Deterministic output processing rules (applied to all models).
	OutputProcessing map[string]OutputRuleConfig // tool → rule

	// Model profiles for capability overrides
	ModelProfiles map[string]ModelProfileConfig // model_id → profile

	// SectionsPresent tracks which ## sections were declared in the parsed recipe.
	// nil for programmatic recipes; populated by parseV1(). Used by ApplyRecipe
	// to decide between atomic (section-replaces-defaults) and legacy (field-by-field)
	// merge behavior.
	SectionsPresent map[string]bool
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
	// Guidance maps tool names to per-tool guidance text from "- name: guidance" format.
	// nil means no per-tool guidance overrides (use code defaults).
	Guidance map[string]string
}

// MCPServerEntry is one named MCP server subprocess.
type MCPServerEntry struct {
	Name    string // display name
	Command string // full command string including arguments
}

// ConstraintsConfig limits agentic loop execution.
type ConstraintsConfig struct {
	MaxSteps    int           // 0 = not set (unlimited)
	Timeout     time.Duration // 0 = not set (use runner default)
	BashTimeout time.Duration // 0 = not set (use tools default 2m)
	ProjectRoot string        // working directory override; "" = cwd
}

// ContextConfig controls conversation history management.
type ContextConfig struct {
	MaxHistoryTurns  int     // 0 = not set
	Strategy         string  // "" | "auto_compact" | "manual" | "off"
	CompactThreshold float64 // 0 = not set
	TaskMode         string  // "" | "independent" | "sequential"
}

// SandboxConfig restricts the execution environment.
type SandboxConfig struct {
	Filesystem      string // "" | "unrestricted" | "project_only" | "read_only"
	Network         string // "" | "full" | "none"
	AllowLocalFetch bool   // allow web_fetch to target localhost URLs
}

// OutputRuleConfig is a deterministic output processing rule (Gap 7).
type OutputRuleConfig struct {
	MaxLines          int    // truncate at this many lines (0 = unlimited)
	MaxTokens         int    // truncate at this many tokens (0 = unlimited)
	Truncation        string // "head", "tail", "head_tail"
	TruncationMessage string // template with {line_count}, {token_count}
}

// ModelProfileConfig overrides auto-discovered model capabilities.
type ModelProfileConfig struct {
	ContextBudget  int    // override context budget (0 = not set)
	ToolFormat     string // "native", "function_calling", "text_in_prompt" ("" = not set)
	SystemRole     *bool  // nil = not set
	MidConvoSystem *bool  // nil = not set
}

// ─── Runner (version-pinned execution) ──────────────────────────────────────

// Runner encapsulates version-specific recipe execution behavior.
// Each recipe version maps to its own Runner implementation, ensuring
// that a recipe's runtime behavior is pinned to the version it was written for.
type Runner interface {
	// Version returns the recipe format version this runner implements.
	Version() int
	// Recipe returns the underlying recipe configuration.
	Recipe() *Recipe
}

// v1Runner implements Runner for version 1 recipes.
type v1Runner struct {
	recipe *Recipe
}

// NewV1Runner creates a Runner for a version 1 recipe.
func NewV1Runner(r *Recipe) (Runner, error) {
	if r.Version != 1 {
		return nil, fmt.Errorf("NewV1Runner: expected version 1, got %d", r.Version)
	}
	return &v1Runner{recipe: r}, nil
}

func (v *v1Runner) Version() int    { return 1 }
func (v *v1Runner) Recipe() *Recipe { return v.recipe }

// BuildRunner creates a version-specific Runner for this recipe.
// Returns an error if the recipe version is unsupported.
func (r *Recipe) BuildRunner() (Runner, error) {
	switch r.Version {
	case 1:
		return NewV1Runner(r)
	default:
		return nil, fmt.Errorf("unsupported recipe version %d (max supported: %d)", r.Version, MaxVersion)
	}
}

// MaxVersion is the highest recipe version supported by this binary.
const MaxVersion = 1

// HasSection reports whether the named section (lowercase) was declared in the
// parsed recipe file. Returns false when SectionsPresent is nil, which is the
// case for programmatically constructed recipes.
func (r *Recipe) HasSection(name string) bool {
	if r.SectionsPresent == nil {
		return false
	}
	return r.SectionsPresent[name]
}

// ─── Constructor ──────────────────────────────────────────────────────────────

// newRecipe returns a fresh Recipe with zero values.
func newRecipe() *Recipe {
	return &Recipe{}
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

// ParseContent parses raw recipe markdown bytes into a Recipe.
// This is the programmatic equivalent of Parse but accepts content directly
// instead of reading from a file path.
func ParseContent(data []byte) (*Recipe, error) {
	return parseBytes(data)
}

// parseEmbedded parses the built-in default recipe from the embedded FS.
// Panics on failure — a broken embedded recipe is a build/development error
// that must be caught immediately, not silently degraded.
func parseEmbedded() *Recipe {
	data, err := fs.ReadFile(defaultFS, "default.recipe.md")
	if err != nil {
		panic("embedded default.recipe.md missing: " + err.Error())
	}
	r, err := parseBytes(data)
	if err != nil {
		panic("embedded default.recipe.md parse error: " + err.Error())
	}
	return r
}

// parseBytes extracts the recipe version and dispatches to the appropriate
// version-specific parser. Every recipe must declare its version explicitly
// in the header area (before the first ## section):
//
//	version: 1
func parseBytes(data []byte) (*Recipe, error) {
	version, err := extractVersion(data)
	if err != nil {
		return nil, err
	}
	switch version {
	case 1:
		return parseV1(data)
	default:
		return nil, fmt.Errorf("recipe version %d not supported (max supported: %d)", version, MaxVersion)
	}
}

// extractVersion scans the header lines (before the first ## section) for a
// "version: N" declaration. Returns an error if no version is found or the
// value is not a positive integer.
func extractVersion(data []byte) (int, error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			break // past header area
		}
		trimmed := strings.TrimSpace(line)
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(strings.ToLower(key)) == "version" {
			v, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return 0, fmt.Errorf("recipe version must be an integer, got %q", strings.TrimSpace(val))
			}
			if v < 1 {
				return 0, fmt.Errorf("recipe version must be >= 1, got %d", v)
			}
			return v, nil
		}
	}
	return 0, fmt.Errorf("recipe missing required version field (add \"version: 1\" before the first section)")
}

// parseV1 parses a version 1 recipe. All currently defined sections are v1.
func parseV1(data []byte) (*Recipe, error) {
	r := newRecipe()
	r.Version = 1
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

	r.SectionsPresent = make(map[string]bool, len(sections))
	for _, s := range sections {
		body := strings.Join(s.lines, "\n")
		normalized := strings.ToLower(s.header)
		r.SectionsPresent[normalized] = true
		switch normalized {
		case "models":
			r.Models = parseList(body)
		case "system prompt":
			r.SystemPrompt = parseProse(body)
		case "tools":
			r.Tools = parseTools(body)
		case "mcp servers":
			r.MCPServers = parseMCPServers(body)
		case "constraints":
			r.Constraints = parseConstraints(body)
		case "context":
			r.Context = parseContext(body)
		case "sandbox":
			r.Sandbox = parseSandbox(body)
		case "tasks":
			r.Tasks = parseList(body)
		case "success criteria":
			r.SuccessCriteria = parseList(body)
		case "tool descriptions":
			r.ToolDescriptions = parseSubSectionMap(body)
		case "context summarization prompt":
			r.SummarizationPrompt = parseProse(body)
		case "output processing":
			r.OutputProcessing = parseOutputRules(body)
		case "model profiles":
			r.ModelProfiles = parseModelProfiles(body)
		case "model parameters", "sub-agent", "sub-agent modes", "system reminders", "hooks", "metadata":
			// Removed sections — silently ignored for backward compatibility.

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
	for line := range strings.SplitSeq(body, "\n") {
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
	for line := range strings.SplitSeq(body, "\n") {
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

// ─── Sub-section parsing helpers ─────────────────────────────────────────────

// subSection is a named sub-section parsed from ### headers within a ## section.
type subSection struct {
	name string
	body string
}

// splitSubSections splits a section body on "### " header boundaries.
// Returns one subSection per ### block. Lines before the first ### are ignored.
func splitSubSections(body string) []subSection {
	return splitOnPrefix(body, "### ")
}

// splitOnPrefix splits body into named blocks at lines starting with prefix.
func splitOnPrefix(body, prefix string) []subSection {
	var out []subSection
	var cur *subSection
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			name := strings.TrimSpace(line[len(prefix):])
			out = append(out, subSection{name: name})
			cur = &out[len(out)-1]
			continue
		}
		if cur != nil {
			if cur.body != "" {
				cur.body += "\n"
			}
			cur.body += line
		}
	}
	// Trim trailing whitespace from each sub-section body.
	for i := range out {
		out[i].body = strings.TrimSpace(out[i].body)
	}
	return out
}

// parseMapProse extracts leading key: value lines from body, returning them as a map
// parseSubSectionMap parses a section body into a map[name]content using ### sub-headers.
func parseSubSectionMap(body string) map[string]string {
	subs := splitSubSections(body)
	if len(subs) == 0 {
		return nil
	}
	m := make(map[string]string, len(subs))
	for _, s := range subs {
		m[s.name] = s.body
	}
	return m
}

// parseOutputRules parses ### sub-sections into OutputRuleConfig entries.
func parseOutputRules(body string) map[string]OutputRuleConfig {
	subs := splitSubSections(body)
	if len(subs) == 0 {
		return nil
	}
	m := make(map[string]OutputRuleConfig, len(subs))
	for _, s := range subs {
		m[s.name] = parseOneOutputRule(s.body)
	}
	return m
}

// parseOneOutputRule parses key:value pairs into an OutputRuleConfig.
func parseOneOutputRule(body string) OutputRuleConfig {
	kv := parseMap(body)
	var rule OutputRuleConfig
	if v, ok := kv["max_lines"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rule.MaxLines = n
		}
	}
	if v, ok := kv["max_tokens"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rule.MaxTokens = n
		}
	}
	if v, ok := kv["truncation"]; ok {
		switch v {
		case "head", "tail", "head_tail":
			rule.Truncation = v
		}
	}
	if v, ok := kv["truncation_message"]; ok {
		rule.TruncationMessage = v
	}
	return rule
}

// parseModelProfiles parses ### sub-sections into ModelProfileConfig entries.
func parseModelProfiles(body string) map[string]ModelProfileConfig {
	subs := splitSubSections(body)
	if len(subs) == 0 {
		return nil
	}
	m := make(map[string]ModelProfileConfig, len(subs))
	for _, s := range subs {
		kv := parseMap(s.body)
		var p ModelProfileConfig
		if v, ok := kv["context_budget"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				p.ContextBudget = n
			}
		}
		if v, ok := kv["tool_format"]; ok {
			p.ToolFormat = v
		}
		if v, ok := kv["system_role"]; ok {
			b := strings.EqualFold(v, "true")
			p.SystemRole = &b
		}
		if v, ok := kv["mid_convo_system"]; ok {
			b := strings.EqualFold(v, "true")
			p.MidConvoSystem = &b
		}
		m[s.name] = p
	}
	return m
}

// parseTools parses the ## Tools section into a ToolsConfig.
// Called only when ## Tools is present in the recipe. An empty section yields
// a non-nil ToolsConfig with a non-nil empty Allowlist (meaning zero tools),
// distinguishable from a nil ToolsConfig (section absent → all tools).
//
// Each bullet may use "- name: guidance" format. The tool name is everything
// before the first colon; guidance text is everything after. No colon means
// no guidance override. The bash(prefix1, prefix2) syntax is preserved and
// supports an optional trailing ": guidance" after the closing parenthesis.
func parseTools(body string) *ToolsConfig {
	tc := &ToolsConfig{Allowlist: []string{}}
	for _, item := range parseList(body) {
		var name, guidance string

		if strings.HasPrefix(item, "bash(") {
			// bash(prefix1, prefix2, ...): optional guidance
			closeIdx := strings.Index(item, ")")
			if closeIdx < 0 {
				// Malformed, treat entire item as tool name
				tc.Allowlist = append(tc.Allowlist, item)
				continue
			}
			inner := item[5:closeIdx]
			var prefixes []string
			for p := range strings.SplitSeq(inner, ",") {
				if p = strings.TrimSpace(p); p != "" {
					prefixes = append(prefixes, p)
				}
			}
			tc.BashPrefixes = prefixes
			name = "bash"
			// Check for guidance after ")"
			rest := item[closeIdx+1:]
			if after, ok := strings.CutPrefix(rest, ":"); ok {
				guidance = strings.TrimSpace(after)
			}
		} else if toolName, guidanceText, ok := strings.Cut(item, ":"); ok {
			name = strings.TrimSpace(toolName)
			guidance = strings.TrimSpace(guidanceText)
		} else {
			name = item
		}

		tc.Allowlist = append(tc.Allowlist, name)
		if guidance != "" {
			if tc.Guidance == nil {
				tc.Guidance = make(map[string]string)
			}
			tc.Guidance[name] = guidance
		}
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
	if v, ok := m["bash_timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.BashTimeout = d
		} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BashTimeout = time.Duration(n) * time.Second
		}
	}
	if v, ok := m["max_steps"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MaxSteps = n
		}
	}
	if v, ok := m["project_root"]; ok {
		cfg.ProjectRoot = v
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
	if v, ok := m["task_mode"]; ok {
		switch v {
		case "independent", "sequential":
			cfg.TaskMode = v
		default:
			fmt.Fprintf(os.Stderr, "recipe: unknown task_mode %q, ignoring\n", v)
		}
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
	if v, ok := m["allow_local_fetch"]; ok {
		cfg.AllowLocalFetch = strings.EqualFold(v, "true")
	}
	return cfg
}

// ─── Discover ─────────────────────────────────────────────────────────────────

// Discover parses the recipe at explicitPath, or returns the built-in
// embedded default if explicitPath is empty.
func Discover(explicitPath string) (*Recipe, error) {
	if explicitPath != "" {
		return Parse(explicitPath)
	}
	return parseEmbedded(), nil
}

// Default returns the built-in default Recipe (embedded default.recipe.md).
// Always returns a non-nil Recipe.
func Default() *Recipe {
	return parseEmbedded()
}


// MarshalMarkdown serializes the recipe back to the Markdown format used by
// recipe.md files. Only non-zero/non-default fields are included.
func (r *Recipe) MarshalMarkdown() string {
	var sb strings.Builder

	// Title
	name := r.Name
	if name == "" {
		name = "Errata Recipe"
	}
	fmt.Fprintf(&sb, "# %s\n", name)

	// Version (must come before any ## section)
	if r.Version > 0 {
		fmt.Fprintf(&sb, "version: %d\n", r.Version)
	}

	// Models
	if len(r.Models) > 0 {
		sb.WriteString("\n## Models\n")
		for _, m := range r.Models {
			fmt.Fprintf(&sb, "- %s\n", m)
		}
	}

	// System Prompt
	if r.SystemPrompt != "" {
		sb.WriteString("\n## System Prompt\n")
		sb.WriteString(r.SystemPrompt)
		sb.WriteByte('\n')
	}

	// Tools
	if r.Tools != nil {
		sb.WriteString("\n## Tools\n")
		for _, t := range r.Tools.Allowlist {
			guidance := r.Tools.Guidance[t]
			if t == "bash" && len(r.Tools.BashPrefixes) > 0 {
				bashName := fmt.Sprintf("bash(%s)", strings.Join(r.Tools.BashPrefixes, ", "))
				if guidance != "" {
					fmt.Fprintf(&sb, "- %s: %s\n", bashName, guidance)
				} else {
					fmt.Fprintf(&sb, "- %s\n", bashName)
				}
			} else if guidance != "" {
				fmt.Fprintf(&sb, "- %s: %s\n", t, guidance)
			} else {
				fmt.Fprintf(&sb, "- %s\n", t)
			}
		}
	}

	// MCP Servers
	if len(r.MCPServers) > 0 {
		sb.WriteString("\n## MCP Servers\n")
		for _, s := range r.MCPServers {
			fmt.Fprintf(&sb, "- %s: %s\n", s.Name, s.Command)
		}
	}

	// Constraints
	hasConstraints := r.Constraints.Timeout > 0 || r.Constraints.MaxSteps > 0 ||
		r.Constraints.BashTimeout > 0 || r.Constraints.ProjectRoot != ""
	if hasConstraints {
		sb.WriteString("\n## Constraints\n")
		if r.Constraints.Timeout > 0 {
			fmt.Fprintf(&sb, "timeout: %s\n", r.Constraints.Timeout.String())
		}
		if r.Constraints.BashTimeout > 0 {
			fmt.Fprintf(&sb, "bash_timeout: %s\n", r.Constraints.BashTimeout.String())
		}
		if r.Constraints.MaxSteps > 0 {
			fmt.Fprintf(&sb, "max_steps: %d\n", r.Constraints.MaxSteps)
		}
		if r.Constraints.ProjectRoot != "" {
			fmt.Fprintf(&sb, "project_root: %s\n", r.Constraints.ProjectRoot)
		}
	}

	// Context
	if r.Context.Strategy != "" || r.Context.MaxHistoryTurns > 0 || r.Context.CompactThreshold > 0 {
		sb.WriteString("\n## Context\n")
		if r.Context.MaxHistoryTurns > 0 {
			fmt.Fprintf(&sb, "max_history_turns: %d\n", r.Context.MaxHistoryTurns)
		}
		if r.Context.Strategy != "" {
			fmt.Fprintf(&sb, "strategy: %s\n", r.Context.Strategy)
		}
		if r.Context.CompactThreshold > 0 {
			fmt.Fprintf(&sb, "compact_threshold: %s\n", strconv.FormatFloat(r.Context.CompactThreshold, 'f', -1, 64))
		}
	}

	// Sandbox
	if r.Sandbox.Filesystem != "" || r.Sandbox.Network != "" || r.Sandbox.AllowLocalFetch {
		sb.WriteString("\n## Sandbox\n")
		if r.Sandbox.Filesystem != "" {
			fmt.Fprintf(&sb, "filesystem: %s\n", r.Sandbox.Filesystem)
		}
		if r.Sandbox.Network != "" {
			fmt.Fprintf(&sb, "network: %s\n", r.Sandbox.Network)
		}
		if r.Sandbox.AllowLocalFetch {
			sb.WriteString("allow_local_fetch: true\n")
		}
	}

	// Tasks
	if len(r.Tasks) > 0 {
		sb.WriteString("\n## Tasks\n")
		for _, t := range r.Tasks {
			fmt.Fprintf(&sb, "- %s\n", t)
		}
	}

	// Success Criteria
	if len(r.SuccessCriteria) > 0 {
		sb.WriteString("\n## Success Criteria\n")
		for _, c := range r.SuccessCriteria {
			fmt.Fprintf(&sb, "- %s\n", c)
		}
	}

	// Context Summarization Prompt
	if r.SummarizationPrompt != "" {
		sb.WriteString("\n## Context Summarization Prompt\n")
		sb.WriteString(r.SummarizationPrompt)
		sb.WriteByte('\n')
	}

	return sb.String()
}
