package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	tea "charm.land/bubbletea/v2"
	"github.com/errata-app/errata-cli/internal/adapters"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/internal/tools"
)

// ── types ───────────────────────────────────────────────────────────────────

// configSection represents one section in the config overlay.
type configSection struct {
	Name       string // kebab-case: "models", "system-prompt", etc.
	Summary    string // one-line summary of current value
	Kind       string // "list" | "scalar" | "text"
	Desc       string // brief one-line description (shown in nav view)
	DetailDesc string // detailed description (shown in expanded view)
	Path       string // dot-path into configPaths (text sections)
}

// listItem is one toggleable row in a list-type section editor.
type listItem struct {
	Label  string
	Active bool
	Header bool // non-selectable section header (e.g. provider name)
}

// scalarField is one key-value row in a scalar-type section editor.
type scalarField struct {
	Key     string // display name (e.g. "timeout")
	Path    string // dot-path for get/setConfigValue (e.g. "constraints.timeout")
	Value   string // current value as a string
	Editing bool   // true when user is typing into this field
}

// interactiveSections lists the section names shown in /config for interactive
// sessions. Tasks and SuccessCriteria are hidden.
var interactiveSections = []string{
	"models", "system-prompt", "tools", "mcp-servers",
	"constraints", "context", "sandbox",
}

// ── section descriptions ─────────────────────────────────────────────────────

// sectionDesc holds the brief and detailed descriptions for a config section.
type sectionDesc struct {
	Brief  string
	Detail string
}

var sectionDescriptions = map[string]sectionDesc{
	"models": {
		Brief:  "Which AI models to compare",
		Detail: "Toggle which models participate in A/B comparisons. All configured models are shown; unchecked models are excluded from subsequent runs.",
	},
	"system-prompt": {
		Brief:  "Custom instructions prepended to every prompt",
		Detail: "A system prompt appended after the built-in tool guidance. Use for project-specific context, coding conventions, or domain knowledge that should influence all models.",
	},
	"tools": {
		Brief:  "Available tools for the agentic loop",
		Detail: "Enable or disable built-in and MCP tools. Disabled tools are hidden from models and cannot be called. Changes take effect on the next run.",
	},
	"mcp-servers": {
		Brief:  "External MCP tool server processes",
		Detail: "MCP servers expose additional tools via the Model Context Protocol. Servers are configured at startup via ERRATA_MCP_SERVERS and shown here for reference.",
	},
	"parameters": {
		Brief:  "Model sampling parameters (seed, temperature, max_tokens)",
		Detail: "Control model sampling behavior. Seed enables reproducible outputs. Temperature adjusts randomness (0 = deterministic, higher = more creative). Max tokens caps response length.",
	},
	"constraints": {
		Brief:  "Timeout and step limits for agent runs",
		Detail: "Set wall-clock timeout and maximum tool-call steps per model. Timeout cancels the run after the specified duration. Max steps limits how many tool calls a model can make.",
	},
	"context": {
		Brief:  "Conversation history and compaction settings",
		Detail: "Strategy controls when history is compacted: auto_compact triggers at the threshold, manual requires /compact, off disables compaction. Max history turns limits how many turns are kept.",
	},
	"sub-agent": {
		Brief:  "Sub-agent spawning configuration",
		Detail: "Configure the spawn_agent tool. Model sets which model sub-agents use (empty = same as parent). Max depth limits recursion. Tools controls which tools sub-agents can access.",
	},
	"sandbox": {
		Brief:  "Filesystem and network access restrictions",
		Detail: "Filesystem: unrestricted (full access), project_only (cwd subtree), read_only (no writes). Network: full (all access) or none (block outbound).",
	},
	"reminders": {
		Brief:  "Conditional mid-conversation injections",
		Detail: "System reminders are injected into the conversation when their trigger condition is met. Use /remind to fire a reminder manually. Configured in the recipe file.",
	},
	"hooks": {
		Brief:  "Lifecycle event hooks (shell commands)",
		Detail: "Hooks run shell commands in response to lifecycle events (e.g. before_run, after_run). Each hook has an event type, optional matcher pattern, and a command to execute.",
	},
}

// ── section builders ────────────────────────────────────────────────────────

func buildConfigSections(rec *recipe.Recipe, adapters []models.ModelAdapter, disabled map[string]bool) []configSection {
	if rec == nil {
		rec = recipe.Default()
	}
	sections := []configSection{
		{Name: "models", Summary: summarizeModels(rec, adapters), Kind: "list"},
		{Name: "system-prompt", Summary: summarizeSystemPrompt(rec), Kind: "text", Path: "system_prompt"},
		{Name: "tools", Summary: summarizeTools(rec, disabled), Kind: "list"},
		{Name: "mcp-servers", Summary: summarizeMCPServers(rec), Kind: "list"},
		{Name: "constraints", Summary: summarizeConstraints(rec), Kind: "scalar"},
		{Name: "context", Summary: summarizeContext(rec), Kind: "scalar"},
		{Name: "sandbox", Summary: summarizeSandbox(rec), Kind: "scalar"},
	}
	for i := range sections {
		if desc, ok := sectionDescriptions[sections[i].Name]; ok {
			sections[i].Desc = desc.Brief
			sections[i].DetailDesc = desc.Detail
		}
	}
	return sections
}

func summarizeModels(rec *recipe.Recipe, adapters []models.ModelAdapter) string {
	if len(rec.Models) > 0 {
		names := rec.Models
		if len(names) > 3 {
			return fmt.Sprintf("%s, ... (%d active)", strings.Join(names[:3], ", "), len(names))
		}
		return fmt.Sprintf("%s (%d active)", strings.Join(names, ", "), len(names))
	}
	if len(adapters) > 0 {
		var ids []string
		for _, a := range adapters {
			ids = append(ids, a.ID())
		}
		if len(ids) > 3 {
			return fmt.Sprintf("%s, ... (%d active)", strings.Join(ids[:3], ", "), len(ids))
		}
		return fmt.Sprintf("%s (%d active)", strings.Join(ids, ", "), len(ids))
	}
	return "(none configured)"
}

func summarizeSystemPrompt(rec *recipe.Recipe) string {
	if rec.SystemPrompt == "" {
		return "(not set)"
	}
	preview := rec.SystemPrompt
	if runes := []rune(preview); len(runes) > 50 {
		preview = string(runes[:50]) + "..."
	}
	preview = strings.ReplaceAll(preview, "\n", " ")
	return fmt.Sprintf("%q (%d chars)", preview, len(rec.SystemPrompt))
}

func summarizeTools(rec *recipe.Recipe, disabled map[string]bool) string {
	if rec.Tools == nil {
		enabled := len(tools.Definitions) - len(disabled)
		return fmt.Sprintf("%d enabled", enabled)
	}
	enabled := 0
	for _, name := range rec.Tools.Allowlist {
		if !disabled[name] {
			enabled++
		}
	}
	return fmt.Sprintf("%d enabled", enabled)
}

func summarizeMCPServers(rec *recipe.Recipe) string {
	if len(rec.MCPServers) == 0 {
		return "(none)"
	}
	var names []string
	for _, s := range rec.MCPServers {
		names = append(names, s.Name)
	}
	return fmt.Sprintf("%d configured (%s)", len(names), strings.Join(names, ", "))
}

func summarizeConstraints(rec *recipe.Recipe) string {
	var parts []string
	if rec.Constraints.Timeout > 0 {
		parts = append(parts, "timeout: "+rec.Constraints.Timeout.String())
	}
	if rec.Constraints.MaxSteps > 0 {
		parts = append(parts, fmt.Sprintf("max_steps: %d", rec.Constraints.MaxSteps))
	}
	if len(parts) == 0 {
		return "(defaults: timeout=5m, max_steps=unlimited)"
	}
	return strings.Join(parts, ", ")
}

func summarizeContext(rec *recipe.Recipe) string {
	var parts []string
	if rec.Context.Strategy != "" {
		parts = append(parts, rec.Context.Strategy)
	}
	if rec.Context.MaxHistoryTurns > 0 {
		parts = append(parts, fmt.Sprintf("%d turns", rec.Context.MaxHistoryTurns))
	}
	if rec.Context.CompactThreshold > 0 {
		parts = append(parts, fmt.Sprintf("threshold: %.2f", rec.Context.CompactThreshold))
	}
	if len(parts) == 0 {
		return "(defaults: auto_compact, 20 turns, threshold=0.80)"
	}
	return strings.Join(parts, ", ")
}

func summarizeSandbox(rec *recipe.Recipe) string {
	var parts []string
	if rec.Sandbox.Filesystem != "" {
		parts = append(parts, "filesystem: "+rec.Sandbox.Filesystem)
	}
	if rec.Sandbox.Network != "" {
		parts = append(parts, "network: "+rec.Sandbox.Network)
	}
	if len(parts) == 0 {
		return "(defaults: filesystem=unrestricted, network=full)"
	}
	return strings.Join(parts, ", ")
}

// ── list/scalar data builders ───────────────────────────────────────────────

// buildModelsList builds the full catalogue list for /config models.
// Layout: "Active" header + active models, then per-provider headers + inactive models.
// filter is a case-insensitive substring match on model IDs; empty = show all.
func buildModelsList(
	activeAdapters []models.ModelAdapter,
	allAdapters []models.ModelAdapter,
	providerModels []adapters.ProviderModels,
	filter string,
) []listItem {
	activeSet := make(map[string]bool, len(activeAdapters))
	for _, ad := range activeAdapters {
		activeSet[ad.ID()] = true
	}
	// poolSet tracks all models that have an adapter already (to distinguish
	// catalogue-only models from those already in the adapter pool).
	poolSet := make(map[string]bool, len(allAdapters))
	for _, ad := range allAdapters {
		poolSet[ad.ID()] = true
	}

	lf := strings.ToLower(filter)
	matches := func(id string) bool {
		return lf == "" || strings.Contains(strings.ToLower(id), lf)
	}

	var items []listItem

	// ── Active section ──────────────────────────────────────────────────────
	var activeItems []listItem
	for _, ad := range activeAdapters {
		if matches(ad.ID()) {
			activeItems = append(activeItems, listItem{Label: ad.ID(), Active: true})
		}
	}
	if len(activeItems) > 0 {
		items = append(items, listItem{Label: "Active", Header: true})
		items = append(items, activeItems...)
	}

	// ── Per-provider sections (inactive models only) ────────────────────────
	for _, pm := range providerModels {
		if pm.Err != nil {
			continue
		}
		var section []listItem
		for _, id := range pm.Models {
			if activeSet[id] {
				continue // already shown in Active section
			}
			if matches(id) {
				section = append(section, listItem{Label: id, Active: false})
			}
		}
		// Also include pool adapters whose IDs are not in any provider listing
		// (e.g. manually configured models). We attribute them to the first
		// matching provider by checking the adapter routing prefix, but only
		// the pool adapters that are not active and not already listed.
		if len(section) > 0 {
			items = append(items, listItem{Label: pm.Provider, Header: true})
			items = append(items, section...)
		}
	}

	// ── Pool-only adapters not covered by any provider listing ──────────────
	// These are adapters in a.adapters whose IDs don't appear in any
	// providerModels listing and are not active.
	catalogueSet := make(map[string]bool)
	for _, pm := range providerModels {
		for _, id := range pm.Models {
			catalogueSet[id] = true
		}
	}
	var orphan []listItem
	for _, ad := range allAdapters {
		id := ad.ID()
		if activeSet[id] || catalogueSet[id] {
			continue
		}
		if matches(id) {
			orphan = append(orphan, listItem{Label: id, Active: false})
		}
	}
	if len(orphan) > 0 {
		items = append(items, listItem{Label: "Other", Header: true})
		items = append(items, orphan...)
	}

	return items
}

func buildToolsList(allowlist []string, disabled map[string]bool) []listItem {
	// Show ALL tools. Initial enabled state: in allowlist (or no allowlist) AND not disabled.
	allowSet := make(map[string]bool, len(allowlist))
	for _, name := range allowlist {
		allowSet[name] = true
	}
	items := make([]listItem, 0, len(tools.Definitions))
	for _, d := range tools.Definitions {
		active := !disabled[d.Name]
		if len(allowlist) > 0 {
			active = allowSet[d.Name] && !disabled[d.Name]
		}
		items = append(items, listItem{Label: d.Name, Active: active})
	}
	return items
}

func buildScalarFields(sectionName string, rec *recipe.Recipe) []scalarField {
	switch sectionName {
	case "constraints":
		return []scalarField{
			{Key: "timeout", Path: "constraints.timeout", Value: configPathGet(rec, "constraints.timeout")},
			{Key: "max_steps", Path: "constraints.max_steps", Value: configPathGet(rec, "constraints.max_steps")},
		}
	case "context":
		return []scalarField{
			{Key: "strategy", Path: "context.strategy", Value: configPathGet(rec, "context.strategy")},
			{Key: "max_history_turns", Path: "context.max_history_turns", Value: configPathGet(rec, "context.max_history_turns")},
			{Key: "compact_threshold", Path: "context.compact_threshold", Value: configPathGet(rec, "context.compact_threshold")},
		}
	case "sandbox":
		return []scalarField{
			{Key: "filesystem", Path: "sandbox.filesystem", Value: configPathGet(rec, "sandbox.filesystem")},
			{Key: "network", Path: "sandbox.network", Value: configPathGet(rec, "sandbox.network")},
		}
	case "output-processing":
		// Build scalar fields for each configured output rule.
		var fields []scalarField
		for toolName, rule := range rec.OutputProcessing {
			prefix := "output." + toolName
			if rule.MaxLines > 0 {
				fields = append(fields, scalarField{
					Key: toolName + ".max_lines", Path: prefix + ".max_lines",
					Value: strconv.Itoa(rule.MaxLines),
				})
			}
			if rule.Truncation != "" {
				fields = append(fields, scalarField{
					Key: toolName + ".truncation", Path: prefix + ".truncation",
					Value: rule.Truncation,
				})
			}
		}
		if len(fields) == 0 {
			fields = append(fields, scalarField{Key: "(no rules)", Path: "", Value: ""})
		}
		return fields
	}
	return nil
}

// ── config path map ─────────────────────────────────────────────────────────

type configPathEntry struct {
	Get func(*recipe.Recipe) string
	Set func(*recipe.Recipe, string) error
}

var configPaths = map[string]configPathEntry{
	"constraints.timeout": {
		Get: func(r *recipe.Recipe) string {
			if r.Constraints.Timeout == 0 {
				return ""
			}
			return r.Constraints.Timeout.String()
		},
		Set: func(r *recipe.Recipe, v string) error {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("invalid duration %q: %w", v, err)
			}
			r.Constraints.Timeout = d
			return nil
		},
	},
	"constraints.max_steps": {
		Get: func(r *recipe.Recipe) string {
			if r.Constraints.MaxSteps == 0 {
				return ""
			}
			return strconv.Itoa(r.Constraints.MaxSteps)
		},
		Set: func(r *recipe.Recipe, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid integer %q", v)
			}
			r.Constraints.MaxSteps = n
			return nil
		},
	},
	"context.strategy": {
		Get: func(r *recipe.Recipe) string { return r.Context.Strategy },
		Set: func(r *recipe.Recipe, v string) error {
			switch v {
			case "auto_compact", "manual", "off", "":
				r.Context.Strategy = v
				return nil
			}
			return fmt.Errorf("unknown strategy %q (valid: auto_compact, manual, off)", v)
		},
	},
	"context.max_history_turns": {
		Get: func(r *recipe.Recipe) string {
			if r.Context.MaxHistoryTurns == 0 {
				return ""
			}
			return strconv.Itoa(r.Context.MaxHistoryTurns)
		},
		Set: func(r *recipe.Recipe, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid integer %q", v)
			}
			r.Context.MaxHistoryTurns = n
			return nil
		},
	},
	"context.compact_threshold": {
		Get: func(r *recipe.Recipe) string {
			if r.Context.CompactThreshold == 0 {
				return ""
			}
			return strconv.FormatFloat(r.Context.CompactThreshold, 'f', -1, 64)
		},
		Set: func(r *recipe.Recipe, v string) error {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 1 {
				return fmt.Errorf("invalid threshold %q (must be 0-1)", v)
			}
			r.Context.CompactThreshold = f
			return nil
		},
	},
	"sandbox.filesystem": {
		Get: func(r *recipe.Recipe) string { return r.Sandbox.Filesystem },
		Set: func(r *recipe.Recipe, v string) error {
			switch v {
			case "unrestricted", "project_only", "read_only", "":
				r.Sandbox.Filesystem = v
				return nil
			}
			return fmt.Errorf("unknown filesystem %q (valid: unrestricted, project_only, read_only)", v)
		},
	},
	"sandbox.network": {
		Get: func(r *recipe.Recipe) string { return r.Sandbox.Network },
		Set: func(r *recipe.Recipe, v string) error {
			switch v {
			case "full", "none", "":
				r.Sandbox.Network = v
				return nil
			}
			return fmt.Errorf("unknown network %q (valid: full, none)", v)
		},
	},
	"system_prompt": {
		Get: func(r *recipe.Recipe) string { return r.SystemPrompt },
		Set: func(r *recipe.Recipe, v string) error {
			r.SystemPrompt = v
			return nil
		},
	},
}

// configPathGet returns the current string value for a dot-path.
func configPathGet(rec *recipe.Recipe, path string) string {
	if entry, ok := configPaths[path]; ok {
		return entry.Get(rec)
	}
	return ""
}

// getConfigValue returns the current value for a config path, or an error message.
func getConfigValue(rec *recipe.Recipe, path string) string {
	if entry, ok := configPaths[path]; ok {
		v := entry.Get(rec)
		if v == "" {
			return "(not set)"
		}
		return v
	}
	return "(unknown path)"
}

// setConfigValue sets a config value by dot-path with validation.
func setConfigValue(rec *recipe.Recipe, path, value string) error {
	entry, ok := configPaths[path]
	if !ok {
		return fmt.Errorf("unknown config path %q", path)
	}
	return entry.Set(rec, value)
}

// configPathDefaults maps config dot-paths to their default value descriptions.
// Used to show "default: <value>" instead of bare "(not set)" in the overlay.
var configPathDefaults = map[string]string{
	"constraints.timeout":         "5m",
	"constraints.max_steps":       "unlimited",
	"context.strategy":            "auto_compact",
	"context.max_history_turns":   "20",
	"context.compact_threshold":   "0.80",
	"sandbox.filesystem":          "unrestricted",
	"sandbox.network":             "full",
	"parameters.seed":             "none",
	"parameters.temperature":      "provider default",
	"parameters.max_tokens":       "provider default",
	"system_prompt":                "none",
}

// configPathCandidates returns all valid config dot-paths for tab-completion.
func configPathCandidates() []string {
	out := make([]string, 0, len(configPaths))
	for k := range configPaths {
		out = append(out, k)
	}
	return out
}

// ── cloneRecipe ─────────────────────────────────────────────────────────────

func cloneRecipe(r *recipe.Recipe) *recipe.Recipe {
	if r == nil {
		return recipe.Default()
	}
	clone := *r
	if r.Models != nil {
		clone.Models = append([]string(nil), r.Models...)
	}
	if r.Tools != nil {
		tc := *r.Tools
		if r.Tools.Allowlist != nil {
			tc.Allowlist = append([]string(nil), r.Tools.Allowlist...)
		}
		if r.Tools.BashPrefixes != nil {
			tc.BashPrefixes = append([]string(nil), r.Tools.BashPrefixes...)
		}
		clone.Tools = &tc
	}
	if r.MCPServers != nil {
		clone.MCPServers = append([]recipe.MCPServerEntry(nil), r.MCPServers...)
	}
	if r.Tasks != nil {
		clone.Tasks = append([]string(nil), r.Tasks...)
	}
	if r.SuccessCriteria != nil {
		clone.SuccessCriteria = append([]string(nil), r.SuccessCriteria...)
	}
	return &clone
}

// ── apply session recipe ────────────────────────────────────────────────────

// applySessionRecipe syncs the session recipe overrides back to the App's
// runtime fields so that subsequent runs use the updated configuration.
// Also persists the session recipe to disk so changes survive a restart.
func (a *App) applySessionRecipe() {
	rec := a.store.SessionRecipe()
	if rec == nil {
		return
	}
	if rec.Tools != nil {
		a.toolAllowlist = rec.Tools.Allowlist
		a.bashPrefixes = rec.Tools.BashPrefixes
	}
	a.contextStrategy = rec.Context.Strategy
	a.sandboxFilesystem = rec.Sandbox.Filesystem
	a.sandboxNetwork = rec.Sandbox.Network
	if rec.Constraints.ProjectRoot != "" {
		a.projectRoot = rec.Constraints.ProjectRoot
	}
	a.store.PersistSessionRecipe()
}

// syncToolAllowlist rebuilds the session recipe's tool allowlist from the
// current configListItems state and syncs it back to app runtime fields.
func (a *App) syncToolAllowlist() {
	sessRec := a.store.SessionRecipe()
	if sessRec == nil {
		return
	}
	// Build allowlist from active tools in the config list.
	// Use a non-nil empty slice so that unchecking all tools produces
	// "no tools" rather than nil (which means "all tools").
	allowlist := []string{}
	for _, item := range a.configListItems {
		if item.Active {
			allowlist = append(allowlist, item.Label)
		}
	}
	if sessRec.Tools == nil {
		sessRec.Tools = &recipe.ToolsConfig{}
	}
	sessRec.Tools.Allowlist = allowlist
	a.toolAllowlist = allowlist
	a.store.PersistSessionRecipe()
}

// ── rendering ───────────────────────────────────────────────────────────────

func renderConfigOverlay(sections []configSection, selectedIdx, expandedIdx int, width, maxHeight int, listItems []listItem, listCursor, listOffset int, scalarFields []scalarField, scalarCursor int, editBuf string, textEditing bool, textAreaView string, listFilter string) string {
	var sb strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF")).Width(16)
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AF00"))
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

	sb.WriteString(titleStyle.Render("  Configuration"))
	sb.WriteByte('\n')

	if expandedIdx >= 0 && expandedIdx < len(sections) {
		// Expanded section view.
		sec := sections[expandedIdx]
		sb.WriteString(titleStyle.Render(fmt.Sprintf("  Configuration > %s", sec.Name)))
		sb.WriteByte('\n')
		if sec.DetailDesc != "" {
			sb.WriteString(wrapText(sec.DetailDesc, width, 2, dimStyle))
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')

		switch sec.Kind {
		case "list":
			headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

			// Compute visible window. Overhead: title + breadcrumb + desc + blank + footer hint + trailing newline.
			overhead := 5 // title, breadcrumb, blank after desc, footer hint, trailing newline
			if sec.DetailDesc != "" {
				overhead++ // description line
			}
			if listFilter != "" {
				overhead++ // filter bar line
			}
			windowSize := max(len(listItems), 1)
			if maxHeight > 0 {
				windowSize = max(maxHeight-overhead, 3)
			}

			// Filter bar.
			if listFilter != "" {
				sb.WriteString(dimStyle.Render(fmt.Sprintf("  Filter: %s", listFilter)))
				sb.WriteByte('\n')
			}

			start := listOffset
			end := min(start+windowSize, len(listItems))

			if start > 0 {
				sb.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more", start)))
				sb.WriteByte('\n')
			}
			for i := start; i < end; i++ {
				item := listItems[i]
				if item.Header {
					// Non-selectable section header.
					label := fmt.Sprintf("  ── %s ", item.Label)
					padLen := min(max(width-4-len(label), 0), 40)
					sb.WriteString(headerStyle.Render(label + strings.Repeat("─", padLen)))
					sb.WriteByte('\n')
					continue
				}
				cursor := "  "
				if i == listCursor {
					cursor = "> "
				}
				check := "[x]"
				style := activeStyle
				if !item.Active {
					check = "[ ]"
					style = inactiveStyle
				}
				sb.WriteString(style.Render(fmt.Sprintf("  %s%s %s", cursor, check, item.Label)))
				sb.WriteByte('\n')
			}
			if end < len(listItems) {
				sb.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(listItems)-end)))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
			if sec.Name == "models" {
				sb.WriteString(dimStyle.Render("  Space = toggle  Type to filter  Escape = back"))
			} else {
				sb.WriteString(dimStyle.Render("  Space = toggle  Escape = back"))
			}

		case "scalar":
			for i, f := range scalarFields {
				cursor := "  "
				if i == scalarCursor {
					cursor = "> "
				}
				if f.Editing {
					sb.WriteString(selectedStyle.Render(fmt.Sprintf("  %s%-20s %s_", cursor, f.Key+":", editBuf)))
				} else {
					val := f.Value
					if val == "" {
						if dflt, ok := configPathDefaults[f.Path]; ok {
							val = "(default: " + dflt + ")"
						} else {
							val = "(not set)"
						}
					}
					sb.WriteString(dimStyle.Render(fmt.Sprintf("  %s", cursor)))
					sb.WriteString(nameStyle.Render(f.Key + ":"))
					sb.WriteString(dimStyle.Render("  " + val))
				}
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
			sb.WriteString(dimStyle.Render("  Enter = edit field  Escape = back"))

		case "text":
			if textEditing {
				sb.WriteString(textAreaView)
				sb.WriteByte('\n')
				sb.WriteString(dimStyle.Render("  Ctrl+S = save  Escape = cancel"))
			} else {
				preview := editBuf
				if preview == "" {
					preview = "(empty)"
				} else if runes := []rune(preview); len(runes) > 200 {
					preview = string(runes[:200]) + "..."
				}
				for line := range strings.SplitSeq(preview, "\n") {
					sb.WriteString(wrapText(line, width, 2, dimStyle))
					sb.WriteByte('\n')
				}
				sb.WriteString(dimStyle.Render("  Enter = edit  Escape = back"))
			}
		}

		sb.WriteByte('\n')
		return sb.String()
	}

	// Section navigation view.
	for i, sec := range sections {
		cursor := "  "
		if i == selectedIdx {
			cursor = "> "
		}
		prefix := fmt.Sprintf("  %s%-16s ", cursor, sec.Name)
		if i == selectedIdx {
			sb.WriteString(wrapText(prefix+sec.Summary, width, 0, selectedStyle))
			if sec.Desc != "" {
				sb.WriteByte('\n')
				sb.WriteString(wrapText(sec.Desc, width, 4, dimStyle))
			}
		} else {
			sb.WriteString(wrapText(prefix+sec.Summary, width, 0, dimStyle))
		}
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("  Enter = edit section  r = reset  Escape = close"))
	sb.WriteByte('\n')

	return sb.String()
}

// configListWindowHeight returns the number of list items visible in the
// windowed list view, given the current terminal height.
func (a App) configListWindowHeight() int { //nolint:gocritic // called from bubbletea value-receiver methods
	const configHeaderLines = 3 // Errata header + pinned models + blank
	overlayHeight := max(a.height-configHeaderLines, 5)
	// Overhead: title + breadcrumb + desc + blank + footer hint + trailing newline.
	overhead := 5
	if a.configExpandedIdx >= 0 && a.configExpandedIdx < len(a.configSections) {
		if a.configSections[a.configExpandedIdx].DetailDesc != "" {
			overhead++
		}
	}
	return max(overlayHeight-overhead, 3)
}

// ── key handling ────────────────────────────────────────────────────────────

func (a App) handleConfigKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if a.configExpandedIdx >= 0 {
		sec := a.configSections[a.configExpandedIdx]
		switch sec.Kind {
		case "list":
			return a.handleConfigListKey(msg)
		case "scalar":
			return a.handleConfigScalarKey(msg)
		case "text":
			return a.handleConfigTextKey(msg)
		}
		return a, nil
	}
	return a.handleConfigNavKey(msg)
}

func (a App) handleConfigNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	switch msg.Code {
	case tea.KeyEscape:
		a.configOverlayActive = false
		return a, nil
	case tea.KeyUp:
		if a.configSelectedIdx > 0 {
			a.configSelectedIdx--
		}
		return a, nil
	case tea.KeyDown:
		if a.configSelectedIdx < len(a.configSections)-1 {
			a.configSelectedIdx++
		}
		return a, nil
	case tea.KeyEnter:
		a.configExpandedIdx = a.configSelectedIdx
		sec := a.configSections[a.configSelectedIdx]
		sessRec := a.store.SessionRecipe()
		switch sec.Kind {
		case "list":
			switch sec.Name {
			case "models":
				a.configListFilter = ""
				a.configListItems = buildModelsList(a.activeAdapters, a.adapters, a.providerModels, "")
			case "tools":
				a.configListItems = buildToolsList(a.toolAllowlist, a.disabledTools)
			case "mcp-servers":
				// MCP servers are read-only in the overlay for now.
				var items []listItem
				for _, s := range sessRec.MCPServers {
					items = append(items, listItem{Label: s.Name + ": " + s.Command, Active: true})
				}
				a.configListItems = items
			}
			a.configListCursor = 0
			a.configListOffset = 0
		case "scalar":
			a.configScalarFields = buildScalarFields(sec.Name, sessRec)
			a.configScalarCursor = 0
			a.configEditBuf = ""
		case "text":
			content := configPathGet(sessRec, sec.Path)
			a.configEditBuf = content
			a.configTextArea.SetValue(content)
			a.configTextArea.Focus()
			a.configTextEditing = true
		}
		return a, nil
	default:
		if len(msg.Text) > 0 {
			switch strings.ToLower(msg.Text) {
			case "r":
				a.store.SetSessionRecipe(cloneRecipe(a.store.BaseRecipe()))
				a.configSections = buildConfigSections(a.store.SessionRecipe(), a.adapters, a.disabledTools)
				return a, nil
			case "q":
				a.configOverlayActive = false
				return a, nil
			}
		}
	}
	return a, nil
}

// nextNonHeaderDown returns the next non-header index after idx, or idx if none.
func nextNonHeaderDown(items []listItem, idx int) int {
	for i := idx + 1; i < len(items); i++ {
		if !items[i].Header {
			return i
		}
	}
	return idx
}

// nextNonHeaderUp returns the next non-header index before idx, or idx if none.
func nextNonHeaderUp(items []listItem, idx int) int {
	for i := idx - 1; i >= 0; i-- {
		if !items[i].Header {
			return i
		}
	}
	return idx
}

// firstNonHeaderIdx returns the index of the first non-header item, or 0 if none.
func firstNonHeaderIdx(items []listItem) int {
	for i, item := range items {
		if !item.Header {
			return i
		}
	}
	return 0
}

func (a App) handleConfigListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	sec := a.configSections[a.configExpandedIdx]
	isModels := sec.Name == "models"

	switch msg.Code {
	case tea.KeyEscape:
		if isModels && a.configListFilter != "" {
			// Clear filter first; second Escape exits section.
			a.configListFilter = ""
			a.configListItems = buildModelsList(a.activeAdapters, a.adapters, a.providerModels, "")
			a.configListCursor = firstNonHeaderIdx(a.configListItems)
			a.configListOffset = 0
			return a, nil
		}
		a.configExpandedIdx = -1
		a.configListOffset = 0
		a.configListFilter = ""
		a.configSections = buildConfigSections(a.store.SessionRecipe(), a.adapters, a.disabledTools)
		return a, nil
	case tea.KeyUp:
		next := nextNonHeaderUp(a.configListItems, a.configListCursor)
		if next != a.configListCursor {
			a.configListCursor = next
			if a.configListCursor < a.configListOffset {
				a.configListOffset = a.configListCursor
				// Also skip header above if visible.
				if a.configListOffset > 0 && a.configListItems[a.configListOffset-1].Header {
					a.configListOffset--
				}
			}
		}
		return a, nil
	case tea.KeyDown:
		next := nextNonHeaderDown(a.configListItems, a.configListCursor)
		if next != a.configListCursor {
			a.configListCursor = next
			wh := a.configListWindowHeight()
			if a.configListCursor >= a.configListOffset+wh {
				a.configListOffset = a.configListCursor - wh + 1
			}
		}
		return a, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if isModels && a.configListFilter != "" {
			a.configListFilter = a.configListFilter[:len(a.configListFilter)-1]
			a.configListItems = buildModelsList(a.activeAdapters, a.adapters, a.providerModels, a.configListFilter)
			a.configListCursor = firstNonHeaderIdx(a.configListItems)
			a.configListOffset = 0
			return a, nil
		}
		return a, nil
	case tea.KeyEnter, ' ':
		if a.configListCursor < len(a.configListItems) {
			item := &a.configListItems[a.configListCursor]
			if item.Header {
				return a, nil // non-selectable
			}
			toggledID := item.Label

			switch sec.Name {
			case "models":
				if !item.Active {
					// Toggling ON: ensure adapter exists in the pool.
					found := false
					for _, ad := range a.adapters {
						if ad.ID() == toggledID {
							found = true
							break
						}
					}
					if !found {
						newAd, err := adapters.NewAdapter(toggledID, a.cfg)
						if err != nil {
							// Revert toggle — can't create adapter (missing API key, etc.)
							a.configSections = buildConfigSections(a.store.SessionRecipe(), a.adapters, a.disabledTools)
							return a, nil
						}
						a.adapters = append(a.adapters, newAd)
					}
				}
				item.Active = !item.Active
	

				// Rebuild activeAdapters from the list.
				var active []models.ModelAdapter
				for _, li := range a.configListItems {
					if li.Active && !li.Header {
						for _, ad := range a.adapters {
							if ad.ID() == li.Label {
								active = append(active, ad)
								break
							}
						}
					}
				}
				a.activeAdapters = active

				// Sync session recipe models.
				sessRec := a.store.SessionRecipe()
				sessRec.Models = a.activeModelIDs()

				// Rebuild list (model moves between Active/provider sections).
				a.configListItems = buildModelsList(a.activeAdapters, a.adapters, a.providerModels, a.configListFilter)
				// Reposition cursor on the toggled item.
				for i, li := range a.configListItems {
					if !li.Header && li.Label == toggledID {
						a.configListCursor = i
						break
					}
				}
				// Ensure cursor is within visible window.
				wh := a.configListWindowHeight()
				if a.configListCursor < a.configListOffset {
					a.configListOffset = a.configListCursor
					if a.configListOffset > 0 && a.configListItems[a.configListOffset-1].Header {
						a.configListOffset--
					}
				} else if a.configListCursor >= a.configListOffset+wh {
					a.configListOffset = a.configListCursor - wh + 1
				}
			case "tools":
				item.Active = !item.Active
	
				if a.disabledTools == nil {
					a.disabledTools = make(map[string]bool)
				}
				if item.Active {
					delete(a.disabledTools, item.Label)
				} else {
					a.disabledTools[item.Label] = true
				}
				// Sync the effective tool set back to the session recipe allowlist.
				a.syncToolAllowlist()
			default:
				item.Active = !item.Active
	
			}
		}
		return a, nil
	default:
		// Type-to-filter for models section.
		if isModels && len(msg.Text) > 0 {
			a.configListFilter += msg.Text
			a.configListItems = buildModelsList(a.activeAdapters, a.adapters, a.providerModels, a.configListFilter)
			a.configListCursor = firstNonHeaderIdx(a.configListItems)
			a.configListOffset = 0
			return a, nil
		}
	}
	return a, nil
}

func (a App) handleConfigScalarKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	// Check if we're currently editing a field.
	editing := false
	if a.configScalarCursor < len(a.configScalarFields) {
		editing = a.configScalarFields[a.configScalarCursor].Editing
	}

	if editing {
		switch msg.Code {
		case tea.KeyEscape:
			// Cancel editing.
			a.configScalarFields[a.configScalarCursor].Editing = false
			a.configEditBuf = ""
			return a, nil
		case tea.KeyEnter:
			// Confirm edit.
			field := &a.configScalarFields[a.configScalarCursor]
			err := setConfigValue(a.store.SessionRecipe(), field.Path, a.configEditBuf)
			if err != nil {
				// Leave editing mode but don't apply.
				field.Editing = false
				a.configEditBuf = ""
				return a, nil
			}
			field.Value = a.configEditBuf
			field.Editing = false
			a.configEditBuf = ""

			a.applySessionRecipe()
			return a, nil
		case tea.KeyBackspace, tea.KeyDelete:
			if len(a.configEditBuf) > 0 {
				a.configEditBuf = a.configEditBuf[:len(a.configEditBuf)-1]
			}
			return a, nil
		default:
			if len(msg.Text) > 0 {
				a.configEditBuf += msg.Text
			}
			return a, nil
		}
	}

	switch msg.Code {
	case tea.KeyEscape:
		a.configExpandedIdx = -1
		a.configSections = buildConfigSections(a.store.SessionRecipe(), a.adapters, a.disabledTools)
		return a, nil
	case tea.KeyUp:
		if a.configScalarCursor > 0 {
			a.configScalarCursor--
		}
		return a, nil
	case tea.KeyDown:
		if a.configScalarCursor < len(a.configScalarFields)-1 {
			a.configScalarCursor++
		}
		return a, nil
	case tea.KeyEnter:
		if a.configScalarCursor < len(a.configScalarFields) {
			field := &a.configScalarFields[a.configScalarCursor]
			field.Editing = true
			a.configEditBuf = field.Value
		}
		return a, nil
	}
	return a, nil
}

func (a App) handleConfigTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	if !a.configTextEditing {
		// Dead-end state — should not normally be reached since save/cancel
		// now return to section navigation. Handle defensively: Escape or
		// any key goes back.
		a.configExpandedIdx = -1
		a.configSections = buildConfigSections(a.store.SessionRecipe(), a.adapters, a.disabledTools)
		return a, nil
	}

	// Ctrl+S or Ctrl+D saves the text and returns to section navigation.
	if msg.Mod.Contains(tea.ModCtrl) && (msg.Code == 's' || msg.Code == 'd') {
		sec := a.configSections[a.configExpandedIdx]
		val := a.configTextArea.Value()
		_ = setConfigValue(a.store.SessionRecipe(), sec.Path, val)
		a.configTextEditing = false
		a.configTextArea.Blur()
		a.applySessionRecipe()
		a.configExpandedIdx = -1
		a.configSections = buildConfigSections(a.store.SessionRecipe(), a.adapters, a.disabledTools)
		return a, nil
	}

	// Escape cancels editing and returns to section navigation.
	if msg.Code == tea.KeyEscape {
		a.configTextEditing = false
		a.configTextArea.Blur()
		a.configExpandedIdx = -1
		a.configSections = buildConfigSections(a.store.SessionRecipe(), a.adapters, a.disabledTools)
		return a, nil
	}

	// Delegate all other keys to the textarea.
	var cmd tea.Cmd
	a.configTextArea, cmd = a.configTextArea.Update(msg)
	return a, cmd
}
