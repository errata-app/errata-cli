package adapters

import (
	"context"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ─── stub ────────────────────────────────────────────────────────────────────

type stubBedrockConverser struct {
	responses []*bedrockruntime.ConverseOutput
	idx       int
}

func (s *stubBedrockConverser) Converse(ctx context.Context, _ *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s.idx >= len(s.responses) {
		return &bedrockruntime.ConverseOutput{}, nil
	}
	i := s.idx
	s.idx++
	return s.responses[i], nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func testBedrockConfig(stub *stubBedrockConverser) *bedrockRunConfig {
	return &bedrockRunConfig{
		client:      stub,
		modelID:     "bedrock/anthropic.claude-test",
		bareModelID: "anthropic.claude-test",
		qualifiedID: "anthropic/claude-test",
	}
}

func bedrockToolCtx() context.Context {
	return tools.WithActiveTools(context.Background(), tools.Definitions)
}

func int32Ptr(v int32) *int32 { return &v }

// bedrockTextOutput creates a ConverseOutput with a text content block and end_turn stop reason.
func bedrockTextOutput(text string, inputTokens, outputTokens int32) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		Output: &bedrocktypes.ConverseOutputMemberMessage{
			Value: bedrocktypes.Message{
				Role: bedrocktypes.ConversationRoleAssistant,
				Content: []bedrocktypes.ContentBlock{
					&bedrocktypes.ContentBlockMemberText{Value: text},
				},
			},
		},
		StopReason: bedrocktypes.StopReasonEndTurn,
		Usage: &bedrocktypes.TokenUsage{
			InputTokens:  int32Ptr(inputTokens),
			OutputTokens: int32Ptr(outputTokens),
		},
	}
}

// bedrockToolUseOutput creates a ConverseOutput with a tool_use content block.
func bedrockToolUseOutput(toolUseID, name string, inputMap map[string]any, inputTokens, outputTokens int32) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		Output: &bedrocktypes.ConverseOutputMemberMessage{
			Value: bedrocktypes.Message{
				Role: bedrocktypes.ConversationRoleAssistant,
				Content: []bedrocktypes.ContentBlock{
					&bedrocktypes.ContentBlockMemberToolUse{
						Value: bedrocktypes.ToolUseBlock{
							ToolUseId: aws.String(toolUseID),
							Name:      aws.String(name),
							Input:     bedrockdocument.NewLazyDocument(inputMap),
						},
					},
				},
			},
		},
		StopReason: bedrocktypes.StopReasonToolUse,
		Usage: &bedrocktypes.TokenUsage{
			InputTokens:  int32Ptr(inputTokens),
			OutputTokens: int32Ptr(outputTokens),
		},
	}
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestBedrockLoop_NoTools(t *testing.T) {
	stub := &stubBedrockConverser{
		responses: []*bedrockruntime.ConverseOutput{
			bedrockTextOutput("No tools here.", 100, 20),
		},
	}

	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{})
	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "hello",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Equal(t, "No tools here.", resp.Text)
	assert.Empty(t, resp.ProposedWrites)
	assert.True(t, resp.OK())
}

func TestBedrockLoop_TextOnly(t *testing.T) {
	stub := &stubBedrockConverser{
		responses: []*bedrockruntime.ConverseOutput{
			bedrockTextOutput("Hello from Bedrock!", 100, 20),
		},
	}

	ctx := bedrockToolCtx()
	var events []models.AgentEvent
	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "say hello",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "Hello from Bedrock!", resp.Text)
	assert.Equal(t, int64(100), resp.InputTokens)
	assert.Equal(t, int64(20), resp.OutputTokens)
	assert.Empty(t, resp.ProposedWrites)
	assert.True(t, resp.OK())

	var textEvents int
	for _, e := range events {
		if e.Type == models.EventText {
			textEvents++
		}
	}
	assert.Equal(t, 1, textEvents)
}

func TestBedrockLoop_SingleToolCall(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("bedrock.txt", []byte("bedrock data"), 0o600))

	stub := &stubBedrockConverser{
		responses: []*bedrockruntime.ConverseOutput{
			bedrockToolUseOutput("tu_1", "read_file", map[string]any{"path": "bedrock.txt"}, 100, 25),
			bedrockTextOutput("File read.", 200, 15),
		},
	}

	ctx := bedrockToolCtx()
	var events []models.AgentEvent
	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "read bedrock.txt",
		func(e models.AgentEvent) { events = append(events, e) })

	require.NoError(t, err)
	assert.Equal(t, "File read.", resp.Text)
	assert.Empty(t, resp.ProposedWrites)

	var readEvents int
	for _, e := range events {
		if e.Type == models.EventReading {
			readEvents++
		}
	}
	assert.GreaterOrEqual(t, readEvents, 1)
}

func TestBedrockLoop_WriteFileIntercepted(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stub := &stubBedrockConverser{
		responses: []*bedrockruntime.ConverseOutput{
			bedrockToolUseOutput("tu_w", "write_file", map[string]any{"path": "out.go", "content": "package out"}, 100, 20),
			bedrockTextOutput("Written.", 200, 10),
		},
	}

	ctx := bedrockToolCtx()
	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "write a file",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	require.Len(t, resp.ProposedWrites, 1)
	assert.Equal(t, "out.go", resp.ProposedWrites[0].Path)
	assert.Equal(t, "package out", resp.ProposedWrites[0].Content)

	_, err = os.Stat("out.go")
	assert.True(t, os.IsNotExist(err), "write_file must not write to disk")
}

func TestBedrockLoop_TokenAccumulation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("t.txt", []byte("t"), 0o600))

	stub := &stubBedrockConverser{
		responses: []*bedrockruntime.ConverseOutput{
			bedrockToolUseOutput("tu_t", "read_file", map[string]any{"path": "t.txt"}, 80, 20),
			bedrockTextOutput("done", 200, 15),
		},
	}

	ctx := bedrockToolCtx()
	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Equal(t, int64(280), resp.InputTokens)  // 80 + 200
	assert.Equal(t, int64(35), resp.OutputTokens)   // 20 + 15
}

func TestBedrockLoop_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(bedrockToolCtx())
	cancel()

	stub := &stubBedrockConverser{responses: nil}

	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "test",
		func(models.AgentEvent) {})

	require.Error(t, err)
	assert.True(t, resp.Interrupted)
}

func TestBedrockLoop_StopReasonEndTurn(t *testing.T) {
	// Even if content has tool use blocks, end_turn stop reason should end the loop.
	stub := &stubBedrockConverser{
		responses: []*bedrockruntime.ConverseOutput{
			bedrockTextOutput("Final answer.", 100, 20),
		},
	}

	ctx := bedrockToolCtx()
	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	assert.Equal(t, "Final answer.", resp.Text)
	assert.True(t, resp.OK())
}

func TestBedrockLoop_MaxSteps(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("f.txt", []byte("data"), 0o600))

	stub := &stubBedrockConverser{responses: []*bedrockruntime.ConverseOutput{
		// Turn 1: tool call
		bedrockToolUseOutput("tu_1", "read_file", map[string]any{"path": "f.txt"}, 100, 30),
		// Turn 2: tool call — should be skipped by maxSteps=1
		bedrockToolUseOutput("tu_2", "read_file", map[string]any{"path": "f.txt"}, 200, 40),
		// Turn 3: text — should never be reached
		bedrockTextOutput("done", 300, 50),
	}}

	ctx := tools.WithMaxSteps(bedrockToolCtx(), 1)
	resp, err := runBedrockAgentLoop(ctx, testBedrockConfig(stub), nil, "test",
		func(models.AgentEvent) {})

	require.NoError(t, err)
	// Only 1 API call made; turn 2+ never executed.
	assert.Equal(t, int64(100), resp.InputTokens)
	assert.Equal(t, int64(30), resp.OutputTokens)
}
