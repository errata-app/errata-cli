package adapters

import (
	"strconv"
	"strings"
	"time"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
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
func DispatchTool(
	name string,
	args map[string]string,
	onEvent func(models.AgentEvent),
	proposed *[]tools.FileWrite,
) (result string, ok bool) {
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
		onEvent(models.AgentEvent{Type: "reading", Data: path})
		return tools.ExecuteRead(path, offset, limit), true

	case tools.WriteToolName:
		path := args["path"]
		onEvent(models.AgentEvent{Type: "writing", Data: path})
		*proposed = append(*proposed, tools.FileWrite{Path: path, Content: args["content"]})
		return writeAck, true

	case tools.EditToolName:
		path := args["path"]
		onEvent(models.AgentEvent{Type: "writing", Data: path})
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
		onEvent(models.AgentEvent{Type: "reading", Data: path})
		return tools.ExecuteListDirectory(path, depth), true

	case tools.SearchFilesName:
		pattern := args["pattern"]
		basePath := args["base_path"]
		onEvent(models.AgentEvent{Type: "reading", Data: pattern})
		return tools.ExecuteSearchFiles(pattern, basePath), true

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
		onEvent(models.AgentEvent{Type: "reading", Data: pattern})
		return tools.ExecuteSearchCode(pattern, path, fileGlob, contextLines), true

	case tools.BashToolName:
		command := args["command"]
		desc := args["description"]
		if desc == "" {
			desc = command
		}
		onEvent(models.AgentEvent{Type: "bash", Data: desc})
		return tools.ExecuteBash(command), true

	case tools.WebFetchToolName:
		rawURL := args["url"]
		onEvent(models.AgentEvent{Type: "reading", Data: rawURL})
		return tools.ExecuteWebFetch(rawURL), true
	}
	return "", false
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

// BuildSuccessResponse constructs a ModelResponse after a completed agentic loop.
// qualifiedID is the provider-prefixed model ID passed to CostUSD.
func BuildSuccessResponse(modelID, qualifiedID string, textParts []string, start time.Time, totalInput, totalOutput int64, proposed []tools.FileWrite) models.ModelResponse {
	return models.ModelResponse{
		ModelID:        modelID,
		Text:           join(textParts),
		LatencyMS:      time.Since(start).Milliseconds(),
		InputTokens:    totalInput,
		OutputTokens:   totalOutput,
		CostUSD:        pricing.CostUSD(qualifiedID, totalInput, totalOutput),
		ProposedWrites: proposed,
	}
}
