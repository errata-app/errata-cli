// Package headless runs Errata recipe tasks without user interaction.
//
// It iterates over the tasks defined in a recipe, fans each out to all
// configured model adapters via runner.RunAll, evaluates success criteria,
// and produces a structured JSON report.
package headless

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/errata-app/errata-cli/internal/adapters"
	"github.com/errata-app/errata-cli/internal/api"
	"github.com/errata-app/errata-cli/internal/checkpoint"
	"github.com/errata-app/errata-cli/internal/criteria"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
	"github.com/errata-app/errata-cli/internal/prompt"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/internal/runner"
	"github.com/errata-app/errata-cli/internal/sandbox"
	"github.com/errata-app/errata-cli/internal/tools"
)

// Options controls headless execution behaviour.
type Options struct {
	Recipe         *recipe.Recipe
	Adapters       []models.ModelAdapter
	OutputDir      string // directory for output reports (required)
	CheckpointPath string // path for checkpoint file (required)
	Verbose        bool
	JSON           bool // also emit report to stdout
	FullUpload     bool // --full flag: override privacy to upload full report

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
	if err := rec.ValidateVersion(); err != nil {
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
	// Generate the report ID early so we can use it for the bundled run directory.
	reportID := newReportID()
	runDir := filepath.Join(opts.OutputDir, RunDirName(recipeName(rec), reportID))
	workBase := filepath.Join(runDir, "worktrees")

	setupStart := time.Now()
	workDirs, _, baselines, cleanupFn, err := createModelWorkDirs(cwd, workBase, opts.Adapters)
	if err != nil {
		return nil, fmt.Errorf("create work dirs (is there enough disk space?): %w", err)
	}
	setupMs := time.Since(setupStart).Milliseconds()

	isGit := isGitRepo(cwd)
	mode := "snapshot"
	if isGit {
		mode = "git worktree"
	} else if baselines == nil {
		mode = "copy"
	}
	fmt.Fprintf(w, "errata: worktrees ready (%s mode, %dms, base: %s)\n", mode, setupMs, workBase)

	if opts.Verbose {
		for id, dir := range workDirs {
			fmt.Fprintf(w, "  %s → %s\n", id, dir)
		}
	}

	// cleanupFn is kept for error-path cleanup only; worktrees are preserved for inspection.
	_ = cleanupFn

	histories := make(map[string][]models.ConversationTurn)
	var taskResults []TaskResult
	var totalCost float64

	for i, taskPrompt := range rec.Tasks {
		fmt.Fprintf(w, "\n[%d/%d] %s\n", i+1, len(rec.Tasks), truncate(taskPrompt, 70))

		// Independent mode: reset histories each task.
		if taskMode == "independent" {
			histories = make(map[string][]models.ConversationTurn)
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
				status := string(resp.StopReason)
				if status == "" {
					status = "done"
				}
				fmt.Fprintf(w, "  %-22s %-11s  %5dms  $%.4f\n",
					resp.ModelID, status, resp.LatencyMS, resp.CostUSD)
			}
			return nil, fmt.Errorf("interrupted at task %d/%d", i+1, len(rec.Tasks))
		}

		// Post-process: diff each worktree to populate ProposedWrites.
		for j := range responses {
			if dir := workDirs[responses[j].ModelID]; dir != "" {
				var writes []tools.FileWrite
				var diffErr error
				if snap, ok := baselines[responses[j].ModelID]; ok {
					writes, diffErr = diffSnapshot(dir, snap)
				} else {
					writes, diffErr = diffWorktree(dir)
				}
				if diffErr != nil {
					fmt.Fprintf(w, "  warning: diff for %s: %v\n", responses[j].ModelID, diffErr)
				}
				responses[j].ProposedWrites = writes
			}
		}

		// Build the per-task output report.
		toolNames := toolNameList(activeDefs)
		report := output.BuildReport("", rec, taskPrompt, responses, collector, toolNames)

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
		ID:        reportID,
		Timestamp: time.Now().UTC(),
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

	path, err := Save(runDir, headlessReport)
	if err != nil {
		fmt.Fprintf(w, "warning: could not save report: %v\n", err)
	} else {
		fmt.Fprintf(w, "\nReport saved to %s\n", path)
	}

	metaReport := BuildMetadataReport(headlessReport)
	if metaPath, metaErr := SaveMetadata(runDir, metaReport); metaErr != nil {
		fmt.Fprintf(w, "warning: could not save metadata report: %v\n", metaErr)
	} else {
		fmt.Fprintf(w, "Metadata report saved to %s\n", metaPath)
	}

	// Upload report if logged in (non-fatal).
	if client := api.NewClient(); client.IsLoggedIn() {
		var reportBytes []byte
		if opts.FullUpload {
			reportBytes, _ = json.Marshal(headlessReport)
		} else {
			reportBytes, _ = json.Marshal(metaReport)
		}
		if uploadErr := client.UploadReport(json.RawMessage(reportBytes)); uploadErr != nil {
			fmt.Fprintf(w, "warning: report upload failed: %v\n", uploadErr)
		} else {
			fmt.Fprintf(w, "Report uploaded to errata.app\n")
		}
	}

	fmt.Fprintf(w, "Run output saved to %s\n", runDir)
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
	if rec.Tools != nil {
		activeDefs = tools.ApplyDescriptions(activeDefs, rec.Tools.Guidance)
	}
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
	if rec.Tools != nil {
		ctx = tools.WithToolGuidanceMap(ctx, rec.Tools.Guidance)
	}
	ctx = prompt.WithSummarizationPrompt(ctx, rec.Context.SummarizationPrompt)
	ctx = sandbox.WithConfig(ctx, sandbox.Config{
		Filesystem:      rec.Sandbox.Filesystem,
		Network:         rec.Sandbox.Network,
		ProjectRoot:     rec.Constraints.ProjectRoot,
		AllowLocalFetch: rec.Sandbox.AllowLocalFetch,
	})
	if rec.Constraints.BashTimeout > 0 {
		ctx = tools.WithBashTimeout(ctx, rec.Constraints.BashTimeout)
	}
	ctx = runner.WithRunOptions(ctx, runner.RunOptions{
		Timeout:          rec.Constraints.Timeout,
		CompactThreshold: rec.Context.CompactThreshold,
		MaxHistoryTurns:  rec.Context.MaxHistoryTurns,
		MaxSteps:         rec.Constraints.MaxSteps,
		CheckpointPath:   opts.CheckpointPath,
		WorkDirs:         workDirs,
	})
	return ctx
}

// ─── Per-model filesystem isolation ──────────────────────────────────────────

// sanitizeModelID replaces characters that are unsafe for directory names.
func sanitizeModelID(id string) string {
	return strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(id)
}

// createModelWorkDirs creates an isolated working directory for each adapter.
// In a git repo, it creates lightweight worktrees (git worktree add --detach).
// In a non-git directory with git available, it copies the project and creates a git baseline.
// Without git, it copies the project and takes a checksum snapshot for diffing.
// baseDir specifies where to create the worktrees. If empty, a temp directory is used.
// Returns the work-dir map (adapter ID → abs path), the base directory,
// baselines (non-nil only for snapshot mode), and a cleanup function.
func createModelWorkDirs(projectDir, baseDir string, adpts []models.ModelAdapter) (dirs map[string]string, base string, baselines map[string]map[string]string, cleanup func(), err error) {
	var tmpBase string
	if baseDir != "" {
		if mkErr := os.MkdirAll(baseDir, 0o750); mkErr != nil {
			return nil, "", nil, nil, fmt.Errorf("create base dir: %w", mkErr)
		}
		tmpBase = baseDir
	} else {
		var mkErr error
		tmpBase, mkErr = os.MkdirTemp("", "errata-workdirs-*")
		if mkErr != nil {
			return nil, "", nil, nil, fmt.Errorf("create temp dir: %w", mkErr)
		}
	}

	dirMap := make(map[string]string, len(adpts))
	isGit := isGitRepo(projectDir)
	gitAvail := gitAvailable()
	var snapshotBaselines map[string]map[string]string

	for _, a := range adpts {
		dirName := "errata-" + sanitizeModelID(a.ID())
		worktree := filepath.Join(tmpBase, dirName)

		if isGit {
			// Create a detached worktree from HEAD.
			cmd := exec.Command("git", "-C", projectDir, "worktree", "add", "--detach", worktree, "HEAD")
			if out, gitErr := cmd.CombinedOutput(); gitErr != nil {
				// Clean up already-created worktrees.
				cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
				return nil, "", nil, nil, fmt.Errorf("git worktree add for %s: %w\n%s", a.ID(), gitErr, out)
			}
		} else if gitAvail {
			// Non-git fallback with git available: copy directory and create a baseline commit.
			if cpErr := copyDir(projectDir, worktree); cpErr != nil {
				cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
				return nil, "", nil, nil, fmt.Errorf("copy for %s: %w", a.ID(), cpErr)
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
					return nil, "", nil, nil, fmt.Errorf("git %v for %s: %w\n%s", args[0], a.ID(), gitErr, out)
				}
			}
		} else {
			// No git: copy directory and take a checksum snapshot for diffing.
			if cpErr := copyDir(projectDir, worktree); cpErr != nil {
				cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
				return nil, "", nil, nil, fmt.Errorf("copy for %s: %w", a.ID(), cpErr)
			}
			snap, snapErr := snapshotDir(worktree)
			if snapErr != nil {
				cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase)
				return nil, "", nil, nil, fmt.Errorf("snapshot for %s: %w", a.ID(), snapErr)
			}
			if snapshotBaselines == nil {
				snapshotBaselines = make(map[string]map[string]string, len(adpts))
			}
			snapshotBaselines[a.ID()] = snap
		}
		dirMap[a.ID()] = worktree
	}

	return dirMap, tmpBase, snapshotBaselines, func() { cleanupWorkDirs(projectDir, dirMap, isGit, tmpBase) }, nil
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

// gitAvailable reports whether a git binary is on $PATH.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// copyDir recursively copies src into dst, creating dst if needed.
// It skips .git/ directories and preserves file modes and symlinks.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o750)
		}

		// Symlinks: recreate the link rather than copying the target.
		if d.Type()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}

		// Regular files: copy contents preserving mode.
		if !d.Type().IsRegular() {
			return nil // skip special files (devices, sockets, etc.)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// snapshotDir walks root and returns a map of relative-path → SHA-256 hex digest
// for every regular file. It skips .git/ directories and symlinks.
func snapshotDir(root string) (map[string]string, error) {
	snap := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks and other non-regular files
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		h := sha256.Sum256(data)
		snap[rel] = hex.EncodeToString(h[:])
		return nil
	})
	return snap, err
}

// diffSnapshot compares the current state of dir against a baseline snapshot
// and returns FileWrite entries for every added, modified, or deleted file.
func diffSnapshot(dir string, baseline map[string]string) ([]tools.FileWrite, error) {
	var writes []tools.FileWrite
	seen := make(map[string]bool)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		seen[rel] = true
		h := sha256.Sum256(data)
		hexHash := hex.EncodeToString(h[:])
		if oldHash, exists := baseline[rel]; !exists || oldHash != hexHash {
			writes = append(writes, tools.FileWrite{Path: rel, Content: string(data)})
		}
		return nil
	})
	if err != nil {
		return writes, err
	}

	// Detect deletions: baseline paths no longer present on disk.
	for rel := range baseline {
		if !seen[rel] {
			writes = append(writes, tools.FileWrite{Path: rel, Delete: true})
		}
	}
	return writes, nil
}

// diffWorktree returns the files changed in the worktree relative to HEAD.
// Each changed/added file is returned as a FileWrite with its current content.
// Deleted files are returned with Delete: true.
func diffWorktree(dir string) ([]tools.FileWrite, error) {
	// Get modified files.
	cmd := exec.Command("git", "-C", dir, "diff", "--name-only", "HEAD")
	modOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	// Get deleted files.
	cmd = exec.Command("git", "-C", dir, "diff", "--name-only", "--diff-filter=D", "HEAD")
	delOut, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --diff-filter=D: %w", err)
	}

	deleted := make(map[string]bool)
	for line := range strings.SplitSeq(strings.TrimSpace(string(delOut)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			deleted[line] = true
		}
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
		if deleted[f] {
			writes = append(writes, tools.FileWrite{Path: f, Delete: true})
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(dir, f))
		if readErr != nil {
			continue // file may have been deleted outside git tracking
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
	return "default"
}

func printModelResult(w io.Writer, resp models.ModelResponse, cr []criteria.Result, totalCriteria int) {
	status := string(resp.StopReason)
	if status == "" {
		status = "done"
	}
	tok := fmtTokens(resp.InputTokens, resp.OutputTokens)
	steps := fmt.Sprintf("%d steps", resp.Steps)

	if totalCriteria > 0 {
		passed := criteria.PassCount(cr)
		mark := "✓"
		if passed < totalCriteria {
			mark = "✗"
		}
		if resp.Error != "" {
			fmt.Fprintf(w, "  %-22s %-10s  %s  %s %d/%d criteria\n",
				resp.ModelID, status, truncate(resp.Error, 30), mark, passed, totalCriteria)
		} else {
			fmt.Fprintf(w, "  %-22s %-10s  %5dms  %s  %s  $%.4f  %s %d/%d criteria\n",
				resp.ModelID, status, resp.LatencyMS, steps, tok, resp.CostUSD, mark, passed, totalCriteria)
		}
	} else {
		if resp.Error != "" {
			fmt.Fprintf(w, "  %-22s %-10s  %s\n",
				resp.ModelID, status, truncate(resp.Error, 50))
		} else {
			fmt.Fprintf(w, "  %-22s %-10s  %5dms  %s  %s  $%.4f\n",
				resp.ModelID, status, resp.LatencyMS, steps, tok, resp.CostUSD)
		}
	}
}

// fmtTokens formats input/output token counts as a compact string like "12.3k in / 4.5k out".
func fmtTokens(input, output int64) string {
	return fmt.Sprintf("%s in / %s out", fmtCount(input), fmtCount(output))
}

// fmtCount formats a token count: values ≥1000 are shown as "X.Yk", otherwise as-is.
func fmtCount(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
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
