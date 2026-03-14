// Package capabilities provides hardcoded model capability defaults and
// merging logic for user-provided overrides.
//
// This follows the same pattern as the pricingTable in internal/pricing:
// a hardcoded last-resort fallback, keyed by provider/model.
// Last updated: 2026-02-25.
package capabilities

import (
	"log"
	"strings"

	"github.com/errata-app/errata-cli/internal/models"
)

// providerDefaults holds provider-level capability defaults.
// Used when no model-specific entry exists.
var providerDefaults = map[string]models.ModelCapabilities{
	"anthropic": {
		Provider:            "anthropic",
		ContextWindow:       200_000,
		MaxOutputTokens:     8096,
		ToolFormat:          models.ToolFormatNative,
		SystemRole:          true,
		MidConvoSystem:      false,
		SupportedInputMedia: []string{"text", "image", "pdf"},
	},
	"openai": {
		Provider:            "openai",
		ContextWindow:       128_000,
		MaxOutputTokens:     16_384,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		MidConvoSystem:      true,
		SupportedInputMedia: []string{"text", "image"},
	},
	"google": {
		Provider:            "google",
		ContextWindow:       1_000_000,
		MaxOutputTokens:     8192,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		MidConvoSystem:      false,
		SupportedInputMedia: []string{"text", "image", "pdf", "video", "audio"},
	},
	"bedrock": {
		Provider:            "bedrock",
		ContextWindow:       200_000,
		MaxOutputTokens:     4096,
		ToolFormat:          models.ToolFormatNative,
		SystemRole:          true,
		MidConvoSystem:      false,
		SupportedInputMedia: []string{"text", "image"},
	},
	"azure": {
		Provider:            "azure",
		ContextWindow:       128_000,
		MaxOutputTokens:     16_384,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		MidConvoSystem:      true,
		SupportedInputMedia: []string{"text", "image"},
	},
	"vertex": {
		Provider:            "vertex",
		ContextWindow:       1_000_000,
		MaxOutputTokens:     8192,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		MidConvoSystem:      false,
		SupportedInputMedia: []string{"text", "image", "pdf", "video", "audio"},
	},
}

// modelDefaults holds model-specific capability overrides.
// Keyed by "provider/model" (same convention as pricing).
var modelDefaults = map[string]models.ModelCapabilities{
	// Anthropic
	"anthropic/claude-opus-4-6": {
		Provider:            "anthropic",
		ContextWindow:       200_000,
		MaxOutputTokens:     32_000,
		ToolFormat:          models.ToolFormatNative,
		SystemRole:          true,
		SupportedInputMedia: []string{"text", "image", "pdf"},
	},
	"anthropic/claude-sonnet-4-6": {
		Provider:            "anthropic",
		ContextWindow:       200_000,
		MaxOutputTokens:     16_000,
		ToolFormat:          models.ToolFormatNative,
		SystemRole:          true,
		SupportedInputMedia: []string{"text", "image", "pdf"},
	},
	"anthropic/claude-haiku-4-5": {
		Provider:            "anthropic",
		ContextWindow:       200_000,
		MaxOutputTokens:     8096,
		ToolFormat:          models.ToolFormatNative,
		SystemRole:          true,
		SupportedInputMedia: []string{"text", "image", "pdf"},
	},
	// OpenAI
	"openai/gpt-4o": {
		Provider:            "openai",
		ContextWindow:       128_000,
		MaxOutputTokens:     16_384,
		ToolFormat:          models.ToolFormatFunctionCall,
		ParallelToolCalls:   true,
		SystemRole:          true,
		MidConvoSystem:      true,
		SupportedInputMedia: []string{"text", "image"},
	},
	"openai/gpt-4o-mini": {
		Provider:            "openai",
		ContextWindow:       128_000,
		MaxOutputTokens:     16_384,
		ToolFormat:          models.ToolFormatFunctionCall,
		ParallelToolCalls:   true,
		SystemRole:          true,
		MidConvoSystem:      true,
		SupportedInputMedia: []string{"text", "image"},
	},
	"openai/o1": {
		Provider:            "openai",
		ContextWindow:       200_000,
		MaxOutputTokens:     100_000,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		MidConvoSystem:      true,
		SupportedInputMedia: []string{"text", "image"},
	},
	"openai/o3-mini": {
		Provider:            "openai",
		ContextWindow:       200_000,
		MaxOutputTokens:     100_000,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		MidConvoSystem:      true,
		SupportedInputMedia: []string{"text"},
	},
	// Google
	"google/gemini-2.5-flash": {
		Provider:            "google",
		ContextWindow:       1_000_000,
		MaxOutputTokens:     65_536,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		SupportedInputMedia: []string{"text", "image", "pdf", "video", "audio"},
	},
	"google/gemini-2.0-flash": {
		Provider:            "google",
		ContextWindow:       1_000_000,
		MaxOutputTokens:     8192,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		SupportedInputMedia: []string{"text", "image", "pdf", "video", "audio"},
	},
	"google/gemini-1.5-pro": {
		Provider:            "google",
		ContextWindow:       2_000_000,
		MaxOutputTokens:     8192,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		SupportedInputMedia: []string{"text", "image", "pdf", "video", "audio"},
	},
	"google/gemini-2.5-pro": {
		Provider:            "google",
		ContextWindow:       1_000_000,
		MaxOutputTokens:     65_536,
		ToolFormat:          models.ToolFormatFunctionCall,
		SystemRole:          true,
		SupportedInputMedia: []string{"text", "image", "pdf", "video", "audio"},
	},
}

// DefaultCapabilities returns the best-known capabilities for a model based on
// hardcoded defaults. The provider parameter should be the lowercase provider name
// (e.g. "anthropic", "openai", "google"). All source fields are set to SourceDefault.
// Returns a zero-value ModelCapabilities with only ModelID and Provider set for
// unknown providers.
func DefaultCapabilities(provider, modelID string) models.ModelCapabilities {
	qualifiedID := provider + "/" + modelID

	// Try exact model match first.
	if caps, ok := modelDefaults[qualifiedID]; ok {
		caps.ModelID = modelID
		caps.ContextWindowSource = models.SourceDefault
		caps.ToolFormatSource = models.SourceDefault
		return caps
	}

	// Fall back to provider-level defaults.
	if caps, ok := providerDefaults[provider]; ok {
		caps.ModelID = modelID
		caps.ContextWindowSource = models.SourceDefault
		caps.ToolFormatSource = models.SourceDefault
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

// ModelProfile holds user-provided overrides for model capabilities,
// typically derived from a recipe's "Model Profiles" section.
type ModelProfile struct {
	ContextBudget  int
	ToolFormat     string
	SystemRole     *bool
	MidConvoSystem *bool
}

// MergeWithProfile applies user overrides from a ModelProfile to capabilities.
// Non-zero/non-nil profile fields override the corresponding capability values.
// Source is set to SourceConfig for overridden fields.
func MergeWithProfile(caps models.ModelCapabilities, profile ModelProfile) models.ModelCapabilities {
	if profile.ContextBudget > 0 {
		caps.ContextWindow = profile.ContextBudget
		caps.ContextWindowSource = models.SourceConfig
	}
	if profile.ToolFormat != "" {
		caps.ToolFormat = ParseToolFormat(profile.ToolFormat)
		caps.ToolFormatSource = models.SourceConfig
	}
	if profile.SystemRole != nil {
		caps.SystemRole = *profile.SystemRole
	}
	if profile.MidConvoSystem != nil {
		caps.MidConvoSystem = *profile.MidConvoSystem
	}
	return caps
}

// ParseToolFormat converts a string tool format name to a ToolFormat enum value.
func ParseToolFormat(s string) models.ToolFormat {
	switch strings.ToLower(s) {
	case "native":
		return models.ToolFormatNative
	case "function_calling":
		return models.ToolFormatFunctionCall
	case "text_in_prompt":
		return models.ToolFormatTextInPrompt
	default:
		return models.ToolFormatNone
	}
}
