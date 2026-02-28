// Package models defines the ModelAdapter interface and shared data types.
package models

import (
	"context"

	"github.com/suarezc/errata/internal/tools"
)

// ConversationTurn is one prior exchange in the conversation history.
// Role is "user" or "assistant".
type ConversationTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// EventType is the kind of observable event emitted by an agent.
type EventType string

const (
	EventText     EventType = "text"
	EventReading  EventType = "reading"
	EventWriting  EventType = "writing"
	EventError    EventType = "error"
	EventBash     EventType = "bash"
	EventSnapshot EventType = "snapshot"
)

// AgentEvent is a single observable event emitted by an agent during its run.
type AgentEvent struct {
	Type EventType
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

// CapabilitySource indicates where a capability value was determined.
type CapabilitySource int

const (
	SourceDefault CapabilitySource = iota // hardcoded fallback
	SourceConfig                          // user override from recipe ModelProfiles
	SourceAPI                             // discovered from provider API
)

// ToolFormat describes how a model receives tool definitions.
type ToolFormat int

const (
	ToolFormatNone         ToolFormat = iota // no tool support
	ToolFormatNative                         // Anthropic-style (tool blocks in content)
	ToolFormatFunctionCall                   // OpenAI-style (function_call / tool_call)
	ToolFormatTextInPrompt                   // no API support; described in system prompt
)

// ModelCapabilities describes a model's known capabilities, used by the prompt
// assembler to produce model-specific payloads.
type ModelCapabilities struct {
	ModelID             string
	Provider            string
	ContextWindow       int
	MaxOutputTokens     int
	ToolFormat          ToolFormat
	ParallelToolCalls   bool
	SystemRole          bool     // model accepts a system role message
	MidConvoSystem      bool     // model accepts system messages mid-conversation
	SupportedInputMedia []string // e.g. ["text", "image", "pdf"]
	ContextWindowSource CapabilitySource
	ToolFormatSource    CapabilitySource
}

// ModelAdapter is the interface every provider adapter must implement.
type ModelAdapter interface {
	// ID returns the model identifier (e.g. "claude-sonnet-4-6").
	ID() string

	// Capabilities returns the model's known capabilities.
	// Called once at session startup; results are cached and merged with user overrides.
	Capabilities(ctx context.Context) ModelCapabilities

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
