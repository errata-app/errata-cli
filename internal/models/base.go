// Package models defines the ModelAdapter interface and shared data types.
package models

import (
	"context"

	"github.com/suarezc/errata/internal/tools"
)

// AgentEvent is a single observable event emitted by an agent during its run.
// Type is one of: "text", "reading", "writing", "error".
type AgentEvent struct {
	Type string
	Data string
}

// ModelResponse is the final result from one agent run.
type ModelResponse struct {
	ModelID        string
	Text           string
	LatencyMS      int64
	InputTokens    int64
	OutputTokens   int64
	CostUSD        float64
	ProposedWrites []tools.FileWrite
	Error          string // empty = success
}

// OK returns true when the response carries no error.
func (r ModelResponse) OK() bool { return r.Error == "" }

// ModelAdapter is the interface every provider adapter must implement.
type ModelAdapter interface {
	// ID returns the model identifier (e.g. "claude-sonnet-4-6").
	ID() string

	// RunAgent runs the multi-turn agentic tool-use loop.
	// It calls onEvent for each tool event and text chunk.
	// read_file calls execute immediately; write_file calls are intercepted
	// and returned in ModelResponse.ProposedWrites.
	RunAgent(
		ctx context.Context,
		prompt string,
		onEvent func(AgentEvent),
	) (ModelResponse, error)
}
