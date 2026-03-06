package config

import (
	"strings"

	"github.com/suarezc/errata/pkg/recipe"
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

	// ── Sub-Agent ──
	if r.HasSection("sub-agent") {
		cfg.SubagentModel = r.SubAgent.Model
		if r.SubAgent.MaxDepth == -1 {
			// Section present but max_depth not mentioned → disable (0).
			cfg.SubagentMaxDepth = 0
		} else {
			cfg.SubagentMaxDepth = r.SubAgent.MaxDepth
		}
	} else {
		if r.SubAgent.Model != "" {
			cfg.SubagentModel = r.SubAgent.Model
		}
		if r.SubAgent.MaxDepth >= 0 {
			cfg.SubagentMaxDepth = r.SubAgent.MaxDepth
		}
	}

	// ── Context ──
	if r.HasSection("context") {
		cfg.MaxHistoryTurns = r.Context.MaxHistoryTurns
		cfg.CompactThreshold = r.Context.CompactThreshold
	} else {
		if r.Context.MaxHistoryTurns > 0 {
			cfg.MaxHistoryTurns = r.Context.MaxHistoryTurns
		}
		if r.Context.CompactThreshold > 0 {
			cfg.CompactThreshold = r.Context.CompactThreshold
		}
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

	// ── Model Parameters ──
	if r.HasSection("model parameters") {
		cfg.Seed = r.ModelParams.Seed // nil if not mentioned → clears default
	} else if r.ModelParams.Seed != nil {
		cfg.Seed = r.ModelParams.Seed
	}
}
