package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/tools"
)

// ── types ───────────────────────────────────────────────────────────────────

// configSection represents one section in the config overlay.
type configSection struct {
	Name    string // kebab-case: "models", "system-prompt", etc.
	Summary string // one-line summary of current value
	Kind    string // "list" | "scalar" | "text"
}

// listItem is one toggleable row in a list-type section editor.
type listItem struct {
	Label  string
	Active bool
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
	"parameters", "constraints", "context", "sub-agent", "sandbox",
}

// ── section builders ────────────────────────────────────────────────────────

func buildConfigSections(rec *recipe.Recipe, adapters []models.ModelAdapter, disabled map[string]bool) []configSection {
	if rec == nil {
		rec = recipe.Default()
	}
	return []configSection{
		{Name: "models", Summary: summarizeModels(rec, adapters), Kind: "list"},
		{Name: "system-prompt", Summary: summarizeSystemPrompt(rec), Kind: "text"},
		{Name: "tools", Summary: summarizeTools(rec, disabled), Kind: "list"},
		{Name: "mcp-servers", Summary: summarizeMCPServers(rec), Kind: "list"},
		{Name: "parameters", Summary: summarizeParameters(rec), Kind: "scalar"},
		{Name: "constraints", Summary: summarizeConstraints(rec), Kind: "scalar"},
		{Name: "context", Summary: summarizeContext(rec), Kind: "scalar"},
		{Name: "sub-agent", Summary: summarizeSubAgent(rec), Kind: "scalar"},
		{Name: "sandbox", Summary: summarizeSandbox(rec), Kind: "scalar"},
	}
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

func summarizeParameters(rec *recipe.Recipe) string {
	var parts []string
	if rec.ModelParams.Seed != nil {
		parts = append(parts, fmt.Sprintf("seed: %d", *rec.ModelParams.Seed))
	}
	if rec.ModelParams.Temperature != nil {
		parts = append(parts, fmt.Sprintf("temperature: %.1f", *rec.ModelParams.Temperature))
	}
	if rec.ModelParams.MaxTokens != nil {
		parts = append(parts, fmt.Sprintf("max_tokens: %d", *rec.ModelParams.MaxTokens))
	}
	if len(parts) == 0 {
		return "(defaults)"
	}
	return strings.Join(parts, ", ")
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
		return "(defaults)"
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
		return "(defaults)"
	}
	return strings.Join(parts, ", ")
}

func summarizeSubAgent(rec *recipe.Recipe) string {
	var parts []string
	if rec.SubAgent.Model != "" {
		parts = append(parts, rec.SubAgent.Model)
	}
	if rec.SubAgent.MaxDepth >= 0 {
		parts = append(parts, fmt.Sprintf("depth: %d", rec.SubAgent.MaxDepth))
	}
	if rec.SubAgent.Tools != "" {
		parts = append(parts, "tools: "+rec.SubAgent.Tools)
	}
	if len(parts) == 0 {
		return "(defaults)"
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
		return "(defaults)"
	}
	return strings.Join(parts, ", ")
}

// ── list/scalar data builders ───────────────────────────────────────────────

func buildModelsList(rec *recipe.Recipe, adapters []models.ModelAdapter, activeAdapters []models.ModelAdapter) []listItem {
	activeSet := make(map[string]bool)
	if activeAdapters != nil {
		for _, a := range activeAdapters {
			activeSet[a.ID()] = true
		}
	}
	var items []listItem
	for _, a := range adapters {
		active := activeAdapters == nil || activeSet[a.ID()]
		items = append(items, listItem{Label: a.ID(), Active: active})
	}
	return items
}

func buildToolsList(disabled map[string]bool) []listItem {
	var items []listItem
	for _, d := range tools.Definitions {
		items = append(items, listItem{Label: d.Name, Active: !disabled[d.Name]})
	}
	return items
}

func buildScalarFields(sectionName string, rec *recipe.Recipe) []scalarField {
	switch sectionName {
	case "parameters":
		return []scalarField{
			{Key: "seed", Path: "parameters.seed", Value: configPathGet(rec, "parameters.seed")},
			{Key: "temperature", Path: "parameters.temperature", Value: configPathGet(rec, "parameters.temperature")},
			{Key: "max_tokens", Path: "parameters.max_tokens", Value: configPathGet(rec, "parameters.max_tokens")},
		}
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
	case "sub-agent":
		return []scalarField{
			{Key: "model", Path: "sub_agent.model", Value: configPathGet(rec, "sub_agent.model")},
			{Key: "max_depth", Path: "sub_agent.max_depth", Value: configPathGet(rec, "sub_agent.max_depth")},
			{Key: "tools", Path: "sub_agent.tools", Value: configPathGet(rec, "sub_agent.tools")},
		}
	case "sandbox":
		return []scalarField{
			{Key: "filesystem", Path: "sandbox.filesystem", Value: configPathGet(rec, "sandbox.filesystem")},
			{Key: "network", Path: "sandbox.network", Value: configPathGet(rec, "sandbox.network")},
		}
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
	"sub_agent.model": {
		Get: func(r *recipe.Recipe) string { return r.SubAgent.Model },
		Set: func(r *recipe.Recipe, v string) error {
			r.SubAgent.Model = v
			return nil
		},
	},
	"sub_agent.max_depth": {
		Get: func(r *recipe.Recipe) string {
			if r.SubAgent.MaxDepth < 0 {
				return ""
			}
			return strconv.Itoa(r.SubAgent.MaxDepth)
		},
		Set: func(r *recipe.Recipe, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid integer %q", v)
			}
			r.SubAgent.MaxDepth = n
			return nil
		},
	},
	"sub_agent.tools": {
		Get: func(r *recipe.Recipe) string { return r.SubAgent.Tools },
		Set: func(r *recipe.Recipe, v string) error {
			r.SubAgent.Tools = v
			return nil
		},
	},
	"parameters.seed": {
		Get: func(r *recipe.Recipe) string {
			if r.ModelParams.Seed == nil {
				return ""
			}
			return strconv.FormatInt(*r.ModelParams.Seed, 10)
		},
		Set: func(r *recipe.Recipe, v string) error {
			if v == "" {
				r.ModelParams.Seed = nil
				return nil
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid integer %q", v)
			}
			r.ModelParams.Seed = &n
			return nil
		},
	},
	"parameters.temperature": {
		Get: func(r *recipe.Recipe) string {
			if r.ModelParams.Temperature == nil {
				return ""
			}
			return strconv.FormatFloat(*r.ModelParams.Temperature, 'f', -1, 64)
		},
		Set: func(r *recipe.Recipe, v string) error {
			if v == "" {
				r.ModelParams.Temperature = nil
				return nil
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("invalid float %q", v)
			}
			r.ModelParams.Temperature = &f
			return nil
		},
	},
	"parameters.max_tokens": {
		Get: func(r *recipe.Recipe) string {
			if r.ModelParams.MaxTokens == nil {
				return ""
			}
			return strconv.Itoa(*r.ModelParams.MaxTokens)
		},
		Set: func(r *recipe.Recipe, v string) error {
			if v == "" {
				r.ModelParams.MaxTokens = nil
				return nil
			}
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid positive integer %q", v)
			}
			r.ModelParams.MaxTokens = &n
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
	if r.ModelParams.Seed != nil {
		v := *r.ModelParams.Seed
		clone.ModelParams.Seed = &v
	}
	if r.ModelParams.Temperature != nil {
		v := *r.ModelParams.Temperature
		clone.ModelParams.Temperature = &v
	}
	if r.ModelParams.MaxTokens != nil {
		v := *r.ModelParams.MaxTokens
		clone.ModelParams.MaxTokens = &v
	}
	return &clone
}

// ── apply session recipe ────────────────────────────────────────────────────

// applySessionRecipe syncs the session recipe overrides back to the App's
// runtime fields so that subsequent runs use the updated configuration.
func (a *App) applySessionRecipe() {
	if a.sessionRecipe == nil {
		return
	}
	rec := a.sessionRecipe
	if rec.Tools != nil {
		a.toolAllowlist = rec.Tools.Allowlist
		a.bashPrefixes = rec.Tools.BashPrefixes
	}
	a.contextStrategy = rec.Context.Strategy
	a.sandboxFilesystem = rec.Sandbox.Filesystem
	a.sandboxNetwork = rec.Sandbox.Network
	if rec.Metadata.ProjectRoot != "" {
		a.projectRoot = rec.Metadata.ProjectRoot
	}
	if rec.ModelParams.Seed != nil {
		a.seed = rec.ModelParams.Seed
	}
	if rec.Constraints.Timeout > 0 {
		a.cfg.AgentTimeout = rec.Constraints.Timeout
	}
	if rec.Context.MaxHistoryTurns > 0 {
		a.cfg.MaxHistoryTurns = rec.Context.MaxHistoryTurns
	}
	if rec.Context.CompactThreshold > 0 {
		a.cfg.CompactThreshold = rec.Context.CompactThreshold
	}
}

// ── rendering ───────────────────────────────────────────────────────────────

func renderConfigOverlay(sections []configSection, selectedIdx, expandedIdx int, modified bool, width int, listItems []listItem, listCursor int, scalarFields []scalarField, scalarCursor int, editBuf string) string {
	var sb strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF")).Width(16)
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AF00"))
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

	title := "  Configuration"
	if modified {
		title += "  [modified]"
	}
	sb.WriteString(titleStyle.Render(title))
	sb.WriteByte('\n')

	if expandedIdx >= 0 && expandedIdx < len(sections) {
		// Expanded section view.
		sec := sections[expandedIdx]
		sb.WriteString(titleStyle.Render(fmt.Sprintf("  Configuration > %s", sec.Name)))
		sb.WriteByte('\n')

		switch sec.Kind {
		case "list":
			for i, item := range listItems {
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
			sb.WriteByte('\n')
			sb.WriteString(dimStyle.Render("  Space = toggle  Escape = back"))

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
						val = "(not set)"
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
			sb.WriteString(dimStyle.Render(fmt.Sprintf("  %s", editBuf)))
			sb.WriteByte('\n')
			sb.WriteString(dimStyle.Render("  (editing not yet implemented — use /set)"))
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
		line := fmt.Sprintf("  %s%-16s %s", cursor, sec.Name, sec.Summary)
		if i == selectedIdx {
			sb.WriteString(selectedStyle.Render(line))
		} else {
			sb.WriteString(dimStyle.Render(line))
		}
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(dimStyle.Render("  Enter = edit section  r = reset  Escape = close"))
	sb.WriteByte('\n')

	return sb.String()
}

// ── key handling ────────────────────────────────────────────────────────────

func (a App) handleConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.configExpandedIdx >= 0 {
		sec := a.configSections[a.configExpandedIdx]
		switch sec.Kind {
		case "list":
			return a.handleConfigListKey(msg)
		case "scalar":
			return a.handleConfigScalarKey(msg)
		}
		// text: fall through to close on escape
		if msg.Type == tea.KeyEsc {
			a.configExpandedIdx = -1
			return a, nil
		}
		return a, nil
	}
	return a.handleConfigNavKey(msg)
}

func (a App) handleConfigNavKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
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
		switch sec.Kind {
		case "list":
			switch sec.Name {
			case "models":
				a.configListItems = buildModelsList(a.sessionRecipe, a.adapters, a.activeAdapters)
			case "tools":
				a.configListItems = buildToolsList(a.disabledTools)
			case "mcp-servers":
				// MCP servers are read-only in the overlay for now.
				var items []listItem
				for _, s := range a.sessionRecipe.MCPServers {
					items = append(items, listItem{Label: s.Name + ": " + s.Command, Active: true})
				}
				a.configListItems = items
			}
			a.configListCursor = 0
		case "scalar":
			a.configScalarFields = buildScalarFields(sec.Name, a.sessionRecipe)
			a.configScalarCursor = 0
			a.configEditBuf = ""
		case "text":
			a.configEditBuf = a.sessionRecipe.SystemPrompt
		}
		return a, nil
	case tea.KeyRunes:
		if string(msg.Runes) == "r" || string(msg.Runes) == "R" {
			a.sessionRecipe = cloneRecipe(a.recipe)
			a.recipeModified = false
			a.configSections = buildConfigSections(a.sessionRecipe, a.adapters, a.disabledTools)
			return a, nil
		}
		if string(msg.Runes) == "q" || string(msg.Runes) == "Q" {
			a.configOverlayActive = false
			return a, nil
		}
	}
	return a, nil
}

func (a App) handleConfigListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		a.configExpandedIdx = -1
		a.configSections = buildConfigSections(a.sessionRecipe, a.adapters, a.disabledTools)
		return a, nil
	case tea.KeyUp:
		if a.configListCursor > 0 {
			a.configListCursor--
		}
		return a, nil
	case tea.KeyDown:
		if a.configListCursor < len(a.configListItems)-1 {
			a.configListCursor++
		}
		return a, nil
	case tea.KeyEnter, tea.KeySpace:
		if a.configListCursor < len(a.configListItems) {
			item := &a.configListItems[a.configListCursor]
			item.Active = !item.Active
			a.recipeModified = true

			sec := a.configSections[a.configExpandedIdx]
			switch sec.Name {
			case "models":
				// Sync toggled models back to activeAdapters.
				var active []models.ModelAdapter
				for _, li := range a.configListItems {
					if li.Active {
						for _, ad := range a.adapters {
							if ad.ID() == li.Label {
								active = append(active, ad)
								break
							}
						}
					}
				}
				if len(active) == len(a.adapters) {
					a.activeAdapters = nil // all active = no filter
				} else {
					a.activeAdapters = active
				}
			case "tools":
				if a.disabledTools == nil {
					a.disabledTools = make(map[string]bool)
				}
				if item.Active {
					delete(a.disabledTools, item.Label)
				} else {
					a.disabledTools[item.Label] = true
				}
			}
		}
		return a, nil
	}
	return a, nil
}

func (a App) handleConfigScalarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check if we're currently editing a field.
	editing := false
	if a.configScalarCursor < len(a.configScalarFields) {
		editing = a.configScalarFields[a.configScalarCursor].Editing
	}

	if editing {
		switch msg.Type {
		case tea.KeyEsc:
			// Cancel editing.
			a.configScalarFields[a.configScalarCursor].Editing = false
			a.configEditBuf = ""
			return a, nil
		case tea.KeyEnter:
			// Confirm edit.
			field := &a.configScalarFields[a.configScalarCursor]
			err := setConfigValue(a.sessionRecipe, field.Path, a.configEditBuf)
			if err != nil {
				// Leave editing mode but don't apply.
				field.Editing = false
				a.configEditBuf = ""
				return a, nil
			}
			field.Value = a.configEditBuf
			field.Editing = false
			a.configEditBuf = ""
			a.recipeModified = true
			a.applySessionRecipe()
			return a, nil
		case tea.KeyBackspace, tea.KeyDelete:
			if len(a.configEditBuf) > 0 {
				a.configEditBuf = a.configEditBuf[:len(a.configEditBuf)-1]
			}
			return a, nil
		case tea.KeyRunes:
			a.configEditBuf += string(msg.Runes)
			return a, nil
		}
		return a, nil
	}

	switch msg.Type {
	case tea.KeyEsc:
		a.configExpandedIdx = -1
		a.configSections = buildConfigSections(a.sessionRecipe, a.adapters, a.disabledTools)
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
