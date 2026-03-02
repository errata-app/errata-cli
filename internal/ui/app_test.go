package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/datastore"
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
	store, err := datastore.New(datastore.Options{
		HistoryPath:    sp.HistoryPath,
		PromptHistPath: filepath.Join(tmp, "prompt_hist.jsonl"),
		SessionPaths:   sp,
		SessionID:      "session",
		PrefPath:       filepath.Join(tmp, "pref.jsonl"),
		Meta:           meta,
	})
	require.NoError(t, err)
	a := New(ads, config.Config{}, nil, nil, nil, nil, false, store)
	return *a
}

// ─── prompt history navigation ────────────────────────────────────────────────

func appWithHistory(t *testing.T, prompts []string) App {
	t.Helper()
	a := newAppForTest(t, nil)
	// Inject history newest-first by recording in reverse order.
	for i := len(prompts) - 1; i >= 0; i-- {
		a.store.RecordPrompt(prompts[i])
	}
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
	assert.NotNil(t, cmd, "withMessage now returns a tea.Println cmd")
	app := result.(App)
	require.NotEmpty(t, app.feed, "expected message in feed")
	msg := app.feed[len(app.feed)-1].text
	assert.Contains(t, msg, "Stats")
}

func TestHandleStatsCmd_WithSessionCost(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.AccumulateCost("claude-sonnet-4-6", 0.0042)
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

func TestActiveModelIDs_SubsetShown(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	a.activeAdapters = []models.ModelAdapter{uiStub{"m1"}}

	ids := a.activeModelIDs()
	assert.Equal(t, []string{"m1"}, ids)
}

func TestActiveModelIDs_AllModelsShown(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)

	ids := a.activeModelIDs()
	assert.Equal(t, []string{"m1", "m2"}, ids)
}

func TestModelIDCandidates_UsesAvailableModels(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	a.availableModels = []string{"alpha", "beta", "m1"}

	got := a.modelIDCandidates()
	assert.Equal(t, []string{"alpha", "beta", "m1"}, got)
}

func TestModelIDCandidates_FallsBackToAdapters(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	// availableModels is nil (default from newAppForTest).

	got := a.modelIDCandidates()
	assert.Equal(t, []string{"m1", "m2"}, got)
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

func TestPaste_MultiLineStoresBadge(t *testing.T) {
	a := newAppForTest(t, nil)
	result, cmd := a.handlePaste("line1\nline2\nline3")
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Equal(t, "line1\nline2\nline3", app.pastedText)
	assert.Equal(t, 3, app.pastedLineCount)
}

func TestPaste_MultiLineWithCarriageReturns(t *testing.T) {
	a := newAppForTest(t, nil)
	// Terminals send \r instead of \n inside bracket paste.
	result, cmd := a.handlePaste("line1\rline2\rline3")
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Equal(t, "line1\nline2\nline3", app.pastedText)
	assert.Equal(t, 3, app.pastedLineCount)
}

func TestPaste_MultiLineWithCRLF(t *testing.T) {
	a := newAppForTest(t, nil)
	result, cmd := a.handlePaste("line1\r\nline2\r\nline3")
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Equal(t, "line1\nline2\nline3", app.pastedText)
	assert.Equal(t, 3, app.pastedLineCount)
}

func TestPaste_TwoLinesPassesToTextarea(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handlePaste("line1\nline2")
	app := result.(App)
	// 2-line paste should NOT be intercepted — goes to textarea.
	assert.Empty(t, app.pastedText)
	assert.Equal(t, 0, app.pastedLineCount)
}

func TestPaste_EnterSubmitsPastedText(t *testing.T) {
	a := newAppForTest(t, nil)
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	result, _ := a.handleIdleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	result, _ := a.handleIdleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	result, cmd := a.handleIdleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
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

	result, _ := a.handleIdleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
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

	view := a.View().Content
	assert.Contains(t, view, "pasted 5 lines")
}

func TestPaste_BadgeNotShownWhenNoPaste(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 80
	a.height = 40

	view := a.View().Content
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

// ─── double-ESC to clear ─────────────────────────────────────────────────────

func escMsg() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEscape}
}

func TestDoubleEsc_ClearsTextarea(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetValue("some text to clear")

	// First ESC — records timestamp, shows hint, does not clear.
	result, _ := a.handleIdleKey(escMsg())
	a = result.(App)
	assert.Equal(t, "some text to clear", a.input.Value())
	assert.True(t, a.escHintVisible)

	// Simulate rapid second ESC within the 300ms window.
	a.lastEscTime = time.Now()
	result, cmd := a.handleIdleKey(escMsg())
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Empty(t, app.input.Value())
	assert.False(t, app.escHintVisible)
}

func TestSingleEsc_DoesNotClearTextarea(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetValue("keep this")

	result, _ := a.handleIdleKey(escMsg())
	app := result.(App)
	// Single ESC should NOT clear — text remains (textarea processes ESC normally).
	assert.NotEmpty(t, app.input.Value())
	// Hint should be visible after first ESC with content.
	assert.True(t, app.escHintVisible)
}

func TestSingleEsc_NoHintWhenEmpty(t *testing.T) {
	a := newAppForTest(t, nil)
	// Textarea is empty — no hint should appear.
	result, _ := a.handleIdleKey(escMsg())
	app := result.(App)
	assert.False(t, app.escHintVisible)
}

func TestDoubleEsc_ClearsPasteState(t *testing.T) {
	a := newAppForTest(t, nil)
	a.pastedText = "line1\nline2\nline3"
	a.pastedLineCount = 3

	// Simulate double-ESC: set lastEscTime to now, then send ESC.
	a.lastEscTime = time.Now()
	result, cmd := a.handleIdleKey(escMsg())
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Empty(t, app.pastedText)
	assert.Equal(t, 0, app.pastedLineCount)
}

func TestDoubleEsc_ClearsHistoryNavigation(t *testing.T) {
	a := appWithHistory(t, []string{"old prompt"})
	a.input.SetValue("old prompt")
	a.historyIdx = 0
	a.historyInputBuf = "draft"

	a.lastEscTime = time.Now()
	result, _ := a.handleIdleKey(escMsg())
	app := result.(App)
	assert.Empty(t, app.input.Value())
	assert.Equal(t, -1, app.historyIdx)
	assert.Empty(t, app.historyInputBuf)
}

func TestDoubleEsc_NoopWhenEmpty(t *testing.T) {
	a := newAppForTest(t, nil)
	// Textarea is empty, no paste — double-ESC should not trigger clear.
	a.lastEscTime = time.Now()
	result, _ := a.handleIdleKey(escMsg())
	app := result.(App)
	// Should just record lastEscTime, pass through to textarea.
	assert.NotEqual(t, time.Time{}, app.lastEscTime)
}

// ─── Shift+Enter / Alt+Enter newline insertion ──────────────────────────────

func TestShiftEnter_InsertsNewline(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetValue("hello")
	a.input.CursorEnd()

	msg := tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
	result, _ := a.handleIdleKey(msg)
	app := result.(App)
	// Shift+Enter should NOT submit — should stay idle and insert a newline.
	assert.Equal(t, modeIdle, app.mode)
	// The textarea uses splitLine (not InsertString), so LineCount reflects
	// the new row even though Value() trims the trailing empty line.
	assert.Equal(t, 2, app.input.LineCount(), "Shift+Enter should create a second line")
}

func TestAltEnter_StillInsertsNewline(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetValue("hello")
	a.input.CursorEnd()

	msg := tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
	result, _ := a.handleIdleKey(msg)
	app := result.(App)
	// Alt+Enter should NOT submit — should stay idle and insert a newline.
	assert.Equal(t, modeIdle, app.mode)
	assert.Equal(t, 2, app.input.LineCount(), "Alt+Enter should create a second line")
}

func TestCtrlJ_InsertsNewline(t *testing.T) {
	a := newAppForTest(t, nil)
	a.input.SetValue("hello")
	a.input.CursorEnd()

	// Terminals send Ctrl+J (linefeed) for Shift+Enter.
	msg := tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	result, cmd := a.handleIdleKey(msg)
	assert.Nil(t, cmd)
	app := result.(App)
	assert.Equal(t, modeIdle, app.mode, "Ctrl+J should not submit")
	assert.Equal(t, 2, app.input.LineCount(), "Ctrl+J should insert a newline")
}

func TestPlainJ_DoesNotInsertNewline(t *testing.T) {
	a := newAppForTest(t, nil)

	// Plain 'j' with no modifier should type the character, not insert a newline.
	msg := tea.KeyPressMsg{Code: 'j', Mod: 0, Text: "j"}
	result, _ := a.handleIdleKey(msg)
	app := result.(App)
	assert.Equal(t, "j", app.input.Value(), "plain 'j' should type the character")
	assert.Equal(t, 1, app.input.LineCount(), "plain 'j' should not insert a newline")
}

func TestPlainEnter_Submits(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	a.input.SetValue("do something")

	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	result, _ := a.handleIdleKey(msg)
	app := result.(App)
	// Plain Enter should submit — mode transitions to running and input is cleared.
	assert.Equal(t, modeRunning, app.mode)
	assert.Empty(t, app.input.Value())
}

// ─── inline rendering ────────────────────────────────────────────────────────

func TestWithMessage_ReturnsPrintCmd(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 80
	result, cmd := a.withMessage("hello world")
	assert.NotNil(t, cmd, "withMessage should return a non-nil tea.Println cmd")
	assert.Len(t, result.feed, 1, "withMessage should append to feed")
	assert.Equal(t, "hello world", result.feed[0].text)
}

func TestView_InlineModeNoAltScreen(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 80
	a.height = 40

	v := a.View()
	assert.False(t, v.AltScreen, "View should use inline rendering (AltScreen=false)")
}

func TestView_ConfigOverlayUsesAltScreen(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 80
	a.height = 40
	a.configOverlayActive = true

	v := a.View()
	assert.True(t, v.AltScreen, "config overlay should use AltScreen=true")
}
