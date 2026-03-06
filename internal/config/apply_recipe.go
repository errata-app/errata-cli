package config

import (
	"strings"

	"github.com/suarezc/errata/pkg/recipe"
)

// ApplyRecipe overlays recipe settings onto cfg.
// Only fields that are explicitly set in the recipe override cfg values;
// unset recipe fields leave cfg unchanged.
func ApplyRecipe(r *recipe.Recipe, cfg *Config) {
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
	if r.Constraints.MaxSteps > 0 {
		cfg.MaxSteps = r.Constraints.MaxSteps
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
