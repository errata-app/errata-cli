package adapters

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBedrockQualifiedID_AnthropicModel(t *testing.T) {
	tests := []struct {
		bareModelID string
		want        string
	}{
		{
			bareModelID: "anthropic.claude-sonnet-4-20250514-v1:0",
			want:        "anthropic/claude-sonnet-4-20250514",
		},
		{
			bareModelID: "anthropic.claude-opus-4-6-20250714-v2:0",
			want:        "anthropic/claude-opus-4-6-20250714",
		},
		{
			bareModelID: "meta.llama3-70b-instruct-v1:0",
			want:        "meta/llama3-70b-instruct",
		},
		{
			bareModelID: "amazon.nova-pro-v1:0",
			want:        "amazon/nova-pro",
		},
	}
	for _, tc := range tests {
		t.Run(tc.bareModelID, func(t *testing.T) {
			got := bedrockQualifiedID(tc.bareModelID)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBedrockQualifiedID_NoDotSeparator(t *testing.T) {
	// If no dot separator, return as-is.
	got := bedrockQualifiedID("some-custom-model")
	assert.Equal(t, "some-custom-model", got)
}

func TestBedrockQualifiedID_CrossRegionPrefix(t *testing.T) {
	// Cross-region IDs like "us.anthropic.claude-..." — first dot splits provider.
	got := bedrockQualifiedID("us.anthropic.claude-sonnet-4-20250115-v1:0")
	// "us" becomes the provider, "anthropic.claude-sonnet-4-20250115" becomes the model
	assert.Equal(t, "us/anthropic.claude-sonnet-4-20250115", got)
}

func TestNewBedrockAdapter_PrefixStripping(t *testing.T) {
	a := NewBedrockAdapter("bedrock/anthropic.claude-sonnet-4-20250514-v1:0", "us-east-1")
	assert.Equal(t, "bedrock/anthropic.claude-sonnet-4-20250514-v1:0", a.ID())
	assert.Equal(t, "anthropic.claude-sonnet-4-20250514-v1:0", a.bareModelID)
	assert.Equal(t, "us-east-1", a.region)
}

func TestNewBedrockAdapter_Capabilities(t *testing.T) {
	a := NewBedrockAdapter("bedrock/anthropic.claude-sonnet-4-20250514-v1:0", "us-east-1")
	// Capabilities should infer sub-provider from the dotted ID.
	caps := a.Capabilities(context.Background())
	assert.Equal(t, "bedrock", caps.Provider)
	assert.Equal(t, "bedrock/anthropic.claude-sonnet-4-20250514-v1:0", caps.ModelID)
	// Should inherit anthropic defaults (context window ≥ 200k).
	assert.GreaterOrEqual(t, caps.ContextWindow, 200_000)
}
