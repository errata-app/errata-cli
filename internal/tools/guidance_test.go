package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/tools"
)

// ─── DefaultToolGuidance ────────────────────────────────────────────────────

func TestDefaultToolGuidance_ContainsKeyTools(t *testing.T) {
	g := tools.DefaultToolGuidance()
	assert.Contains(t, g, "list_directory")
	assert.Contains(t, g, "write_file")
	assert.Contains(t, g, "search_code")
}

// ─── WithToolGuidance ─────────────────────────────────────────────────────────

func TestWithToolGuidance_OverridesEffectiveGuidance(t *testing.T) {
	original := tools.SystemPromptSuffix(context.Background())
	ctx := tools.WithToolGuidance(context.Background(), "Custom guidance: use tools wisely.")

	modified := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, modified, "Custom guidance: use tools wisely.")
	assert.NotContains(t, modified, "list_directory")
	assert.NotEqual(t, original, modified)
}

func TestWithToolGuidance_EmptyUsesDefault(t *testing.T) {
	original := tools.SystemPromptSuffix(context.Background())
	// An empty tool guidance in context should not override the default.
	ctx := tools.WithToolGuidance(context.Background(), "")
	restored := tools.SystemPromptSuffix(ctx)
	assert.Equal(t, original, restored)
}

func TestWithToolGuidance_WithSystemPromptExtra(t *testing.T) {
	ctx := tools.WithToolGuidance(context.Background(), "Custom guidance.")
	ctx = tools.WithSystemPromptExtra(ctx, "Extra context.")

	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "Custom guidance.")
	assert.Contains(t, s, "Extra context.")
	assert.NotContains(t, s, "list_directory")
}

// ─── SystemPromptGuidance ───────────────────────────────────────────────────

func TestSystemPromptGuidance_AlwaysReturnsDefault(t *testing.T) {
	// SystemPromptGuidance() always returns the built-in default guidance
	// (no context, no overrides).
	g := tools.SystemPromptGuidance()
	assert.Contains(t, g, "list_directory")
	assert.Contains(t, g, "Tool use guidance")
}

func TestSystemPromptGuidance_IsSubsetOfSuffix(t *testing.T) {
	guidance := tools.SystemPromptGuidance()
	suffix := tools.SystemPromptSuffix(context.Background())
	assert.True(t, strings.HasPrefix(suffix, guidance),
		"SystemPromptSuffix should start with SystemPromptGuidance")
}

// ─── SystemPromptSuffix ───────────────────────────────────────────────────────

func TestSystemPromptSuffix_NonEmpty(t *testing.T) {
	s := tools.SystemPromptSuffix(context.Background())
	assert.NotEmpty(t, s)
}

func TestSystemPromptSuffix_ContainsKeyGuidance(t *testing.T) {
	s := tools.SystemPromptSuffix(context.Background())
	assert.Contains(t, s, "write_file")
	assert.Contains(t, s, "list_directory")
	assert.Contains(t, s, "search_code")
}

func TestSystemPromptSuffix_AllToolsActive_MatchesFullGuidance(t *testing.T) {
	// When all tools are active, the output should match the unfiltered guidance.
	allDefs := tools.Definitions
	ctx := tools.WithActiveTools(context.Background(), allDefs)
	filtered := tools.SystemPromptSuffix(ctx)
	unfiltered := tools.SystemPromptSuffix(context.Background())
	assert.Equal(t, unfiltered, filtered)
}

func TestSystemPromptSuffix_SingleToolBash(t *testing.T) {
	// Only bash active → only bash guidance line present.
	bashDef := tools.ToolDef{Name: "bash"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef})
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "bash")
	assert.NotContains(t, s, "list_directory")
	assert.NotContains(t, s, "read_file")
	assert.NotContains(t, s, "write_file")
	assert.NotContains(t, s, "search_code")
}

func TestSystemPromptSuffix_NoActiveTools_ReturnsFullGuidance(t *testing.T) {
	// WithActiveTools(nil) is a no-op — no value stored → full guidance.
	ctx := tools.WithActiveTools(context.Background(), nil)
	s := tools.SystemPromptSuffix(ctx)
	unfiltered := tools.SystemPromptSuffix(context.Background())
	assert.Equal(t, unfiltered, s)
}

func TestSystemPromptSuffix_ExplicitlyEmptyTools_ReturnsNoGuidance(t *testing.T) {
	// Explicitly setting an empty tool slice (all tools disabled) should return
	// no tool guidance at all — distinct from "no tools in context" (nil).
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{})
	s := tools.SystemPromptSuffix(ctx)
	assert.NotContains(t, s, "list_directory")
	assert.NotContains(t, s, "read_file")
	assert.NotContains(t, s, "bash")
	assert.NotContains(t, s, "Tool use guidance")
}

func TestSystemPromptSuffix_AllToolsDisabledViaDefinitionsAllowed(t *testing.T) {
	// Mirrors the exact TUI code path: DefinitionsAllowed with every built-in
	// tool disabled → non-nil empty slice → WithActiveTools → no guidance.
	disabled := map[string]bool{
		tools.ReadToolName: true, tools.WriteToolName: true, tools.EditToolName: true,
		tools.ListDirToolName: true, tools.SearchFilesName: true, tools.SearchCodeName: true,
		tools.BashToolName: true, tools.WebFetchToolName: true, tools.WebSearchToolName: true,
	}
	activeDefs := tools.DefinitionsAllowed(nil, disabled)
	require.NotNil(t, activeDefs, "DefinitionsAllowed should return non-nil empty slice")
	assert.Empty(t, activeDefs)

	ctx := tools.WithActiveTools(context.Background(), activeDefs)
	s := tools.SystemPromptSuffix(ctx)
	assert.NotContains(t, s, "Tool use guidance")
	assert.NotContains(t, s, "list_directory")
	assert.NotContains(t, s, "bash")
}

func TestSystemPromptSuffix_PartialOverlap_EditWithoutWrite(t *testing.T) {
	// edit_file active but not write_file → lines mentioning edit_file are included
	// (any tagged tool matching suffices).
	editDef := tools.ToolDef{Name: "edit_file"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{editDef})
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "edit_file")
	// The edit/write combo line should be included because edit_file matches.
	assert.Contains(t, s, "edit_file for targeted changes")
	// Lines for unrelated tools should not appear.
	assert.NotContains(t, s, "list_directory")
	assert.NotContains(t, s, "search_code")
	assert.NotContains(t, s, "bash")
}

func TestSystemPromptSuffix_CustomGuidance_NotFiltered(t *testing.T) {
	// Custom guidance override is never filtered — user wrote it intentionally.
	bashDef := tools.ToolDef{Name: "bash"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef})
	ctx = tools.WithToolGuidance(ctx, "Custom: always use all the things.")
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "Custom: always use all the things.")
	// Should NOT contain built-in guidance since it's overridden.
	assert.NotContains(t, s, "list_directory")
}

func TestSystemPromptSuffix_WithContextAndExtra(t *testing.T) {
	// Filtered guidance + system prompt extra both present via context.
	bashDef := tools.ToolDef{Name: "bash"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef})
	ctx = tools.WithSystemPromptExtra(ctx, "Extra project context.")
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "bash")
	assert.Contains(t, s, "Extra project context.")
	assert.NotContains(t, s, "list_directory")
}

// ─── WithSystemPromptExtra ──────────────────────────────────────────────────

func TestWithSystemPromptExtra_AffectsSuffix(t *testing.T) {
	original := tools.SystemPromptSuffix(context.Background())
	ctx := tools.WithSystemPromptExtra(context.Background(), "TEST_EXTRA_CONTENT")
	modified := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, modified, "TEST_EXTRA_CONTENT")
	assert.NotEqual(t, original, modified)
}

func TestWithSystemPromptExtra_ContextSetsExtra(t *testing.T) {
	ctx := tools.WithSystemPromptExtra(context.Background(), "context extra")
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "context extra")
}

// ─── Context-based guidance accessors ───────────────────────────────────────

func TestWithToolGuidance_ContextSetsGuidance(t *testing.T) {
	ctx := tools.WithToolGuidance(context.Background(), "context guidance")
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "context guidance")
	// Default guidance should be absent (overridden by context guidance).
	assert.NotContains(t, s, "list_directory")
}

func TestSystemPromptSuffix_NoContextMeansNoExtra(t *testing.T) {
	// Without WithSystemPromptExtra on context, no extra text is appended.
	s := tools.SystemPromptSuffix(context.Background())
	assert.Contains(t, s, "Tool use guidance")
	assert.NotContains(t, s, "extra")
}

func TestSystemPromptSuffix_NoContextUsesDefault(t *testing.T) {
	s := tools.SystemPromptSuffix(context.Background())
	// Default guidance should be present.
	assert.Contains(t, s, "Tool use guidance")
	assert.Contains(t, s, "list_directory")
	// No extra text appended.
	assert.NotContains(t, s, "global")
	assert.NotContains(t, s, "context")
}

// ─── Combined recipe flow regression test ───────────────────────────────────

func TestSystemPromptSuffix_CombinedRecipeFlow(t *testing.T) {
	// Pins the exact composition rules adapters depend on:
	// 1. Custom guidance replaces default guidance
	// 2. Extra text is appended after guidance
	// 3. Without context values, default behavior is used

	// Capture original state (no context values).
	original := tools.SystemPromptSuffix(context.Background())

	// Set custom guidance and extra prompt via context (simulates recipe flow).
	ctx := tools.WithToolGuidance(context.Background(), "custom guidance")
	ctx = tools.WithSystemPromptExtra(ctx, "custom extra")

	combined := tools.SystemPromptSuffix(ctx)

	// Both must be present.
	assert.Contains(t, combined, "custom guidance")
	assert.Contains(t, combined, "custom extra")

	// Guidance must come before extra (guidance is the base, extra is appended).
	guidanceIdx := strings.Index(combined, "custom guidance")
	extraIdx := strings.Index(combined, "custom extra")
	assert.Less(t, guidanceIdx, extraIdx, "guidance should appear before extra in SystemPromptSuffix")

	// Default guidance must be absent (overridden by custom).
	assert.NotContains(t, combined, "list_directory")

	// A fresh context without values should return the original default.
	restored := tools.SystemPromptSuffix(context.Background())
	assert.Equal(t, original, restored)
}
