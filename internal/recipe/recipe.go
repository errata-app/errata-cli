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
	"github.com/suarezc/errata/internal/prompt"
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
	Tasks           []string
	SuccessCriteria []string
	Metadata        MetadataConfig

	// ── Variant/override system (Gaps 1-3, 6) ────────────────────────────

	// Gap 1: per-model system prompt variants/overrides
	SystemPromptVariants  map[string]string // variant_name → content
	SystemPromptOverrides map[string]string // model_id or "provider:" → content or variant ref

	// Gap 2: per-tool description variants/overrides
	ToolDescriptions         map[string]string            // tool_name → description
	ToolDescriptionVariants  map[string]map[string]string // tool → variant → description
	ToolDescriptionOverrides map[string]map[string]string // model → tool → description

	// Gap 3: sub-agent mode prompts with variants/overrides
	SubAgentModes         map[string]string            // mode_name → prompt
	SubAgentModeVariants  map[string]map[string]string // mode → variant → prompt
	SubAgentModeOverrides map[string]map[string]string // model → mode → prompt

	// Gap 4: conditional mid-conversation injections
	SystemReminders []SystemReminderConfig

	// Gap 5: lifecycle event hooks
	Hooks []HookConfig

	// Gap 6: context summarization prompt with variants
	SummarizationPrompt         string
	SummarizationPromptVariants map[string]string // variant_name → prompt

	// Gap 7: deterministic output processing rules
	OutputProcessing          map[string]OutputRuleConfig            // tool → rule
	OutputProcessingOverrides map[string]map[string]OutputRuleConfig // model → tool → rule

	// Model profiles for capability overrides
	ModelProfiles map[string]ModelProfileConfig // model_id → profile
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
	MaxSteps int           // 0 = not set (unlimited)
	Timeout  time.Duration // 0 = not set (use runner default)
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
	Tier           string // maps to variant names for section resolution
	SystemRole     *bool  // nil = not set
	MidConvoSystem *bool  // nil = not set
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
			r.SuccessCriteria = parseList(body)
		case "metadata":
			r.Metadata = parseMetadata(body)
		// ── Gap 1: System Prompt variants/overrides ──
		case "system prompt variants":
			r.SystemPromptVariants = parseSubSectionMap(body)
		case "system prompt overrides":
			r.SystemPromptOverrides = parseSubSectionMap(body)

		// ── Gap 2: Tool Descriptions ──
		case "tool descriptions":
			r.ToolDescriptions = parseSubSectionMap(body)
		case "tool description variants":
			r.ToolDescriptionVariants = parseTwoLevelMap(body)
		case "tool description overrides":
			r.ToolDescriptionOverrides = parseTwoLevelMap(body)

		// ── Gap 3: Sub-Agent Modes ──
		case "sub-agent modes":
			r.SubAgentModes = parseSubSectionMap(body)
		case "sub-agent mode variants":
			r.SubAgentModeVariants = parseTwoLevelMap(body)
		case "sub-agent mode overrides":
			r.SubAgentModeOverrides = parseTwoLevelMap(body)

		// ── Gap 4: System Reminders ──
		case "system reminders":
			r.SystemReminders = parseSystemReminders(body)

		// ── Gap 5: Hooks ──
		case "hooks":
			r.Hooks = parseHooks(body)

		// ── Gap 6: Summarization ──
		case "context summarization prompt":
			r.SummarizationPrompt = parseProse(body)
		case "context summarization prompt variants":
			r.SummarizationPromptVariants = parseSubSectionMap(body)

		// ── Gap 7: Output Processing ──
		case "output processing":
			r.OutputProcessing = parseOutputRules(body)
		case "output processing overrides":
			r.OutputProcessingOverrides = parseOutputOverrides(body)

		// ── Model Profiles ──
		case "model profiles":
			r.ModelProfiles = parseModelProfiles(body)

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

// splitDeepSubSections splits a sub-section body on "#### " header boundaries.
func splitDeepSubSections(body string) []subSection {
	return splitOnPrefix(body, "#### ")
}

// splitOnPrefix splits body into named blocks at lines starting with prefix.
func splitOnPrefix(body, prefix string) []subSection {
	var out []subSection
	var cur *subSection
	for _, line := range strings.Split(body, "\n") {
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

// parseTwoLevelMap parses a section body into a map[outer]map[inner]content
// using ### for outer keys and #### for inner keys.
func parseTwoLevelMap(body string) map[string]map[string]string {
	outers := splitSubSections(body)
	if len(outers) == 0 {
		return nil
	}
	m := make(map[string]map[string]string, len(outers))
	for _, outer := range outers {
		inners := splitDeepSubSections(outer.body)
		if len(inners) == 0 {
			continue
		}
		inner := make(map[string]string, len(inners))
		for _, s := range inners {
			inner[s.name] = s.body
		}
		m[outer.name] = inner
	}
	return m
}

// parseSystemReminders parses ### sub-sections into SystemReminderConfig entries.
func parseSystemReminders(body string) []SystemReminderConfig {
	subs := splitSubSections(body)
	var out []SystemReminderConfig
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
	var out []HookConfig
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
		if v := m["inject_output"]; strings.ToLower(v) == "true" {
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

// parseOutputOverrides parses ### model / #### tool sub-sections.
func parseOutputOverrides(body string) map[string]map[string]OutputRuleConfig {
	outers := splitSubSections(body)
	if len(outers) == 0 {
		return nil
	}
	m := make(map[string]map[string]OutputRuleConfig, len(outers))
	for _, outer := range outers {
		inners := splitDeepSubSections(outer.body)
		if len(inners) == 0 {
			continue
		}
		inner := make(map[string]OutputRuleConfig, len(inners))
		for _, s := range inners {
			inner[s.name] = parseOneOutputRule(s.body)
		}
		m[outer.name] = inner
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
		if v, ok := kv["tier"]; ok {
			p.Tier = v
		}
		if v, ok := kv["system_role"]; ok {
			b := strings.ToLower(v) == "true"
			p.SystemRole = &b
		}
		if v, ok := kv["mid_convo_system"]; ok {
			b := strings.ToLower(v) == "true"
			p.MidConvoSystem = &b
		}
		m[s.name] = p
	}
	return m
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
	if r.ModelParams.Seed != nil {
		cfg.Seed = r.ModelParams.Seed
	}
}

// ─── VariantSet helpers ─────────────────────────────────────────────────────

// SystemPromptVS returns a VariantSet for the system prompt section.
func (r *Recipe) SystemPromptVS() prompt.VariantSet {
	return prompt.VariantSet{
		Default:   r.SystemPrompt,
		Variants:  r.SystemPromptVariants,
		Overrides: r.SystemPromptOverrides,
	}
}

// ToolDescriptionVS returns a VariantSet for a specific tool's description.
func (r *Recipe) ToolDescriptionVS(toolName string) prompt.VariantSet {
	var overrides map[string]string
	if len(r.ToolDescriptionOverrides) > 0 {
		// Flatten: model → tool → desc becomes tool-specific overrides keyed by model
		overrides = make(map[string]string)
		for model, tools := range r.ToolDescriptionOverrides {
			if desc, ok := tools[toolName]; ok {
				overrides[model] = desc
			}
		}
		if len(overrides) == 0 {
			overrides = nil
		}
	}
	return prompt.VariantSet{
		Default:   r.ToolDescriptions[toolName],
		Variants:  r.ToolDescriptionVariants[toolName],
		Overrides: overrides,
	}
}

// SubAgentModeVS returns a VariantSet for a specific sub-agent mode.
func (r *Recipe) SubAgentModeVS(modeName string) prompt.VariantSet {
	var overrides map[string]string
	if len(r.SubAgentModeOverrides) > 0 {
		overrides = make(map[string]string)
		for model, modes := range r.SubAgentModeOverrides {
			if p, ok := modes[modeName]; ok {
				overrides[model] = p
			}
		}
		if len(overrides) == 0 {
			overrides = nil
		}
	}
	return prompt.VariantSet{
		Default:   r.SubAgentModes[modeName],
		Variants:  r.SubAgentModeVariants[modeName],
		Overrides: overrides,
	}
}

// SummarizationVS returns a VariantSet for the summarization prompt.
func (r *Recipe) SummarizationVS() prompt.VariantSet {
	return prompt.VariantSet{
		Default:  r.SummarizationPrompt,
		Variants: r.SummarizationPromptVariants,
	}
}

// AllToolDescriptionNames returns all tool names that have any description content
// across base descriptions, variants, or overrides.
func (r *Recipe) AllToolDescriptionNames() []string {
	seen := make(map[string]bool)
	for name := range r.ToolDescriptions {
		seen[name] = true
	}
	for name := range r.ToolDescriptionVariants {
		seen[name] = true
	}
	for _, tools := range r.ToolDescriptionOverrides {
		for name := range tools {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// AllSubAgentModeNames returns all mode names that have any prompt content
// across base modes, variants, or overrides.
func (r *Recipe) AllSubAgentModeNames() []string {
	seen := make(map[string]bool)
	for name := range r.SubAgentModes {
		seen[name] = true
	}
	for name := range r.SubAgentModeVariants {
		seen[name] = true
	}
	for _, modes := range r.SubAgentModeOverrides {
		for name := range modes {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// TierForModel returns the tier string from the model's profile, if any.
// Returns "" when the model has no profile or no tier set.
func (r *Recipe) TierForModel(modelID string) string {
	if profile, ok := r.ModelProfiles[modelID]; ok {
		return profile.Tier
	}
	return ""
}
