package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PreferenceUpload is the bulk upload request body for POST /preferences.
type PreferenceUpload struct {
	Sessions []SessionUpload `json:"sessions"`
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
