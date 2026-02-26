package prompt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/prompt"
)

// mockRecipe implements prompt.RecipeAccessor for testing.
type mockRecipe struct {
	systemVS       prompt.VariantSet
	toolDescs      map[string]prompt.VariantSet
	subAgentModes  map[string]prompt.VariantSet
	sumVS          prompt.VariantSet
	toolNames      []string
	subAgentNames  []string
	tiers          map[string]string
}

func (m *mockRecipe) SystemPromptVS() prompt.VariantSet { return m.systemVS }
func (m *mockRecipe) ToolDescriptionVS(toolName string) prompt.VariantSet {
	if m.toolDescs == nil {
		return prompt.VariantSet{}
	}
	return m.toolDescs[toolName]
}
func (m *mockRecipe) SubAgentModeVS(modeName string) prompt.VariantSet {
	if m.subAgentModes == nil {
		return prompt.VariantSet{}
	}
	return m.subAgentModes[modeName]
}
func (m *mockRecipe) SummarizationVS() prompt.VariantSet { return m.sumVS }
func (m *mockRecipe) AllToolDescriptionNames() []string   { return m.toolNames }
func (m *mockRecipe) AllSubAgentModeNames() []string      { return m.subAgentNames }
func (m *mockRecipe) TierForModel(modelID string) string {
	if m.tiers == nil {
		return ""
	}
	return m.tiers[modelID]
}

func TestAssemble_BasicSystemPrompt(t *testing.T) {
	rec := &mockRecipe{
		systemVS: prompt.VariantSet{Default: "You are a helpful assistant."},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatNative}

	payload := prompt.Assemble(rec, "claude-sonnet-4-6", "anthropic", caps)
	assert.Equal(t, "You are a helpful assistant.", payload.SystemPrompt)
	assert.Equal(t, "default", payload.SystemPromptSource)
}

func TestAssemble_SystemPromptOverride(t *testing.T) {
	rec := &mockRecipe{
		systemVS: prompt.VariantSet{
			Default:   "default prompt",
			Overrides: map[string]string{"gpt-4o": "custom gpt-4o prompt"},
		},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatFunctionCall}

	payload := prompt.Assemble(rec, "gpt-4o", "openai", caps)
	assert.Equal(t, "custom gpt-4o prompt", payload.SystemPrompt)
	assert.Equal(t, "override:gpt-4o", payload.SystemPromptSource)
}

func TestAssemble_SystemPromptVariantByTier(t *testing.T) {
	rec := &mockRecipe{
		systemVS: prompt.VariantSet{
			Default:  "full prompt",
			Variants: map[string]string{"minimal": "tiny prompt"},
		},
		tiers: map[string]string{"local-llama": "minimal"},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatTextInPrompt}

	payload := prompt.Assemble(rec, "local-llama", "", caps)
	assert.Equal(t, "tiny prompt", payload.SystemPrompt)
	assert.Equal(t, "variant:minimal", payload.SystemPromptSource)
}

func TestAssemble_ToolDescriptions_NativeFormat(t *testing.T) {
	rec := &mockRecipe{
		toolNames: []string{"bash", "read_file"},
		toolDescs: map[string]prompt.VariantSet{
			"bash":      {Default: "Run shell commands."},
			"read_file": {Default: "Read a file."},
		},
		systemVS: prompt.VariantSet{Default: "system"},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatNative}

	payload := prompt.Assemble(rec, "claude-sonnet-4-6", "anthropic", caps)
	assert.Equal(t, "Run shell commands.", payload.ToolDescriptions["bash"])
	assert.Equal(t, "Read a file.", payload.ToolDescriptions["read_file"])
	// System prompt should NOT contain tool descriptions for native format.
	assert.NotContains(t, payload.SystemPrompt, "=== Tool:")
}

func TestAssemble_ToolDescriptions_TextInPrompt(t *testing.T) {
	rec := &mockRecipe{
		toolNames: []string{"bash"},
		toolDescs: map[string]prompt.VariantSet{
			"bash": {Default: "Run shell commands."},
		},
		systemVS: prompt.VariantSet{Default: "system prompt"},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatTextInPrompt}

	payload := prompt.Assemble(rec, "local-model", "", caps)
	// Tool descriptions should be embedded in the system prompt.
	assert.Contains(t, payload.SystemPrompt, "=== Tool: bash ===")
	assert.Contains(t, payload.SystemPrompt, "Run shell commands.")
	// ToolDescriptions map should be nil for text_in_prompt models.
	assert.Nil(t, payload.ToolDescriptions)
}

func TestAssemble_SubAgentPrompts(t *testing.T) {
	rec := &mockRecipe{
		subAgentNames: []string{"explore", "plan"},
		subAgentModes: map[string]prompt.VariantSet{
			"explore": {Default: "read-only explorer"},
			"plan":    {Default: "careful planner"},
		},
		systemVS: prompt.VariantSet{Default: "system"},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatNative}

	payload := prompt.Assemble(rec, "claude-sonnet-4-6", "anthropic", caps)
	assert.Equal(t, "read-only explorer", payload.SubAgentPrompts["explore"])
	assert.Equal(t, "careful planner", payload.SubAgentPrompts["plan"])
}

func TestAssemble_SummarizationPrompt(t *testing.T) {
	rec := &mockRecipe{
		sumVS:    prompt.VariantSet{Default: "custom summarize prompt"},
		systemVS: prompt.VariantSet{Default: "system"},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatNative}

	payload := prompt.Assemble(rec, "claude-sonnet-4-6", "anthropic", caps)
	assert.Equal(t, "custom summarize prompt", payload.SummarizationPrompt)
}

func TestAssemble_EmptyRecipe(t *testing.T) {
	rec := &mockRecipe{systemVS: prompt.VariantSet{}}
	caps := models.ModelCapabilities{}

	payload := prompt.Assemble(rec, "model", "provider", caps)
	assert.Equal(t, "", payload.SystemPrompt)
	assert.Equal(t, "default", payload.SystemPromptSource)
	assert.Nil(t, payload.ToolDescriptions)
	assert.Nil(t, payload.SubAgentPrompts)
	assert.Equal(t, "", payload.SummarizationPrompt)
}

func TestAssemble_MultiModel_DifferentPayloads(t *testing.T) {
	rec := &mockRecipe{
		systemVS: prompt.VariantSet{
			Default:   "default prompt",
			Variants:  map[string]string{"concise": "short prompt"},
			Overrides: map[string]string{"gpt-4o": "gpt specific"},
		},
		tiers: map[string]string{"local-llama": "concise"},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatNative}

	// gpt-4o: exact override
	p1 := prompt.Assemble(rec, "gpt-4o", "openai", caps)
	assert.Equal(t, "gpt specific", p1.SystemPrompt)

	// claude: default
	p2 := prompt.Assemble(rec, "claude-sonnet-4-6", "anthropic", caps)
	assert.Equal(t, "default prompt", p2.SystemPrompt)

	// local-llama: tier-matched variant
	p3 := prompt.Assemble(rec, "local-llama", "", caps)
	assert.Equal(t, "short prompt", p3.SystemPrompt)
}

func TestBuildSystemMessage_WithPayload(t *testing.T) {
	payload := prompt.PromptPayload{SystemPrompt: "Project context here."}
	msg := prompt.BuildSystemMessage(payload, "Tool guidance here.")
	assert.Equal(t, "Project context here.\nTool guidance here.", msg)
}

func TestBuildSystemMessage_EmptyPayload(t *testing.T) {
	payload := prompt.PromptPayload{}
	msg := prompt.BuildSystemMessage(payload, "Tool guidance here.")
	assert.Equal(t, "Tool guidance here.", msg)
}

func TestPayloadFromContext_Present(t *testing.T) {
	payloads := map[string]prompt.PromptPayload{
		"claude-sonnet-4-6": {SystemPrompt: "hello"},
	}
	ctx := prompt.WithPayloads(context.Background(), payloads)

	p, ok := prompt.PayloadFromContext(ctx, "claude-sonnet-4-6")
	assert.True(t, ok)
	assert.Equal(t, "hello", p.SystemPrompt)
}

func TestPayloadFromContext_Missing(t *testing.T) {
	payloads := map[string]prompt.PromptPayload{
		"claude-sonnet-4-6": {SystemPrompt: "hello"},
	}
	ctx := prompt.WithPayloads(context.Background(), payloads)

	_, ok := prompt.PayloadFromContext(ctx, "gpt-4o")
	assert.False(t, ok)
}

func TestPayloadFromContext_NoPayloads(t *testing.T) {
	_, ok := prompt.PayloadFromContext(context.Background(), "claude-sonnet-4-6")
	assert.False(t, ok)
}

func TestResolveSummarizationPrompt_CustomPayload(t *testing.T) {
	payloads := map[string]prompt.PromptPayload{
		"m1": {SummarizationPrompt: "custom summary prompt"},
	}
	ctx := prompt.WithPayloads(context.Background(), payloads)

	got := prompt.ResolveSummarizationPrompt(ctx, "m1")
	assert.Equal(t, "custom summary prompt", got)
}

func TestResolveSummarizationPrompt_FallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	got := prompt.ResolveSummarizationPrompt(ctx, "m1")
	assert.Equal(t, prompt.DefaultSummarizationPrompt, got)
}

func TestResolveSummarizationPrompt_EmptyPayload_FallsBackToDefault(t *testing.T) {
	payloads := map[string]prompt.PromptPayload{
		"m1": {SummarizationPrompt: ""},
	}
	ctx := prompt.WithPayloads(context.Background(), payloads)

	got := prompt.ResolveSummarizationPrompt(ctx, "m1")
	assert.Equal(t, prompt.DefaultSummarizationPrompt, got)
}

func TestPayloadSummary(t *testing.T) {
	p := prompt.PromptPayload{
		SystemPrompt:        "hello world",
		SystemPromptSource:  "default",
		ToolDescriptions:    map[string]string{"bash": "run cmds"},
		SubAgentPrompts:     map[string]string{"explore": "read only"},
		SummarizationPrompt: "summarize this",
	}
	s := prompt.PayloadSummary(p)
	assert.Contains(t, s, "system_prompt: 11 chars")
	assert.Contains(t, s, "tool_descriptions: 1 tools")
	assert.Contains(t, s, "sub_agent_prompts: 1 modes")
	assert.Contains(t, s, "summarization_prompt: 14 chars")
}

func TestAssemble_ContextWindowWarning(t *testing.T) {
	// System prompt large enough to exceed 30% of context window triggers a log warning.
	// 4000 chars / 4 ≈ 1000 tokens; threshold = 1000 * 30/100 = 300. 1000 > 300 → warning.
	bigPrompt := strings.Repeat("x", 4000)
	rec := &mockRecipe{
		systemVS: prompt.VariantSet{Default: bigPrompt},
	}
	caps := models.ModelCapabilities{
		ToolFormat:    models.ToolFormatNative,
		ContextWindow: 1000,
	}

	payload := prompt.Assemble(rec, "test-model", "test", caps)
	assert.Equal(t, bigPrompt, payload.SystemPrompt)
}

func TestAssemble_ContextWindowBelowThreshold(t *testing.T) {
	// 40 chars / 4 = 10 tokens; threshold = 100000 * 30/100 = 30000. 10 < 30000 → no warning.
	rec := &mockRecipe{
		systemVS: prompt.VariantSet{Default: strings.Repeat("x", 40)},
	}
	caps := models.ModelCapabilities{
		ToolFormat:    models.ToolFormatNative,
		ContextWindow: 100000,
	}

	payload := prompt.Assemble(rec, "model", "provider", caps)
	assert.Len(t, payload.SystemPrompt, 40)
}

// Verify that the assembler never modifies user content.
func TestAssemble_DoesNotModifyContent(t *testing.T) {
	originalPrompt := "This is the user's system prompt with special chars: <>&\"'"
	rec := &mockRecipe{
		systemVS: prompt.VariantSet{Default: originalPrompt},
	}
	caps := models.ModelCapabilities{ToolFormat: models.ToolFormatNative}

	payload := prompt.Assemble(rec, "model", "provider", caps)
	assert.Equal(t, originalPrompt, payload.SystemPrompt, "assembler must not modify user content")
}
