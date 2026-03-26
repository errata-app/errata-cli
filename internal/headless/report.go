package headless

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/errata-app/errata-cli/internal/criteria"
	"github.com/errata-app/errata-cli/internal/jsonutil"
	"github.com/errata-app/errata-cli/internal/output"
	"github.com/errata-app/errata-cli/internal/uid"
)

// RunReport is the top-level JSON report produced by `errata run`.
type RunReport struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	ConfigHash string    `json:"config_hash,omitempty"`

	Recipe   RecipeSnapshot `json:"recipe"`
	TaskMode string         `json:"task_mode"`

	Tasks []TaskResult `json:"tasks"`

	Summary Summary `json:"summary"`
	Setup   SetupInfo `json:"setup"`
}

// SetupInfo records worktree creation metadata for debugging.
type SetupInfo struct {
	WorktreeBase string            `json:"worktree_base"`
	SetupMS      int64             `json:"setup_ms"`
	GitMode      bool              `json:"git_mode"`
	ModelDirs    map[string]string `json:"model_dirs"`
}

// RecipeSnapshot is a JSON-safe subset of recipe.Recipe for the report.
type RecipeSnapshot struct {
	Name            string   `json:"name"`
	Version         int      `json:"version"`
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

// Filename returns the fixed filename within the bundled run directory.
func (r *RunReport) Filename() string {
	return "report.json"
}

// RunDirName returns the directory name for a bundled run output.
func RunDirName(recipeName, reportID string) string {
	return output.SanitizeName(recipeName) + "_run_" + reportID
}

// Save writes the report as pretty-printed JSON to dir/report.json.
// Parent directories are created as needed. Returns the full path.
func Save(dir string, report *RunReport) (string, error) {
	return jsonutil.SaveJSON(dir, report.Filename(), report)
}

// Load reads a RunReport JSON file at the given path.
func Load(path string) (*RunReport, error) {
	return jsonutil.LoadJSON[RunReport](path)
}

// newReportID generates a type-prefixed UUID v7 report ID.
func newReportID() string {
	return uid.New("rpt_")
}

// ─── Metadata Report ─────────────────────────────────────────────────────────

// MetadataReport is a shareable, redacted report containing only benchmark
// metrics — no prompts, responses, file contents, or raw events.
type MetadataReport struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	ConfigHash string    `json:"config_hash,omitempty"`

	Recipe   MetaRecipeSnapshot `json:"recipe"`
	TaskMode string             `json:"task_mode"`

	Tasks []MetaTaskResult `json:"tasks"`

	Summary Summary `json:"summary"`
}

// MetaRecipeSnapshot captures recipe configuration without the system prompt.
type MetaRecipeSnapshot struct {
	Name            string   `json:"name"`
	Version         int      `json:"version"`
	Models          []string `json:"models,omitempty"`
	Tasks           []string `json:"tasks"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
}

// MetaTaskResult captures one task's metrics without sensitive content.
type MetaTaskResult struct {
	Index           int                          `json:"index"`
	PromptHash      string                       `json:"prompt_hash"`
	Models          []MetaModelResult            `json:"models"`
	CriteriaResults map[string][]criteria.Result  `json:"criteria_results,omitempty"`
	SelectedModel   string                       `json:"selected_model,omitempty"`
}

// MetaModelResult captures per-model metrics without text or file contents.
type MetaModelResult struct {
	ModelID           string         `json:"model_id"`
	LatencyMS         int64          `json:"latency_ms"`
	InputTokens       int64          `json:"input_tokens"`
	OutputTokens      int64          `json:"output_tokens"`
	ReasoningTokens   int64          `json:"reasoning_tokens,omitempty"`
	CostUSD           float64        `json:"cost_usd"`
	StopReason        string         `json:"stop_reason,omitempty"`
	Steps             int            `json:"steps,omitempty"`
	ToolCalls         map[string]int `json:"tool_calls,omitempty"`
	FilesChangedCount int            `json:"files_changed_count"`
	Error             string         `json:"error,omitempty"`
}

// BuildMetadataReport constructs a MetadataReport from a full RunReport,
// hashing prompts and stripping sensitive content.
func BuildMetadataReport(full *RunReport) *MetadataReport {
	meta := &MetadataReport{
		ID:         full.ID,
		Timestamp:  full.Timestamp,
		ConfigHash: full.ConfigHash,
		Recipe: MetaRecipeSnapshot{
			Name:            full.Recipe.Name,
			Version:         full.Recipe.Version,
			Models:          full.Recipe.Models,
			Tasks:           full.Recipe.Tasks,
			SuccessCriteria: full.Recipe.SuccessCriteria,
		},
		TaskMode: full.TaskMode,
		Tasks:    make([]MetaTaskResult, len(full.Tasks)),
		Summary:  full.Summary,
	}

	for i, task := range full.Tasks {
		hash := sha256.Sum256([]byte(task.Prompt))

		var metaModels []MetaModelResult
		if task.Report != nil {
			metaModels = make([]MetaModelResult, len(task.Report.Models))
			for j, mr := range task.Report.Models {
				metaModels[j] = MetaModelResult{
					ModelID:           mr.ModelID,
					LatencyMS:         mr.LatencyMS,
					InputTokens:       mr.InputTokens,
					OutputTokens:      mr.OutputTokens,
					ReasoningTokens:   mr.ReasoningTokens,
					CostUSD:           mr.CostUSD,
					StopReason:        mr.StopReason,
					Steps:             mr.Steps,
					ToolCalls:         mr.ToolCalls,
					FilesChangedCount: len(mr.ProposedWrites),
					Error:             mr.Error,
				}
			}
		}

		// Redact criteria results.
		var redactedCriteria map[string][]criteria.Result
		if len(task.CriteriaResults) > 0 {
			redactedCriteria = make(map[string][]criteria.Result, len(task.CriteriaResults))
			for modelID, results := range task.CriteriaResults {
				redactedCriteria[modelID] = criteria.RedactSensitiveDetails(results)
			}
		}

		meta.Tasks[i] = MetaTaskResult{
			Index:           task.Index,
			PromptHash:      fmt.Sprintf("ph_%x", hash),
			Models:          metaModels,
			CriteriaResults: redactedCriteria,
			SelectedModel:   task.SelectedModel,
		}
	}

	return meta
}

// SaveMetadata writes the metadata report as pretty-printed JSON to dir/meta.json.
// Parent directories are created as needed. Returns the full path.
func SaveMetadata(dir string, report *MetadataReport) (string, error) {
	return jsonutil.SaveJSON(dir, "meta.json", report)
}

// LoadMetadata reads a MetadataReport JSON file at the given path.
func LoadMetadata(path string) (*MetadataReport, error) {
	return jsonutil.LoadJSON[MetadataReport](path)
}
