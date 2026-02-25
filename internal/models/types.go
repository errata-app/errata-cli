// Package models defines the ModelAdapter interface and shared data types.
package models

import (
	"context"

	"github.com/suarezc/errata/internal/tools"
)

// ConversationTurn is one prior exchange in the conversation history.
// Role is "user" or "assistant".
type ConversationTurn struct {
	Role    string
	Content string
}

// AgentEvent is a single observable event emitted by an agent during its run.
// Type is one of: "text", "reading", "writing", "error", "bash", "snapshot".
type AgentEvent struct {
	Type string
	Data string
}

// PartialSnapshot is emitted by adapters at turn boundaries (as JSON in
// AgentEvent.Data with Type "snapshot") for incremental checkpoint persistence.
// Placed in models (not adapters) so both adapters and runner can use it
// without import cycles.
type PartialSnapshot struct {
	Text         string            `json:"text"`
	InputTokens  int64             `json:"input_tokens"`
	OutputTokens int64             `json:"output_tokens"`
	CostUSD      float64           `json:"cost_usd"`
	LatencyMS    int64             `json:"latency_ms"`
	Writes       []tools.FileWrite `json:"writes,omitempty"`
}

// ModelResponse is the final result from one agent run.
type ModelResponse struct {
	ModelID             string
	Text                string
	LatencyMS           int64
	InputTokens         int64 // total input tokens displayed (regular + cache read + cache creation)
	OutputTokens        int64
	CacheReadTokens     int64 // tokens served from cache at a discounted rate (subset of InputTokens)
	CacheCreationTokens int64 // tokens written to cache at a premium rate (Anthropic only; subset of InputTokens)
	CostUSD             float64
	ProposedWrites      []tools.FileWrite
	Error               string // empty = success
	Interrupted         bool   // true when run was cancelled mid-flight (partial data preserved)
}

// OK returns true when the response carries no error.
func (r ModelResponse) OK() bool { return r.Error == "" }

// ModelAdapter is the interface every provider adapter must implement.
type ModelAdapter interface {
	// ID returns the model identifier (e.g. "claude-sonnet-4-6").
	ID() string

	// RunAgent runs the multi-turn agentic tool-use loop.
	// history contains prior (user, assistant) turns for this model; nil = fresh conversation.
	// It calls onEvent for each tool event and text chunk.
	// read_file calls execute immediately; write_file calls are intercepted
	// and returned in ModelResponse.ProposedWrites.
	RunAgent(
		ctx     context.Context,
		history []ConversationTurn,
		prompt  string,
		onEvent func(AgentEvent),
	) (ModelResponse, error)
}
