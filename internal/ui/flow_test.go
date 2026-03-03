package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ── test infrastructure ─────────────────────────────────────────────────────

// scenarioAdapter is a configurable mock adapter for TUI flow tests.
// It supports custom responses, emitting events, and returning errors.
type scenarioAdapter struct {
	id       string
	response models.ModelResponse
	events   []models.AgentEvent
	err      error // if non-nil, RunAgent returns this error
}

func (s *scenarioAdapter) ID() string { return s.id }
func (s *scenarioAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s *scenarioAdapter) RunAgent(
	_ context.Context,
	_ []models.ConversationTurn,
	_ string,
	onEvent func(models.AgentEvent),
) (models.ModelResponse, error) {
	for _, e := range s.events {
		onEvent(e)
	}
	if s.err != nil {
		return models.ModelResponse{}, s.err
	}
	resp := s.response
	if resp.ModelID == "" {
		resp.ModelID = s.id
	}
	return resp, nil
}

// keyRunes constructs a tea.KeyPressMsg for printable rune input.
func keyRunes(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// keyType constructs a tea.KeyPressMsg for a special key.
func keyType(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// ctrlKey constructs a tea.KeyPressMsg for a Ctrl+key combo.
func ctrlKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
}

// appFrom extracts the App value from an Update() return.
func appFrom(t *testing.T, m tea.Model) App {
	t.Helper()
	a, ok := m.(App)
	if !ok {
		t.Fatalf("expected App, got %T", m)
	}
	return a
}

// setupRunState simulates the synchronous portion of launchRun:
// sets mode, panels, panelIdx, lastPrompt, and pushes a feed entry.
func setupRunState(a *App, prompt string, adapterIDs []string) {
	a.mode = modeRunning
	a.lastPrompt = prompt
	a.panels = nil
	a.panelIdx = make(map[string]int)
	for i, id := range adapterIDs {
		ps := newPanelState(id, i)
		a.panels = append(a.panels, ps)
		a.panelIdx[id] = i
	}
	a.feed = append(a.feed, feedItem{
		kind:   "run",
		prompt: prompt,
		panels: a.panels,
	})
}

// cwdTempDir creates a temporary directory under the current working directory
// (which is the package dir during `go test`). This is necessary because
// tools.ApplyWrites validates paths against os.Getwd().
func cwdTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "flowtest-*")
	require.NoError(t, err)
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(abs) })
	return abs
}

// lastFeedNote returns the note of the last feed item, or "" if no feed items.
func lastFeedNote(a *App) string {
	if len(a.feed) == 0 {
		return ""
	}
	return a.feed[len(a.feed)-1].note
}

// ── Group A: State Machine Transitions ──────────────────────────────────────

func TestStateMachine_IdleToRunningToSelectingToIdle(t *testing.T) {
	ads := []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
	}
	a := newAppForTest(t, ads)

	// Simulate launchRun setup.
	setupRunState(&a, "test prompt", []string{"m1", "m2"})
	assert.Equal(t, modeRunning, a.mode)

	// Send runCompleteMsg with proposed writes.
	tmpDir := cwdTempDir(t)
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{
			ModelID:   "m1",
			Text:      "I wrote code",
			LatencyMS: 500,
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "x.go"), Content: "package x"},
			},
		},
		{
			ModelID:   "m2",
			Text:      "I also wrote code",
			LatencyMS: 600,
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "y.go"), Content: "package y"},
			},
		},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	assert.Equal(t, modeSelecting, a.mode)
	assert.NotNil(t, a.responses)
	assert.Empty(t, a.selection)

	// Select response 1 (type "1" then Enter).
	result, _ = a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Nil(t, a.responses)
	assert.Contains(t, lastFeedNote(&a), "Applied:")
}

func TestStateMachine_IdleToRunningToRatingToIdle(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "test prompt", []string{"m1"})

	// Single OK text-only response (no writes) → modeRating.
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "here is my answer", LatencyMS: 300},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	assert.Equal(t, modeRating, a.mode)

	// Rate good.
	result, _ = a.handleRatingKey(keyRunes("y"))
	a = appFrom(t, result)
	assert.Equal(t, modeIdle, a.mode)
	assert.Contains(t, lastFeedNote(&a), "Rated good")
}

func TestStateMachine_RunCompleteAllErrors_GoesIdle(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
	})
	setupRunState(&a, "prompt", []string{"m1", "m2"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Error: "failed 1"},
		{ModelID: "m2", Error: "failed 2"},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	assert.Equal(t, modeIdle, a.mode)
}

func TestStateMachine_RunCompleteNoTextNoWrites_GoesIdle(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
	})
	setupRunState(&a, "prompt", []string{"m1", "m2"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: ""},
		{ModelID: "m2", Text: ""},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	assert.Equal(t, modeIdle, a.mode)
}

func TestStateMachine_RunCompleteMultipleTextOnly_GoesSelecting(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
	})
	setupRunState(&a, "prompt", []string{"m1", "m2"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer A", LatencyMS: 100},
		{ModelID: "m2", Text: "answer B", LatencyMS: 200},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	// Two OK text-only responses → modeSelecting (vote mode).
	assert.Equal(t, modeSelecting, a.mode)
}

// ── Group B: Event Streaming ────────────────────────────────────────────────

func TestEventStreaming_AgentEventUpdatesPanels(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	msg := agentEventMsg{modelID: "m1", event: models.AgentEvent{Type: models.EventReading, Data: "foo.go"}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	require.Len(t, a.panels, 1)
	assert.Len(t, a.panels[0].events, 1)
	assert.Equal(t, models.EventReading, a.panels[0].events[0].Type)
	assert.Equal(t, "foo.go", a.panels[0].events[0].Data)
	assert.Equal(t, 1, a.panels[0].toolUseCount)
}

func TestEventStreaming_MultiplePanelsIndependent(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
	})
	setupRunState(&a, "prompt", []string{"m1", "m2"})

	// Send events to different panels.
	result, _ := a.Update(agentEventMsg{modelID: "m1", event: models.AgentEvent{Type: models.EventReading, Data: "a.go"}})
	a = appFrom(t, result)
	result, _ = a.Update(agentEventMsg{modelID: "m2", event: models.AgentEvent{Type: models.EventWriting, Data: "b.go"}})
	a = appFrom(t, result)
	result, _ = a.Update(agentEventMsg{modelID: "m1", event: models.AgentEvent{Type: models.EventBash, Data: "go test"}})
	a = appFrom(t, result)

	assert.Len(t, a.panels[0].events, 2) // m1 got 2 events
	assert.Len(t, a.panels[1].events, 1) // m2 got 1 event
	assert.Equal(t, 2, a.panels[0].toolUseCount)
	assert.Equal(t, 1, a.panels[1].toolUseCount)
}

func TestEventStreaming_UnknownModelIDIgnored(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	// Should not panic.
	result, _ := a.Update(agentEventMsg{modelID: "unknown", event: models.AgentEvent{Type: models.EventReading, Data: "x"}})
	a = appFrom(t, result)

	// m1 panel should be unaffected.
	assert.Empty(t, a.panels[0].events)
}

// ── Group C: Selection Flow ─────────────────────────────────────────────────

func selectionApp(t *testing.T, responses []models.ModelResponse) App {
	t.Helper()
	var ads []models.ModelAdapter
	ids := make([]string, len(responses))
	for i, r := range responses {
		ads = append(ads, &scenarioAdapter{id: r.ModelID})
		ids[i] = r.ModelID
	}
	a := newAppForTest(t, ads)
	setupRunState(&a, "test prompt", ids)
	a.mode = modeSelecting
	a.responses = responses
	a.selection = ""
	a.selectionErr = ""
	return a
}

func TestSelection_NumericChoiceAppliesWrites(t *testing.T) {
	tmpDir := cwdTempDir(t)
	responses := []models.ModelResponse{
		{
			ModelID: "m1",
			Text:    "wrote it",
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "out.go"), Content: "package out"},
			},
		},
	}
	a := selectionApp(t, responses)

	// Type "1" then Enter.
	result, _ := a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Contains(t, lastFeedNote(&a), "Applied:")
	assert.Contains(t, lastFeedNote(&a), "out.go")
}

func TestSelection_SkipSetsNote(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "answer"},
	}
	a := selectionApp(t, responses)

	result, _ := a.handleSelectKey(keyRunes("s"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Equal(t, "Skipped.", lastFeedNote(&a))
}

func TestSelection_InvalidInputShowsError(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "answer"},
	}
	a := selectionApp(t, responses)

	result, _ := a.handleSelectKey(keyRunes("x"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeSelecting, a.mode) // stays in selecting
	assert.NotEmpty(t, a.selectionErr)
	assert.Contains(t, a.selectionErr, "x")
}

func TestSelection_OutOfRangeNumber(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "only one"},
	}
	a := selectionApp(t, responses)

	result, _ := a.handleSelectKey(keyRunes("9"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeSelecting, a.mode)
	assert.NotEmpty(t, a.selectionErr)
	assert.Contains(t, a.selectionErr, "9")
}

func TestSelection_BackspaceEditsSelection(t *testing.T) {
	tmpDir := cwdTempDir(t)
	responses := []models.ModelResponse{
		{
			ModelID: "m1",
			Text:    "first",
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "f1.go"), Content: "package f1"},
			},
		},
		{
			ModelID: "m2",
			Text:    "second",
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "f2.go"), Content: "package f2"},
			},
		},
	}
	a := selectionApp(t, responses)

	// Type "12", backspace to "1", then Enter → selects response 1.
	result, _ := a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyRunes("2"))
	a = appFrom(t, result)
	assert.Equal(t, "12", a.selection)

	result, _ = a.handleSelectKey(keyType(tea.KeyBackspace))
	a = appFrom(t, result)
	assert.Equal(t, "1", a.selection)

	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Contains(t, lastFeedNote(&a), "Applied:")
	assert.Contains(t, lastFeedNote(&a), "f1.go")
}

func TestSelection_VoteWhenNoWrites(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "answer A"},
		{ModelID: "m2", Text: "answer B"},
	}
	a := selectionApp(t, responses)

	result, _ := a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Contains(t, lastFeedNote(&a), "Voted for:")
}

// ── Group D: Rating Flow ────────────────────────────────────────────────────

func ratingApp(t *testing.T, resp models.ModelResponse) App {
	t.Helper()
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: resp.ModelID}})
	setupRunState(&a, "test prompt", []string{resp.ModelID})
	a.mode = modeRating
	a.responses = []models.ModelResponse{resp}
	return a
}

func TestRating_YRecordsGood(t *testing.T) {
	a := ratingApp(t, models.ModelResponse{ModelID: "m1", Text: "good answer"})
	result, _ := a.handleRatingKey(keyRunes("y"))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Nil(t, a.responses)
	assert.Contains(t, lastFeedNote(&a), "Rated good")
	assert.Contains(t, lastFeedNote(&a), "m1")
}

func TestRating_NRecordsBad(t *testing.T) {
	a := ratingApp(t, models.ModelResponse{ModelID: "m1", Text: "bad answer"})
	result, _ := a.handleRatingKey(keyRunes("n"))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Nil(t, a.responses)
	assert.Contains(t, lastFeedNote(&a), "Rated bad")
	assert.Contains(t, lastFeedNote(&a), "m1")
}

func TestRating_SSkips(t *testing.T) {
	a := ratingApp(t, models.ModelResponse{ModelID: "m1", Text: "meh"})
	result, _ := a.handleRatingKey(keyRunes("s"))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Nil(t, a.responses)
	assert.Equal(t, "Skipped.", lastFeedNote(&a))
}

func TestRating_CtrlDQuits(t *testing.T) {
	a := ratingApp(t, models.ModelResponse{ModelID: "m1", Text: "answer"})
	_, cmd := a.handleRatingKey(ctrlKey('d'))
	// tea.Quit is a function that returns tea.QuitMsg{}.
	require.NotNil(t, cmd)
	quitMsg := cmd()
	_, isQuit := quitMsg.(tea.QuitMsg)
	assert.True(t, isQuit, "expected tea.QuitMsg, got %T", quitMsg)
}

// ── Group E: runCompleteMsg Handling ────────────────────────────────────────

func TestRunComplete_PanelStatsUpdated(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{
			ModelID:      "m1",
			Text:         "done",
			LatencyMS:    1234,
			InputTokens:  5000,
			OutputTokens: 2000,
			CostUSD:      0.0042,
		},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	require.Len(t, a.panels, 1)
	p := a.panels[0]
	assert.True(t, p.done)
	assert.Equal(t, int64(1234), p.latencyMS)
	assert.Equal(t, int64(5000), p.inputTokens)
	assert.Equal(t, int64(2000), p.outputTokens)
	assert.InDelta(t, 0.0042, p.costUSD, 0.0001)
}

func TestRunComplete_CostAccumulation(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
	})

	// First run.
	setupRunState(&a, "prompt1", []string{"m1", "m2"})
	msg1 := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "a", LatencyMS: 100, CostUSD: 0.01},
		{ModelID: "m2", Text: "b", LatencyMS: 200, CostUSD: 0.02},
	}}
	result, _ := a.Update(msg1)
	a = appFrom(t, result)

	assert.InDelta(t, 0.03, a.store.TotalCost(), 0.001)
	assert.InDelta(t, 0.01, a.store.CostPerModel()["m1"], 0.001)
	assert.InDelta(t, 0.02, a.store.CostPerModel()["m2"], 0.001)

	// Second run (only m1).
	// Reset mode for the second run.
	a.mode = modeIdle
	setupRunState(&a, "prompt2", []string{"m1"})
	msg2 := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "c", LatencyMS: 150, CostUSD: 0.005},
	}}
	result, _ = a.Update(msg2)
	a = appFrom(t, result)

	assert.InDelta(t, 0.035, a.store.TotalCost(), 0.001)
	assert.InDelta(t, 0.015, a.store.CostPerModel()["m1"], 0.001)
	assert.InDelta(t, 0.02, a.store.CostPerModel()["m2"], 0.001)
}

func TestRunComplete_ContextOverflowReplacesError(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Error: "context_length_exceeded: too many tokens"},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	require.Len(t, a.panels, 1)
	assert.Contains(t, a.panels[0].errMsg, "context limit reached")
	assert.Contains(t, a.panels[0].errMsg, "/compact")
}

func TestRunComplete_HistoryUpdatedAfterRun(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "what is Go?", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "Go is a programming language.", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	require.NotNil(t, a.store.Histories())
	h := a.store.Histories()["m1"]
	require.Len(t, h, 2)
	assert.Equal(t, "user", h[0].Role)
	assert.Equal(t, "what is Go?", h[0].Content)
	assert.Equal(t, "assistant", h[1].Role)
	assert.Equal(t, "Go is a programming language.", h[1].Content)
}

func TestRunComplete_CompactedHistoriesApplied(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	// Pre-populate history.
	a.store.SetHistories(map[string][]models.ConversationTurn{
		"m1": {
			{Role: "user", Content: "old question"},
			{Role: "assistant", Content: "old answer"},
		},
	})
	setupRunState(&a, "new question", []string{"m1"})

	// runCompleteMsg carries compacted histories (simulating auto-compact).
	compacted := map[string][]models.ConversationTurn{
		"m1": {
			{Role: "user", Content: "[Previous conversation -- compacted]"},
			{Role: "assistant", Content: "Summary of prior talk."},
		},
	}
	msg := runCompleteMsg{
		responses: []models.ModelResponse{
			{ModelID: "m1", Text: "answer to new question", LatencyMS: 100},
		},
		compactedHistories: compacted,
	}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	// The compacted history should have replaced the original,
	// then AppendHistory added the new turn pair.
	h := a.store.Histories()["m1"]
	require.Len(t, h, 4)
	assert.Contains(t, h[0].Content, "compacted")
	assert.Equal(t, "new question", h[2].Content)
	assert.Equal(t, "answer to new question", h[3].Content)
}

func TestRunComplete_ResponsesSortedByLatency(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
		&scenarioAdapter{id: "m3"},
	})
	setupRunState(&a, "prompt", []string{"m1", "m2", "m3"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "slow", LatencyMS: 900, ProposedWrites: []tools.FileWrite{{Path: "/tmp/a", Content: "a"}}},
		{ModelID: "m2", Text: "fast", LatencyMS: 100, ProposedWrites: []tools.FileWrite{{Path: "/tmp/b", Content: "b"}}},
		{ModelID: "m3", Text: "mid", LatencyMS: 500, ProposedWrites: []tools.FileWrite{{Path: "/tmp/c", Content: "c"}}},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	require.Len(t, a.feed, 1)
	feedResponses := a.feed[0].responses
	require.Len(t, feedResponses, 3)
	assert.Equal(t, int64(100), feedResponses[0].LatencyMS)
	assert.Equal(t, int64(500), feedResponses[1].LatencyMS)
	assert.Equal(t, int64(900), feedResponses[2].LatencyMS)
}

// ── Group F: Feed Accumulation ──────────────────────────────────────────────

func TestFeed_MultipleRunsAccumulate(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})

	// First run.
	setupRunState(&a, "prompt 1", []string{"m1"})
	result, _ := a.Update(runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer 1", LatencyMS: 100},
	}})
	a = appFrom(t, result)
	// Transition back to idle for second run.
	a.mode = modeIdle

	// Second run.
	setupRunState(&a, "prompt 2", []string{"m1"})
	result, _ = a.Update(runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer 2", LatencyMS: 200},
	}})
	a = appFrom(t, result)

	runCount := 0
	for _, item := range a.feed {
		if item.kind == "run" {
			runCount++
		}
	}
	assert.Equal(t, 2, runCount)
}

func TestFeed_NoteSetAfterSelection(t *testing.T) {
	tmpDir := cwdTempDir(t)
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	// Complete the run with writes.
	result, _ := a.Update(runCompleteMsg{responses: []models.ModelResponse{
		{
			ModelID: "m1",
			Text:    "done",
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "out.go"), Content: "package out"},
			},
		},
	}})
	a = appFrom(t, result)
	assert.Equal(t, modeSelecting, a.mode)
	assert.Empty(t, lastFeedNote(&a))

	// Skip selection.
	result, _ = a.handleSelectKey(keyRunes("s"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, "Skipped.", lastFeedNote(&a))
}

// ── Group G: Input Handling ─────────────────────────────────────────────────

func TestInput_EnterWithEmptyPromptDoesNothing(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	a.mode = modeIdle
	a.input.SetValue("   ") // whitespace-only

	result, cmd := a.handleIdleKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Nil(t, cmd)
	assert.Empty(t, a.feed)
}

func TestInput_CtrlDQuitsFromIdle(t *testing.T) {
	a := newAppForTest(t, nil)
	a.mode = modeIdle

	_, cmd := a.handleIdleKey(ctrlKey('d'))
	require.NotNil(t, cmd)
	quitMsg := cmd()
	_, isQuit := quitMsg.(tea.QuitMsg)
	assert.True(t, isQuit)
}

func TestInput_CtrlREntersSearchMode(t *testing.T) {
	a := newAppForTest(t, nil)
	a.mode = modeIdle
	a.store.RecordPrompt("add feature")
	a.store.RecordPrompt("fix bug")

	result, _ := a.handleIdleKey(ctrlKey('r'))
	a = appFrom(t, result)

	assert.True(t, a.searchActive)
	assert.Empty(t, a.searchQuery)
}

func TestInput_EscExitsSearchMode(t *testing.T) {
	a := newAppForTest(t, nil)
	a.searchActive = true
	a.searchQuery = "fix"

	result, _ := a.handleSearchKey(keyType(tea.KeyEscape))
	a = appFrom(t, result)

	assert.False(t, a.searchActive)
	assert.Empty(t, a.searchQuery)
}

func TestInput_RunningModeIgnoresKeys(t *testing.T) {
	a := newAppForTest(t, nil)
	a.mode = modeRunning

	// Various keys should be ignored.
	keys := []tea.KeyPressMsg{
		keyRunes("x"),
		keyType(tea.KeyEnter),
		keyType(tea.KeyTab),
	}
	for _, k := range keys {
		result, cmd := a.Update(k)
		a = appFrom(t, result)
		assert.Equal(t, modeRunning, a.mode, "key %v should not change mode", k)
		assert.Nil(t, cmd, "key %v should not produce cmd", k)
	}
}

// ── Group H: launchRun Setup ────────────────────────────────────────────────

func TestLaunchRun_SetsModeRunning(t *testing.T) {
	ads := []models.ModelAdapter{&scenarioAdapter{id: "m1"}}
	a := newAppForTest(t, ads)
	a.mode = modeIdle

	result, _ := a.launchRun("test prompt")
	app := appFrom(t, result)

	assert.Equal(t, modeRunning, app.mode)
}

func TestLaunchRun_CreatesPanelsForAdapters(t *testing.T) {
	ads := []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
	}
	a := newAppForTest(t, ads)

	result, _ := a.launchRun("test prompt")
	app := appFrom(t, result)

	assert.Len(t, app.panels, 2)
	assert.Equal(t, "m1", app.panels[0].modelID)
	assert.Equal(t, "m2", app.panels[1].modelID)
	assert.Equal(t, 0, app.panelIdx["m1"])
	assert.Equal(t, 1, app.panelIdx["m2"])
}

func TestLaunchRun_PushesFeedItem(t *testing.T) {
	ads := []models.ModelAdapter{&scenarioAdapter{id: "m1"}}
	a := newAppForTest(t, ads)

	result, _ := a.launchRun("hello world")
	app := appFrom(t, result)

	require.NotEmpty(t, app.feed)
	last := app.feed[len(app.feed)-1]
	assert.Equal(t, "run", last.kind)
	assert.Equal(t, "hello world", last.prompt)
	assert.Len(t, last.panels, 1)
}

func TestLaunchRun_RecordsPromptHistory(t *testing.T) {
	ads := []models.ModelAdapter{&scenarioAdapter{id: "m1"}}
	a := newAppForTest(t, ads)

	result, _ := a.launchRun("my prompt")
	app := appFrom(t, result)

	require.Len(t, app.store.PromptHistory(), 1)
	assert.Equal(t, "my prompt", app.store.PromptHistory()[0])
}

func TestLaunchRun_UsesActiveAdapters(t *testing.T) {
	ads := []models.ModelAdapter{
		&scenarioAdapter{id: "m1"},
		&scenarioAdapter{id: "m2"},
		&scenarioAdapter{id: "m3"},
	}
	a := newAppForTest(t, ads)
	// Restrict to m2 only.
	a.activeAdapters = []models.ModelAdapter{&scenarioAdapter{id: "m2"}}

	result, _ := a.launchRun("test")
	app := appFrom(t, result)

	assert.Len(t, app.panels, 1)
	assert.Equal(t, "m2", app.panels[0].modelID)
}

func TestLaunchRun_DeduplicatesPromptHistory(t *testing.T) {
	ads := []models.ModelAdapter{&scenarioAdapter{id: "m1"}}
	a := newAppForTest(t, ads)
	a.store.RecordPrompt("same prompt")

	result, _ := a.launchRun("same prompt")
	app := appFrom(t, result)

	// Should not add a duplicate at the front.
	assert.Len(t, app.store.PromptHistory(), 1)
}

// ── Group I: Misc Edge Cases ────────────────────────────────────────────────

func TestSelection_ErrorResponseSkippedInNumbering(t *testing.T) {
	tmpDir := cwdTempDir(t)
	responses := []models.ModelResponse{
		{ModelID: "m1", Error: "failed"},
		{
			ModelID: "m2",
			Text:    "good answer",
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "out.go"), Content: "package out"},
			},
		},
	}
	a := selectionApp(t, responses)

	// "1" should select m2 (the first OK response), since errors are unlisted.
	result, _ := a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	assert.Equal(t, modeIdle, a.mode)
	assert.Contains(t, lastFeedNote(&a), "Applied:")
	assert.Contains(t, lastFeedNote(&a), "out.go")
}

func TestSelection_CtrlDQuitsFromSelecting(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "answer"},
	}
	a := selectionApp(t, responses)

	_, cmd := a.handleSelectKey(ctrlKey('d'))
	require.NotNil(t, cmd)
	quitMsg := cmd()
	_, isQuit := quitMsg.(tea.QuitMsg)
	assert.True(t, isQuit)
}

func TestRunComplete_ErrorResponseDoesNotUpdateHistory(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "question", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Error: "something broke"},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	// Error responses should not add history turns.
	if h, ok := a.store.Histories()["m1"]; ok {
		assert.Empty(t, h)
	}
}

func TestWindowSizeMsg_UpdatesDimensions(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 80
	a.height = 24

	result, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a = appFrom(t, result)

	assert.Equal(t, 120, a.width)
	assert.Equal(t, 40, a.height)
}

func TestCompactCompleteMsg_UpdatesHistories(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetHistories(map[string][]models.ConversationTurn{
		"m1": {
			{Role: "user", Content: "old q"},
			{Role: "assistant", Content: "old a"},
		},
	})

	compacted := map[string][]models.ConversationTurn{
		"m1": {
			{Role: "user", Content: "[Previous conversation -- compacted]"},
			{Role: "assistant", Content: "Summary."},
		},
	}
	result, _ := a.Update(compactCompleteMsg{histories: compacted})
	a = appFrom(t, result)

	require.Len(t, a.store.Histories()["m1"], 2)
	assert.Contains(t, a.store.Histories()["m1"][0].Content, "compacted")

	// Feed should contain the compaction message.
	found := false
	for _, item := range a.feed {
		if item.kind == "message" && item.text == "History compacted." {
			found = true
		}
	}
	assert.True(t, found, "expected 'History compacted.' in feed")
}

// ── Group: Judge UX — rating prompt wording ─────────────────────────────────

func TestRatingView_ShowsModelName(t *testing.T) {
	ads := []models.ModelAdapter{&scenarioAdapter{id: "claude-sonnet-4-6"}}
	a := newAppForTest(t, ads)
	setupRunState(&a, "test prompt", []string{"claude-sonnet-4-6"})

	// Simulate runCompleteMsg with a single OK text response (no writes).
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "claude-sonnet-4-6", Text: "Here is my answer.", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	require.Equal(t, modeRating, a.mode)

	view := a.View().Content
	assert.Contains(t, view, "claude-sonnet-4-6's response:")
	assert.NotContains(t, view, "Rate this response:")
}

func TestRatingView_FallbackWhenNoOKResponse(t *testing.T) {
	ads := []models.ModelAdapter{&scenarioAdapter{id: "m1"}}
	a := newAppForTest(t, ads)
	// Manually set mode and responses with no OK responses (defensive case).
	a.mode = modeRating
	a.responses = []models.ModelResponse{
		{ModelID: "m1", Error: "failed"},
	}

	view := a.View().Content
	// Falls back to generic "this" when no OK response found.
	assert.Contains(t, view, "this's response:")
}

func TestSelectionMenu_UsesComparisonWording(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "answer 1", LatencyMS: 100},
		{ModelID: "m2", Text: "answer 2", LatencyMS: 200},
	}
	menu := RenderSelectionMenu(responses)
	assert.Contains(t, menu, "Vote for a response:")
	assert.NotContains(t, menu, "Rate")
}

// ── Group: /rewind ──────────────────────────────────────────────────────────

func TestRewind_EmptyStack(t *testing.T) {
	a := newAppForTest(t, nil)
	result, cmd := a.handleRewindCmd()
	a = appFrom(t, result)
	assert.NotNil(t, cmd, "withMessage returns a tea.Println cmd")
	// Should show "Nothing to rewind." in the feed.
	found := false
	for _, item := range a.feed {
		if item.kind == "message" && item.text == "Nothing to rewind." {
			found = true
		}
	}
	assert.True(t, found, "expected 'Nothing to rewind.' in feed")
}

func TestRewind_RevertsConversationHistory(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "question", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	// History should have 2 turns (user + assistant).
	require.Len(t, a.store.Histories()["m1"], 2)
	assert.True(t, a.store.CanRewind())

	// Rewind.
	result, _ = a.handleRewindCmd()
	a = appFrom(t, result)

	// History should be empty.
	assert.Empty(t, a.store.Histories()["m1"])
	assert.False(t, a.store.CanRewind())
}

func TestRewind_AnnotatesFeedItem(t *testing.T) {
	tmpDir := cwdTempDir(t)
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{
			ModelID:   "m1",
			Text:      "done",
			LatencyMS: 100,
			ProposedWrites: []tools.FileWrite{
				{Path: filepath.Join(tmpDir, "f.go"), Content: "package f"},
			},
		},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	assert.Equal(t, modeSelecting, a.mode)

	// Select response 1.
	result, _ = a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)
	assert.Contains(t, lastFeedNote(&a), "Applied:")

	// Rewind.
	result, _ = a.handleRewindCmd()
	a = appFrom(t, result)

	// The run feed item should be annotated.
	var runNote string
	for _, item := range a.feed {
		if item.kind == "run" {
			runNote = item.note
		}
	}
	assert.Contains(t, runNote, "[rewound]")
	assert.Contains(t, runNote, "Applied:")
}

func TestRewind_RestoresFiles(t *testing.T) {
	tmpDir := cwdTempDir(t)
	origPath := filepath.Join(tmpDir, "existing.go")
	require.NoError(t, os.WriteFile(origPath, []byte("original"), 0o644))

	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{
			ModelID:   "m1",
			Text:      "done",
			LatencyMS: 100,
			ProposedWrites: []tools.FileWrite{
				{Path: origPath, Content: "changed"},
			},
		},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	// Apply writes.
	result, _ = a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	// Verify file was written.
	got, _ := os.ReadFile(origPath)
	assert.Equal(t, "changed", string(got))

	// Rewind.
	result, _ = a.handleRewindCmd()
	a = appFrom(t, result)

	// File should be restored.
	got, err := os.ReadFile(origPath)
	require.NoError(t, err)
	assert.Equal(t, "original", string(got))
}

func TestRewind_DeletesNewlyCreatedFiles(t *testing.T) {
	tmpDir := cwdTempDir(t)
	newPath := filepath.Join(tmpDir, "new.go")

	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{
			ModelID:   "m1",
			Text:      "done",
			LatencyMS: 100,
			ProposedWrites: []tools.FileWrite{
				{Path: newPath, Content: "package new"},
			},
		},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)

	result, _ = a.handleSelectKey(keyRunes("1"))
	a = appFrom(t, result)
	result, _ = a.handleSelectKey(keyType(tea.KeyEnter))
	a = appFrom(t, result)

	// Verify file was created.
	_, statErr := os.Stat(newPath)
	require.NoError(t, statErr)

	// Rewind.
	result, _ = a.handleRewindCmd()
	a = appFrom(t, result)

	// File should be gone.
	_, statErr = os.Stat(newPath)
	assert.True(t, os.IsNotExist(statErr), "newly created file should be removed on rewind")
}

func TestRewind_MultipleRewinds(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})

	// First run.
	setupRunState(&a, "prompt1", []string{"m1"})
	msg1 := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer1", LatencyMS: 100},
	}}
	result, _ := a.Update(msg1)
	a = appFrom(t, result)
	a.mode = modeIdle

	// Second run.
	setupRunState(&a, "prompt2", []string{"m1"})
	msg2 := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer2", LatencyMS: 200},
	}}
	result, _ = a.Update(msg2)
	a = appFrom(t, result)
	a.mode = modeIdle

	require.Len(t, a.store.Histories()["m1"], 4) // 2 turns per run
	assert.Equal(t, 2, a.store.RewindStackLen())

	// First rewind — removes second run.
	result, _ = a.handleRewindCmd()
	a = appFrom(t, result)
	assert.Len(t, a.store.Histories()["m1"], 2)
	assert.Equal(t, 1, a.store.RewindStackLen())

	// Second rewind — removes first run.
	result, _ = a.handleRewindCmd()
	a = appFrom(t, result)
	assert.Empty(t, a.store.Histories()["m1"])
	assert.False(t, a.store.CanRewind())
}

func TestRewind_TextOnlyRun(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "question", []string{"m1"})

	// Single text-only response (no writes).
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "just text", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	// Goes to modeRating; skip it.
	result, _ = a.handleRatingKey(keyRunes("s"))
	a = appFrom(t, result)

	require.Len(t, a.store.Histories()["m1"], 2)
	assert.True(t, a.store.CanRewind())

	// Rewind — no files, just history.
	result, _ = a.handleRewindCmd()
	a = appFrom(t, result)
	assert.Empty(t, a.store.Histories()["m1"])
	assert.False(t, a.store.CanRewind())
}

func TestRewind_ClearClearsStack(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "question", []string{"m1"})
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	a.mode = modeIdle
	assert.True(t, a.store.CanRewind())

	result, _ = a.handleClearCmd()
	a = appFrom(t, result)
	assert.False(t, a.store.CanRewind())
}

func TestRewind_WipeClearsStack(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a, "question", []string{"m1"})
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	a.mode = modeIdle
	assert.True(t, a.store.CanRewind())

	result, _ = a.handleWipeCmd()
	a = appFrom(t, result)
	assert.False(t, a.store.CanRewind())
}

// ── Group: Ctrl+O expand/collapse in View ───────────────────────────────────

func TestCtrlO_ToggleVisibleInView(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	a.width = 80
	a.height = 40
	setupRunState(&a, "test prompt", []string{"m1"})

	// Add an event so we can verify it shows up in expanded view.
	a.panels[0].addEvent(models.AgentEvent{Type: models.EventReading, Data: "foo.go"})

	// Complete the run with a text-only response → modeRating.
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "here is my answer", LatencyMS: 300},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	require.Equal(t, modeRating, a.mode)
	require.True(t, a.lastRunInView)

	// Default (expanded): panels show events, no "ctrl+o to expand" hint.
	view := a.View().Content
	assert.NotContains(t, view, "ctrl+o to expand")
	assert.Contains(t, view, "foo.go", "expanded panels should show tool events")

	// Press Ctrl+O to collapse.
	result, _ = a.handleRatingKey(ctrlKey('o'))
	a = appFrom(t, result)
	view = a.View().Content
	assert.Contains(t, view, "ctrl+o to expand", "collapsed panels should show expand hint")
	assert.NotContains(t, view, "foo.go", "collapsed panels should hide tool events")

	// Press Ctrl+O again to expand.
	result, _ = a.handleRatingKey(ctrlKey('o'))
	a = appFrom(t, result)
	view = a.View().Content
	assert.NotContains(t, view, "ctrl+o to expand", "re-expanded panels should hide hint")
	assert.Contains(t, view, "foo.go", "re-expanded panels should show tool events")
}

func TestLastRunInView_FlushedOnNewRun(t *testing.T) {
	ads := []models.ModelAdapter{&scenarioAdapter{id: "m1"}}
	a := newAppForTest(t, ads)
	setupRunState(&a,"first prompt", []string{"m1"})

	// Complete the first run.
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	a.mode = modeIdle
	assert.True(t, a.lastRunInView)

	// Launch a new run — should flush previous run to scrollback.
	result, _ = a.launchRun("second prompt")
	a = appFrom(t, result)
	assert.False(t, a.lastRunInView, "launchRun should flush last run to scrollback")
	assert.Equal(t, modeRunning, a.mode)
}

func TestLastRunInView_ClearedBySlashClear(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a,"prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	a.mode = modeIdle
	assert.True(t, a.lastRunInView)

	result, _ = a.handleClearCmd()
	a = appFrom(t, result)
	assert.False(t, a.lastRunInView, "/clear should reset lastRunInView")
}

func TestLastRunInView_ClearedBySlashWipe(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	setupRunState(&a,"prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	a.mode = modeIdle
	assert.True(t, a.lastRunInView)

	result, _ = a.handleWipeCmd()
	a = appFrom(t, result)
	assert.False(t, a.lastRunInView, "/wipe should reset lastRunInView")
}

func TestLastRunInView_SetOnRunComplete(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	assert.False(t, a.lastRunInView, "should start false")

	setupRunState(&a,"prompt", []string{"m1"})
	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	assert.True(t, a.lastRunInView, "runCompleteMsg should set lastRunInView=true")
}

func TestLastRunInView_FlushedByWithMessage(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{&scenarioAdapter{id: "m1"}})
	a.width = 80
	setupRunState(&a,"prompt", []string{"m1"})

	msg := runCompleteMsg{responses: []models.ModelResponse{
		{ModelID: "m1", Text: "answer", LatencyMS: 100},
	}}
	result, _ := a.Update(msg)
	a = appFrom(t, result)
	a.mode = modeIdle
	assert.True(t, a.lastRunInView)

	// withMessage should flush the last run first.
	flushed, cmd := a.withMessage("some message")
	assert.False(t, flushed.lastRunInView, "withMessage should flush last run")
	assert.NotNil(t, cmd, "should return a cmd")
}
