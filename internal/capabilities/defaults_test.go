package capabilities_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/errata-app/errata-cli/internal/capabilities"
)

func TestDefaultCapabilities_Anthropic(t *testing.T) {
	caps := capabilities.DefaultCapabilities("anthropic", "claude-sonnet-4-6")
	assert.Equal(t, "claude-sonnet-4-6", caps.ModelID)
	assert.Equal(t, "anthropic", caps.Provider)
	assert.Equal(t, 200_000, caps.ContextWindow)
	assert.Equal(t, 16_000, caps.MaxOutputTokens)
}

func TestDefaultCapabilities_OpenAI(t *testing.T) {
	caps := capabilities.DefaultCapabilities("openai", "gpt-4o")
	assert.Equal(t, 128_000, caps.ContextWindow)
	assert.Equal(t, 16_384, caps.MaxOutputTokens)
}

func TestDefaultCapabilities_Google(t *testing.T) {
	caps := capabilities.DefaultCapabilities("google", "gemini-2.5-pro")
	assert.Equal(t, 1_000_000, caps.ContextWindow)
	assert.Equal(t, 65_536, caps.MaxOutputTokens)
}

func TestDefaultCapabilities_ProviderFallback(t *testing.T) {
	caps := capabilities.DefaultCapabilities("anthropic", "claude-unknown-99")
	assert.Equal(t, "claude-unknown-99", caps.ModelID)
	assert.Equal(t, 200_000, caps.ContextWindow)
	assert.Equal(t, 8192, caps.MaxOutputTokens)
}

func TestDefaultCapabilities_UnknownProvider(t *testing.T) {
	caps := capabilities.DefaultCapabilities("unknown", "mystery-model")
	assert.Equal(t, "mystery-model", caps.ModelID)
	assert.Equal(t, "unknown", caps.Provider)
	assert.Equal(t, 0, caps.ContextWindow)
	assert.Equal(t, 0, caps.MaxOutputTokens)
}
