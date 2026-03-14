package capabilities_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/errata-app/errata-cli/internal/capabilities"
	"github.com/errata-app/errata-cli/internal/models"
)

func TestDefaultCapabilities_KnownModel(t *testing.T) {
	caps := capabilities.DefaultCapabilities("anthropic", "claude-sonnet-4-6")
	assert.Equal(t, "claude-sonnet-4-6", caps.ModelID)
	assert.Equal(t, "anthropic", caps.Provider)
	assert.Equal(t, 200_000, caps.ContextWindow)
	assert.Equal(t, 16_000, caps.MaxOutputTokens)
	assert.Equal(t, models.ToolFormatNative, caps.ToolFormat)
	assert.True(t, caps.SystemRole)
	assert.False(t, caps.MidConvoSystem)
	assert.Equal(t, models.SourceDefault, caps.ContextWindowSource)
	assert.Equal(t, models.SourceDefault, caps.ToolFormatSource)
	assert.Contains(t, caps.SupportedInputMedia, "text")
	assert.Contains(t, caps.SupportedInputMedia, "image")
}

func TestDefaultCapabilities_KnownOpenAIModel(t *testing.T) {
	caps := capabilities.DefaultCapabilities("openai", "gpt-4o")
	assert.Equal(t, "gpt-4o", caps.ModelID)
	assert.Equal(t, "openai", caps.Provider)
	assert.Equal(t, 128_000, caps.ContextWindow)
	assert.Equal(t, models.ToolFormatFunctionCall, caps.ToolFormat)
	assert.True(t, caps.ParallelToolCalls)
	assert.True(t, caps.SystemRole)
	assert.True(t, caps.MidConvoSystem)
}

func TestDefaultCapabilities_KnownGoogleModel(t *testing.T) {
	caps := capabilities.DefaultCapabilities("google", "gemini-1.5-pro")
	assert.Equal(t, 2_000_000, caps.ContextWindow)
	assert.Equal(t, models.ToolFormatFunctionCall, caps.ToolFormat)
	assert.True(t, caps.SystemRole)
}

func TestDefaultCapabilities_UnknownModelFallsToProviderDefaults(t *testing.T) {
	caps := capabilities.DefaultCapabilities("anthropic", "claude-future-model")
	assert.Equal(t, "claude-future-model", caps.ModelID)
	assert.Equal(t, "anthropic", caps.Provider)
	assert.Equal(t, 200_000, caps.ContextWindow) // provider default
	assert.Equal(t, models.ToolFormatNative, caps.ToolFormat)
	assert.True(t, caps.SystemRole)
}

func TestDefaultCapabilities_UnknownProvider(t *testing.T) {
	caps := capabilities.DefaultCapabilities("unknown", "some-model")
	assert.Equal(t, "some-model", caps.ModelID)
	assert.Equal(t, "unknown", caps.Provider)
	assert.Equal(t, 0, caps.ContextWindow)
	assert.Equal(t, models.ToolFormatNone, caps.ToolFormat)
}

func TestMergeWithProfile_ContextBudget(t *testing.T) {
	caps := capabilities.DefaultCapabilities("anthropic", "claude-sonnet-4-6")
	assert.Equal(t, 200_000, caps.ContextWindow)

	merged := capabilities.MergeWithProfile(caps, capabilities.ModelProfile{
		ContextBudget: 50_000,
	})
	assert.Equal(t, 50_000, merged.ContextWindow)
	assert.Equal(t, models.SourceConfig, merged.ContextWindowSource)
}

func TestMergeWithProfile_ToolFormat(t *testing.T) {
	caps := capabilities.DefaultCapabilities("anthropic", "claude-sonnet-4-6")
	assert.Equal(t, models.ToolFormatNative, caps.ToolFormat)

	merged := capabilities.MergeWithProfile(caps, capabilities.ModelProfile{
		ToolFormat: "text_in_prompt",
	})
	assert.Equal(t, models.ToolFormatTextInPrompt, merged.ToolFormat)
	assert.Equal(t, models.SourceConfig, merged.ToolFormatSource)
}

func TestMergeWithProfile_SystemRole(t *testing.T) {
	caps := capabilities.DefaultCapabilities("anthropic", "claude-sonnet-4-6")
	assert.True(t, caps.SystemRole)

	f := false
	merged := capabilities.MergeWithProfile(caps, capabilities.ModelProfile{
		SystemRole: &f,
	})
	assert.False(t, merged.SystemRole)
}

func TestMergeWithProfile_MidConvoSystem(t *testing.T) {
	caps := capabilities.DefaultCapabilities("openai", "gpt-4o")
	assert.True(t, caps.MidConvoSystem)

	f := false
	merged := capabilities.MergeWithProfile(caps, capabilities.ModelProfile{
		MidConvoSystem: &f,
	})
	assert.False(t, merged.MidConvoSystem)
}

func TestMergeWithProfile_ZeroValuesUnchanged(t *testing.T) {
	caps := capabilities.DefaultCapabilities("openai", "gpt-4o")
	original := caps

	merged := capabilities.MergeWithProfile(caps, capabilities.ModelProfile{})
	assert.Equal(t, original.ContextWindow, merged.ContextWindow)
	assert.Equal(t, original.ToolFormat, merged.ToolFormat)
	assert.Equal(t, original.SystemRole, merged.SystemRole)
	assert.Equal(t, original.MidConvoSystem, merged.MidConvoSystem)
	// Source should not change when no override applied.
	assert.Equal(t, models.SourceDefault, merged.ContextWindowSource)
}

func TestParseToolFormat(t *testing.T) {
	tests := []struct {
		input string
		want  models.ToolFormat
	}{
		{"native", models.ToolFormatNative},
		{"Native", models.ToolFormatNative},
		{"function_calling", models.ToolFormatFunctionCall},
		{"FUNCTION_CALLING", models.ToolFormatFunctionCall},
		{"text_in_prompt", models.ToolFormatTextInPrompt},
		{"", models.ToolFormatNone},
		{"bogus", models.ToolFormatNone},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, capabilities.ParseToolFormat(tt.input))
		})
	}
}

func TestDefaultCapabilities_BedrockProviderDefaults(t *testing.T) {
	caps := capabilities.DefaultCapabilities("bedrock", "anthropic.claude-sonnet-4-20250514-v1:0")
	assert.Equal(t, "bedrock", caps.Provider)
	assert.Equal(t, 200_000, caps.ContextWindow)
	assert.Equal(t, models.ToolFormatNative, caps.ToolFormat)
	assert.True(t, caps.SystemRole)
}

func TestDefaultCapabilities_AzureProviderDefaults(t *testing.T) {
	caps := capabilities.DefaultCapabilities("azure", "gpt-4o")
	assert.Equal(t, "azure", caps.Provider)
	assert.Equal(t, 128_000, caps.ContextWindow)
	assert.Equal(t, models.ToolFormatFunctionCall, caps.ToolFormat)
	assert.True(t, caps.SystemRole)
	assert.True(t, caps.MidConvoSystem)
}

func TestDefaultCapabilities_VertexProviderDefaults(t *testing.T) {
	caps := capabilities.DefaultCapabilities("vertex", "gemini-2.0-flash")
	assert.Equal(t, "vertex", caps.Provider)
	assert.Equal(t, 1_000_000, caps.ContextWindow)
	assert.Equal(t, models.ToolFormatFunctionCall, caps.ToolFormat)
	assert.True(t, caps.SystemRole)
}

// Verify all models in the modelDefaults table are reachable.
func TestDefaultCapabilities_AllKnownModels(t *testing.T) {
	knownModels := []struct {
		provider string
		modelID  string
	}{
		{"anthropic", "claude-opus-4-6"},
		{"anthropic", "claude-sonnet-4-6"},
		{"anthropic", "claude-haiku-4-5"},
		{"openai", "gpt-4o"},
		{"openai", "gpt-4o-mini"},
		{"openai", "o1"},
		{"openai", "o3-mini"},
		{"google", "gemini-2.0-flash"},
		{"google", "gemini-1.5-pro"},
		{"google", "gemini-2.5-pro"},
	}
	for _, m := range knownModels {
		t.Run(m.provider+"/"+m.modelID, func(t *testing.T) {
			caps := capabilities.DefaultCapabilities(m.provider, m.modelID)
			assert.Equal(t, m.modelID, caps.ModelID)
			assert.Equal(t, m.provider, caps.Provider)
			assert.Positive(t, caps.ContextWindow, "context window should be > 0")
			assert.Positive(t, caps.MaxOutputTokens, "max output tokens should be > 0")
			assert.NotEqual(t, models.ToolFormatNone, caps.ToolFormat, "tool format should be set")
			assert.True(t, caps.SystemRole, "all known models support system role")
		})
	}
}
