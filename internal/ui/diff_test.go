package ui_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
	"github.com/suarezc/errata/internal/ui"
)

// --- RenderDiffs ---

func TestRenderDiffs_EmptyResponses(t *testing.T) {
	out := ui.RenderDiffs([]models.ModelResponse{})
	assert.Empty(t, out)
}

func TestRenderDiffs_ShowsFailedResponses(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "bad-model", Error: "timeout"},
		{ModelID: "good-model", LatencyMS: 100},
	}
	out := ui.RenderDiffs(responses)
	assert.Contains(t, out, "bad-model")
	assert.Contains(t, out, "timeout")
	assert.Contains(t, out, "good-model")
}

func TestRenderDiffs_NoWritesShowsTextNotPlaceholder(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "claude-sonnet-4-6", LatencyMS: 200, Text: "here is my answer"},
	}
	out := ui.RenderDiffs(responses)
	assert.Contains(t, out, "here is my answer")
	assert.NotContains(t, out, "no file writes proposed")
}

func TestRenderDiffs_ShowsModelIDAndLatency(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "gpt-4o", LatencyMS: 1234},
	}
	out := ui.RenderDiffs(responses)
	assert.Contains(t, out, "gpt-4o")
	assert.Contains(t, out, "1234ms")
}

func TestRenderDiffs_WithProposedWriteShowsPath(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID:   "claude-sonnet-4-6",
			LatencyMS: 500,
			ProposedWrites: []tools.FileWrite{
				{Path: "src/utils.py", Content: "def foo(): pass\n"},
			},
		},
	}
	out := ui.RenderDiffs(responses)
	assert.Contains(t, out, "src/utils.py")
}

func TestRenderDiffs_TextPreviewWhenNoWrites(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID:   "gemini-2.0-flash",
			LatencyMS: 300,
			Text:      "Here is my analysis of the code.\nSecond line.",
		},
	}
	out := ui.RenderDiffs(responses)
	// First line of text should appear as a preview.
	assert.Contains(t, out, "Here is my analysis of the code.")
}

func TestRenderDiffs_TextShownAlongsideWrites(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID:   "claude-sonnet-4-6",
			LatencyMS: 500,
			Text:      "I changed the function signature.",
			ProposedWrites: []tools.FileWrite{
				{Path: "main.go", Content: "package main\nfunc Foo() {}\n"},
			},
		},
	}
	out := ui.RenderDiffs(responses)
	assert.Contains(t, out, "main.go")
	assert.Contains(t, out, "I changed the function signature.")
	assert.Contains(t, out, "reasoning")
}

func TestRenderDiffs_MultipleResponses(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "model-a", LatencyMS: 100},
		{ModelID: "model-b", LatencyMS: 200},
	}
	out := ui.RenderDiffs(responses)
	assert.Contains(t, out, "model-a")
	assert.Contains(t, out, "model-b")
	// model-a should appear before model-b.
	assert.Less(t, strings.Index(out, "model-a"), strings.Index(out, "model-b"))
}

func TestRenderDiffs_NoDuplicatePrefix(t *testing.T) {
	// Regression: diff.Compute stores "+line" in Content; the TUI renderer
	// must strip the leading prefix to avoid showing "++line" or "--line".
	responses := []models.ModelResponse{
		{
			ModelID:   "test-model",
			LatencyMS: 100,
			ProposedWrites: []tools.FileWrite{
				{Path: "/tmp/_errata_test_noexist_" + t.Name(), Content: "hello\nworld\n"},
			},
		},
	}
	out := ui.RenderDiffs(responses)
	// The rendered output should contain "+ hello" (prefix + content) but never "++".
	assert.NotContains(t, out, "++")
	assert.NotContains(t, out, "--")
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "world")
}

func TestRenderDiffs_WrapsLongTextLines(t *testing.T) {
	longLine := strings.Repeat("x", 120)
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: longLine},
	}
	// Render at 40-char width; the 120-char line should be split across
	// multiple visual lines, none longer than 38 chars (40 - 2 indent).
	out := ui.RenderDiffs(responses, 40)
	for line := range strings.SplitSeq(out, "\n") {
		// Skip header lines (contain model ID/stats) and blank lines.
		if strings.Contains(line, "m1") || strings.TrimSpace(line) == "" {
			continue
		}
		assert.LessOrEqual(t, len([]rune(line)), 40,
			"visual line exceeds terminal width: %q", line)
	}
	// All content should still be present.
	assert.Equal(t, 120, strings.Count(out, "x"))
}

func TestRenderDiffs_WrapsLongReasoningLines(t *testing.T) {
	longLine := strings.Repeat("y", 100)
	responses := []models.ModelResponse{
		{
			ModelID:        "m1",
			Text:           longLine,
			ProposedWrites: []tools.FileWrite{{Path: "f.txt", Content: "a"}},
		},
	}
	out := ui.RenderDiffs(responses, 50)
	assert.Equal(t, 100, strings.Count(out, "y"))
}

// --- RenderSelectionMenu ---

func TestRenderSelectionMenu_ContainsSkipOption(t *testing.T) {
	out := ui.RenderSelectionMenu([]models.ModelResponse{})
	assert.Contains(t, out, "Skip")
}

func TestRenderSelectionMenu_ListsOKModels(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "claude-sonnet-4-6", LatencyMS: 891},
		{ModelID: "gpt-4o", LatencyMS: 1243},
	}
	out := ui.RenderSelectionMenu(responses)
	assert.Contains(t, out, "claude-sonnet-4-6")
	assert.Contains(t, out, "gpt-4o")
	assert.Contains(t, out, "891ms")
	assert.Contains(t, out, "1243ms")
}

func TestRenderSelectionMenu_ShowsFailedResponsesAsNonSelectable(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "ok-model", LatencyMS: 500},
		{ModelID: "err-model", Error: "crashed"},
	}
	out := ui.RenderSelectionMenu(responses)
	assert.Contains(t, out, "ok-model")
	assert.Contains(t, out, "err-model") // shown but not numbered
	assert.Contains(t, out, "(error)")
	// ok-model should be numbered 1; err-model should appear as "-"
	assert.Contains(t, out, "  1  ok-model")
	assert.Contains(t, out, "  -  err-model")
}

func TestRenderSelectionMenu_TextOnlyShowsVoteHeader(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m", LatencyMS: 100},
	}
	out := ui.RenderSelectionMenu(responses)
	assert.Contains(t, out, "Vote for a response:")
	assert.NotContains(t, out, "no writes")
}

func TestRenderSelectionMenu_ShowsFilePathsForWrites(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID:   "m",
			LatencyMS: 100,
			ProposedWrites: []tools.FileWrite{
				{Path: "main.go", Content: "package main"},
			},
		},
	}
	out := ui.RenderSelectionMenu(responses)
	assert.Contains(t, out, "main.go")
}
