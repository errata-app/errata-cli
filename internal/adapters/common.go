package adapters

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/tooloutput"
	"github.com/suarezc/errata/internal/tools"
)

// writeAck is returned to the model when a write_file call is intercepted.
// Writes are queued and applied only if the user selects this model's response.
const writeAck = "Write queued — will be applied if selected."

// extractStringMap converts map[string]any to map[string]string, dropping non-string values.
func extractStringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// join concatenates text parts into a single string.
func join(parts []string) string {
	return strings.Join(parts, "")
}

// DispatchTool executes a tool call by name.
// It emits the appropriate AgentEvent, executes the tool, and returns the result
// string for the adapter to feed back to the model.
// Returns ("", false) for unrecognised tool names.
//
// ctx is checked for MCP dispatchers (registered at startup via tools.WithMCPDispatchers)
// which take priority over built-in tool names.
//
// toolCalls, if non-nil, is incremented for the tool name on successful dispatch.
func DispatchTool(
	ctx context.Context,
	name string,
	args map[string]string,
	onEvent func(models.AgentEvent),
	proposed *[]tools.FileWrite,
	toolCalls *map[string]int,
) (result string, ok bool) {
	defer func() {
		if ok && toolCalls != nil && *toolCalls != nil {
			(*toolCalls)[name]++
		}
	}()
	// MCP-dispatched tools take priority over built-in tool names.
	if dispatchers := tools.MCPDispatchersFromContext(ctx); len(dispatchers) > 0 {
		if dispatcher, found := dispatchers[name]; found {
			onEvent(models.AgentEvent{Type: models.EventReading, Data: "[mcp] " + name})
			result := dispatcher(args)
			if strings.HasPrefix(result, "[mcp error:") {
				onEvent(models.AgentEvent{Type: models.EventError, Data: result})
			}
			return result, true
		}
	}

	switch name {
	case tools.ReadToolName:
		path := args["path"]
		offset, limit := 0, 0
		if v := args["offset"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				offset = n
			}
		}
		if v := args["limit"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		onEvent(models.AgentEvent{Type: models.EventReading, Data: path})
		return applyOutputProcessing(ctx, name, tools.ExecuteRead(path, offset, limit)), true

	case tools.WriteToolName:
		path := args["path"]
		onEvent(models.AgentEvent{Type: models.EventWriting, Data: path})
		*proposed = append(*proposed, tools.FileWrite{Path: path, Content: args["content"]})
		return writeAck, true

	case tools.EditToolName:
		path := args["path"]
		onEvent(models.AgentEvent{Type: models.EventWriting, Data: path})
		newContent, errMsg := tools.ExecuteEditFile(path, args["old_string"], args["new_string"])
		if errMsg != "" {
			return errMsg, true
		}
		*proposed = append(*proposed, tools.FileWrite{Path: path, Content: newContent})
		return writeAck, true

	case tools.ListDirToolName:
		path := args["path"]
		depth := 2
		if d := args["depth"]; d != "" {
			if n, err := strconv.Atoi(d); err == nil {
				depth = n
			}
		}
		onEvent(models.AgentEvent{Type: models.EventReading, Data: path})
		return applyOutputProcessing(ctx, name, tools.ExecuteListDirectory(path, depth)), true

	case tools.SearchFilesName:
		pattern := args["pattern"]
		basePath := args["base_path"]
		onEvent(models.AgentEvent{Type: models.EventReading, Data: pattern})
		return applyOutputProcessing(ctx, name, tools.ExecuteSearchFiles(pattern, basePath)), true

	case tools.SearchCodeName:
		pattern := args["pattern"]
		path := args["path"]
		fileGlob := args["file_glob"]
		contextLines := 0
		if v := args["context_lines"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				contextLines = n
			}
		}
		onEvent(models.AgentEvent{Type: models.EventReading, Data: pattern})
		return applyOutputProcessing(ctx, name, tools.ExecuteSearchCode(pattern, path, fileGlob, contextLines)), true

	case tools.BashToolName:
		command := args["command"]
		desc := args["description"]
		if desc == "" {
			desc = command
		}
		onEvent(models.AgentEvent{Type: models.EventBash, Data: desc})
		return applyOutputProcessing(ctx, name, tools.ExecuteBash(ctx, command)), true

	case tools.WebFetchToolName:
		rawURL := args["url"]
		onEvent(models.AgentEvent{Type: models.EventReading, Data: rawURL})
		return applyOutputProcessing(ctx, name, tools.ExecuteWebFetch(rawURL)), true

	case tools.WebSearchToolName:
		query := args["query"]
		onEvent(models.AgentEvent{Type: models.EventReading, Data: "web_search: " + query})
		return applyOutputProcessing(ctx, name, tools.ExecuteWebSearch(query)), true

	case tools.SpawnAgentToolName:
		dispatcher := tools.SubagentDispatcherFromContext(ctx)
		if dispatcher == nil {
			return "[spawn_agent error: sub-agent spawning is not configured]", true
		}
		task := args["task"]
		onEvent(models.AgentEvent{Type: models.EventReading, Data: "spawn_agent: " + task})
		text, writes, errMsg := dispatcher(ctx, args)
		if errMsg != "" {
			return errMsg, true
		}
		*proposed = append(*proposed, writes...)
		return applyOutputProcessing(ctx, name, text), true
	}
	return "", false
}

// EmitSnapshot sends a "snapshot" event with the current turn state for incremental
// checkpoint persistence. Called at each turn boundary inside the agentic loop.
func EmitSnapshot(onEvent func(models.AgentEvent), qualifiedID string,
	textParts []string, start time.Time, totalInput, totalOutput int64,
	proposed []tools.FileWrite, toolCalls map[string]int) {
	snap := models.PartialSnapshot{
		Text:         join(textParts),
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		CostUSD:      pricing.CostUSD(qualifiedID, totalInput, totalOutput),
		LatencyMS:    time.Since(start).Milliseconds(),
		Writes:       proposed,
		ToolCalls:    toolCalls,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	onEvent(models.AgentEvent{Type: models.EventSnapshot, Data: string(data)})
}

// ─── Debug request logging ────────────────────────────────────────────────────

type debugRequestsKey struct{}

// WithDebugRequests returns a context that enables raw API request logging.
func WithDebugRequests(ctx context.Context) context.Context {
	return context.WithValue(ctx, debugRequestsKey{}, true)
}

// DebugRequestsFromContext reports whether raw API request logging is enabled.
func DebugRequestsFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(debugRequestsKey{}).(bool)
	return v
}

// EmitRequest JSON-marshals params and emits it as an EventRequest event.
// Only emits when debug request logging is enabled in the context, avoiding
// serialization overhead in production.
func EmitRequest(ctx context.Context, onEvent func(models.AgentEvent), params any) {
	if !DebugRequestsFromContext(ctx) {
		return
	}
	b, err := json.Marshal(params)
	if err != nil {
		return
	}
	onEvent(models.AgentEvent{Type: models.EventRequest, Data: string(b)})
}

// applyOutputProcessing truncates tool output according to the rule for the
// named tool, if one exists in the context. Returns the output unchanged
// when no rule applies.
func applyOutputProcessing(ctx context.Context, toolName, output string) string {
	rule := tooloutput.RuleForTool(ctx, toolName)
	if rule.MaxLines <= 0 && rule.MaxTokens <= 0 {
		return output
	}
	return tooloutput.Process(output, rule)
}

// BuildErrorResponse constructs a ModelResponse for an API error encountered mid-loop.
// qualifiedID is the provider-prefixed model ID passed to CostUSD
// (e.g. "anthropic/claude-sonnet-4-6"); pass the bare modelID for OpenRouter/LiteLLM.
func BuildErrorResponse(modelID, qualifiedID string, start time.Time, totalInput, totalOutput int64, err error) models.ModelResponse {
	return models.ModelResponse{
		ModelID:      modelID,
		LatencyMS:    time.Since(start).Milliseconds(),
		InputTokens:  totalInput,
		OutputTokens: totalOutput,
		CostUSD:      pricing.CostUSD(qualifiedID, totalInput, totalOutput),
		Error:        err.Error(),
	}
}

// BuildInterruptedResponse constructs a ModelResponse for a run that was cancelled mid-flight.
// It preserves the partial text, proposed writes, and token counts accumulated before cancellation.
func BuildInterruptedResponse(modelID, qualifiedID string, textParts []string,
	start time.Time, totalInput, totalOutput int64,
	proposed []tools.FileWrite, toolCalls map[string]int, err error) models.ModelResponse {
	return models.ModelResponse{
		ModelID:        modelID,
		Text:           join(textParts),
		LatencyMS:      time.Since(start).Milliseconds(),
		InputTokens:    totalInput,
		OutputTokens:   totalOutput,
		CostUSD:        pricing.CostUSD(qualifiedID, totalInput, totalOutput),
		ProposedWrites: proposed,
		ToolCalls:      toolCalls,
		Error:          err.Error(),
		Interrupted:    true,
	}
}

// BuildSuccessResponse constructs a ModelResponse after a completed agentic loop.
// qualifiedID is the provider-prefixed model ID passed to CostUSD.
func BuildSuccessResponse(modelID, qualifiedID string, textParts []string, start time.Time,
	totalInput, totalOutput int64,
	proposed []tools.FileWrite, toolCalls map[string]int) models.ModelResponse {
	return models.ModelResponse{
		ModelID:        modelID,
		Text:           join(textParts),
		LatencyMS:      time.Since(start).Milliseconds(),
		InputTokens:    totalInput,
		OutputTokens:   totalOutput,
		CostUSD:        pricing.CostUSD(qualifiedID, totalInput, totalOutput),
		ProposedWrites: proposed,
		ToolCalls:      toolCalls,
	}
}
