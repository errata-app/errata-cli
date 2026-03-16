package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/datastore"
	"github.com/errata-app/errata-cli/internal/models"
	"github.com/errata-app/errata-cli/internal/session"
	"github.com/errata-app/errata-cli/pkg/recipe"
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

// ── Group B: replayFromMetadata ─────────────────────────────────────────────

func TestReplayFromMetadata_NilRuns(t *testing.T) {
	items := replayFromMetadata(nil, nil)
	assert.Empty(t, items)
}

func TestReplayFromMetadata_RunEntry(t *testing.T) {
	runs := []session.RunSummary{
		{
			PromptPreview: "fix bug",
			Note:          "Applied: foo.go",
			Models:        []string{"m1", "m2"},
		},
	}
	content := []session.RunContent{
		{
			Prompt: "fix bug",
			Models: []session.ModelRunContent{
				{ModelID: "m1", Text: "response 1"},
				{ModelID: "m2", Text: "response 2"},
			},
		},
	}
	items := replayFromMetadata(runs, content)

	require.Len(t, items, 1)
	assert.Equal(t, "run", items[0].kind)
	assert.Equal(t, "fix bug", items[0].prompt)
	assert.Equal(t, "Applied: foo.go", items[0].note)
	require.Len(t, items[0].panels, 2)
}

func TestReplayFromMetadata_SkipsRewindEntries(t *testing.T) {
	runs := []session.RunSummary{
		{PromptPreview: "original", Models: []string{"m1"}},
		{Type: "rewind", PromptPreview: "original"},
	}
	content := []session.RunContent{
		{Prompt: "original", Models: []session.ModelRunContent{{ModelID: "m1", Text: "resp"}}},
	}
	items := replayFromMetadata(runs, content)
	require.Len(t, items, 1)
	assert.Equal(t, "original", items[0].prompt)
}

func TestReplayFromMetadata_FallbackToIDs(t *testing.T) {
	// More runs in metadata than content — should fall back to IDs.
	runs := []session.RunSummary{
		{PromptPreview: "test", Models: []string{"m1", "m2"}},
	}
	items := replayFromMetadata(runs, nil)

	require.Len(t, items, 1)
	require.Len(t, items[0].panels, 2)
	assert.Equal(t, "m1", items[0].panels[0].modelID)
	assert.Equal(t, "m2", items[0].panels[1].modelID)
}

// ── Group C: replayPanelsFromContent ────────────────────────────────────────

func TestReplayPanelsFromContent_BasicEntries(t *testing.T) {
	entries := []session.ModelRunContent{
		{ModelID: "m1", Text: "response 1"},
		{ModelID: "m2", Text: "response 2"},
	}
	panels := replayPanelsFromContent(entries)

	require.Len(t, panels, 2)
	assert.Equal(t, "m1", panels[0].modelID)
	assert.Equal(t, "m2", panels[1].modelID)
	assert.True(t, panels[0].done)
	assert.True(t, panels[1].done)
}

func TestReplayPanelsFromContent_TextEmittedAsEvent(t *testing.T) {
	entries := []session.ModelRunContent{
		{ModelID: "m1", Text: "response"},
	}
	panels := replayPanelsFromContent(entries)

	require.Len(t, panels, 1)
	require.Len(t, panels[0].events, 1)
	assert.Equal(t, models.EventText, panels[0].events[0].Type)
}

func TestReplayPanelsFromContent_EmptyTextNoEvent(t *testing.T) {
	entries := []session.ModelRunContent{
		{ModelID: "m1", Text: ""},
	}
	panels := replayPanelsFromContent(entries)

	require.Len(t, panels, 1)
	assert.Empty(t, panels[0].events)
}

func TestReplayPanelsFromContent_NilEntries(t *testing.T) {
	panels := replayPanelsFromContent(nil)
	assert.Empty(t, panels)
}

// ── Group D: replayPanelsFromIDs ────────────────────────────────────────────

func TestReplayPanelsFromIDs_BasicIDs(t *testing.T) {
	panels := replayPanelsFromIDs([]string{"m1", "m2"})
	require.Len(t, panels, 2)
	assert.Equal(t, "m1", panels[0].modelID)
	assert.Equal(t, "m2", panels[1].modelID)
	assert.True(t, panels[0].done)
	assert.True(t, panels[1].done)
}

func TestReplayPanelsFromIDs_NilIDs(t *testing.T) {
	panels := replayPanelsFromIDs(nil)
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

// ── Group J: handleRewindCmd metadata persistence ───────────────────────────

func TestHandleRewindCmd_PersistsAnnotationToMetadata(t *testing.T) {
	a := newAppForTest(t, nil)
	// Simulate a run that was persisted.
	a.store.PersistRunState("fix bug", []models.ModelResponse{
		{ModelID: "m1", Text: "fixed", LatencyMS: 100},
	}, nil, nil)
	// Simulate selection note.
	a.store.UpdateLastRunNote("Applied: foo.go")
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
	// Metadata runs should also reflect annotation.
	meta := app.store.Metadata()
	require.NotEmpty(t, meta.Runs)
	assert.Contains(t, meta.Runs[len(meta.Runs)-2].Note, "[rewound]")
}

func TestHandleRewindCmd_AnnotationSavedToDisk(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.PersistRunState("test", []models.ModelResponse{
		{ModelID: "m1", Text: "resp", LatencyMS: 100},
	}, nil, nil)
	a.store.UpdateLastRunNote("Skipped.")
	a.feed = []feedItem{
		{kind: "run", prompt: "test", note: "Skipped."},
	}
	a.store.PushRewindEntry(datastore.RewindEntry{
		FeedIndex: 0, Prompt: "test", HistoryLengths: map[string]int{},
	})

	a.handleRewindCmd()

	// Verify via disk read.
	meta, err := session.LoadMetadata(a.store.MetadataPath())
	require.NoError(t, err)
	// Original run should be annotated, plus a rewind marker.
	require.GreaterOrEqual(t, len(meta.Runs), 1)
	assert.Contains(t, meta.Runs[0].Note, "[rewound]")
}

func TestHandleRewindCmd_EmptyNoteBecomes_Rewound(t *testing.T) {
	a := newAppForTest(t, nil)
	a.store.PersistRunState("test", []models.ModelResponse{
		{ModelID: "m1", Text: "resp", LatencyMS: 100},
	}, nil, nil)
	a.feed = []feedItem{
		{kind: "run", prompt: "test"},
	}
	a.store.PushRewindEntry(datastore.RewindEntry{
		FeedIndex: 0, Prompt: "test", HistoryLengths: map[string]int{},
	})

	result, _ := a.handleRewindCmd()
	app := result.(App)

	meta := app.store.Metadata()
	// The original run note should contain [rewound].
	require.GreaterOrEqual(t, len(meta.Runs), 1)
	assert.Contains(t, meta.Runs[0].Note, "[rewound]")
}
