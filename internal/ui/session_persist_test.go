package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/datastore"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/recipe"
	"github.com/suarezc/errata/internal/session"
	"github.com/suarezc/errata/internal/tools"
)

// ── Group A: truncateStr ────────────────────────────────────────────────────

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"shorter_than_max", "hello", 10, "hello"},
		{"exact_max", "hello", 5, "hello"},
		{"exceeds_max", "hello world", 5, "hello"},
		{"empty_string", "", 10, ""},
		{"zero_max", "hello", 0, ""},
		{"unicode_runes", "café lait", 5, "café "},
		{"emoji", "😀🎉🔥", 2, "😀🎉"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateStr(tt.s, tt.maxLen)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ── Group B: buildFeedEntry ─────────────────────────────────────────────────

func TestBuildFeedEntry_BasicRun(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID: "m1",
			Text:    "fixed it",
			ProposedWrites: []tools.FileWrite{
				{Path: "foo.go", Content: "package foo"},
			},
		},
		{
			ModelID: "m2",
			Text:    "also fixed",
			ProposedWrites: []tools.FileWrite{
				{Path: "bar.go", Content: "package bar"},
			},
		},
	}
	entry := buildFeedEntry("fix bug", responses)

	assert.Equal(t, "run", entry.Kind)
	assert.Equal(t, "fix bug", entry.Prompt)
	require.Len(t, entry.Models, 2)
	assert.Equal(t, "m1", entry.Models[0].ID)
	assert.Equal(t, "m2", entry.Models[1].ID)
	assert.Equal(t, []string{"foo.go"}, entry.Models[0].ProposedFiles)
	assert.Equal(t, []string{"bar.go"}, entry.Models[1].ProposedFiles)
}

func TestBuildFeedEntry_TextTruncatedAt500(t *testing.T) {
	longText := strings.Repeat("x", 600)
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: longText},
	}
	entry := buildFeedEntry("test", responses)

	require.Len(t, entry.Models, 1)
	assert.Len(t, []rune(entry.Models[0].Text), 500)
}

func TestBuildFeedEntry_NoResponses(t *testing.T) {
	entry := buildFeedEntry("test", nil)

	assert.Equal(t, "run", entry.Kind)
	assert.Equal(t, "test", entry.Prompt)
	assert.Empty(t, entry.Models)
}

func TestBuildFeedEntry_NoProposedWrites(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "just text"},
	}
	entry := buildFeedEntry("test", responses)

	require.Len(t, entry.Models, 1)
	assert.Empty(t, entry.Models[0].ProposedFiles)
}

func TestBuildFeedEntry_MultipleWrites(t *testing.T) {
	responses := []models.ModelResponse{
		{
			ModelID: "m1",
			Text:    "wrote files",
			ProposedWrites: []tools.FileWrite{
				{Path: "a.go"},
				{Path: "b.go"},
				{Path: "c.go"},
			},
		},
	}
	entry := buildFeedEntry("test", responses)

	require.Len(t, entry.Models, 1)
	assert.Equal(t, []string{"a.go", "b.go", "c.go"}, entry.Models[0].ProposedFiles)
}

func TestBuildFeedEntry_EmptyPrompt(t *testing.T) {
	entry := buildFeedEntry("", nil)
	assert.Empty(t, entry.Prompt)
}

// ── Group C: replayFeed ─────────────────────────────────────────────────────

func TestReplayFeed_NilEntries(t *testing.T) {
	items := replayFeed(nil)
	assert.Empty(t, items)
}

func TestReplayFeed_MessageEntry(t *testing.T) {
	entries := []session.FeedEntry{
		{Kind: "message", Text: "hello world"},
	}
	items := replayFeed(entries)

	require.Len(t, items, 1)
	assert.Equal(t, "message", items[0].kind)
	assert.Equal(t, "hello world", items[0].text)
}

func TestReplayFeed_RunEntry(t *testing.T) {
	entries := []session.FeedEntry{
		{
			Kind:   "run",
			Prompt: "fix bug",
			Note:   "Applied: foo.go",
			Models: []session.ModelEntry{
				{ID: "m1", Text: "response 1"},
				{ID: "m2", Text: "response 2"},
			},
		},
	}
	items := replayFeed(entries)

	require.Len(t, items, 1)
	assert.Equal(t, "run", items[0].kind)
	assert.Equal(t, "fix bug", items[0].prompt)
	assert.Equal(t, "Applied: foo.go", items[0].note)
	require.Len(t, items[0].panels, 2)
}

func TestReplayFeed_MixedEntries(t *testing.T) {
	entries := []session.FeedEntry{
		{Kind: "message", Text: "msg1"},
		{Kind: "run", Prompt: "do stuff", Models: []session.ModelEntry{{ID: "m1"}}},
		{Kind: "message", Text: "msg2"},
	}
	items := replayFeed(entries)

	require.Len(t, items, 3)
	assert.Equal(t, "message", items[0].kind)
	assert.Equal(t, "run", items[1].kind)
	assert.Equal(t, "message", items[2].kind)
}

func TestReplayFeed_NewlinesReplacedInSummary(t *testing.T) {
	entries := []session.FeedEntry{
		{
			Kind:   "run",
			Prompt: "test",
			Models: []session.ModelEntry{
				{ID: "m1", Text: "line1\nline2\nline3"},
			},
		},
	}
	items := replayFeed(entries)

	// replayFeed creates panels from models — the text event should not have
	// raw newlines since it's truncated. The summary is built internally and
	// replaces newlines. We verify the panel was created correctly.
	require.Len(t, items, 1)
	require.Len(t, items[0].panels, 1)
	assert.Equal(t, "m1", items[0].panels[0].modelID)
}

func TestReplayFeed_UnknownKindSkipped(t *testing.T) {
	entries := []session.FeedEntry{
		{Kind: "bogus", Text: "should be ignored"},
	}
	items := replayFeed(entries)
	assert.Empty(t, items)
}

// ── Group D: replayPanels ───────────────────────────────────────────────────

func TestReplayPanels_BasicEntries(t *testing.T) {
	entries := []session.ModelEntry{
		{ID: "m1", Text: "response 1"},
		{ID: "m2", Text: "response 2"},
	}
	panels := replayPanels(entries)

	require.Len(t, panels, 2)
	assert.Equal(t, "m1", panels[0].modelID)
	assert.Equal(t, "m2", panels[1].modelID)
	assert.True(t, panels[0].done)
	assert.True(t, panels[1].done)
}

func TestReplayPanels_TextEmittedAsEvent(t *testing.T) {
	entries := []session.ModelEntry{
		{ID: "m1", Text: "response"},
	}
	panels := replayPanels(entries)

	require.Len(t, panels, 1)
	require.Len(t, panels[0].events, 1)
	assert.Equal(t, models.EventText, panels[0].events[0].Type)
}

func TestReplayPanels_EmptyTextNoEvent(t *testing.T) {
	entries := []session.ModelEntry{
		{ID: "m1", Text: ""},
	}
	panels := replayPanels(entries)

	require.Len(t, panels, 1)
	assert.Empty(t, panels[0].events)
}

func TestReplayPanels_NilEntries(t *testing.T) {
	panels := replayPanels(nil)
	assert.Empty(t, panels)
}

// ── Group I: syncToolAllowlist ──────────────────────────────────────────────

func TestSyncToolAllowlist_NilSessionRecipeNoop(t *testing.T) {
	a := newAppForTest(t, nil)
	// sessionRecipe is nil by default in newAppForTest
	assert.NotPanics(t, func() {
		a.syncToolAllowlist()
	})
}

func TestSyncToolAllowlist_BuildsFromActiveItems(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetSessionRecipe(&recipe.Recipe{})
	a.configListItems = []listItem{
		{Label: "read_file", Active: true},
		{Label: "bash", Active: false},
		{Label: "write_file", Active: true},
	}

	a.syncToolAllowlist()

	require.NotNil(t, a.store.SessionRecipe().Tools)
	assert.Equal(t, []string{"read_file", "write_file"}, a.store.SessionRecipe().Tools.Allowlist)
}

func TestSyncToolAllowlist_CreatesToolsConfig(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetSessionRecipe(&recipe.Recipe{}) // Tools is nil
	a.configListItems = []listItem{
		{Label: "bash", Active: true},
	}

	a.syncToolAllowlist()

	require.NotNil(t, a.store.SessionRecipe().Tools)
	assert.Equal(t, []string{"bash"}, a.store.SessionRecipe().Tools.Allowlist)
}

func TestSyncToolAllowlist_SyncsToAppField(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetSessionRecipe(&recipe.Recipe{})
	a.configListItems = []listItem{
		{Label: "read_file", Active: true},
		{Label: "bash", Active: true},
	}

	a.syncToolAllowlist()

	assert.Equal(t, a.store.SessionRecipe().Tools.Allowlist, a.toolAllowlist)
}

func TestSyncToolAllowlist_AllInactiveEmptiesAllowlist(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetSessionRecipe(&recipe.Recipe{})
	a.configListItems = []listItem{
		{Label: "read_file", Active: false},
		{Label: "bash", Active: false},
	}

	a.syncToolAllowlist()

	assert.NotNil(t, a.store.SessionRecipe().Tools.Allowlist, "should be non-nil empty slice (zero tools)")
	assert.Empty(t, a.store.SessionRecipe().Tools.Allowlist)
	assert.NotNil(t, a.toolAllowlist, "should be non-nil empty slice (zero tools)")
	assert.Empty(t, a.toolAllowlist)
}

// ── Group J: handleRewindCmd feed persistence ───────────────────────────────

func TestHandleRewindCmd_PersistsAnnotationToSessionFeed(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "fix bug", Note: "Applied: foo.go"},
	})
	a.feed = []feedItem{
		{kind: "run", prompt: "fix bug", note: "Applied: foo.go"},
	}
	a.store.PushRewindEntry(datastore.RewindEntry{
		FeedIndex: 0, Prompt: "fix bug", HistoryLengths: map[string]int{},
	})

	result, _ := a.handleRewindCmd()
	app := result.(App)

	// Display feed gets annotated.
	assert.Equal(t, "[rewound] Applied: foo.go", app.feed[0].note)
	// Session feed (persisted to disk) must also be updated.
	require.Len(t, app.store.SessionFeed(), 1)
	assert.Equal(t, "[rewound] Applied: foo.go", app.store.SessionFeed()[0].Note)
}

func TestHandleRewindCmd_AnnotationSavedToDisk(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "test", Note: "Skipped."},
	})
	a.feed = []feedItem{
		{kind: "run", prompt: "test", note: "Skipped."},
	}
	a.store.PushRewindEntry(datastore.RewindEntry{
		FeedIndex: 0, Prompt: "test", HistoryLengths: map[string]int{},
	})

	a.handleRewindCmd()

	entries, err := session.LoadFeed(a.store.FeedPath())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "[rewound] Skipped.", entries[0].Note)
}

func TestHandleRewindCmd_EmptyNoteBecomes_Rewound(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.SetSessionFeed([]session.FeedEntry{
		{Kind: "run", Prompt: "test"},
	})
	a.feed = []feedItem{
		{kind: "run", prompt: "test"},
	}
	a.store.PushRewindEntry(datastore.RewindEntry{
		FeedIndex: 0, Prompt: "test", HistoryLengths: map[string]int{},
	})

	result, _ := a.handleRewindCmd()
	app := result.(App)

	assert.Equal(t, "[rewound]", app.store.SessionFeed()[0].Note)
}
