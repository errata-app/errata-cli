// Package output generates structured JSON reports after each Errata run.
//
// A report captures the complete execution snapshot: recipe configuration,
// prompt, per-model results (text, tokens, cost, latency, tool events,
// proposed writes), aggregate statistics, and optional selection outcome.
//
// Reports are written to data/outputs/ as pretty-printed JSON files named
// {recipeName}_output_{hex}.json. They are intended as a fundamental
// primitive for users to interrogate past runs programmatically.
package output

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/errata-app/errata-cli/internal/jsonutil"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/pkg/recipe"
	"github.com/errata-app/errata-cli/internal/uid"
)

// ─── Report types ────────────────────────────────────────────────────────────

// Report is the complete execution snapshot written to data/outputs/.
type Report struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`

	Recipe RecipeSnapshot `json:"recipe"`
	Prompt string         `json:"prompt"`

	Models    []ModelResult  `json:"models"`
	Aggregate AggregateStats `json:"aggregate"`

	Selection *SelectionOutcome `json:"selection,omitempty"`
}

// RecipeSnapshot captures the active configuration at the time of the run.
type RecipeSnapshot struct {
	Name         string               `json:"name"`
	Models       []string             `json:"models,omitempty"`
	SystemPrompt string               `json:"system_prompt,omitempty"`
	Tools        []string             `json:"tools,omitempty"`
	Constraints *ConstraintsSnapshot `json:"constraints,omitempty"`
}

// ConstraintsSnapshot captures constraint settings.
type ConstraintsSnapshot struct {
	MaxSteps int    `json:"max_steps,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
}

// ModelResult is the per-model execution result.
type ModelResult struct {
	ModelID             string       `json:"model_id"`
	Text                string       `json:"text"`
	LatencyMS           int64        `json:"latency_ms"`
	InputTokens         int64        `json:"input_tokens"`
	OutputTokens        int64        `json:"output_tokens"`
	ReasoningTokens     int64        `json:"reasoning_tokens,omitempty"`
	CostUSD             float64      `json:"cost_usd"`
	Error               string       `json:"error,omitempty"`
	StopReason          string       `json:"stop_reason,omitempty"`
	Steps               int            `json:"steps,omitempty"`
	ToolCalls           map[string]int `json:"tool_calls,omitempty"`
	ProposedWrites      []WriteEntry   `json:"proposed_writes,omitempty"`
	Events              []EventEntry   `json:"events"`
}

// WriteEntry captures one proposed file write.
type WriteEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Delete  bool   `json:"delete,omitempty"`
}

// EventEntry captures a single tool event during execution.
type EventEntry struct {
	Type models.EventType `json:"type"`
	Data string `json:"data"`
}

// AggregateStats summarizes the run across all models.
type AggregateStats struct {
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
	ModelCount        int     `json:"model_count"`
	SuccessCount      int     `json:"success_count"`
	FastestModel      string  `json:"fastest_model,omitempty"`
	FastestMS         int64   `json:"fastest_ms,omitempty"`
}

// SelectionOutcome records the user's choice after the run.
type SelectionOutcome struct {
	SelectedModel string   `json:"selected_model"`
	AppliedFiles  []string `json:"applied_files,omitempty"`
	Rating        string   `json:"rating,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

// ─── Session Report ──────────────────────────────────────────────────────────

// SessionReport aggregates all runs within a session into a single export.
type SessionReport struct {
	ID        string       `json:"id"`
	Timestamp time.Time    `json:"timestamp"`
	SessionID string       `json:"session_id"`
	Turns     []TurnReport `json:"turns"`
	Aggregate SessionStats `json:"aggregate"`
}

// TurnReport is one prompt+results within the session.
type TurnReport struct {
	TurnIndex int               `json:"turn_index"`
	Timestamp time.Time         `json:"timestamp"`
	Prompt    string            `json:"prompt"`
	Recipe    RecipeSnapshot    `json:"recipe"`
	Models    []ModelResult     `json:"models"`
	Aggregate AggregateStats    `json:"aggregate"`
	Selection *SelectionOutcome `json:"selection,omitempty"`
}

// SessionStats summarizes the session across all turns.
type SessionStats struct {
	TurnCount         int     `json:"turn_count"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
}

// BuildSessionReport constructs a SessionReport from a slice of per-run Reports.
func BuildSessionReport(sessionID string, reports []*Report) *SessionReport {
	sr := &SessionReport{
		ID:        uid.New("srpt_"),
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		Turns:     make([]TurnReport, len(reports)),
	}

	var totalCost float64
	var totalIn, totalOut int64

	for i, r := range reports {
		sr.Turns[i] = TurnReport{
			TurnIndex: i,
			Timestamp: r.Timestamp,
			Prompt:    r.Prompt,
			Recipe:    r.Recipe,
			Models:    r.Models,
			Aggregate: r.Aggregate,
			Selection: r.Selection,
		}
		totalCost += r.Aggregate.TotalCostUSD
		totalIn += r.Aggregate.TotalInputTokens
		totalOut += r.Aggregate.TotalOutputTokens
	}

	sr.Aggregate = SessionStats{
		TurnCount:         len(reports),
		TotalCostUSD:      totalCost,
		TotalInputTokens:  totalIn,
		TotalOutputTokens: totalOut,
	}

	return sr
}

// Filename returns the output file name: session_output_{id}.json
func (r *SessionReport) Filename() string {
	return "session_output_" + r.ID + ".json"
}

// SaveSession writes the session report as pretty-printed JSON to dir/{filename}.
// Parent directories are created as needed. Returns the full path.
func SaveSession(dir string, report *SessionReport) (string, error) {
	return jsonutil.SaveJSON(dir, report.Filename(), report)
}

// LoadSession reads a session report JSON file at the given path.
func LoadSession(path string) (*SessionReport, error) {
	return jsonutil.LoadJSON[SessionReport](path)
}

// ─── Collector ───────────────────────────────────────────────────────────────

// Collector accumulates per-model AgentEvents during a run.
// It is safe for concurrent use from multiple goroutines.
type Collector struct {
	mu     sync.Mutex
	events map[string][]EventEntry
}

// NewCollector returns a ready-to-use Collector.
func NewCollector() *Collector {
	return &Collector{events: make(map[string][]EventEntry)}
}

// WrapOnEvent returns an onEvent callback that records the event
// and then forwards it to the original onEvent function.
func (c *Collector) WrapOnEvent(
	original func(modelID string, event models.AgentEvent),
) func(modelID string, event models.AgentEvent) {
	return func(modelID string, event models.AgentEvent) {
		c.mu.Lock()
		c.events[modelID] = append(c.events[modelID], EventEntry{
			Type: event.Type,
			Data: event.Data,
		})
		c.mu.Unlock()
		original(modelID, event)
	}
}

// Events returns a copy of the collected events for the given model.
func (c *Collector) Events(modelID string) []EventEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	src := c.events[modelID]
	if src == nil {
		return nil
	}
	out := make([]EventEntry, len(src))
	copy(out, src)
	return out
}

// ─── Build ───────────────────────────────────────────────────────────────────

// BuildReport constructs a Report from the run results and collected events.
func BuildReport(
	sessionID string,
	rec *recipe.Recipe,
	prompt string,
	responses []models.ModelResponse,
	collector *Collector,
	activeToolNames []string,
) *Report {
	id := uid.New("rpt_")

	recipeName := "default"
	if rec != nil && rec.Name != "" {
		recipeName = rec.Name
	}

	snap := RecipeSnapshot{
		Name:  recipeName,
		Tools: activeToolNames,
	}
	if rec != nil {
		snap.Models = rec.Models
		snap.SystemPrompt = rec.SystemPrompt
		if rec.Constraints.MaxSteps > 0 || rec.Constraints.Timeout > 0 {
			snap.Constraints = &ConstraintsSnapshot{
				MaxSteps: rec.Constraints.MaxSteps,
			}
			if rec.Constraints.Timeout > 0 {
				snap.Constraints.Timeout = rec.Constraints.Timeout.String()
			}
		}
	}

	var (
		totalCost float64
		totalIn   int64
		totalOut  int64
		successes int
		fastestID string
		fastestMS int64
	)

	modelResults := make([]ModelResult, len(responses))
	for i, resp := range responses {
		var writes []WriteEntry
		for _, fw := range resp.ProposedWrites {
			writes = append(writes, WriteEntry{Path: fw.Path, Content: fw.Content, Delete: fw.Delete})
		}

		var events []EventEntry
		if collector != nil {
			events = collector.Events(resp.ModelID)
		}
		if events == nil {
			events = []EventEntry{}
		}

		modelResults[i] = ModelResult{
			ModelID:             resp.ModelID,
			Text:                resp.Text,
			LatencyMS:           resp.LatencyMS,
			InputTokens:         resp.InputTokens,
			OutputTokens:        resp.OutputTokens,
			ReasoningTokens:     resp.ReasoningTokens,
			CostUSD:             resp.CostUSD,
			Error:               resp.Error,
			StopReason:          string(resp.StopReason),
			Steps:               resp.Steps,
			ToolCalls:           resp.ToolCalls,
			ProposedWrites:      writes,
			Events:              events,
		}

		totalCost += resp.CostUSD
		totalIn += resp.InputTokens
		totalOut += resp.OutputTokens
		if resp.OK() {
			successes++
			if fastestID == "" || resp.LatencyMS < fastestMS {
				fastestID = resp.ModelID
				fastestMS = resp.LatencyMS
			}
		}
	}

	return &Report{
		ID:        id,
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		Recipe:    snap,
		Prompt:    prompt,
		Models:    modelResults,
		Aggregate: AggregateStats{
			TotalCostUSD:      totalCost,
			TotalInputTokens:  totalIn,
			TotalOutputTokens: totalOut,
			ModelCount:        len(responses),
			SuccessCount:      successes,
			FastestModel:      fastestID,
			FastestMS:         fastestMS,
		},
	}
}

// ─── Filename ────────────────────────────────────────────────────────────────

// unsafeChars matches characters that are unsafe in filenames.
var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// SanitizeName cleans a recipe name for use in a filename.
// Empty input returns "default".
func SanitizeName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return "default"
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = unsafeChars.ReplaceAllString(s, "")
	if s == "" {
		return "default"
	}
	return s
}

// Filename returns the output file name: {sanitizedRecipeName}_output_{id}.json
func (r *Report) Filename() string {
	return SanitizeName(r.Recipe.Name) + "_output_" + r.ID + ".json"
}


