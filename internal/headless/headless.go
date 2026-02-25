// Package headless runs Errata recipe tasks without user interaction.
//
// It iterates over the tasks defined in a recipe, fans each out to all
// configured model adapters via runner.RunAll, evaluates success criteria,
// and produces a structured JSON report.
package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/criteria"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/sandbox"
	"github.com/suarezc/errata/internal/subagent"
	"github.com/suarezc/errata/internal/tools"
)

// Options controls headless execution behaviour.
type Options struct {
	Recipe     *recipe.Recipe
	Adapters   []models.ModelAdapter
	SessionID  string
	Cfg        config.Config
	OutputDir  string // "" → output.DefaultDir
	Verbose    bool
	JSON       bool // also emit report to stdout

	// MCP state.
	MCPDefs        []tools.ToolDef
	MCPDispatchers map[string]tools.MCPDispatcher

	// Stderr is the writer for progress output. nil → os.Stderr.
	Stderr io.Writer
}

func (o Options) stderr() io.Writer {
	if o.Stderr != nil {
		return o.Stderr
	}
	return os.Stderr
}

// Run executes all recipe tasks and returns the headless report.
func Run(ctx context.Context, opts Options) (*RunReport, error) {
	rec := opts.Recipe
	if len(rec.Tasks) == 0 {
		return nil, fmt.Errorf("recipe has no tasks — ## Tasks section is required for headless mode")
	}

	w := opts.stderr()
	taskMode := rec.Context.TaskMode
	if taskMode == "" {
		taskMode = "independent"
	}

	parsedCriteria := criteria.Parse(rec.SuccessCriteria)

	// Build the tool set once (no session-level disabling in headless mode).
	activeDefs := buildActiveDefs(rec, opts.MCPDefs)

	fmt.Fprintf(w, "errata: %s (%d tasks, %d models, task_mode=%s)\n",
		recipeName(rec), len(rec.Tasks), len(opts.Adapters), taskMode)

	histories := make(map[string][]models.ConversationTurn)
	var taskResults []TaskResult
	var totalCost float64

	for i, taskPrompt := range rec.Tasks {
		fmt.Fprintf(w, "\n[%d/%d] %s\n", i+1, len(rec.Tasks), truncate(taskPrompt, 70))

		// Independent mode: reset histories each task.
		if taskMode == "independent" {
			histories = make(map[string][]models.ConversationTurn)
		}

		// Auto-compact in sequential mode when threshold exceeded.
		if taskMode == "sequential" && rec.Context.Strategy != "manual" && rec.Context.Strategy != "off" {
			for _, ad := range opts.Adapters {
				if runner.ShouldAutoCompact(histories, ad.ID(), opts.Cfg.CompactThreshold) {
					fmt.Fprintf(w, "  [auto-compacting history for %s…]\n", ad.ID())
					histories = runner.CompactHistories(
						ctx, []models.ModelAdapter{ad},
						histories, func(id string, e models.AgentEvent) {},
					)
				}
			}
		}

		runCtx := buildRunContext(ctx, opts, rec, activeDefs)

		collector := output.NewCollector()
		onEvent := collector.WrapOnEvent(func(modelID string, event models.AgentEvent) {
			if opts.Verbose {
				fmt.Fprintf(w, "    [%s] %s: %s\n", modelID, event.Type, truncate(event.Data, 80))
			}
		})

		responses := runner.RunAll(runCtx, opts.Adapters, histories, taskPrompt, onEvent, opts.Verbose)

		// Check for context cancellation (SIGINT/SIGTERM).
		if ctx.Err() != nil {
			adapterIDs := make([]string, len(opts.Adapters))
			for j, a := range opts.Adapters {
				adapterIDs[j] = a.ID()
			}
			if cp := checkpoint.Build(taskPrompt, adapterIDs, responses, opts.Verbose); cp != nil {
				if err := checkpoint.Save(checkpoint.DefaultPath, *cp); err != nil {
					fmt.Fprintf(w, "warning: could not save checkpoint: %v\n", err)
				} else {
					fmt.Fprintf(w, "\nCheckpoint saved to %s. Use `errata run` again to resume.\n", checkpoint.DefaultPath)
				}
			}
			// Print partial results for the interrupted task.
			for _, resp := range responses {
				status := "done"
				if resp.Interrupted {
					status = "interrupted"
				} else if resp.Error != "" {
					status = "error"
				}
				fmt.Fprintf(w, "  %-22s %-11s  %5dms  $%.4f\n",
					resp.ModelID, status, resp.LatencyMS, resp.CostUSD)
			}
			return nil, fmt.Errorf("interrupted at task %d/%d", i+1, len(rec.Tasks))
		}

		// Build the per-task output report.
		toolNames := toolNameList(activeDefs)
		report := output.BuildReport(opts.SessionID, rec, taskPrompt, responses, collector, toolNames)

		// Evaluate criteria.
		criteriaResults := make(map[string][]criteria.Result)
		for _, resp := range responses {
			results := criteria.Evaluate(parsedCriteria, resp)
			criteriaResults[resp.ModelID] = results
		}

		// Print per-model status.
		for _, resp := range responses {
			cr := criteriaResults[resp.ModelID]
			printModelResult(w, resp, cr, len(parsedCriteria))
		}

		taskResult := TaskResult{
			Index:           i,
			Prompt:          taskPrompt,
			Report:          report,
			CriteriaResults: criteriaResults,
		}

		// Sequential mode: pick winner, apply writes, carry history.
		if taskMode == "sequential" {
			winner := selectWinner(responses, criteriaResults)
			if winner != nil {
				taskResult.SelectedModel = winner.ModelID
				if len(winner.ProposedWrites) > 0 {
					if err := tools.ApplyWrites(winner.ProposedWrites); err != nil {
						fmt.Fprintf(w, "  warning: could not apply writes from %s: %v\n", winner.ModelID, err)
					} else {
						fmt.Fprintf(w, "  → applied %d files from %s\n", len(winner.ProposedWrites), winner.ModelID)
					}
				}
			}
		}

		// Update conversation histories.
		adapterIDs := make([]string, len(opts.Adapters))
		for j, a := range opts.Adapters {
			adapterIDs[j] = a.ID()
		}
		histories = runner.AppendHistory(histories, adapterIDs, responses, taskPrompt)

		for _, resp := range responses {
			totalCost += resp.CostUSD
		}

		taskResults = append(taskResults, taskResult)
	}

	summary := buildSummary(taskResults, parsedCriteria, totalCost)

	headlessReport := &RunReport{
		ID:        newReportID(),
		Timestamp: time.Now().UTC(),
		SessionID: opts.SessionID,
		Recipe:    snapshotRecipe(rec),
		TaskMode:  taskMode,
		Tasks:     taskResults,
		Summary:   summary,
	}

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = output.DefaultDir
	}
	path, err := Save(outputDir, headlessReport)
	if err != nil {
		fmt.Fprintf(w, "warning: could not save report: %v\n", err)
	} else {
		fmt.Fprintf(w, "\nReport saved to %s\n", path)
	}

	fmt.Fprintf(w, "Summary: %d tasks, $%.4f total cost\n", len(rec.Tasks), totalCost)

	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(headlessReport)
	}

	return headlessReport, nil
}

// ─── Context building ─────────────────────────────────────────────────────────

// buildActiveDefs constructs the tool list from recipe settings.
func buildActiveDefs(rec *recipe.Recipe, mcpDefs []tools.ToolDef) []tools.ToolDef {
	var toolAllowlist []string
	if rec.Tools != nil {
		toolAllowlist = rec.Tools.Allowlist
	}
	activeDefs := tools.DefinitionsAllowed(toolAllowlist, nil)
	activeDefs = append(activeDefs, tools.FilterDefs(mcpDefs, nil)...)
	// Apply sandbox restrictions.
	if rec.Sandbox.Filesystem == "read_only" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WriteToolName: true,
			tools.EditToolName:  true,
		})
	}
	if rec.Sandbox.Network == "none" {
		activeDefs = tools.FilterDefs(activeDefs, map[string]bool{
			tools.WebFetchToolName:  true,
			tools.WebSearchToolName: true,
		})
	}
	return activeDefs
}

// buildRunContext creates the fully-wired context for a single task run.
func buildRunContext(parent context.Context, opts Options, rec *recipe.Recipe, activeDefs []tools.ToolDef) context.Context {
	var bashPrefixes []string
	if rec.Tools != nil {
		bashPrefixes = rec.Tools.BashPrefixes
	}

	ctx := tools.WithActiveTools(parent, activeDefs)
	ctx = tools.WithMCPDispatchers(ctx, opts.MCPDispatchers)
	ctx = tools.WithBashPrefixes(ctx, bashPrefixes)
	ctx = sandbox.WithConfig(ctx, sandbox.Config{
		Filesystem:  rec.Sandbox.Filesystem,
		Network:     rec.Sandbox.Network,
		ProjectRoot: rec.Metadata.ProjectRoot,
	})
	ctx = runner.WithRunOptions(ctx, runner.RunOptions{
		Timeout:          opts.Cfg.AgentTimeout,
		CompactThreshold: opts.Cfg.CompactThreshold,
		MaxHistoryTurns:  opts.Cfg.MaxHistoryTurns,
		CheckpointPath:   checkpoint.DefaultPath,
	})
	ctx = tools.WithSubagentDispatcher(ctx, subagent.NewDispatcher(
		opts.Adapters, opts.Cfg, opts.MCPDispatchers,
		func(modelID string, e models.AgentEvent) {
			if opts.Verbose {
				fmt.Fprintf(opts.stderr(), "    [sub-agent %s] %s: %s\n", modelID, e.Type, truncate(e.Data, 80))
			}
		},
	))
	ctx = tools.WithSubagentDepth(ctx, 0)
	if opts.Cfg.Seed != nil {
		ctx = tools.WithSeed(ctx, *opts.Cfg.Seed)
	}
	return ctx
}

// ─── Winner selection (sequential mode) ───────────────────────────────────────

// selectWinner picks the best model for sequential mode.
// Priority: most criteria passed → lowest cost → lowest latency.
// Returns nil if no successful responses exist.
func selectWinner(
	responses []models.ModelResponse,
	criteriaResults map[string][]criteria.Result,
) *models.ModelResponse {
	var best *models.ModelResponse
	bestScore := -1
	var bestCost float64
	var bestLatency int64

	for i := range responses {
		resp := &responses[i]
		if !resp.OK() {
			continue
		}
		score := criteria.PassCount(criteriaResults[resp.ModelID])
		better := score > bestScore ||
			(score == bestScore && resp.CostUSD < bestCost) ||
			(score == bestScore && resp.CostUSD == bestCost && resp.LatencyMS < bestLatency)
		if best == nil || better {
			best = resp
			bestScore = score
			bestCost = resp.CostUSD
			bestLatency = resp.LatencyMS
		}
	}
	return best
}

// ─── Summary builder ──────────────────────────────────────────────────────────

func buildSummary(tasks []TaskResult, parsedCriteria []criteria.Criterion, totalCost float64) Summary {
	perModel := make(map[string]ModelSummary)
	completed := 0

	for _, task := range tasks {
		if task.Report != nil {
			completed++
		}
		for _, mr := range task.Report.Models {
			ms := perModel[mr.ModelID]
			if mr.Error == "" {
				ms.TasksSucceeded++
			}
			ms.TotalCostUSD += mr.CostUSD
			ms.AvgLatencyMS += float64(mr.LatencyMS) // accumulate; divide later

			if cr, ok := task.CriteriaResults[mr.ModelID]; ok {
				ms.CriteriaPassed += criteria.PassCount(cr)
				ms.CriteriaTotal += len(cr)
			}

			perModel[mr.ModelID] = ms
		}
	}

	// Convert accumulated latency to average.
	for id, ms := range perModel {
		taskCount := 0
		for _, task := range tasks {
			for _, mr := range task.Report.Models {
				if mr.ModelID == id {
					taskCount++
				}
			}
		}
		if taskCount > 0 {
			ms.AvgLatencyMS = ms.AvgLatencyMS / float64(taskCount)
		}
		perModel[id] = ms
	}

	return Summary{
		TotalTasks:     len(tasks),
		CompletedTasks: completed,
		TotalCostUSD:   totalCost,
		PerModel:       perModel,
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func snapshotRecipe(rec *recipe.Recipe) RecipeSnapshot {
	return RecipeSnapshot{
		Name:            recipeName(rec),
		Models:          rec.Models,
		SystemPrompt:    rec.SystemPrompt,
		Tasks:           rec.Tasks,
		SuccessCriteria: rec.SuccessCriteria,
	}
}

func recipeName(rec *recipe.Recipe) string {
	if rec.Name != "" {
		return rec.Name
	}
	if rec.Metadata.Name != "" {
		return rec.Metadata.Name
	}
	return "default"
}

func printModelResult(w io.Writer, resp models.ModelResponse, cr []criteria.Result, totalCriteria int) {
	status := "done"
	if resp.Error != "" {
		status = "error"
	}

	if totalCriteria > 0 {
		passed := criteria.PassCount(cr)
		mark := "✓"
		if passed < totalCriteria {
			mark = "✗"
		}
		if resp.Error != "" {
			fmt.Fprintf(w, "  %-22s %-5s  %s  %s %d/%d criteria\n",
				resp.ModelID, status, truncate(resp.Error, 30), mark, passed, totalCriteria)
		} else {
			fmt.Fprintf(w, "  %-22s %-5s  %5dms  $%.4f  %s %d/%d criteria\n",
				resp.ModelID, status, resp.LatencyMS, resp.CostUSD, mark, passed, totalCriteria)
		}
	} else {
		if resp.Error != "" {
			fmt.Fprintf(w, "  %-22s %-5s  %s\n",
				resp.ModelID, status, truncate(resp.Error, 50))
		} else {
			fmt.Fprintf(w, "  %-22s %-5s  %5dms  $%.4f\n",
				resp.ModelID, status, resp.LatencyMS, resp.CostUSD)
		}
	}
}

func toolNameList(defs []tools.ToolDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
