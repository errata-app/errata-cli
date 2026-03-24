// Package capabilities provides hardcoded model capability defaults.
//
// This follows the same pattern as the pricingTable in internal/pricing:
// a hardcoded last-resort fallback, keyed by provider/model.
// Last updated: 2026-03-23.
package capabilities

import (
	"log"

	"github.com/errata-app/errata-cli/internal/models"
)

// providerDefaults holds provider-level capability defaults.
// Used when no model-specific entry exists.
var providerDefaults = map[string]models.ModelCapabilities{
	"anthropic": {
		Provider:        "anthropic",
		ContextWindow:   200_000,
		MaxOutputTokens: 8192,
	},
	"openai": {
		Provider:        "openai",
		ContextWindow:   128_000,
		MaxOutputTokens: 16_384,
	},
	"google": {
		Provider:        "google",
		ContextWindow:   1_000_000,
		MaxOutputTokens: 8192,
	},
	"bedrock": {
		Provider:        "bedrock",
		ContextWindow:   200_000,
		MaxOutputTokens: 4096,
	},
	"azure": {
		Provider:        "azure",
		ContextWindow:   128_000,
		MaxOutputTokens: 16_384,
	},
	"vertex": {
		Provider:        "vertex",
		ContextWindow:   1_000_000,
		MaxOutputTokens: 8192,
	},
}

// modelDefaults holds model-specific capability overrides.
// Keyed by "provider/model" (same convention as pricing).
var modelDefaults = map[string]models.ModelCapabilities{
	// Anthropic
	"anthropic/claude-opus-4-6": {
		Provider:        "anthropic",
		ContextWindow:   200_000,
		MaxOutputTokens: 32_000,
	},
	"anthropic/claude-sonnet-4-6": {
		Provider:        "anthropic",
		ContextWindow:   200_000,
		MaxOutputTokens: 16_000,
	},
	"anthropic/claude-haiku-4-5": {
		Provider:        "anthropic",
		ContextWindow:   200_000,
		MaxOutputTokens: 8192,
	},
	// OpenAI
	"openai/gpt-4o": {
		Provider:        "openai",
		ContextWindow:   128_000,
		MaxOutputTokens: 16_384,
	},
	"openai/gpt-4o-mini": {
		Provider:        "openai",
		ContextWindow:   128_000,
		MaxOutputTokens: 16_384,
	},
	"openai/o1": {
		Provider:        "openai",
		ContextWindow:   200_000,
		MaxOutputTokens: 100_000,
	},
	"openai/o3": {
		Provider:        "openai",
		ContextWindow:   200_000,
		MaxOutputTokens: 100_000,
	},
	"openai/o3-mini": {
		Provider:        "openai",
		ContextWindow:   200_000,
		MaxOutputTokens: 100_000,
	},
	// Google
	"google/gemini-2.5-flash": {
		Provider:        "google",
		ContextWindow:   1_000_000,
		MaxOutputTokens: 65_536,
	},
	"google/gemini-2.5-pro": {
		Provider:        "google",
		ContextWindow:   1_000_000,
		MaxOutputTokens: 65_536,
	},
	"google/gemini-2.0-flash": {
		Provider:        "google",
		ContextWindow:   1_000_000,
		MaxOutputTokens: 8192,
	},
	"google/gemini-1.5-pro": {
		Provider:        "google",
		ContextWindow:   2_000_000,
		MaxOutputTokens: 8192,
	},
}

// DefaultCapabilities returns the best-known capabilities for a model based on
// hardcoded defaults. The provider parameter should be the lowercase provider name
// (e.g. "anthropic", "openai", "google"). Returns a zero-value ModelCapabilities
// with only ModelID and Provider set for unknown providers.
func DefaultCapabilities(provider, modelID string) models.ModelCapabilities {
	qualifiedID := provider + "/" + modelID

	// Try exact model match first.
	if caps, ok := modelDefaults[qualifiedID]; ok {
		caps.ModelID = modelID
		return caps
	}

	// Fall back to provider-level defaults.
	if caps, ok := providerDefaults[provider]; ok {
		caps.ModelID = modelID
		log.Printf("capabilities: using provider-level defaults for %s (no model-specific entry)", qualifiedID)
		return caps
	}

	// Unknown provider — return minimal capabilities.
	log.Printf("capabilities: no defaults for provider %q, model %q", provider, modelID)
	return models.ModelCapabilities{
		ModelID:  modelID,
		Provider: provider,
	}
}
