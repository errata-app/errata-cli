package models

import (
	"context"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/suarezc/errata/internal/tools"
	"google.golang.org/api/option"
)

// GeminiAdapter implements ModelAdapter for Gemini models.
type GeminiAdapter struct {
	modelID string
	apiKey  string
}

// NewGeminiAdapter creates a GeminiAdapter.
func NewGeminiAdapter(modelID, apiKey string) *GeminiAdapter {
	return &GeminiAdapter{modelID: modelID, apiKey: apiKey}
}

func (a *GeminiAdapter) ID() string { return a.modelID }

func (a *GeminiAdapter) RunAgent(
	ctx context.Context,
	prompt string,
	onEvent func(AgentEvent),
	verbose bool,
) (ModelResponse, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(a.apiKey))
	if err != nil {
		return ModelResponse{ModelID: a.modelID, Error: err.Error()}, err
	}
	defer client.Close()

	model := client.GenerativeModel(a.modelID)
	model.Tools = buildGeminiTools()

	chat := model.StartChat()

	var textParts []string
	var proposed []tools.FileWrite
	var totalInput, totalOutput int64
	start := time.Now()

	userMsg := genai.Text(prompt)
	var currentMsg []genai.Part
	currentMsg = append(currentMsg, userMsg)

	for {
		resp, err := chat.SendMessage(ctx, currentMsg...)
		if err != nil {
			return ModelResponse{
				ModelID:      a.modelID,
				LatencyMS:    time.Since(start).Milliseconds(),
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
				CostUSD:      CostUSD(a.modelID, totalInput, totalOutput),
				Error:        err.Error(),
			}, err
		}

		if resp.UsageMetadata != nil {
			totalInput += int64(resp.UsageMetadata.PromptTokenCount)
			totalOutput += int64(resp.UsageMetadata.CandidatesTokenCount)
		}

		candidate := resp.Candidates[0]
		var funcResponses []genai.Part
		currentMsg = nil

		for _, part := range candidate.Content.Parts {
			switch v := part.(type) {
			case genai.Text:
				textParts = append(textParts, string(v))
				if verbose {
					onEvent(AgentEvent{Type: "text", Data: string(v)})
				}

			case genai.FunctionCall:
				args := extractStringArgs(v.Args)
				switch v.Name {
				case tools.ReadToolName:
					path := args["path"]
					onEvent(AgentEvent{Type: "reading", Data: path})
					content := tools.ExecuteRead(path)
					funcResponses = append(funcResponses, genai.FunctionResponse{
						Name:     v.Name,
						Response: map[string]any{"result": content},
					})

				case tools.WriteToolName:
					path := args["path"]
					onEvent(AgentEvent{Type: "writing", Data: path})
					proposed = append(proposed, tools.FileWrite{Path: path, Content: args["content"]})
					funcResponses = append(funcResponses, genai.FunctionResponse{
						Name:     v.Name,
						Response: map[string]any{"result": "Write queued — will be applied if selected."},
					})
				}
			}
		}

		if len(funcResponses) == 0 {
			break
		}
		currentMsg = funcResponses
	}

	return ModelResponse{
		ModelID:        a.modelID,
		Text:           join(textParts),
		LatencyMS:      time.Since(start).Milliseconds(),
		InputTokens:    totalInput,
		OutputTokens:   totalOutput,
		CostUSD:        CostUSD(a.modelID, totalInput, totalOutput),
		ProposedWrites: proposed,
	}, nil
}

func buildGeminiTools() []*genai.Tool {
	var decls []*genai.FunctionDeclaration
	for _, def := range tools.Definitions {
		props := map[string]*genai.Schema{}
		for name, p := range def.Properties {
			props[name] = &genai.Schema{
				Type:        genai.TypeString,
				Description: p.Description,
			}
		}
		required := make([]string, len(def.Required))
		copy(required, def.Required)
		decls = append(decls, &genai.FunctionDeclaration{
			Name:        def.Name,
			Description: def.Description,
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: props,
				Required:   required,
			},
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

func extractStringArgs(args map[string]any) map[string]string {
	out := make(map[string]string, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
