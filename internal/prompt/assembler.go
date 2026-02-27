package prompt

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"context"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// PromptPayload is the per-model result of deterministic prompt assembly.
// Every field is user-authored content that was resolved from recipe variants/overrides.
// No LLM-based rewriting occurs — the assembler only selects and composes.
type PromptPayload struct {
	SystemPrompt        string            // resolved system prompt for this model
	SystemPromptSource  string            // debug string: "override:gpt-4o", "variant:concise", "default"
	ToolDescriptions    map[string]string // tool_name → custom description (non-empty only)
	SubAgentPrompts     map[string]string // mode_name → resolved behavioral prompt
	SummarizationPrompt string            // resolved summarization prompt (may be empty)
}

// RecipeAccessor is the subset of recipe.Recipe that the assembler needs.
// Using an interface avoids a direct import of the recipe package, keeping
// the dependency graph clean (prompt ← recipe, not prompt → recipe).
type RecipeAccessor interface {
	SystemPromptVS() VariantSet
	ToolDescriptionVS(toolName string) VariantSet
	SubAgentModeVS(modeName string) VariantSet
	SummarizationVS() VariantSet
	AllToolDescriptionNames() []string
	AllSubAgentModeNames() []string
	TierForModel(modelID string) string
}

// Assemble produces a PromptPayload for a specific model by resolving all
// recipe variant sets against the model's identity and capabilities.
// This is purely deterministic — no LLM calls, no content modification.
func Assemble(rec RecipeAccessor, modelID, provider string, caps models.ModelCapabilities) PromptPayload {
	tier := rec.TierForModel(modelID)

	// Resolve system prompt.
	sp, spSource := rec.SystemPromptVS().Resolve(modelID, provider, tier)

	// Resolve tool descriptions for all tools that have any configured content.
	toolDescs := make(map[string]string)
	for _, toolName := range rec.AllToolDescriptionNames() {
		desc, _ := rec.ToolDescriptionVS(toolName).Resolve(modelID, provider, tier)
		if desc != "" {
			toolDescs[toolName] = desc
		}
	}

	// For text_in_prompt models, append tool descriptions to the system prompt
	// since the API doesn't support structured tool definitions.
	if caps.ToolFormat == models.ToolFormatTextInPrompt && len(toolDescs) > 0 {
		var sb strings.Builder
		sb.WriteString(sp)

		// Sort tool names for deterministic output.
		names := make([]string, 0, len(toolDescs))
		for name := range toolDescs {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			sb.WriteString("\n\n=== Tool: ")
			sb.WriteString(name)
			sb.WriteString(" ===\n")
			sb.WriteString(toolDescs[name])
		}
		sp = sb.String()
		// For text_in_prompt, descriptions are in the system prompt, not separate.
		toolDescs = nil
	}

	// Resolve sub-agent mode prompts (only when sub-agent feature is enabled).
	var subPrompts map[string]string
	if tools.SubagentEnabled {
		subPrompts = make(map[string]string)
		for _, modeName := range rec.AllSubAgentModeNames() {
			p, _ := rec.SubAgentModeVS(modeName).Resolve(modelID, provider, tier)
			if p != "" {
				subPrompts[modeName] = p
			}
		}
	}

	// Resolve summarization prompt.
	sumPrompt, _ := rec.SummarizationVS().Resolve(modelID, provider, tier)

	// Warn if system prompt is very large relative to context window.
	if caps.ContextWindow > 0 && len(sp) > 0 {
		// Rough estimate: 4 chars ≈ 1 token.
		estimatedTokens := len(sp) / 4
		threshold := caps.ContextWindow * 30 / 100
		if estimatedTokens > threshold {
			log.Printf("prompt: system prompt for %s is ~%d tokens (%.0f%% of %d context window)",
				modelID, estimatedTokens,
				float64(estimatedTokens)/float64(caps.ContextWindow)*100,
				caps.ContextWindow)
		}
	}

	payload := PromptPayload{
		SystemPrompt:        sp,
		SystemPromptSource:  spSource,
		SummarizationPrompt: sumPrompt,
	}
	if len(toolDescs) > 0 {
		payload.ToolDescriptions = toolDescs
	}
	if len(subPrompts) > 0 {
		payload.SubAgentPrompts = subPrompts
	}
	return payload
}

// BuildSystemMessage composes the full system message from a payload and
// tool-use guidance. If the payload has a system prompt, it comes first,
// followed by the guidance. If the payload's system prompt is empty,
// only the guidance is returned.
func BuildSystemMessage(payload PromptPayload, guidance string) string {
	if payload.SystemPrompt == "" {
		return guidance
	}
	return payload.SystemPrompt + "\n" + guidance
}

// ─── Context-based payload delivery ──────────────────────────────────────────

type payloadsKey struct{}

// WithPayloads returns a context carrying per-model prompt payloads.
// modelID → PromptPayload.
func WithPayloads(ctx context.Context, payloads map[string]PromptPayload) context.Context {
	return context.WithValue(ctx, payloadsKey{}, payloads)
}

// PayloadFromContext retrieves the PromptPayload for modelID from ctx.
// Returns (zero PromptPayload, false) when no payloads are stored or the
// model has no entry.
func PayloadFromContext(ctx context.Context, modelID string) (PromptPayload, bool) {
	m, _ := ctx.Value(payloadsKey{}).(map[string]PromptPayload)
	if m == nil {
		return PromptPayload{}, false
	}
	p, ok := m[modelID]
	return p, ok
}

// ─── Default summarization prompt ────────────────────────────────────────────

// DefaultSummarizationPrompt is used when no recipe-level summarization prompt
// is configured. It is designed to produce high-quality context summaries that
// preserve essential information for conversation continuity.
const DefaultSummarizationPrompt = `Summarize this conversation for context continuity. Preserve:
- All file paths mentioned and their current state
- Decisions made and their rationale
- Errors encountered and how they were resolved
- The current task and its progress
- Code snippets actively being worked on
Discard verbose tool output and abandoned tangents.
Format: Start with "Current task: ..." then list items concisely.
Reply with ONLY the summary.`

// ResolveSummarizationPrompt returns the summarization prompt for a model,
// falling back to DefaultSummarizationPrompt when the payload has none.
func ResolveSummarizationPrompt(ctx context.Context, modelID string) string {
	if payload, ok := PayloadFromContext(ctx, modelID); ok && payload.SummarizationPrompt != "" {
		return payload.SummarizationPrompt
	}
	return DefaultSummarizationPrompt
}

// ─── Diagnostics ─────────────────────────────────────────────────────────────

// PayloadSummary returns a human-readable summary of the payload for debugging.
func PayloadSummary(p PromptPayload) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("system_prompt: %d chars (source: %s)", len(p.SystemPrompt), p.SystemPromptSource))
	if len(p.ToolDescriptions) > 0 {
		parts = append(parts, fmt.Sprintf("tool_descriptions: %d tools", len(p.ToolDescriptions)))
	}
	if len(p.SubAgentPrompts) > 0 {
		parts = append(parts, fmt.Sprintf("sub_agent_prompts: %d modes", len(p.SubAgentPrompts)))
	}
	if p.SummarizationPrompt != "" {
		parts = append(parts, fmt.Sprintf("summarization_prompt: %d chars", len(p.SummarizationPrompt)))
	}
	return strings.Join(parts, ", ")
}
