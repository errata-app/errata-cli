package prompt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/suarezc/errata/internal/prompt"
)

func TestResolve_DefaultWhenEmpty(t *testing.T) {
	vs := prompt.VariantSet{}
	content, source := vs.Resolve("gpt-4o", "openai", "")
	assert.Equal(t, "", content)
	assert.Equal(t, "default", source)
}

func TestResolve_DefaultContent(t *testing.T) {
	vs := prompt.VariantSet{Default: "full system prompt"}
	content, source := vs.Resolve("claude-sonnet-4-6", "anthropic", "full")
	assert.Equal(t, "full system prompt", content)
	assert.Equal(t, "default", source)
}

func TestResolve_ExactModelOverride_InlineContent(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default prompt",
		Overrides: map[string]string{"gpt-4o": "custom gpt-4o prompt"},
	}
	content, source := vs.Resolve("gpt-4o", "openai", "")
	assert.Equal(t, "custom gpt-4o prompt", content)
	assert.Equal(t, "override:gpt-4o", source)
}

func TestResolve_ExactModelOverride_VariantReference(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default prompt",
		Variants:  map[string]string{"concise": "short prompt"},
		Overrides: map[string]string{"gemini-2.0-flash": "concise"},
	}
	content, source := vs.Resolve("gemini-2.0-flash", "google", "")
	assert.Equal(t, "short prompt", content)
	assert.Equal(t, "override:gemini-2.0-flash→variant:concise", source)
}

func TestResolve_ProviderOverride(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default prompt",
		Overrides: map[string]string{"anthropic:": "all anthropic models get this"},
	}
	content, source := vs.Resolve("claude-opus-4-6", "anthropic", "")
	assert.Equal(t, "all anthropic models get this", content)
	assert.Equal(t, "override:anthropic:", source)
}

func TestResolve_ProviderOverride_VariantRef(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default prompt",
		Variants:  map[string]string{"concise": "short prompt"},
		Overrides: map[string]string{"google:": "concise"},
	}
	content, source := vs.Resolve("gemini-2.5-pro", "google", "")
	assert.Equal(t, "short prompt", content)
	assert.Equal(t, "override:google:→variant:concise", source)
}

func TestResolve_ExactModelTakesPriorityOverProvider(t *testing.T) {
	vs := prompt.VariantSet{
		Default: "default",
		Overrides: map[string]string{
			"gpt-4o":  "exact match",
			"openai:": "provider match",
		},
	}
	content, _ := vs.Resolve("gpt-4o", "openai", "")
	assert.Equal(t, "exact match", content)

	content, _ = vs.Resolve("gpt-4o-mini", "openai", "")
	assert.Equal(t, "provider match", content)
}

func TestResolve_TierMatchedVariant(t *testing.T) {
	vs := prompt.VariantSet{
		Default:  "default prompt",
		Variants: map[string]string{"minimal": "tiny prompt"},
	}
	content, source := vs.Resolve("local-llama", "", "minimal")
	assert.Equal(t, "tiny prompt", content)
	assert.Equal(t, "variant:minimal", source)
}

func TestResolve_TierNoMatchingVariant(t *testing.T) {
	vs := prompt.VariantSet{
		Default:  "default prompt",
		Variants: map[string]string{"concise": "short prompt"},
	}
	content, source := vs.Resolve("local-llama", "", "minimal")
	assert.Equal(t, "default prompt", content)
	assert.Equal(t, "default", source)
}

func TestResolve_OverrideTakesPriorityOverTier(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default prompt",
		Variants:  map[string]string{"minimal": "tiny", "concise": "short"},
		Overrides: map[string]string{"gemini-2.0-flash": "concise"},
	}
	// Model has tier "minimal" but exact override says "concise"
	content, _ := vs.Resolve("gemini-2.0-flash", "google", "minimal")
	assert.Equal(t, "short", content)
}

func TestResolve_MissingVariantReference_FallsBackToInline(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default prompt",
		Variants:  map[string]string{"concise": "short"},
		Overrides: map[string]string{"gpt-4o": "nonexistent-variant"},
	}
	// "nonexistent-variant" doesn't match any variant; treated as inline content
	content, source := vs.Resolve("gpt-4o", "openai", "")
	assert.Equal(t, "nonexistent-variant", content)
	assert.Equal(t, "override:gpt-4o", source)
}

func TestResolve_MultiLineOverride_NotTreatedAsVariantRef(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default",
		Variants:  map[string]string{"concise": "short"},
		Overrides: map[string]string{"gpt-4o": "This is a multi-line\nprompt override"},
	}
	content, source := vs.Resolve("gpt-4o", "openai", "")
	assert.Equal(t, "This is a multi-line\nprompt override", content)
	assert.Equal(t, "override:gpt-4o", source)
}

func TestResolve_EmptyProviderSkipsProviderCheck(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default",
		Overrides: map[string]string{"openai:": "openai specific"},
	}
	content, _ := vs.Resolve("unknown-model", "", "")
	assert.Equal(t, "default", content)
}

func TestIsEmpty(t *testing.T) {
	assert.True(t, prompt.VariantSet{}.IsEmpty())
	assert.False(t, (prompt.VariantSet{Default: "x"}).IsEmpty())
	assert.False(t, (prompt.VariantSet{Variants: map[string]string{"a": "b"}}).IsEmpty())
	assert.False(t, (prompt.VariantSet{Overrides: map[string]string{"a": "b"}}).IsEmpty())
}

func TestResolve_OverrideWithUppercase_NotTreatedAsVariantRef(t *testing.T) {
	// Override value "LOUD" has uppercase chars → isIdentifier returns false → no warning.
	vs := prompt.VariantSet{
		Default:   "default",
		Variants:  map[string]string{"concise": "short"},
		Overrides: map[string]string{"gpt-4o": "LOUD"},
	}
	content, source := vs.Resolve("gpt-4o", "openai", "")
	assert.Equal(t, "LOUD", content)
	assert.Equal(t, "override:gpt-4o", source)
}

func TestResolve_OverrideWithSpecialChars_NotTreatedAsVariantRef(t *testing.T) {
	vs := prompt.VariantSet{
		Default:   "default",
		Variants:  map[string]string{"concise": "short"},
		Overrides: map[string]string{"gpt-4o": "test!value"},
	}
	content, source := vs.Resolve("gpt-4o", "openai", "")
	assert.Equal(t, "test!value", content)
	assert.Equal(t, "override:gpt-4o", source)
}

// Table-driven test matching the specification table from the prompt.
func TestResolve_SpecificationTable(t *testing.T) {
	vs := prompt.VariantSet{
		Default:  "full default prompt",
		Variants: map[string]string{"concise": "short prompt", "minimal": "tiny prompt"},
		Overrides: map[string]string{
			"gpt-4o":          "custom gpt-4o inline text",
			"gemini-2.0-flash": "concise",
			"anthropic:":      "all anthropic custom",
		},
	}

	tests := []struct {
		name     string
		modelID  string
		provider string
		tier     string
		wantSrc  string
		wantText string
	}{
		{
			name: "no override, no tier → default",
			modelID: "claude-sonnet-4-6", provider: "", tier: "full",
			wantText: "full default prompt", wantSrc: "default",
		},
		{
			name: "exact model override, inline text",
			modelID: "gpt-4o", provider: "openai", tier: "",
			wantText: "custom gpt-4o inline text", wantSrc: "override:gpt-4o",
		},
		{
			name: "exact model override, variant reference",
			modelID: "gemini-2.0-flash", provider: "google", tier: "",
			wantText: "short prompt", wantSrc: "override:gemini-2.0-flash→variant:concise",
		},
		{
			name: "tier matches variant",
			modelID: "local-llama", provider: "", tier: "minimal",
			wantText: "tiny prompt", wantSrc: "variant:minimal",
		},
		{
			name: "tier matches no variant → default",
			modelID: "local-llama", provider: "", tier: "unknown-tier",
			wantText: "full default prompt", wantSrc: "default",
		},
		{
			name: "provider-level override",
			modelID: "claude-opus-4-6", provider: "anthropic", tier: "",
			wantText: "all anthropic custom", wantSrc: "override:anthropic:",
		},
		{
			name: "exact model override beats provider override",
			modelID: "gpt-4o", provider: "openai", tier: "minimal",
			wantText: "custom gpt-4o inline text", wantSrc: "override:gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, source := vs.Resolve(tt.modelID, tt.provider, tt.tier)
			assert.Equal(t, tt.wantText, content)
			assert.Equal(t, tt.wantSrc, source)
		})
	}
}
