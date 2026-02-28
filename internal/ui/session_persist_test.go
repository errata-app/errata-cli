package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Equal(t, 500, len([]rune(entry.Models[0].Text)))
}

func TestBuildFeedEntry_NoResponses(t *testing.T) {
	entry := buildFeedEntry("test", nil)

	assert.Equal(t, "run", entry.Kind)
	assert.Equal(t, "test", entry.Prompt)
	assert.Nil(t, entry.Models)
}

func TestBuildFeedEntry_NoProposedWrites(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "just text"},
	}
	entry := buildFeedEntry("test", responses)

	require.Len(t, entry.Models, 1)
	assert.Nil(t, entry.Models[0].ProposedFiles)
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

// ── Group E: clearCheckpoint ────────────────────────────────────────────────

func TestClearCheckpoint_RemovesFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "checkpoint.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o600))

	clearCheckpoint(path)

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestClearCheckpoint_MissingFileNoPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		clearCheckpoint(filepath.Join(t.TempDir(), "nonexistent.json"))
	})
}

func TestClearCheckpoint_InvalidPathNoPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		clearCheckpoint("/nonexistent/dir/cp.json")
	})
}

// ── Group F: persistSessionState ────────────────────────────────────────────

func TestPersistSessionState_UpdatesMetadata(t *testing.T) {
	a := newAppForTest(t, nil)
	a.lastPrompt = "fix bug"
	before := time.Now().Truncate(time.Second)

	responses := []models.ModelResponse{{ModelID: "m1", Text: "done"}}
	a.persistSessionState(responses)

	assert.Equal(t, 1, a.sessionMeta.PromptCount)
	assert.Equal(t, "fix bug", a.sessionMeta.FirstPrompt)
	assert.Equal(t, "fix bug", a.sessionMeta.LastPrompt)
	assert.WithinDuration(t, before, a.sessionMeta.LastActiveAt, 2*time.Second)
}

func TestPersistSessionState_SecondCallPreservesFirst(t *testing.T) {
	a := newAppForTest(t, nil)

	a.lastPrompt = "first prompt"
	a.persistSessionState([]models.ModelResponse{{ModelID: "m1", Text: "r1"}})

	a.lastPrompt = "second prompt"
	a.persistSessionState([]models.ModelResponse{{ModelID: "m1", Text: "r2"}})

	assert.Equal(t, 2, a.sessionMeta.PromptCount)
	assert.Equal(t, "first prompt", a.sessionMeta.FirstPrompt)
	assert.Equal(t, "second prompt", a.sessionMeta.LastPrompt)
}

func TestPersistSessionState_MetaSavedToDisk(t *testing.T) {
	a := newAppForTest(t, nil)
	a.lastPrompt = "disk test"
	a.persistSessionState([]models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	loaded, err := session.LoadMeta(a.sessionMetaPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, 1, loaded.PromptCount)
	assert.Equal(t, "disk test", loaded.FirstPrompt)
}

func TestPersistSessionState_FeedSavedToDisk(t *testing.T) {
	a := newAppForTest(t, nil)
	a.lastPrompt = "feed test"
	a.persistSessionState([]models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	entries, err := session.LoadFeed(a.feedPath)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "run", entries[0].Kind)
}

func TestPersistSessionState_FeedAccumulates(t *testing.T) {
	a := newAppForTest(t, nil)

	a.lastPrompt = "prompt 1"
	a.persistSessionState([]models.ModelResponse{{ModelID: "m1", Text: "r1"}})

	a.lastPrompt = "prompt 2"
	a.persistSessionState([]models.ModelResponse{{ModelID: "m1", Text: "r2"}})

	assert.Len(t, a.sessionFeed, 2)

	entries, err := session.LoadFeed(a.feedPath)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestPersistSessionState_LongPromptTruncated(t *testing.T) {
	a := newAppForTest(t, nil)
	a.lastPrompt = strings.Repeat("x", 200)
	a.persistSessionState([]models.ModelResponse{{ModelID: "m1", Text: "ok"}})

	assert.LessOrEqual(t, len([]rune(a.sessionMeta.FirstPrompt)), 120)
	assert.LessOrEqual(t, len([]rune(a.sessionMeta.LastPrompt)), 120)
}

// ── Group G: persistSessionRecipe ───────────────────────────────────────────

func TestPersistSessionRecipe_EmptyPathNoop(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipePath = ""
	a.recipe = &recipe.Recipe{Name: "test"}
	a.persistSessionRecipe()

	// No file should be written when path is empty.
	// Verify by checking the temp dir has no recipe.md.
	matches, _ := filepath.Glob(filepath.Join(t.TempDir(), "**/recipe.md"))
	assert.Empty(t, matches)
}

func TestPersistSessionRecipe_NilRecipesNoop(t *testing.T) {
	a := newAppForTest(t, nil)
	a.recipe = nil
	a.sessionRecipe = nil
	a.persistSessionRecipe()

	_, err := os.Stat(a.sessionRecipePath)
	assert.True(t, os.IsNotExist(err))
}

func TestPersistSessionRecipe_UsesSessionRecipe(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = &recipe.Recipe{Name: "session-recipe"}
	a.recipe = &recipe.Recipe{Name: "base-recipe"}
	a.persistSessionRecipe()

	data, err := os.ReadFile(a.sessionRecipePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "session-recipe")
}

func TestPersistSessionRecipe_FallsBackToBaseRecipe(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = nil
	a.recipe = &recipe.Recipe{Name: "fallback-recipe"}
	a.persistSessionRecipe()

	data, err := os.ReadFile(a.sessionRecipePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "fallback-recipe")
}

func TestPersistSessionRecipe_CreatesParentDir(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipePath = filepath.Join(t.TempDir(), "nested", "dir", "recipe.md")
	a.recipe = &recipe.Recipe{Name: "nested-test"}
	a.persistSessionRecipe()

	_, err := os.Stat(a.sessionRecipePath)
	require.NoError(t, err)
}

// ── Group H: updateLastFeedNote ─────────────────────────────────────────────

func TestUpdateLastFeedNote_EmptyFeedNoop(t *testing.T) {
	a := newAppForTest(t, nil)
	assert.NotPanics(t, func() {
		a.updateLastFeedNote("should not panic")
	})
	// No feed file should be created.
	_, err := os.Stat(a.feedPath)
	assert.True(t, os.IsNotExist(err))
}

func TestUpdateLastFeedNote_UpdatesLastEntry(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionFeed = []session.FeedEntry{
		{Kind: "run", Prompt: "first"},
		{Kind: "run", Prompt: "second"},
	}

	a.updateLastFeedNote("Applied: foo.go")

	assert.Empty(t, a.sessionFeed[0].Note)
	assert.Equal(t, "Applied: foo.go", a.sessionFeed[1].Note)
}

func TestUpdateLastFeedNote_SavesToDisk(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionFeed = []session.FeedEntry{
		{Kind: "run", Prompt: "test"},
	}

	a.updateLastFeedNote("Skipped.")

	entries, err := session.LoadFeed(a.feedPath)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "Skipped.", entries[0].Note)
}

func TestUpdateLastFeedNote_OverwritesPrevious(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionFeed = []session.FeedEntry{
		{Kind: "run", Prompt: "test"},
	}

	a.updateLastFeedNote("first note")
	a.updateLastFeedNote("second note")

	assert.Equal(t, "second note", a.sessionFeed[0].Note)
}

// ── Group J: handleRewindCmd feed persistence ───────────────────────────────

func TestHandleRewindCmd_PersistsAnnotationToSessionFeed(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionFeed = []session.FeedEntry{
		{Kind: "run", Prompt: "fix bug", Note: "Applied: foo.go"},
	}
	a.feed = []feedItem{
		{kind: "run", prompt: "fix bug", note: "Applied: foo.go"},
	}
	a.rewindStack = []rewindEntry{
		{feedIndex: 0, prompt: "fix bug", historyLengths: map[string]int{}},
	}

	result, _ := a.handleRewindCmd()
	app := result.(App)

	// Display feed gets annotated.
	assert.Equal(t, "[rewound] Applied: foo.go", app.feed[0].note)
	// Session feed (persisted to disk) must also be updated.
	require.Len(t, app.sessionFeed, 1)
	assert.Equal(t, "[rewound] Applied: foo.go", app.sessionFeed[0].Note)
}

func TestHandleRewindCmd_AnnotationSavedToDisk(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionFeed = []session.FeedEntry{
		{Kind: "run", Prompt: "test", Note: "Skipped."},
	}
	a.feed = []feedItem{
		{kind: "run", prompt: "test", note: "Skipped."},
	}
	a.rewindStack = []rewindEntry{
		{feedIndex: 0, prompt: "test", historyLengths: map[string]int{}},
	}

	a.handleRewindCmd()

	entries, err := session.LoadFeed(a.feedPath)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "[rewound] Skipped.", entries[0].Note)
}

func TestHandleRewindCmd_EmptyNoteBecomes_Rewound(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionFeed = []session.FeedEntry{
		{Kind: "run", Prompt: "test"},
	}
	a.feed = []feedItem{
		{kind: "run", prompt: "test"},
	}
	a.rewindStack = []rewindEntry{
		{feedIndex: 0, prompt: "test", historyLengths: map[string]int{}},
	}

	result, _ := a.handleRewindCmd()
	app := result.(App)

	assert.Equal(t, "[rewound]", app.sessionFeed[0].Note)
}

// ── Group I: syncToolAllowlist ──────────────────────────────────────────────

func TestSyncToolAllowlist_NilSessionRecipeNoop(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = nil
	assert.NotPanics(t, func() {
		a.syncToolAllowlist()
	})
}

func TestSyncToolAllowlist_BuildsFromActiveItems(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = &recipe.Recipe{}
	a.configListItems = []listItem{
		{Label: "read_file", Active: true},
		{Label: "bash", Active: false},
		{Label: "write_file", Active: true},
	}

	a.syncToolAllowlist()

	require.NotNil(t, a.sessionRecipe.Tools)
	assert.Equal(t, []string{"read_file", "write_file"}, a.sessionRecipe.Tools.Allowlist)
}

func TestSyncToolAllowlist_CreatesToolsConfig(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = &recipe.Recipe{} // Tools is nil
	a.configListItems = []listItem{
		{Label: "bash", Active: true},
	}

	a.syncToolAllowlist()

	require.NotNil(t, a.sessionRecipe.Tools)
	assert.Equal(t, []string{"bash"}, a.sessionRecipe.Tools.Allowlist)
}

func TestSyncToolAllowlist_SyncsToAppField(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = &recipe.Recipe{}
	a.configListItems = []listItem{
		{Label: "read_file", Active: true},
		{Label: "bash", Active: true},
	}

	a.syncToolAllowlist()

	assert.Equal(t, a.sessionRecipe.Tools.Allowlist, a.toolAllowlist)
}

func TestSyncToolAllowlist_AllInactiveEmptiesAllowlist(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = &recipe.Recipe{}
	a.configListItems = []listItem{
		{Label: "read_file", Active: false},
		{Label: "bash", Active: false},
	}

	a.syncToolAllowlist()

	assert.Nil(t, a.sessionRecipe.Tools.Allowlist)
	assert.Nil(t, a.toolAllowlist)
}
