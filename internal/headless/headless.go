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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/criteria"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/output"
	"github.com/suarezc/errata/internal/prompt"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/sandbox"
	"github.com/suarezc/errata/internal/subagent"
	"github.com/suarezc/errata/internal/tools"
)

// Options controls headless execution behaviour.
type Options struct {
	Recipe         *recipe.Recipe
	Adapters       []models.ModelAdapter
	SessionID      string
	Cfg            config.Config
	OutputDir      string // directory for output reports (required)
	CheckpointPath string // path for checkpoint file (required)
	Verbose        bool
	JSON           bool // also emit report to stdout

	// DebugLog enables raw API request logging in adapter loops.
	DebugLog bool

	// MCP state.
	MCPDefs        []tools.ToolDef
	MCPDispatchers map[string]tools.MCPDispatcher

	// Stderr is the writer for progress output. nil → os.Stderr.
	Stderr io.Writer
}

func (o *Options) stderr() io.Writer {
	if o.Stderr != nil {
		return o.Stderr
	}
	return os.Stderr
}

// Run executes all recipe tasks and returns the headless report.
func Run(ctx context.Context, opts *Options) (*RunReport, error) {
	rec := opts.Recipe

	// Validate recipe version before execution.
	if _, err := rec.BuildRunner(); err != nil {
		return nil, err
	}

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

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine working directory: %w", err)
	}

	fmt.Fprintf(w, "errata: %s (%d tasks, %d models, task_mode=%s)\n",
		recipeName(rec), len(rec.Tasks), len(opts.Adapters), taskMode)

	// Create per-model working directories for filesystem isolation.
	setupStart := time.Now()
	workDirs, workBase, cleanup, err := createModelWorkDirs(cwd, opts.Adapters)
	if err != nil {
		return nil, fmt.Errorf("create work dirs (is there enough disk space?): %w", err)
	}
	setupMs := time.Since(setupStart).Milliseconds()

	isGit := isGitRepo(cwd)
	mode := "copy"
	if isGit {
		mode = "git worktree"
	}
	fmt.Fprintf(w, "errata: worktrees ready (%s mode, %dms, base: %s)\n", mode, setupMs, workBase)

	if opts.Verbose {
		for id, dir := range workDirs {
			fmt.Fprintf(w, "  %s → %s\n", id, dir)
		}
	}

	defer func() {
		cleanup()
		fmt.Fprintf(w, "errata: worktrees cleaned up\n")
	}()

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

		runCtx := buildRunContext(ctx, opts, rec, activeDefs, workDirs)

		collector := output.NewCollector()
		onEvent := collector.WrapOnEvent(func(modelID string, event models.AgentEvent) {
			if opts.Verbose {
				fmt.Fprintf(w, "    [%s] %s: %s\n", modelID, event.Type, truncate(event.Data, 80))
			}
		})

		responses := runner.RunAll(runCtx, opts.Adapters, histories, taskPrompt, onEvent, nil, opts.Verbose)

		// Check for context cancellation (SIGINT/SIGTERM).
		if ctx.Err() != nil {
			ids := adapterIDs(opts.Adapters)
			if cp := checkpoint.Build(taskPrompt, ids, responses, opts.Verbose); cp != nil {
				if err := checkpoint.Save(opts.CheckpointPath, *cp); err != nil {
					fmt.Fprintf(w, "warning: could not save checkpoint: %v\n", err)
				} else {
					fmt.Fprintf(w, "\nCheckpoint saved to %s. Use `errata run` again to resume.\n", opts.CheckpointPath)
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

		// Post-process: diff each worktree to populate ProposedWrites.
		for j := range responses {
			if dir := workDirs[responses[j].ModelID]; dir != "" {
				writes, diffErr := diffWorktree(dir)
				if diffErr != nil {
					fmt.Fprintf(w, "  warning: diff for %s: %v\n", responses[j].ModelID, diffErr)
				}
				responses[j].ProposedWrites = writes
			}
		}

		// Build the per-task output report.
		toolNames := toolNameList(activeDefs)
		report := output.BuildReport(opts.SessionID, rec, taskPrompt, responses, collector, toolNames)

		// Evaluate criteria.
		criteriaResults := make(map[string][]criteria.Result)
		for _, resp := range responses {
			ectx := criteria.EvalContext{WorkDir: workDirs[resp.ModelID]}
			results := criteria.Evaluate(parsedCriteria, resp, ectx)
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

		// Update conversation histories.
		histories = runner.AppendHistory(histories, adapterIDs(opts.Adapters), responses, taskPrompt)

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
		Setup: SetupInfo{
			WorktreeBase: workBase,
			SetupMS:      setupMs,
			GitMode:      isGit,
			ModelDirs:    workDirs,
		},
	}

	outputDir := opts.OutputDir
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
	// Apply recipe-level tool description overrides (uniform for all models).
	activeDefs = tools.ApplyDescriptions(activeDefs, rec.ToolDescriptions)
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
func buildRunContext(parent context.Context, opts *Options, rec *recipe.Recipe, activeDefs []tools.ToolDef, workDirs map[string]string) context.Context {
	var bashPrefixes []string
	if rec.Tools != nil {
		bashPrefixes = rec.Tools.BashPrefixes
	}

	ctx := parent
	if opts.DebugLog {
		ctx = adapters.WithDebugRequests(ctx)
	}
	ctx = tools.WithActiveTools(ctx, activeDefs)
	ctx = tools.WithMCPDispatchers(ctx, opts.MCPDispatchers)
	ctx = tools.WithBashPrefixes(ctx, bashPrefixes)
	ctx = tools.WithSystemPromptExtra(ctx, rec.SystemPrompt)
	ctx = tools.WithToolGuidance(ctx, rec.ToolGuidance)
	ctx = prompt.WithSummarizationPrompt(ctx, rec.SummarizationPrompt)
	ctx = sandbox.WithConfig(ctx, sandbox.Config{
		Filesystem:  rec.Sandbox.Filesystem,
		Network:     rec.Sandbox.Network,
		ProjectRoot: rec.Metadata.ProjectRoot,
	})
	ctx = runner.WithRunOptions(ctx, runner.RunOptions{
		Timeout:          opts.Cfg.AgentTimeout,
		CompactThreshold: opts.Cfg.CompactThreshold,
		MaxHistoryTurns:  opts.Cfg.MaxHistoryTurns,
		MaxSteps:         opts.Cfg.MaxSteps,
		CheckpointPath:   opts.CheckpointPath,
		WorkDirs:         workDirs,
	})
	if tools.SubagentEnabled {
		ctx = tools.WithSubagentDispatcher(ctx, subagent.NewDispatcher(
			opts.Adapters, opts.Cfg, opts.MCPDispatchers,
			func(modelID string, e models.AgentEvent) {
				if opts.Verbose {
					fmt.Fprintf(opts.stderr(), "    [sub-agent %s] %s: %s\n", modelID, e.Type, truncate(e.Data, 80))
				}
			},
		))
		ctx = tools.WithSubagentDepth(ctx, 0)
	}
	if opts.Cfg.Seed != nil {
		ctx = tools.WithSeed(ctx, *opts.Cfg.Seed)
	}
	return ctx
}

// ─── Per-model filesystem isolation ──────────────────────────────────────────

// sanitizeModelID replaces characters that are unsafe for directory names.
func sanitizeModelID(id string) string {
	return strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(id)
}

// createModelWorkDirs creates an isolated working directory for each adapter.
// In a git repo, it creates lightweight worktrees (git worktree add --detach).
// In a non-git directory, it copies the project and creates a git baseline.
// Returns the work-dir map (adapter ID → abs path), the temp base directory,
// and a cleanup function.
func createModelWorkDirs(projectDir string, adpts []models.ModelAdapter) (dirs map[string]string, base string, cleanup func(), err error) {
	tmpBase, mkErr := os.MkdirTemp("", "errata-workdirs-*")
	if mkErr != nil {
		return nil, "", nil, fmt.Errorf("create temp dir: %w", mkErr)
	}

	dirMap := make(map[string]string, len(adpts))
	isGit := isGitRepo(projectDir)

	for _, a := range adpts {
		dirName := "errata-" + sanitizeModelID(a.ID())
		worktree := filepath.Join(tmpBase, dirName)

		if isGit {
			// Create a detached worktree from HEAD.
			cmd := exec.Command("git", "-C", projectDir, "worktree", "add", "--detach", worktree, "HEAD")
			if out, gitErr := cmd.CombinedOutput(); gitErr != nil {
				// Clean up already-created worktrees.
				cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
				return nil, "", nil, fmt.Errorf("git worktree add for %s: %w\n%s", a.ID(), gitErr, out)
			}
		} else {
			// Non-git fallback: copy directory and create a baseline commit.
			cmd := exec.Command("cp", "-a", projectDir+"/.", worktree+"/")
			if mkdirErr := os.MkdirAll(worktree, 0o750); mkdirErr != nil {
				cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
				return nil, "", nil, fmt.Errorf("mkdir for %s: %w", a.ID(), mkdirErr)
			}
			if out, cpErr := cmd.CombinedOutput(); cpErr != nil {
				cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
				return nil, "", nil, fmt.Errorf("cp for %s: %w\n%s", a.ID(), cpErr, out)
			}
			// Create git baseline for diffing.
			for _, args := range [][]string{
				{"init"},
				{"add", "-A"},
				{"commit", "-m", "baseline", "--allow-empty"},
			} {
				gitCmd := exec.Command("git", args...)
				gitCmd.Dir = worktree
				if out, gitErr := gitCmd.CombinedOutput(); gitErr != nil {
					cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
					return nil, "", nil, fmt.Errorf("git %v for %s: %w\n%s", args[0], a.ID(), gitErr, out)
				}
			}
		}
		dirMap[a.ID()] = worktree
	}

	return dirMap, tmpBase, func() { cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase) }, nil
}

// cleanupWorkDirs removes worktrees (or plain directories) and the temp base.
func cleanupWorkDirs(projectDir string, dirs map[string]string, isGit bool, tmpBase string) {
	if isGit {
		for _, dir := range dirs {
			_ = exec.Command("git", "-C", projectDir, "worktree", "remove", "--force", dir).Run()
		}
	}
	_ = os.RemoveAll(tmpBase)
}

// isGitRepo checks whether the given directory is inside a git repository.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

// diffWorktree returns the files changed in the worktree relative to HEAD.
// Each changed/added file is returned as a FileWrite with its current content.
func diffWorktree(dir string) ([]tools.FileWrite, error) {
	// Get modified files.
	cmd := exec.Command("git", "-C", dir, "diff", "--name-only", "HEAD")
	modOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	// Get newly added (untracked) files.
	cmd = exec.Command("git", "-C", dir, "ls-files", "--others", "--exclude-standard")
	untrackedOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}

	// Combine and deduplicate.
	seen := make(map[string]bool)
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(modOut)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(untrackedOut)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
	}

	var writes []tools.FileWrite
	for _, f := range files {
		content, readErr := os.ReadFile(filepath.Join(dir, f))
		if readErr != nil {
			continue // file may have been deleted
		}
		writes = append(writes, tools.FileWrite{Path: f, Content: string(content)})
	}
	return writes, nil
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
			ms.AvgLatencyMS /= float64(taskCount)
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
		Version:         rec.Version,
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

// adapterIDs returns the IDs of all adapters.
func adapterIDs(adapters []models.ModelAdapter) []string {
	ids := make([]string, len(adapters))
	for i, a := range adapters {
		ids[i] = a.ID()
	}
	return ids
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
