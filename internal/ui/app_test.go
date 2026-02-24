package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

type uiStub struct{ id string }

func (s uiStub) ID() string { return s.id }
func (s uiStub) RunAgent(_ context.Context, _ []models.ConversationTurn, _ string, _ func(models.AgentEvent)) (models.ModelResponse, error) {
	return models.ModelResponse{ModelID: s.id}, nil
}

func newAppForTest(t *testing.T, ads []models.ModelAdapter) App {
	t.Helper()
	a := New(ads, t.TempDir()+"/pref.jsonl", t.TempDir()+"/hist.json", t.TempDir()+"/prompt_hist.jsonl", "session", config.Config{})
	return *a
}

// ─── formatAvailableModels ───────────────────────────────────────────────────

func TestFormatAvailableModels_SmallProviderListsModels(t *testing.T) {
	results := []adapters.ProviderModels{
		{Provider: "Anthropic", Models: []string{"claude-opus-4-6", "claude-sonnet-4-6"}},
	}
	out := formatAvailableModels(results)
	if !strings.Contains(out, "Anthropic (2)") {
		t.Errorf("expected 'Anthropic (2)' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "claude-opus-4-6") {
		t.Errorf("expected model name listed, got:\n%s", out)
	}
	if !strings.Contains(out, "claude-sonnet-4-6") {
		t.Errorf("expected model name listed, got:\n%s", out)
	}
}

func TestFormatAvailableModels_TruncatesAtCap(t *testing.T) {
	total := adapters.ModelListCap + 5
	ms := make([]string, total)
	for i := range ms {
		ms[i] = fmt.Sprintf("model-%d", i)
	}
	results := []adapters.ProviderModels{
		{Provider: "OpenRouter", Models: ms, TotalCount: total},
	}
	out := formatAvailableModels(results)

	// Header should reflect the full count.
	wantCount := fmt.Sprintf("OpenRouter (%d)", total)
	if !strings.Contains(out, wantCount) {
		t.Errorf("expected %q in output, got:\n%s", wantCount, out)
	}
	// First model should appear; one beyond the cap should not.
	if !strings.Contains(out, "model-0") {
		t.Errorf("first model should be listed, got:\n%s", out)
	}
	if strings.Contains(out, fmt.Sprintf("model-%d", adapters.ModelListCap)) {
		t.Errorf("model beyond cap should not be listed, got:\n%s", out)
	}
	// Truncation notice should mention the omitted count.
	wantMore := fmt.Sprintf("… and %d more", 5)
	if !strings.Contains(out, wantMore) {
		t.Errorf("expected %q in output, got:\n%s", wantMore, out)
	}
}

func TestFormatAvailableModels_FilteredProviderShowsChatOnlyLabel(t *testing.T) {
	// Simulates OpenAI returning 47 total models but only 15 are chat models.
	ms := make([]string, 15)
	for i := range ms {
		ms[i] = fmt.Sprintf("gpt-4-%d", i)
	}
	results := []adapters.ProviderModels{
		{Provider: "OpenAI", Models: ms, TotalCount: 47},
	}
	out := formatAvailableModels(results)
	if !strings.Contains(out, "15 of 47") {
		t.Errorf("expected '15 of 47' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "chat only") {
		t.Errorf("expected 'chat only' label, got:\n%s", out)
	}
}

func TestFormatAvailableModels_ProviderErrorShown(t *testing.T) {
	results := []adapters.ProviderModels{
		{Provider: "Gemini", Err: fmt.Errorf("connection refused")},
	}
	out := formatAvailableModels(results)
	if !strings.Contains(out, "Gemini") {
		t.Errorf("expected provider name in output, got:\n%s", out)
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("expected error message in output, got:\n%s", out)
	}
}

func TestFormatAvailableModels_EmptyResultsNotEmpty(t *testing.T) {
	out := formatAvailableModels(nil)
	if out == "" {
		t.Error("expected non-empty output for nil results")
	}
	out2 := formatAvailableModels([]adapters.ProviderModels{})
	if out2 == "" {
		t.Error("expected non-empty output for empty results")
	}
}

func TestFormatAvailableModels_ExactlyAtCap(t *testing.T) {
	ms := make([]string, adapters.ModelListCap)
	for i := range ms {
		ms[i] = fmt.Sprintf("m-%d", i)
	}
	results := []adapters.ProviderModels{
		{Provider: "OpenAI", Models: ms},
	}
	out := formatAvailableModels(results)
	// At exactly the cap, all models are listed and there is no truncation notice.
	if !strings.Contains(out, "m-0") {
		t.Errorf("at cap boundary should still list models, got:\n%s", out)
	}
}

func TestFormatAvailableModels_MultipleProviders(t *testing.T) {
	results := []adapters.ProviderModels{
		{Provider: "Anthropic", Models: []string{"claude-sonnet-4-6"}},
		{Provider: "OpenAI", Models: []string{"gpt-4o", "gpt-4o-mini"}},
	}
	out := formatAvailableModels(results)
	if !strings.Contains(out, "Anthropic") {
		t.Errorf("expected Anthropic in output, got:\n%s", out)
	}
	if !strings.Contains(out, "OpenAI") {
		t.Errorf("expected OpenAI in output, got:\n%s", out)
	}
}

// ─── handleModelCommand ──────────────────────────────────────────────────────

func TestHandleModelCommand_BareResetsClearFilter(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"a"}, uiStub{"b"}}
	a := newAppForTest(t, ads)
	// Set a filter first.
	a.activeAdapters = []models.ModelAdapter{uiStub{"a"}}
	result, cmd := a.handleModelCommand("")
	if cmd != nil {
		t.Error("expected nil cmd for bare /model")
	}
	app, ok := result.(App)
	if !ok {
		t.Fatalf("expected App, got %T", result)
	}
	if app.activeAdapters != nil {
		t.Errorf("expected activeAdapters reset to nil, got %v", app.activeAdapters)
	}
	// Message should list all model IDs.
	if len(app.feed) == 0 || (!strings.Contains(app.feed[len(app.feed)-1].text, "a") || !strings.Contains(app.feed[len(app.feed)-1].text, "b")) {
		t.Errorf("expected feed message listing all models")
	}
}

func TestHandleModelCommand_ValidIDSetsFilter(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"x"}, uiStub{"y"}}
	a := newAppForTest(t, ads)
	result, _ := a.handleModelCommand("x")
	app, ok := result.(App)
	if !ok {
		t.Fatalf("expected App, got %T", result)
	}
	if len(app.activeAdapters) != 1 || app.activeAdapters[0].ID() != "x" {
		t.Errorf("expected activeAdapters=[x], got %v", app.activeAdapters)
	}
}

func TestHandleModelCommand_MultipleValidIDs(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"x"}, uiStub{"y"}, uiStub{"z"}}
	a := newAppForTest(t, ads)
	result, _ := a.handleModelCommand("x z")
	app, ok := result.(App)
	if !ok {
		t.Fatalf("expected App, got %T", result)
	}
	if len(app.activeAdapters) != 2 {
		t.Errorf("expected 2 active adapters, got %d", len(app.activeAdapters))
	}
}

func TestHandleModelCommand_UnknownIDShowsError(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"real-model"}}
	a := newAppForTest(t, ads)
	result, cmd := a.handleModelCommand("nonexistent")
	if cmd != nil {
		t.Error("expected nil cmd on error")
	}
	app, ok := result.(App)
	if !ok {
		t.Fatalf("expected App, got %T", result)
	}
	// Active adapters should remain unchanged (nil = all).
	if app.activeAdapters != nil {
		t.Errorf("filter should be unchanged after error, got %v", app.activeAdapters)
	}
	// Feed should contain an error message mentioning the bad ID.
	if len(app.feed) == 0 {
		t.Fatal("expected error message in feed")
	}
	last := app.feed[len(app.feed)-1].text
	if !strings.Contains(last, "nonexistent") {
		t.Errorf("error message should mention bad ID, got: %s", last)
	}
}

func TestHandleModelCommand_UnknownIDListsAvailable(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"real-a"}, uiStub{"real-b"}}
	a := newAppForTest(t, ads)
	result, _ := a.handleModelCommand("bogus")
	app := result.(App)
	if len(app.feed) == 0 {
		t.Fatal("expected message in feed")
	}
	last := app.feed[len(app.feed)-1].text
	if !strings.Contains(last, "real-a") || !strings.Contains(last, "real-b") {
		t.Errorf("error message should list available models, got: %s", last)
	}
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
	res2, _ := res1.(App).historyBack()
	app := res2.(App)
	if app.historyIdx != 1 {
		t.Errorf("expected historyIdx=1, got %d", app.historyIdx)
	}
	if app.input.Value() != "older" {
		t.Errorf("expected input='older', got %q", app.input.Value())
	}
}

func TestHistoryBack_ClampAtOldest(t *testing.T) {
	a := appWithHistory(t, []string{"only"})
	res1, _ := a.historyBack()
	res2, _ := res1.(App).historyBack() // already at oldest — should not move
	app := res2.(App)
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
