package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
)

// PreferenceUpload is the bulk upload request body for POST /preferences.
type PreferenceUpload struct {
	Recipe   string                 `json:"recipe,omitempty"`
	Sessions []SessionUpload        `json:"sessions"`
	Content  []SessionContentUpload `json:"content,omitempty"`
}

// SessionContentUpload holds full session content for upload (privacy=full).
type SessionContentUpload struct {
	SessionID string                                `json:"session_id"`
	Runs      []RunContentUpload                    `json:"runs"`
	Histories map[string][]models.ConversationTurn  `json:"histories,omitempty"`
}

// RunContentUpload holds the full prompt and per-model response data for one run.
type RunContentUpload struct {
	Prompt string                  `json:"prompt"`
	Models []ModelRunContentUpload `json:"models"`
}

// ModelRunContentUpload holds the full response data for one model in a run.
type ModelRunContentUpload struct {
	ModelID         string              `json:"model_id"`
	Text            string              `json:"text"`
	ProposedWrites  []output.WriteEntry `json:"proposed_writes,omitempty"`
	Events          []output.EventEntry `json:"events"`
	StopReason      string              `json:"stop_reason,omitempty"`
	Steps           int                 `json:"steps,omitempty"`
	ReasoningTokens int64               `json:"reasoning_tokens,omitempty"`
}

// SessionUpload is a redacted session metadata record for upload.
// Sensitive fields (FirstPrompt, LastPrompt) are excluded.
type SessionUpload struct {
	ID           string      `json:"id"`
	CreatedAt    time.Time   `json:"created_at"`
	LastActiveAt time.Time   `json:"last_active_at"`
	Models       []string    `json:"models,omitempty"`
	PromptCount  int         `json:"prompt_count"`
	ConfigHash   string      `json:"config_hash,omitempty"`
	RecipeName   string      `json:"recipe_name,omitempty"`
	Runs         []RunUpload `json:"runs"`
}

// RunUpload is a redacted run summary for upload.
// Sensitive fields (PromptPreview, AppliedFiles, Note) are excluded.
type RunUpload struct {
	Timestamp           time.Time                 `json:"timestamp"`
	PromptHash          string                    `json:"prompt_hash"`
	Models              []string                  `json:"models"`
	Selected            string                    `json:"selected,omitempty"`
	Rating              string                    `json:"rating,omitempty"`
	Type                string                    `json:"type,omitempty"`
	LatenciesMS         map[string]int64          `json:"latencies_ms,omitempty"`
	CostsUSD            map[string]float64        `json:"costs_usd,omitempty"`
	InputTokens         map[string]int64          `json:"input_tokens,omitempty"`
	OutputTokens        map[string]int64          `json:"output_tokens,omitempty"`
	ToolCalls           map[string]map[string]int `json:"tool_calls,omitempty"`
	ProposedWritesCount map[string]int            `json:"proposed_writes_count,omitempty"`
	ConfigHash          string                    `json:"config_hash,omitempty"`
}

// ReportUploadResult is the response body from POST /reports.
type ReportUploadResult struct {
	ID       string `json:"id"`
	RecipeID string `json:"recipe_id"`
}

// UploadReport uploads a headless run report (MetadataReport or RunReport).
// The caller marshals the appropriate struct and passes it as raw JSON.
func (c *Client) UploadReport(payload json.RawMessage) (*ReportUploadResult, error) {
	resp, err := c.do("POST", "/reports", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, parseError(resp)
	}
	var result ReportUploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("api: decode report response: %w", err)
	}
	return &result, nil
}

// UploadPreferences uploads redacted preference data.
// Returns the number of runs the server accepted.
func (c *Client) UploadPreferences(payload PreferenceUpload) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("api: marshal preferences: %w", err)
	}
	resp, err := c.do("POST", "/preferences", bytes.NewReader(body), "application/json")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, parseError(resp)
	}
	var result struct {
		Accepted int `json:"accepted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("api: decode upload response: %w", err)
	}
	return result.Accepted, nil
}
