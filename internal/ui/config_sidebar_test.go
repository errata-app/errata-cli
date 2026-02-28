package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/recipe"
)

// ── renderSidebar ────────────────────────────────────────────────────────────

func TestRenderSidebar_AllSectionsPresent(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderSidebar(sections, nil, 0, sidebarWidth, 40, false, false)
	for _, sec := range sections {
		assert.Contains(t, out, sec.Name, "section %q should appear in sidebar", sec.Name)
	}
}

func TestRenderSidebar_ExpandedSection(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	expanded := map[int]bool{0: true} // expand first section (models)
	out := renderSidebar(sections, expanded, 0, sidebarWidth, 40, false, false)
	// Expanded section should show the detail description.
	assert.Contains(t, out, sections[0].DetailDesc[:20], "expanded section should show detail text")
	// Should use the down-pointing triangle.
	assert.Contains(t, out, "\u25be", "expanded section should show \u25be marker")
}

func TestRenderSidebar_CollapsedSection(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderSidebar(sections, nil, 0, sidebarWidth, 40, false, false)
	// All collapsed: should use right-pointing triangle.
	assert.Contains(t, out, "\u25b8", "collapsed section should show \u25b8 marker")
}

func TestRenderSidebar_ScrollClamps(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	// scrollY far beyond content should not panic.
	out := renderSidebar(sections, nil, 9999, sidebarWidth, 20, false, false)
	assert.NotEmpty(t, out)
	// Output should have exactly `height` lines.
	lines := strings.Split(out, "\n")
	assert.Equal(t, 20, len(lines))
}

func TestRenderSidebar_ModifiedBadge(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderSidebar(sections, nil, 0, sidebarWidth, 40, true, false)
	assert.Contains(t, out, "[mod]")
}

func TestRenderSidebar_NoModifiedBadgeWhenClean(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderSidebar(sections, nil, 0, sidebarWidth, 40, false, false)
	assert.NotContains(t, out, "[mod]")
}

func TestRenderSidebar_ExactHeight(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	for _, h := range []int{5, 10, 30, 50} {
		out := renderSidebar(sections, nil, 0, sidebarWidth, h, false, false)
		lines := strings.Split(out, "\n")
		assert.Equal(t, h, len(lines), "output should have exactly %d lines", h)
	}
}

func TestRenderSidebar_FocusedShowsKeys(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderSidebar(sections, nil, 0, sidebarWidth, 40, false, true)
	assert.Contains(t, out, "j/k scroll")
	assert.Contains(t, out, "Esc back")
}

func TestRenderSidebar_UnfocusedShowsCtrlHint(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderSidebar(sections, nil, 0, sidebarWidth, 40, false, false)
	assert.Contains(t, out, "Ctrl+]")
}

// ── truncateSidebarLine ──────────────────────────────────────────────────────

func TestTruncateSidebarLine(t *testing.T) {
	tests := []struct {
		input string
		maxW  int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell\u2026"},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},        // maxW <= 3: no ellipsis
		{"abcdef", 4, "abc\u2026"}, // truncation at boundary
		{"", 10, ""},
	}
	for _, tt := range tests {
		got := truncateSidebarLine(tt.input, tt.maxW)
		assert.Equal(t, tt.want, got, "truncateSidebarLine(%q, %d)", tt.input, tt.maxW)
	}
}

// ── wrapSidebarText ──────────────────────────────────────────────────────────

func TestWrapSidebarText(t *testing.T) {
	// Simple wrap at width 5.
	lines := wrapSidebarText("HelloWorld", 5)
	require.Len(t, lines, 2)
	assert.Equal(t, "Hello", lines[0])
	assert.Equal(t, "World", lines[1])

	// Short text — no wrap needed.
	lines = wrapSidebarText("Hi", 10)
	require.Len(t, lines, 1)
	assert.Equal(t, "Hi", lines[0])

	// Empty text.
	lines = wrapSidebarText("", 10)
	require.Len(t, lines, 1)
	assert.Equal(t, "", lines[0])
}

func TestWrapSidebarText_ZeroWidth(t *testing.T) {
	lines := wrapSidebarText("hello", 0)
	require.Len(t, lines, 1)
	assert.Equal(t, "hello", lines[0])
}

// ── feedVPWidth ──────────────────────────────────────────────────────────────

func TestFeedVPWidth_WithSidebar(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 150
	a.sidebarPinned = true
	got := a.feedVPWidth()
	assert.Equal(t, 150-sidebarWidth-1, got)
}

func TestFeedVPWidth_WithoutSidebar(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 150
	a.sidebarPinned = false
	got := a.feedVPWidth()
	assert.Equal(t, 150, got)
}

func TestFeedVPWidth_NarrowTerminal(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 60 // below minWidthSidebar + sidebarWidth + 1
	a.sidebarPinned = true
	got := a.feedVPWidth()
	assert.Equal(t, 60, got, "sidebar should auto-hide on narrow terminal")
}

// ── rebuildSidebar ───────────────────────────────────────────────────────────

func TestRebuildSidebar_PopulatesSections(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	a.rebuildSidebar()
	require.Len(t, a.sidebarSections, len(interactiveSections))
}

func TestRebuildSidebar_UsesSessionRecipeIfAvailable(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = &recipe.Recipe{
		SystemPrompt: "Custom sidebar test prompt",
	}
	a.rebuildSidebar()
	// Find the system-prompt section.
	for _, sec := range a.sidebarSections {
		if sec.Name == "system-prompt" {
			assert.Contains(t, sec.Summary, "Custom sidebar test prompt")
			return
		}
	}
	t.Fatal("system-prompt section not found")
}

// ── /config-pin handler ──────────────────────────────────────────────────────

func TestHandleConfigPinCmd_TogglesOn(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 150
	result, _ := a.handleConfigPinCmd()
	app := result.(App)
	assert.True(t, app.sidebarPinned)
	assert.NotNil(t, app.sidebarExpandedSet)
	assert.NotEmpty(t, app.sidebarSections)
}

func TestHandleConfigPinCmd_TogglesOff(t *testing.T) {
	a := newAppForTest(t, nil)
	a.width = 150
	a.sidebarPinned = true
	a.sidebarSections = buildConfigSections(recipe.Default(), nil, nil)
	result, _ := a.handleConfigPinCmd()
	app := result.(App)
	assert.False(t, app.sidebarPinned)
}

// ── sidebar focus mode ───────────────────────────────────────────────────────

func TestHandleSidebarKey_EscapeUnfocuses(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sidebarPinned = true
	a.sidebarFocused = true
	a.sidebarSections = buildConfigSections(recipe.Default(), nil, nil)
	result, _ := a.handleSidebarKey(tea.KeyMsg{Type: tea.KeyEsc})
	app := result.(App)
	assert.False(t, app.sidebarFocused)
}

func TestHandleSidebarKey_JScrollsDown(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sidebarPinned = true
	a.sidebarFocused = true
	a.sidebarSections = buildConfigSections(recipe.Default(), nil, nil)
	a.sidebarScrollY = 0
	result, _ := a.handleSidebarKey(keyRunes("j"))
	app := result.(App)
	assert.Equal(t, 1, app.sidebarScrollY)
}

func TestHandleSidebarKey_KScrollsUp(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sidebarPinned = true
	a.sidebarFocused = true
	a.sidebarSections = buildConfigSections(recipe.Default(), nil, nil)
	a.sidebarScrollY = 3
	result, _ := a.handleSidebarKey(keyRunes("k"))
	app := result.(App)
	assert.Equal(t, 2, app.sidebarScrollY)
}

func TestHandleSidebarKey_KDoesNotGoBelowZero(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sidebarPinned = true
	a.sidebarFocused = true
	a.sidebarSections = buildConfigSections(recipe.Default(), nil, nil)
	a.sidebarScrollY = 0
	result, _ := a.handleSidebarKey(keyRunes("k"))
	app := result.(App)
	assert.Equal(t, 0, app.sidebarScrollY)
}

func TestHandleSidebarKey_NumberTogglesSection(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sidebarPinned = true
	a.sidebarFocused = true
	a.sidebarSections = buildConfigSections(recipe.Default(), nil, nil)
	a.sidebarExpandedSet = make(map[int]bool)

	// Press "1" to expand first section.
	result, _ := a.handleSidebarKey(keyRunes("1"))
	app := result.(App)
	assert.True(t, app.sidebarExpandedSet[0])

	// Press "1" again to collapse.
	result, _ = app.handleSidebarKey(keyRunes("1"))
	app = result.(App)
	assert.False(t, app.sidebarExpandedSet[0])
}
