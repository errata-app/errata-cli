package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewVertexAIAdapter_PrefixStripping(t *testing.T) {
	a := NewVertexAIAdapter("vertex/gemini-2.0-flash", "my-project", "us-central1")
	assert.Equal(t, "vertex/gemini-2.0-flash", a.ID())
	assert.Equal(t, "gemini-2.0-flash", a.bareModelID)
	assert.Equal(t, "my-project", a.project)
	assert.Equal(t, "us-central1", a.location)
}

func TestNewVertexAIAdapter_Capabilities(t *testing.T) {
	a := NewVertexAIAdapter("vertex/gemini-2.0-flash", "my-project", "us-central1")
	// Capabilities queries the API but falls back to defaults on error.
	// Since we don't have real credentials, it should fall back to google defaults.
	caps := a.Capabilities(nil)
	assert.Equal(t, "vertex", caps.Provider)
	assert.Equal(t, "vertex/gemini-2.0-flash", caps.ModelID)
	// Should inherit google defaults.
	assert.GreaterOrEqual(t, caps.ContextWindow, 1_000_000)
}
