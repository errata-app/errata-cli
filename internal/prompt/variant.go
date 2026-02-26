// Package prompt provides the generic variant/override resolution system
// and prompt assembly pipeline for Errata's model-adaptive prompt system.
//
// The variant system resolves per-model prompt content from a three-level
// hierarchy: per-model overrides → named variants (matched by tier) → default.
// This pattern is used for system prompts, tool descriptions, sub-agent modes,
// and summarization prompts.
package prompt

import (
	"log"
	"strings"
)

// VariantSet holds a default value, named variants, and per-model overrides
// for a single prompt section. Resolution selects the appropriate content
// for a given model without any LLM-based rewriting.
type VariantSet struct {
	Default   string            // base content used when no variant/override matches
	Variants  map[string]string // variant_name → content
	Overrides map[string]string // model_id or "provider:" → content or variant name ref
}

// Resolve returns the appropriate content for a model. Resolution order:
//  1. Exact model ID match in Overrides
//  2. Provider-level match in Overrides (key = "provider:", e.g. "anthropic:")
//  3. If override value matches a Variants key → use variant content
//  4. If override value doesn't match → use as inline content
//  5. Variants[tier] if tier is non-empty
//  6. Default
//
// Returns (content, source) where source describes where the content came from
// for logging/debugging (e.g. "override:gpt-4o", "variant:concise", "default").
func (vs VariantSet) Resolve(modelID, provider, tier string) (string, string) {
	// Step 1: exact model override
	if v, ok := vs.Overrides[modelID]; ok {
		return vs.resolveOverrideValue(v, "override:"+modelID)
	}

	// Step 2: provider-level override (e.g. "anthropic:")
	if provider != "" {
		providerKey := provider + ":"
		if v, ok := vs.Overrides[providerKey]; ok {
			return vs.resolveOverrideValue(v, "override:"+providerKey)
		}
	}

	// Step 3: tier-matched variant
	if tier != "" {
		if v, ok := vs.Variants[tier]; ok {
			return v, "variant:" + tier
		}
	}

	// Step 4: default
	return vs.Default, "default"
}

// resolveOverrideValue checks if the override value is a variant name reference.
// If it matches a key in Variants, return the variant content.
// Otherwise return the value as inline content.
func (vs VariantSet) resolveOverrideValue(value, source string) (string, string) {
	trimmed := strings.TrimSpace(value)

	// Single-word value that matches a variant name → use the variant
	if vs.Variants != nil {
		if variantContent, ok := vs.Variants[trimmed]; ok {
			return variantContent, source + "→variant:" + trimmed
		}
	}

	// Check if it looks like a variant reference (single word, no spaces/newlines)
	// but the variant doesn't exist → warn and fall back to default
	if !strings.ContainsAny(trimmed, " \t\n") && len(trimmed) > 0 && len(trimmed) < 64 {
		// Could be a variant ref that doesn't exist, or a very short inline value.
		// Only warn if it looks like an identifier (no punctuation except - and _).
		if isIdentifier(trimmed) && trimmed != vs.Default {
			// If it looks like an identifier but doesn't match a variant,
			// it might be a typo. Log a warning and use it as inline content.
			log.Printf("prompt: override value %q looks like a variant reference but no variant named %q exists; using as inline content", trimmed, trimmed)
		}
	}

	return trimmed, source
}

// isIdentifier returns true if s looks like a variant name (lowercase alphanumeric + _ + -).
func isIdentifier(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return len(s) > 0
}

// IsEmpty returns true if the VariantSet has no content at all.
func (vs VariantSet) IsEmpty() bool {
	return vs.Default == "" && len(vs.Variants) == 0 && len(vs.Overrides) == 0
}
