package config

import (
	"strings"

	"github.com/errata-app/errata-cli/pkg/recipe"
)

// ApplyRecipe overlays recipe settings onto cfg.
//
// When a section was declared in the parsed recipe (tracked by SectionsPresent),
// ALL fields in that section are written atomically — even zero values — so that
// declaring "## Constraints" with only max_steps clears the default timeout.
//
// When a section is absent (or SectionsPresent is nil, i.e. programmatic recipes),
// legacy field-by-field merge is used: only non-zero recipe fields override cfg.
func ApplyRecipe(r *recipe.Recipe, cfg *Config) {
	// ── Single-value fields (already atomic) ──
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

	// ── Context ──
	if r.HasSection("context") {
		cfg.MaxHistoryTurns = r.Context.MaxHistoryTurns
	} else if r.Context.MaxHistoryTurns > 0 {
		cfg.MaxHistoryTurns = r.Context.MaxHistoryTurns
	}

	// ── Constraints ──
	if r.HasSection("constraints") {
		cfg.MaxSteps = r.Constraints.MaxSteps
		cfg.AgentTimeout = r.Constraints.Timeout
	} else {
		if r.Constraints.MaxSteps > 0 {
			cfg.MaxSteps = r.Constraints.MaxSteps
		}
		if r.Constraints.Timeout > 0 {
			cfg.AgentTimeout = r.Constraints.Timeout
		}
	}
}
