package adapters

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAzureOpenAIAdapter_PrefixStripping(t *testing.T) {
	a := NewAzureOpenAIAdapter("azure/gpt-4o", "test-key", "https://myresource.openai.azure.com", "2024-10-21")
	assert.Equal(t, "azure/gpt-4o", a.ID())
	assert.Equal(t, "gpt-4o", a.deployName)
	assert.Equal(t, "test-key", a.apiKey)
	assert.Equal(t, "https://myresource.openai.azure.com", a.endpoint)
	assert.Equal(t, "2024-10-21", a.apiVersion)
}

func TestNewAzureOpenAIAdapter_Capabilities(t *testing.T) {
	a := NewAzureOpenAIAdapter("azure/gpt-4o", "key", "https://endpoint", "2024-10-21")
	caps := a.Capabilities(context.Background())
	assert.Equal(t, "azure", caps.Provider)
	assert.Equal(t, "azure/gpt-4o", caps.ModelID)
	// Should inherit OpenAI defaults.
	assert.GreaterOrEqual(t, caps.ContextWindow, 128_000)
}

func TestNewAzureOpenAIAdapter_CustomDeploymentName(t *testing.T) {
	a := NewAzureOpenAIAdapter("azure/my-production-gpt4", "key", "https://endpoint", "2024-10-21")
	assert.Equal(t, "azure/my-production-gpt4", a.ID())
	assert.Equal(t, "my-production-gpt4", a.deployName)
}
