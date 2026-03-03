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
	MCPServers   []MCPServerEntry
	ModelParams  ModelParamsConfig
	Constraints  ConstraintsConfig
	Context      ContextConfig
	SubAgent     SubAgentConfig
	Sandbox      SandboxConfig
	Tasks           []string
	SuccessCriteria []string
	Metadata        MetadataConfig

	// Uniform per-tool description overrides (applied to all models).
	ToolDescriptions map[string]string // tool_name → description

	// Sub-agent mode prompts (applied to all models).
	SubAgentModes map[string]string // mode_name → prompt

	// Gap 4: conditional mid-conversation injections
	SystemReminders []SystemReminderConfig

	// Gap 5: lifecycle event hooks
	Hooks []HookConfig

	// Context summarization prompt (applied to all models).
	SummarizationPrompt string

	// Deterministic output processing rules (applied to all models).
	OutputProcessing map[string]OutputRuleConfig // tool → rule

	// Model profiles for capability overrides
	ModelProfiles map[string]ModelProfileConfig // model_id → profile

	// ToolGuidance replaces the built-in tool-use guidance when set.
	// Empty = not set (use default).
	ToolGuidance string
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
	Seed        *int64
}

// ConstraintsConfig limits agentic loop execution.
type ConstraintsConfig struct {
	MaxSteps    int           // 0 = not set (unlimited)
	Timeout     time.Duration // 0 = not set (use runner default)
	BashTimeout time.Duration // 0 = not set (use tools default 2m)
}

// ContextConfig controls conversation history management.
type ContextConfig struct {
	MaxHistoryTurns  int     // 0 = not set
	Strategy         string  // "" | "auto_compact" | "manual" | "off"
	CompactThreshold float64 // 0 = not set
	TaskMode         string  // "" | "independent" | "sequential"
}

// SubAgentConfig configures spawn_agent sub-agent behaviour.
type SubAgentConfig struct {
	Model    string // "" = not set (inherit parent)
	MaxDepth int    // -1 = not set; 0 = disable; ≥1 = limit
	Tools    string // "inherit" or comma-separated tool names
}

// SandboxConfig restricts the execution environment.
type SandboxConfig struct {
	Filesystem      string // "" | "unrestricted" | "project_only" | "read_only"
	Network         string // "" | "full" | "none"
	AllowLocalFetch bool   // allow web_fetch to target localhost URLs
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

// SystemReminderConfig is one conditional mid-conversation injection (Gap 4).
type SystemReminderConfig struct {
	Name    string // unique name for this reminder
	Trigger string // trigger expression, e.g. "context_usage > 0.75"
	Content string // prompt text to inject when trigger fires
}

// HookConfig is one lifecycle event hook (Gap 5).
type HookConfig struct {
	Name         string // unique name for this hook
	Event        string // "session_start", "pre_tool_use", "post_tool_use", etc.
	Matcher      string // tool name or glob; "" = all events of type
	Action       string // "command" (Phase 1 only)
	Command      string // shell command to execute
	Timeout      string // duration string, e.g. "30s"
	InjectOutput bool   // feed command stdout back as model context
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
			r.SuccessCriteria = parseList(body)
		case "metadata":
			r.Metadata = parseMetadata(body)
		// ── Tool Descriptions ──
		case "tool descriptions":
			r.ToolDescriptions = parseSubSectionMap(body)

		// ── Sub-Agent Modes ──
		case "sub-agent modes":
			r.SubAgentModes = parseSubSectionMap(body)

		// ── System Reminders ──
		case "system reminders":
			r.SystemReminders = parseSystemReminders(body)

		// ── Hooks ──
		case "hooks":
			r.Hooks = parseHooks(body)

		// ── Summarization ──
		case "context summarization prompt":
			r.SummarizationPrompt = parseProse(body)

		// ── Output Processing ──
		case "output processing":
			r.OutputProcessing = parseOutputRules(body)

		// ── Model Profiles ──
		case "model profiles":
			r.ModelProfiles = parseModelProfiles(body)

		// ── Tool Guidance ──
		case "tool guidance":
			r.ToolGuidance = parseProse(body)

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
// and any remaining prose text that follows. Blank lines separate the key-value
// block from the prose. Used for reminders (metadata + content).
func parseMapProse(body string) (map[string]string, string) {
	m := make(map[string]string)
	lines := strings.Split(body, "\n")
	var proseStart int
	inKV := true

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if inKV {
			if trimmed == "" {
				// Blank line while still in KV block — check if remaining is prose.
				inKV = false
				proseStart = i + 1
				continue
			}
			key, val, ok := strings.Cut(trimmed, ":")
			if !ok {
				// Not a key-value line — start of prose.
				proseStart = i
				break
			}
			key = strings.TrimSpace(strings.ToLower(key))
			val = strings.TrimSpace(val)
			if key != "" {
				m[key] = val
			}
		} else {
			// After blank line — everything is prose.
			proseStart = i
			break
		}
	}

	prose := ""
	if proseStart < len(lines) {
		prose = strings.TrimSpace(strings.Join(lines[proseStart:], "\n"))
	}
	return m, prose
}

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

// parseSystemReminders parses ### sub-sections into SystemReminderConfig entries.
func parseSystemReminders(body string) []SystemReminderConfig {
	subs := splitSubSections(body)
	out := make([]SystemReminderConfig, 0, len(subs))
	for _, s := range subs {
		kv, prose := parseMapProse(s.body)
		out = append(out, SystemReminderConfig{
			Name:    s.name,
			Trigger: kv["trigger"],
			Content: prose,
		})
	}
	return out
}

// parseHooks parses ### sub-sections into HookConfig entries.
func parseHooks(body string) []HookConfig {
	subs := splitSubSections(body)
	out := make([]HookConfig, 0, len(subs))
	for _, s := range subs {
		m := parseMap(s.body)
		h := HookConfig{
			Name:    s.name,
			Event:   m["event"],
			Matcher: m["matcher"],
			Action:  m["action"],
			Command: m["command"],
			Timeout: m["timeout"],
		}
		if h.Action == "" && h.Command != "" {
			h.Action = "command" // default action
		}
		if v := m["inject_output"]; strings.EqualFold(v, "true") {
			h.InjectOutput = true
		}
		out = append(out, h)
	}
	return out
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
func parseTools(body string) *ToolsConfig {
	tc := &ToolsConfig{Allowlist: []string{}}
	for _, item := range parseList(body) {
		if strings.HasPrefix(item, "bash(") && strings.HasSuffix(item, ")") {
			// bash(prefix1, prefix2, ...)
			inner := item[5 : len(item)-1]
			var prefixes []string
			for p := range strings.SplitSeq(inner, ",") {
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
	if v, ok := m["seed"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Seed = &n
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
	if v, ok := m["allow_local_fetch"]; ok {
		cfg.AllowLocalFetch = strings.EqualFold(v, "true")
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
		for t := range strings.SplitSeq(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				cfg.Tags = append(cfg.Tags, t)
			}
		}
	}
	if v, ok := m["contribute"]; ok {
		cfg.Contribute = strings.EqualFold(v, "true")
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
	if r.ModelParams.Seed != nil {
		cfg.Seed = r.ModelParams.Seed
	}
	if r.ToolGuidance != "" {
		cfg.ToolGuidance = r.ToolGuidance
	}
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

	// Tool Guidance
	if r.ToolGuidance != "" {
		sb.WriteString("\n## Tool Guidance\n")
		sb.WriteString(r.ToolGuidance)
		sb.WriteByte('\n')
	}

	// Tools
	if r.Tools != nil {
		sb.WriteString("\n## Tools\n")
		for _, t := range r.Tools.Allowlist {
			if t == "bash" && len(r.Tools.BashPrefixes) > 0 {
				fmt.Fprintf(&sb, "- bash(%s)\n", strings.Join(r.Tools.BashPrefixes, ", "))
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

	// Model Parameters
	if r.ModelParams.Temperature != nil || r.ModelParams.MaxTokens != nil || r.ModelParams.Seed != nil {
		sb.WriteString("\n## Model Parameters\n")
		if r.ModelParams.Temperature != nil {
			fmt.Fprintf(&sb, "temperature: %s\n", strconv.FormatFloat(*r.ModelParams.Temperature, 'f', -1, 64))
		}
		if r.ModelParams.MaxTokens != nil {
			fmt.Fprintf(&sb, "max_tokens: %d\n", *r.ModelParams.MaxTokens)
		}
		if r.ModelParams.Seed != nil {
			fmt.Fprintf(&sb, "seed: %d\n", *r.ModelParams.Seed)
		}
	}

	// Constraints
	if r.Constraints.Timeout > 0 || r.Constraints.MaxSteps > 0 || r.Constraints.BashTimeout > 0 {
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

	// Sub-Agent
	if r.SubAgent.Model != "" || r.SubAgent.MaxDepth > 0 || r.SubAgent.Tools != "" {
		sb.WriteString("\n## Sub-Agent\n")
		if r.SubAgent.Model != "" {
			fmt.Fprintf(&sb, "model: %s\n", r.SubAgent.Model)
		}
		if r.SubAgent.MaxDepth > 0 {
			fmt.Fprintf(&sb, "max_depth: %d\n", r.SubAgent.MaxDepth)
		}
		if r.SubAgent.Tools != "" {
			fmt.Fprintf(&sb, "tools: %s\n", r.SubAgent.Tools)
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

	// System Reminders
	if len(r.SystemReminders) > 0 {
		sb.WriteString("\n## System Reminders\n")
		for _, rem := range r.SystemReminders {
			fmt.Fprintf(&sb, "### %s\n", rem.Name)
			if rem.Trigger != "" {
				fmt.Fprintf(&sb, "trigger: %s\n", rem.Trigger)
			}
			if rem.Content != "" {
				sb.WriteString(rem.Content)
				sb.WriteByte('\n')
			}
		}
	}

	// Hooks
	if len(r.Hooks) > 0 {
		sb.WriteString("\n## Hooks\n")
		for _, h := range r.Hooks {
			fmt.Fprintf(&sb, "### %s\n", h.Name)
			fmt.Fprintf(&sb, "event: %s\n", h.Event)
			if h.Matcher != "" {
				fmt.Fprintf(&sb, "matcher: %s\n", h.Matcher)
			}
			fmt.Fprintf(&sb, "command: %s\n", h.Command)
		}
	}

	// Metadata
	hasMeta := r.Metadata.Description != "" || r.Metadata.Author != "" ||
		r.Metadata.Version != "" || len(r.Metadata.Tags) > 0 || r.Metadata.ProjectRoot != ""
	if hasMeta {
		sb.WriteString("\n## Metadata\n")
		if r.Metadata.Description != "" {
			fmt.Fprintf(&sb, "description: %s\n", r.Metadata.Description)
		}
		if r.Metadata.Author != "" {
			fmt.Fprintf(&sb, "author: %s\n", r.Metadata.Author)
		}
		if r.Metadata.Version != "" {
			fmt.Fprintf(&sb, "version: %s\n", r.Metadata.Version)
		}
		if len(r.Metadata.Tags) > 0 {
			fmt.Fprintf(&sb, "tags: %s\n", strings.Join(r.Metadata.Tags, ", "))
		}
		if r.Metadata.ProjectRoot != "" {
			fmt.Fprintf(&sb, "project_root: %s\n", r.Metadata.ProjectRoot)
		}
	}

	return sb.String()
}
