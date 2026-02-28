// Package subagent implements sub-agent spawning for the spawn_agent tool.
// It builds the SubagentDispatcher that is injected into the run context
// by both the TUI (ui/cmd_handlers.go) and the web handler (web/handlers.go).
package subagent

import (
	"context"
	"fmt"

	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// NewDispatcher returns a SubagentDispatcher that builds and runs sub-agents.
//
// allAdapters is the full list of configured adapters; model routing for
// spawn_agent's optional model_id arg searches this list.
//
// cfg supplies SubagentModel (default model override) and SubagentMaxDepth
// (recursion limit; 1 = no recursion by default).
//
// mcpDispatchers is the MCP dispatcher table so sub-agents can call MCP tools.
//
// onEvent routes sub-agent tool events to the parent's UI panel; the modelID
// string is the sub-agent's model ID (used for panel routing).
func NewDispatcher(
	allAdapters []models.ModelAdapter,
	cfg config.Config,
	mcpDispatchers map[string]tools.MCPDispatcher,
	onEvent func(modelID string, e models.AgentEvent),
) tools.SubagentDispatcher {
	// self is captured below to allow recursive dispatcher injection.
	var self tools.SubagentDispatcher

	self = func(ctx context.Context, args map[string]string) (string, []tools.FileWrite, string) {
		depth := tools.SubagentDepthFromContext(ctx)
		if depth >= cfg.SubagentMaxDepth {
			return "", nil, fmt.Sprintf("[spawn_agent error: max sub-agent depth (%d) reached]", cfg.SubagentMaxDepth)
		}

		task := args["task"]
		if task == "" {
			return "", nil, "[spawn_agent error: task argument is required]"
		}

		// Resolve the adapter to use.
		adapter := resolveAdapter(args["model_id"], cfg.SubagentModel, allAdapters)
		if adapter == nil {
			return "", nil, "[spawn_agent error: no adapter available for sub-agent]"
		}

		role := args["role"]
		if role == "" {
			role = tools.RoleCoder
		}

		// Build the sub-agent's tool set.
		parentDefs := tools.ActiveToolsFromContext(ctx)
		subDefs := tools.ToolsForRole(role, parentDefs)

		// Strip spawn_agent if the sub-agent would be at max depth, preventing
		// the model from attempting to spawn further sub-agents.
		if depth+1 >= cfg.SubagentMaxDepth {
			subDefs = filterOut(subDefs, tools.SpawnAgentToolName)
		}

		// Build the sub-agent context.
		subCtx := tools.WithActiveTools(ctx, subDefs)
		subCtx = tools.WithMCPDispatchers(subCtx, mcpDispatchers)
		subCtx = tools.WithSubagentDepth(subCtx, depth+1)
		subCtx = tools.WithSubagentDispatcher(subCtx, self)

		// Run the sub-agent with a fresh conversation (task-only, no parent history).
		resp, _ := adapter.RunAgent(subCtx, nil, task, func(e models.AgentEvent) {
			onEvent(adapter.ID(), e)
		})

		// Emit a summary event so the parent panel shows the sub-agent result.
		summary := fmt.Sprintf("[sub-agent %s: %d in / %d out tok",
			adapter.ID(), resp.InputTokens, resp.OutputTokens)
		if resp.CostUSD > 0 {
			summary += fmt.Sprintf(" · $%.4f", resp.CostUSD)
		}
		summary += "]"
		onEvent(adapter.ID(), models.AgentEvent{Type: models.EventText, Data: summary})

		if !resp.OK() {
			return "", nil, fmt.Sprintf("[spawn_agent error: %s]", resp.Error)
		}
		return resp.Text, resp.ProposedWrites, ""
	}

	return self
}

// resolveAdapter finds the adapter to use for a sub-agent run.
// Priority: explicit modelID arg → cfg.SubagentModel → first adapter in allAdapters.
func resolveAdapter(modelIDArg, cfgModel string, all []models.ModelAdapter) models.ModelAdapter {
	if modelIDArg != "" {
		if a := findByID(all, modelIDArg); a != nil {
			return a
		}
	}
	if cfgModel != "" {
		if a := findByID(all, cfgModel); a != nil {
			return a
		}
	}
	if len(all) > 0 {
		return all[0]
	}
	return nil
}

// findByID returns the first adapter whose ID matches target, or nil.
func findByID(all []models.ModelAdapter, target string) models.ModelAdapter {
	for _, a := range all {
		if a.ID() == target {
			return a
		}
	}
	return nil
}

// filterOut returns a copy of defs with any entry named name removed.
func filterOut(defs []tools.ToolDef, name string) []tools.ToolDef {
	out := make([]tools.ToolDef, 0, len(defs))
	for _, d := range defs {
		if d.Name != name {
			out = append(out, d)
		}
	}
	return out
}
