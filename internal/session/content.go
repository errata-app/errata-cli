package session

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/output"
)

// SessionContent holds the full prompts, responses, events, and conversation
// histories for a session (session_content.json).
type SessionContent struct {
	SessionID string                                `json:"session_id,omitempty"`
	Runs      []RunContent                          `json:"runs"`
	Histories map[string][]models.ConversationTurn  `json:"histories,omitempty"`
}

// RunContent holds the full prompt and per-model response data for one run.
type RunContent struct {
	Prompt     string            `json:"prompt"`
	PromptHash string            `json:"prompt_hash"`
	Models     []ModelRunContent `json:"models"`
}

// ModelRunContent holds the full response data for one model in a run.
type ModelRunContent struct {
	ModelID         string              `json:"model_id"`
	Text            string              `json:"text"`
	ProposedWrites  []output.WriteEntry `json:"proposed_writes,omitempty"`
	Events          []output.EventEntry `json:"events"`
	StopReason      string              `json:"stop_reason,omitempty"`
	Steps           int                 `json:"steps,omitempty"`
	ReasoningTokens int64               `json:"reasoning_tokens,omitempty"`
}

// SaveContent atomically writes session content to path.
func SaveContent(path string, c SessionContent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadContent reads session content from path.
// Returns (nil, nil) if the file does not exist.
func LoadContent(path string) (*SessionContent, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil //nolint:nilnil // intentional: missing file is not an error
	}
	if err != nil {
		return nil, err
	}
	var c SessionContent
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
