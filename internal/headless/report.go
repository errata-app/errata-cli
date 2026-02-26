package headless

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/suarezc/errata/internal/criteria"
	"github.com/suarezc/errata/internal/logging"
	"github.com/suarezc/errata/internal/output"
)

// RunReport is the top-level JSON report produced by `errata run`.
type RunReport struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`

	Recipe   RecipeSnapshot `json:"recipe"`
	TaskMode string         `json:"task_mode"`

	Tasks []TaskResult `json:"tasks"`

	Summary Summary `json:"summary"`
}

// RecipeSnapshot is a JSON-safe subset of recipe.Recipe for the report.
type RecipeSnapshot struct {
	Name            string   `json:"name"`
	Models          []string `json:"models,omitempty"`
	SystemPrompt    string   `json:"system_prompt,omitempty"`
	Tasks           []string `json:"tasks"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
}

// TaskResult captures one task's execution and evaluation.
type TaskResult struct {
	Index           int                            `json:"index"`
	Prompt          string                         `json:"prompt"`
	Report          *output.Report                 `json:"report"`
	CriteriaResults map[string][]criteria.Result   `json:"criteria_results"`
	SelectedModel   string                         `json:"selected_model,omitempty"`
}

// Summary aggregates across all tasks.
type Summary struct {
	TotalTasks     int                     `json:"total_tasks"`
	CompletedTasks int                     `json:"completed_tasks"`
	TotalCostUSD   float64                 `json:"total_cost_usd"`
	PerModel       map[string]ModelSummary `json:"per_model"`
}

// ModelSummary is per-model aggregate across all tasks.
type ModelSummary struct {
	TasksSucceeded int     `json:"tasks_succeeded"`
	CriteriaPassed int     `json:"criteria_passed"`
	CriteriaTotal  int     `json:"criteria_total"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	AvgLatencyMS   float64 `json:"avg_latency_ms"`
}

// Filename returns the output filename for the headless report.
func (r *RunReport) Filename() string {
	return output.SanitizeName(r.Recipe.Name) + "_run_" + r.ID + ".json"
}

// Save writes the report as pretty-printed JSON to dir/{filename}.
// Parent directories are created as needed. Returns the full path.
func Save(dir string, report *RunReport) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	path := filepath.Join(dir, report.Filename())
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}
	return path, nil
}

// Load reads a RunReport JSON file at the given path.
func Load(path string) (*RunReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r RunReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &r, nil
}

// newReportID generates a random hex ID for reports.
func newReportID() string {
	return logging.RandomHex(8)
}
