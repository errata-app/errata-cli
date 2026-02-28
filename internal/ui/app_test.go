package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/session"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

type uiStub struct{ id string }

func (s uiStub) ID() string { return s.id }
func (s uiStub) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s uiStub) RunAgent(_ context.Context, _ []models.ConversationTurn, _ string, _ func(models.AgentEvent)) (models.ModelResponse, error) {
	return models.ModelResponse{ModelID: s.id}, nil
}

func newAppForTest(t *testing.T, ads []models.ModelAdapter) App {
	t.Helper()
	tmp := t.TempDir()
	sp := session.Paths{
		Dir:            filepath.Join(tmp, "session"),
		HistoryPath:    filepath.Join(tmp, "session", "history.json"),
		CheckpointPath: filepath.Join(tmp, "session", "checkpoint.json"),
		MetaPath:       filepath.Join(tmp, "session", "meta.json"),
		FeedPath:       filepath.Join(tmp, "session", "feed.json"),
		RecipePath:     filepath.Join(tmp, "session", "recipe.md"),
	}
	meta := session.Meta{ID: "test-session"}
	a := New(ads, filepath.Join(tmp, "pref.jsonl"), filepath.Join(tmp, "prompt_hist.jsonl"), "session", config.Config{}, nil, nil, nil, sp, meta)
	return *a
}

// ─── prompt history navigation ────────────────────────────────────────────────

func appWithHistory(t *testing.T, prompts []string) App {
	t.Helper()
	a := newAppForTest(t, nil)
	// Inject history newest-first, as Load() returns them.
	a.promptHistory = prompts
	a.historyIdx = -1
	return a
}

func TestHistoryBack_EmptyHistory(t *testing.T) {
	a := newAppForTest(t, nil)
	result, cmd := a.historyBack()
	if cmd != nil {
		t.Error("expected nil cmd on empty history")
	}
	app := result.(App)
	if app.historyIdx != -1 {
		t.Errorf("historyIdx should remain -1, got %d", app.historyIdx)
	}
}

func TestHistoryBack_FirstPressSelectsMostRecent(t *testing.T) {
	a := appWithHistory(t, []string{"newest", "older", "oldest"})
	a.input.SetValue("draft")
	result, _ := a.historyBack()
	app := result.(App)
	if app.historyIdx != 0 {
		t.Errorf("expected historyIdx=0, got %d", app.historyIdx)
	}
	if app.input.Value() != "newest" {
		t.Errorf("expected input='newest', got %q", app.input.Value())
	}
	if app.historyInputBuf != "draft" {
		t.Errorf("expected historyInputBuf='draft', got %q", app.historyInputBuf)
	}
}

func TestHistoryBack_SubsequentPressMovesFurther(t *testing.T) {
	a := appWithHistory(t, []string{"newest", "older", "oldest"})
	res1, _ := a.historyBack()
	app1, ok := res1.(App)
	if !ok {
		t.Fatal("expected App type from historyBack")
	}
	res2, _ := app1.historyBack()
	app2, ok := res2.(App)
	if !ok {
		t.Fatal("expected App type from historyBack")
	}
	if app2.historyIdx != 1 {
		t.Errorf("expected historyIdx=1, got %d", app2.historyIdx)
	}
	if app2.input.Value() != "older" {
		t.Errorf("expected input='older', got %q", app2.input.Value())
	}
}

func TestHistoryBack_ClampAtOldest(t *testing.T) {
	a := appWithHistory(t, []string{"only"})
	res1, _ := a.historyBack()
	app1, ok := res1.(App)
	if !ok {
		t.Fatal("expected App type from historyBack")
	}
	res2, _ := app1.historyBack() // already at oldest — should not move
	app, ok := res2.(App)
	if !ok {
		t.Fatal("expected App type from historyBack")
	}
	if app.historyIdx != 0 {
		t.Errorf("expected historyIdx clamped at 0, got %d", app.historyIdx)
	}
}

func TestHistoryForward_RestoresInputBuf(t *testing.T) {
	a := appWithHistory(t, []string{"p1", "p2"})
	a.historyInputBuf = "original"
	a.historyIdx = 0
	a.input.SetValue("p1")
	result, _ := a.historyForward()
	app := result.(App)
	if app.historyIdx != -1 {
		t.Errorf("expected historyIdx=-1 after forward past newest, got %d", app.historyIdx)
	}
	if app.input.Value() != "original" {
		t.Errorf("expected input restored to 'original', got %q", app.input.Value())
	}
	if app.historyInputBuf != "" {
		t.Errorf("expected historyInputBuf cleared, got %q", app.historyInputBuf)
	}
}

func TestHistoryForward_MovesForward(t *testing.T) {
	a := appWithHistory(t, []string{"newest", "older"})
	a.historyIdx = 1
	a.input.SetValue("older")
	result, _ := a.historyForward()
	app := result.(App)
	if app.historyIdx != 0 {
		t.Errorf("expected historyIdx=0, got %d", app.historyIdx)
	}
	if app.input.Value() != "newest" {
		t.Errorf("expected input='newest', got %q", app.input.Value())
	}
}

func TestHistoryForward_NoopWhenNotNavigating(t *testing.T) {
	a := appWithHistory(t, []string{"p1"})
	// historyIdx == -1 means not navigating
	result, cmd := a.historyForward()
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	app := result.(App)
	if app.historyIdx != -1 {
		t.Errorf("expected historyIdx to stay -1, got %d", app.historyIdx)
	}
}

// ─── search ───────────────────────────────────────────────────────────────────

func TestSearchResults_EmptyQueryReturnsAll(t *testing.T) {
	a := appWithHistory(t, []string{"fix bug", "add feature", "refactor"})
	results := a.searchResults()
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestSearchResults_FiltersBySubstring(t *testing.T) {
	a := appWithHistory(t, []string{"fix the bug", "add feature", "fix lint"})
	a.searchQuery = "fix"
	results := a.searchResults()
	if len(results) != 2 {
		t.Errorf("expected 2 matches for 'fix', got %d: %v", len(results), results)
	}
}

func TestSearchResults_CaseInsensitive(t *testing.T) {
	a := appWithHistory(t, []string{"Fix Bug", "add feature"})
	a.searchQuery = "fix"
	results := a.searchResults()
	if len(results) != 1 || results[0] != "Fix Bug" {
		t.Errorf("expected case-insensitive match, got %v", results)
	}
}

func TestSearchResults_NoMatch(t *testing.T) {
	a := appWithHistory(t, []string{"hello world"})
	a.searchQuery = "xyz"
	results := a.searchResults()
	if len(results) != 0 {
		t.Errorf("expected 0 matches, got %d", len(results))
	}
}

func TestCurrentSearchResult_ReturnsCorrectIndex(t *testing.T) {
	a := appWithHistory(t, []string{"fix bug", "add feature", "fix lint"})
	a.searchQuery = "fix"
	a.searchResultIdx = 1
	result := a.currentSearchResult()
	if result != "fix lint" {
		t.Errorf("expected 'fix lint' at idx 1, got %q", result)
	}
}

func TestCurrentSearchResult_EmptyWhenOutOfBounds(t *testing.T) {
	a := appWithHistory(t, []string{"only match"})
	a.searchQuery = "only"
	a.searchResultIdx = 5 // out of bounds
	result := a.currentSearchResult()
	if result != "" {
		t.Errorf("expected empty string for out-of-bounds idx, got %q", result)
	}
}

// ─── handleStatsCmd ───────────────────────────────────────────────────────────

func TestHandleStatsCmd_NoData(t *testing.T) {
	a := newAppForTest(t, nil)
	result, cmd := a.handleStatsCmd()
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	app := result.(App)
	if len(app.feed) == 0 {
		t.Fatal("expected message in feed")
	}
	msg := app.feed[len(app.feed)-1].text
	if !strings.Contains(msg, "Stats") {
		t.Errorf("expected 'Stats' in output, got: %s", msg)
	}
}

func TestHandleStatsCmd_WithSessionCost(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionCostPerModel = map[string]float64{"claude-sonnet-4-6": 0.0042}
	a.totalCostUSD = 0.0042
	result, _ := a.handleStatsCmd()
	app := result.(App)
	if len(app.feed) == 0 {
		t.Fatal("expected message in feed")
	}
	msg := app.feed[len(app.feed)-1].text
	if !strings.Contains(msg, "claude-sonnet-4-6") {
		t.Errorf("expected model name in stats output, got: %s", msg)
	}
	if !strings.Contains(msg, "0.0042") {
		t.Errorf("expected cost in stats output, got: %s", msg)
	}
}

func TestSubsetIndicator_ShownInView(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	a.activeAdapters = []models.ModelAdapter{uiStub{"m1"}}

	view := a.View()
	assert.Contains(t, view, "[subset: m1]")
}

func TestSubsetIndicator_NotShownWhenAllModels(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)

	view := a.View()
	assert.NotContains(t, view, "[subset:")
}

// ─── @mention integration ───────────────────────────────────────────────────

func TestHandlePrompt_MentionErrorShowsMessage(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	result, _ := a.handlePrompt("@nonexistent fix bug")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "No model matching")
	assert.Contains(t, last, "nonexistent")
}

func TestHandlePrompt_MentionOnlyNoPromptShowsError(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	result, _ := a.handlePrompt("@m1")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "No prompt text")
}

func TestHandlePrompt_MentionDoesNotChangeActiveAdapters(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	// Set a persistent subset.
	a.activeAdapters = []models.ModelAdapter{uiStub{"m2"}}

	// @mention resolves m1 for this run only.
	result, _ := a.handlePrompt("@m1 hello")
	app := result.(App)
	// activeAdapters should still be the original subset (m2), not changed by @mention.
	require.Len(t, app.activeAdapters, 1)
	assert.Equal(t, "m2", app.activeAdapters[0].ID())
}

// ─── paste badge ─────────────────────────────────────────────────────────────

func pasteMsg(text string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true}
}

func TestPaste_MultiLineStoresBadge(t *testing.T) {
	a := newAppForTest(t, nil)
	result, cmd := a.handleIdleKey(pasteMsg("line1\nline2\nline3"))
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Equal(t, "line1\nline2\nline3", app.pastedText)
	assert.Equal(t, 3, app.pastedLineCount)
}

func TestPaste_TwoLinesPassesToTextarea(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleIdleKey(pasteMsg("line1\nline2"))
	app := result.(App)
	// 2-line paste should NOT be intercepted — goes to textarea.
	assert.Empty(t, app.pastedText)
	assert.Equal(t, 0, app.pastedLineCount)
}

func TestPaste_EnterSubmitsPastedText(t *testing.T) {
	a := newAppForTest(t, nil)
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	result, _ := a.handleIdleKey(tea.KeyMsg{Type: tea.KeyEnter})
	app := result.(App)
	// Paste state should be cleared after submit.
	assert.Empty(t, app.pastedText)
	assert.Equal(t, 0, app.pastedLineCount)
}

func TestPaste_EnterCombinesTypedAndPasted(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetValue("fix this:")
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	result, _ := a.handleIdleKey(tea.KeyMsg{Type: tea.KeyEnter})
	app := result.(App)
	// After submission, paste state should be cleared.
	assert.Empty(t, app.pastedText)
	// The prompt should have been combined (typed + pasted).
	// We can't check the actual prompt value since handlePrompt dispatches,
	// but we verify the state was reset correctly.
	assert.Empty(t, app.input.Value())
}

func TestPaste_BackspaceClearsPasteWhenEmpty(t *testing.T) {
	a := newAppForTest(t, nil)
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	result, cmd := a.handleIdleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Empty(t, app.pastedText)
	assert.Equal(t, 0, app.pastedLineCount)
}

func TestPaste_BackspaceDoesNotClearWhenTextareaHasContent(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetValue("some text")
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	result, _ := a.handleIdleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	app := result.(App)
	// Paste should NOT be cleared because textarea has content.
	assert.Equal(t, "line1\nline2\nline3", app.pastedText)
}

func TestPaste_BadgeShownInView(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 80
	a.height = 40
	a.pastedText = "a\nb\nc\nd\ne"
	a.pastedLineCount = 5

	view := a.View()
	assert.Contains(t, view, "pasted 5 lines")
}

func TestPaste_BadgeNotShownWhenNoPaste(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 80
	a.height = 40

	view := a.View()
	assert.NotContains(t, view, "pasted")
}

func TestPaste_ClearCmdResetsPaste(t *testing.T) {
	a := newAppForTest(t, nil)
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	result, _ := a.handleClearCmd()
	app := result.(App)
	assert.Empty(t, app.pastedText)
	assert.Equal(t, 0, app.pastedLineCount)
}

func TestPaste_WipeCmdResetsPaste(t *testing.T) {
	a := newAppForTest(t, nil)
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	result, _ := a.handleWipeCmd()
	app := result.(App)
	assert.Empty(t, app.pastedText)
	assert.Equal(t, 0, app.pastedLineCount)
}
