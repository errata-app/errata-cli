package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/tools"
)

// ─── SystemPromptGuidance ───────────────────────────────────────────────────

func TestSystemPromptGuidance_AlwaysReturnsDefault(t *testing.T) {
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
	allDefs := tools.Definitions
	ctx := tools.WithActiveTools(context.Background(), allDefs)
	filtered := tools.SystemPromptSuffix(ctx)
	unfiltered := tools.SystemPromptSuffix(context.Background())
	assert.Equal(t, unfiltered, filtered)
}

func TestSystemPromptSuffix_SingleToolBash(t *testing.T) {
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
	ctx := tools.WithActiveTools(context.Background(), nil)
	s := tools.SystemPromptSuffix(ctx)
	unfiltered := tools.SystemPromptSuffix(context.Background())
	assert.Equal(t, unfiltered, s)
}

func TestSystemPromptSuffix_ExplicitlyEmptyTools_ReturnsNoGuidance(t *testing.T) {
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{})
	s := tools.SystemPromptSuffix(ctx)
	assert.NotContains(t, s, "list_directory")
	assert.NotContains(t, s, "read_file")
	assert.NotContains(t, s, "bash")
	assert.NotContains(t, s, "Tool use guidance")
}

func TestSystemPromptSuffix_AllToolsDisabledViaDefinitionsAllowed(t *testing.T) {
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
	editDef := tools.ToolDef{Name: "edit_file"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{editDef})
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "edit_file")
	assert.Contains(t, s, "edit_file for targeted changes")
	assert.NotContains(t, s, "list_directory")
	assert.NotContains(t, s, "search_code")
	assert.NotContains(t, s, "bash")
}

func TestSystemPromptSuffix_WithContextAndExtra(t *testing.T) {
	bashDef := tools.ToolDef{Name: "bash"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef})
	ctx = tools.WithSystemPromptExtra(ctx, "Extra project context.")
	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "bash")
	assert.Contains(t, s, "Extra project context.")
	assert.NotContains(t, s, "list_directory")
}

// ─── WithToolGuidanceMap ─────────────────────────────────────────────────────

func TestWithToolGuidanceMap_OverridesPerTool(t *testing.T) {
	bashDef := tools.ToolDef{Name: "bash"}
	readDef := tools.ToolDef{Name: "read_file"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef, readDef})
	ctx = tools.WithToolGuidanceMap(ctx, map[string]string{
		"bash": "Custom bash guidance.",
	})

	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "Custom bash guidance.")
	// read_file should fall back to code default
	assert.Contains(t, s, "read_file")
}

func TestWithToolGuidanceMap_NilIsNoOp(t *testing.T) {
	original := tools.SystemPromptSuffix(context.Background())
	ctx := tools.WithToolGuidanceMap(context.Background(), nil)
	restored := tools.SystemPromptSuffix(ctx)
	assert.Equal(t, original, restored)
}

func TestWithToolGuidanceMap_WithSystemPromptExtra(t *testing.T) {
	bashDef := tools.ToolDef{Name: "bash"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef})
	ctx = tools.WithToolGuidanceMap(ctx, map[string]string{
		"bash": "Custom bash.",
	})
	ctx = tools.WithSystemPromptExtra(ctx, "Extra context.")

	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "Custom bash.")
	assert.Contains(t, s, "Extra context.")
}

func TestWithToolGuidanceMap_FilteredByActiveTools(t *testing.T) {
	// Only bash is active, but guidance map has entries for bash and read_file.
	// Only the bash guidance should appear.
	bashDef := tools.ToolDef{Name: "bash"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef})
	ctx = tools.WithToolGuidanceMap(ctx, map[string]string{
		"bash":      "Custom bash guidance.",
		"read_file": "Custom read guidance.",
	})

	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "Custom bash guidance.")
	assert.NotContains(t, s, "Custom read guidance.")
	assert.NotContains(t, s, "list_directory")
}

func TestWithToolGuidanceMap_EmptyMapFallsBackToDefaults(t *testing.T) {
	// An empty (non-nil) map means "no overrides" — all tools use code defaults.
	allDefs := tools.Definitions
	ctx := tools.WithActiveTools(context.Background(), allDefs)
	ctx = tools.WithToolGuidanceMap(ctx, map[string]string{})

	s := tools.SystemPromptSuffix(ctx)
	assert.Contains(t, s, "list_directory")
	assert.Contains(t, s, "bash")
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

func TestSystemPromptSuffix_NoContextMeansNoExtra(t *testing.T) {
	s := tools.SystemPromptSuffix(context.Background())
	assert.Contains(t, s, "Tool use guidance")
	assert.NotContains(t, s, "extra")
}

func TestSystemPromptSuffix_NoContextUsesDefault(t *testing.T) {
	s := tools.SystemPromptSuffix(context.Background())
	assert.Contains(t, s, "Tool use guidance")
	assert.Contains(t, s, "list_directory")
	assert.NotContains(t, s, "global")
	assert.NotContains(t, s, "context")
}

// ─── Combined recipe flow regression test ───────────────────────────────────

func TestSystemPromptSuffix_CombinedRecipeFlow(t *testing.T) {
	original := tools.SystemPromptSuffix(context.Background())

	// Set per-tool guidance and extra prompt via context (simulates recipe flow).
	bashDef := tools.ToolDef{Name: "bash"}
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{bashDef})
	ctx = tools.WithToolGuidanceMap(ctx, map[string]string{
		"bash": "custom bash guidance",
	})
	ctx = tools.WithSystemPromptExtra(ctx, "custom extra")

	combined := tools.SystemPromptSuffix(ctx)

	// Both must be present.
	assert.Contains(t, combined, "custom bash guidance")
	assert.Contains(t, combined, "custom extra")

	// Guidance must come before extra.
	guidanceIdx := strings.Index(combined, "custom bash guidance")
	extraIdx := strings.Index(combined, "custom extra")
	assert.Less(t, guidanceIdx, extraIdx, "guidance should appear before extra in SystemPromptSuffix")

	// Default guidance for other tools must be absent (only bash is active).
	assert.NotContains(t, combined, "list_directory")

	// A fresh context should return the original default.
	restored := tools.SystemPromptSuffix(context.Background())
	assert.Equal(t, original, restored)
}
