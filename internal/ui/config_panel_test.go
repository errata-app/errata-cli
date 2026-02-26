package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/recipe"
)

// ── buildConfigSections ─────────────────────────────────────────────────────

func TestBuildConfigSections_CorrectCount(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	require.Len(t, sections, len(interactiveSections))
	for i, sec := range sections {
		assert.Equal(t, interactiveSections[i], sec.Name)
	}
}

func TestBuildConfigSections_ExcludesTasksAndSuccessCriteria(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	for _, sec := range sections {
		assert.NotEqual(t, "tasks", sec.Name)
		assert.NotEqual(t, "success-criteria", sec.Name)
	}
}

func TestBuildConfigSections_SectionKinds(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	kindMap := make(map[string]string)
	for _, sec := range sections {
		kindMap[sec.Name] = sec.Kind
	}
	assert.Equal(t, "list", kindMap["models"])
	assert.Equal(t, "list", kindMap["tools"])
	assert.Equal(t, "text", kindMap["system-prompt"])
	assert.Equal(t, "scalar", kindMap["parameters"])
	assert.Equal(t, "scalar", kindMap["constraints"])
	assert.Equal(t, "scalar", kindMap["context"])
	assert.Equal(t, "scalar", kindMap["sub-agent"])
	assert.Equal(t, "scalar", kindMap["sandbox"])
}

func TestBuildConfigSections_NilRecipeUsesDefault(t *testing.T) {
	sections := buildConfigSections(nil, nil, nil)
	require.Len(t, sections, len(interactiveSections))
}

// ── summarize functions ─────────────────────────────────────────────────────

func TestSummarizeModels_WithModels(t *testing.T) {
	rec := &recipe.Recipe{Models: []string{"m1", "m2"}}
	s := summarizeModels(rec, nil)
	assert.Contains(t, s, "m1")
	assert.Contains(t, s, "m2")
	assert.Contains(t, s, "2 active")
}

func TestSummarizeModels_FromAdapters(t *testing.T) {
	rec := &recipe.Recipe{}
	ads := []models.ModelAdapter{uiStub{"a1"}, uiStub{"a2"}, uiStub{"a3"}}
	s := summarizeModels(rec, ads)
	assert.Contains(t, s, "a1")
	assert.Contains(t, s, "3 active")
}

func TestSummarizeSystemPrompt_NotSet(t *testing.T) {
	rec := &recipe.Recipe{}
	assert.Equal(t, "(not set)", summarizeSystemPrompt(rec))
}

func TestSummarizeSystemPrompt_Set(t *testing.T) {
	rec := &recipe.Recipe{SystemPrompt: "You are a helpful assistant."}
	s := summarizeSystemPrompt(rec)
	assert.Contains(t, s, "28 chars")
	assert.Contains(t, s, "You are a helpful assistant.")
}

func TestSummarizeConstraints_WithValues(t *testing.T) {
	rec := &recipe.Recipe{}
	rec.Constraints.Timeout = 10 * time.Minute
	rec.Constraints.MaxSteps = 50
	s := summarizeConstraints(rec)
	assert.Contains(t, s, "timeout: 10m0s")
	assert.Contains(t, s, "max_steps: 50")
}

func TestSummarizeConstraints_Defaults(t *testing.T) {
	rec := &recipe.Recipe{}
	s := summarizeConstraints(rec)
	assert.Contains(t, s, "timeout=5m")
	assert.Contains(t, s, "max_steps=unlimited")
}

func TestSummarizeSandbox_WithValues(t *testing.T) {
	rec := &recipe.Recipe{}
	rec.Sandbox.Filesystem = "project_only"
	rec.Sandbox.Network = "full"
	s := summarizeSandbox(rec)
	assert.Contains(t, s, "filesystem: project_only")
	assert.Contains(t, s, "network: full")
}

// ── additional summarize tests ───────────────────────────────────────────────

func TestSummarizeTools_NoToolsConfig(t *testing.T) {
	rec := &recipe.Recipe{} // Tools = nil
	s := summarizeTools(rec, nil)
	assert.Contains(t, s, "enabled")
}

func TestSummarizeTools_WithDisabled(t *testing.T) {
	rec := &recipe.Recipe{}
	disabled := map[string]bool{"bash": true, "web_fetch": true}
	s := summarizeTools(rec, disabled)
	assert.Contains(t, s, "enabled")
}

func TestSummarizeTools_WithAllowlist(t *testing.T) {
	rec := &recipe.Recipe{
		Tools: &recipe.ToolsConfig{Allowlist: []string{"read_file", "bash", "write_file"}},
	}
	s := summarizeTools(rec, map[string]bool{"bash": true})
	assert.Equal(t, "2 enabled", s) // read_file + write_file (bash disabled)
}

func TestSummarizeParameters_AllSet(t *testing.T) {
	seed := int64(42)
	temp := 0.7
	maxTok := 4096
	rec := &recipe.Recipe{
		ModelParams: recipe.ModelParamsConfig{
			Seed:        &seed,
			Temperature: &temp,
			MaxTokens:   &maxTok,
		},
	}
	s := summarizeParameters(rec)
	assert.Contains(t, s, "seed: 42")
	assert.Contains(t, s, "temperature: 0.7")
	assert.Contains(t, s, "max_tokens: 4096")
}

func TestSummarizeParameters_Defaults(t *testing.T) {
	rec := &recipe.Recipe{}
	s := summarizeParameters(rec)
	assert.Contains(t, s, "seed=none")
	assert.Contains(t, s, "provider")
}

func TestSummarizeContext_AllSet(t *testing.T) {
	rec := &recipe.Recipe{
		Context: recipe.ContextConfig{
			Strategy:         "auto_compact",
			MaxHistoryTurns:  30,
			CompactThreshold: 0.75,
		},
	}
	s := summarizeContext(rec)
	assert.Contains(t, s, "auto_compact")
	assert.Contains(t, s, "30 turns")
	assert.Contains(t, s, "threshold: 0.75")
}

func TestSummarizeContext_Defaults(t *testing.T) {
	rec := &recipe.Recipe{}
	s := summarizeContext(rec)
	assert.Contains(t, s, "auto_compact")
	assert.Contains(t, s, "20 turns")
	assert.Contains(t, s, "threshold=0.80")
}

func TestSummarizeSubAgent_WithFields(t *testing.T) {
	rec := &recipe.Recipe{
		SubAgent: recipe.SubAgentConfig{
			Model:    "gpt-4o",
			MaxDepth: 2,
			Tools:    "inherit",
		},
	}
	s := summarizeSubAgent(rec)
	assert.Contains(t, s, "gpt-4o")
	assert.Contains(t, s, "depth: 2")
	assert.Contains(t, s, "tools: inherit")
}

func TestSummarizeSubAgent_Defaults(t *testing.T) {
	rec := &recipe.Recipe{SubAgent: recipe.SubAgentConfig{MaxDepth: -1}}
	s := summarizeSubAgent(rec)
	assert.Contains(t, s, "model=parent")
	assert.Contains(t, s, "depth=1")
	assert.Contains(t, s, "tools=all")
}

func TestSummarizeMCPServers_None(t *testing.T) {
	rec := &recipe.Recipe{}
	assert.Equal(t, "(none)", summarizeMCPServers(rec))
}

func TestSummarizeMCPServers_WithServers(t *testing.T) {
	rec := &recipe.Recipe{
		MCPServers: []recipe.MCPServerEntry{
			{Name: "exa", Command: "npx exa"},
			{Name: "brave", Command: "npx brave"},
		},
	}
	s := summarizeMCPServers(rec)
	assert.Contains(t, s, "2 configured")
	assert.Contains(t, s, "exa")
	assert.Contains(t, s, "brave")
}

// ── getConfigValue / setConfigValue ─────────────────────────────────────────

func TestGetConfigValue_KnownPaths(t *testing.T) {
	rec := &recipe.Recipe{}
	rec.Constraints.Timeout = 5 * time.Minute
	assert.Equal(t, "5m0s", getConfigValue(rec, "constraints.timeout"))
}

func TestGetConfigValue_UnsetReturnsNotSet(t *testing.T) {
	rec := &recipe.Recipe{}
	assert.Equal(t, "(not set)", getConfigValue(rec, "constraints.timeout"))
}

func TestGetConfigValue_UnknownPath(t *testing.T) {
	rec := &recipe.Recipe{}
	assert.Equal(t, "(unknown path)", getConfigValue(rec, "bogus.path"))
}

func TestSetConfigValue_RoundTrip(t *testing.T) {
	tests := []struct {
		path  string
		value string
		want  string
	}{
		{"constraints.timeout", "5m", "5m0s"},
		{"constraints.max_steps", "42", "42"},
		{"context.strategy", "manual", "manual"},
		{"context.max_history_turns", "30", "30"},
		{"context.compact_threshold", "0.75", "0.75"},
		{"sandbox.filesystem", "read_only", "read_only"},
		{"sandbox.network", "none", "none"},
		{"sub_agent.model", "claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"sub_agent.max_depth", "3", "3"},
		{"sub_agent.tools", "inherit", "inherit"},
		{"parameters.seed", "42", "42"},
		{"parameters.temperature", "0.7", "0.7"},
		{"parameters.max_tokens", "4096", "4096"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := &recipe.Recipe{SubAgent: recipe.SubAgentConfig{MaxDepth: -1}}
			err := setConfigValue(rec, tt.path, tt.value)
			require.NoError(t, err)
			got := getConfigValue(rec, tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetConfigValue_InvalidPath(t *testing.T) {
	rec := &recipe.Recipe{}
	err := setConfigValue(rec, "bogus.path", "value")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown config path")
}

func TestSetConfigValue_InvalidValue(t *testing.T) {
	rec := &recipe.Recipe{}
	err := setConfigValue(rec, "constraints.timeout", "not-a-duration")
	require.Error(t, err)

	err = setConfigValue(rec, "constraints.max_steps", "abc")
	require.Error(t, err)

	err = setConfigValue(rec, "context.strategy", "invalid")
	require.Error(t, err)

	err = setConfigValue(rec, "context.compact_threshold", "2.0")
	require.Error(t, err)
}

// ── configPathCandidates ────────────────────────────────────────────────────

func TestConfigPathCandidates_ReturnsAllPaths(t *testing.T) {
	candidates := configPathCandidates()
	assert.GreaterOrEqual(t, len(candidates), 10)
	// Check a few expected paths exist.
	pathSet := make(map[string]bool)
	for _, p := range candidates {
		pathSet[p] = true
	}
	assert.True(t, pathSet["constraints.timeout"])
	assert.True(t, pathSet["sandbox.filesystem"])
	assert.True(t, pathSet["parameters.seed"])
}

// ── cloneRecipe ─────────────────────────────────────────────────────────────

func TestCloneRecipe_DeepCopy(t *testing.T) {
	seed := int64(42)
	original := &recipe.Recipe{
		Name:         "test",
		Models:       []string{"m1", "m2"},
		SystemPrompt: "hello",
		ModelParams:  recipe.ModelParamsConfig{Seed: &seed},
		Constraints:  recipe.ConstraintsConfig{Timeout: 5 * time.Minute},
	}
	clone := cloneRecipe(original)

	// Mutate clone and verify original is unchanged.
	clone.Name = "modified"
	clone.Models[0] = "changed"
	clone.SystemPrompt = "world"
	*clone.ModelParams.Seed = 99
	clone.Constraints.Timeout = 10 * time.Minute

	assert.Equal(t, "test", original.Name)
	assert.Equal(t, "m1", original.Models[0])
	assert.Equal(t, "hello", original.SystemPrompt)
	assert.Equal(t, int64(42), *original.ModelParams.Seed)
	assert.Equal(t, 5*time.Minute, original.Constraints.Timeout)
}

func TestCloneRecipe_NilReturnsDefault(t *testing.T) {
	clone := cloneRecipe(nil)
	assert.NotNil(t, clone)
}

// ── buildModelsList / buildToolsList / buildScalarFields ────────────────────

func TestBuildModelsList_AllActive(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	items := buildModelsList(&recipe.Recipe{}, ads, nil)
	require.Len(t, items, 2)
	assert.True(t, items[0].Active)
	assert.True(t, items[1].Active)
}

func TestBuildModelsList_WithSubset(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}, uiStub{"m3"}}
	active := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m3"}}
	items := buildModelsList(&recipe.Recipe{}, ads, active)
	require.Len(t, items, 3)
	assert.True(t, items[0].Active)  // m1
	assert.False(t, items[1].Active) // m2
	assert.True(t, items[2].Active)  // m3
}

func TestBuildToolsList_Disabled(t *testing.T) {
	disabled := map[string]bool{"bash": true}
	items := buildToolsList(disabled)
	for _, item := range items {
		if item.Label == "bash" {
			assert.False(t, item.Active)
		} else {
			assert.True(t, item.Active)
		}
	}
}

func TestBuildScalarFields_Constraints(t *testing.T) {
	rec := &recipe.Recipe{}
	rec.Constraints.Timeout = 10 * time.Minute
	fields := buildScalarFields("constraints", rec)
	require.Len(t, fields, 2)
	assert.Equal(t, "timeout", fields[0].Key)
	assert.Equal(t, "10m0s", fields[0].Value)
}

func TestBuildScalarFields_UnknownSection(t *testing.T) {
	rec := &recipe.Recipe{}
	fields := buildScalarFields("unknown", rec)
	assert.Nil(t, fields)
}

// ── renderConfigOverlay ─────────────────────────────────────────────────────

func TestRenderConfigOverlay_ContainsSectionNames(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderConfigOverlay(sections, 0, -1, false, 80, nil, 0, nil, 0, "", false, "")
	for _, name := range interactiveSections {
		assert.Contains(t, out, name)
	}
	assert.Contains(t, out, "Configuration")
}

func TestRenderConfigOverlay_ModifiedBadge(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderConfigOverlay(sections, 0, -1, true, 80, nil, 0, nil, 0, "", false, "")
	assert.Contains(t, out, "[modified]")
}

func TestRenderConfigOverlay_NoModifiedBadgeWhenClean(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	out := renderConfigOverlay(sections, 0, -1, false, 80, nil, 0, nil, 0, "", false, "")
	assert.NotContains(t, out, "[modified]")
}

func TestRenderConfigOverlay_ListExpanded(t *testing.T) {
	sections := []configSection{{Name: "tools", Summary: "8 enabled", Kind: "list"}}
	items := []listItem{{Label: "bash", Active: true}, {Label: "read_file", Active: false}}
	out := renderConfigOverlay(sections, 0, 0, false, 80, items, 0, nil, 0, "", false, "")
	assert.Contains(t, out, "bash")
	assert.Contains(t, out, "read_file")
	assert.Contains(t, out, "[x]")
	assert.Contains(t, out, "[ ]")
}

func TestRenderConfigOverlay_ScalarExpanded(t *testing.T) {
	sections := []configSection{{Name: "constraints", Summary: "timeout: 5m", Kind: "scalar"}}
	fields := []scalarField{
		{Key: "timeout", Path: "constraints.timeout", Value: "5m0s"},
		{Key: "max_steps", Path: "constraints.max_steps", Value: "50"},
	}
	out := renderConfigOverlay(sections, 0, 0, false, 80, nil, 0, fields, 0, "", false, "")
	assert.Contains(t, out, "timeout")
	assert.Contains(t, out, "max_steps")
	assert.Contains(t, out, "5m0s")
}

// ── /config command handler ─────────────────────────────────────────────────

func TestHandleConfigCommand_OpensOverlay(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}, uiStub{"m2"}}
	a := newAppForTest(t, ads)
	result, _ := a.handleConfigCommand("")
	app := result.(App)
	assert.True(t, app.configOverlayActive)
	assert.NotNil(t, app.sessionRecipe)
	require.Len(t, app.configSections, len(interactiveSections))
	assert.Equal(t, -1, app.configExpandedIdx)
}

func TestHandleConfigCommand_JumpsToSection(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	result, _ := a.handleConfigCommand("constraints")
	app := result.(App)
	assert.True(t, app.configOverlayActive)
	// Find the constraints section index.
	found := false
	for i, sec := range app.configSections {
		if sec.Name == "constraints" {
			assert.Equal(t, i, app.configSelectedIdx)
			assert.Equal(t, i, app.configExpandedIdx)
			found = true
			break
		}
	}
	assert.True(t, found, "expected constraints section found")
}

func TestHandleConfigCommand_Reset(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	a.sessionRecipe = cloneRecipe(a.recipe)
	a.recipeModified = true
	result, _ := a.handleConfigCommand("reset")
	app := result.(App)
	assert.False(t, app.configOverlayActive)
	assert.False(t, app.recipeModified)
}

// ── /set command handler ────────────────────────────────────────────────────

func TestHandleSetCommand_QueryMode(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	a.sessionRecipe = &recipe.Recipe{}
	a.sessionRecipe.Constraints.Timeout = 5 * time.Minute
	result, _ := a.handleSetCommand("constraints.timeout")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "5m0s")
}

func TestHandleSetCommand_SetValue(t *testing.T) {
	ads := []models.ModelAdapter{uiStub{"m1"}}
	a := newAppForTest(t, ads)
	result, _ := a.handleSetCommand("constraints.timeout 15m")
	app := result.(App)
	assert.True(t, app.recipeModified)
	assert.Equal(t, 15*time.Minute, app.sessionRecipe.Constraints.Timeout)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Set constraints.timeout = 15m")
}

func TestHandleSetCommand_InvalidPath(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleSetCommand("bogus.path value")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Error")
	assert.Contains(t, last, "unknown config path")
}

func TestHandleSetCommand_InvalidValue(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleSetCommand("constraints.timeout not-a-duration")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Error")
}

func TestHandleSetCommand_BareShowsUsage(t *testing.T) {
	a := newAppForTest(t, nil)
	result, _ := a.handleSetCommand("")
	app := result.(App)
	last := app.feed[len(app.feed)-1].text
	assert.Contains(t, last, "Usage")
}

// ── /config overlay key navigation ──────────────────────────────────────────

func TestConfigOverlay_EscapeCloses(t *testing.T) {
	a := newAppForTest(t, nil)
	a.configOverlayActive = true
	a.configSections = buildConfigSections(recipe.Default(), nil, nil)
	a.configExpandedIdx = -1
	result, _ := a.handleConfigKey(keyType(tea.KeyEsc))
	app := result.(App)
	assert.False(t, app.configOverlayActive)
}

func TestConfigOverlay_UpDown(t *testing.T) {
	a := newAppForTest(t, nil)
	a.configOverlayActive = true
	a.configSections = buildConfigSections(recipe.Default(), nil, nil)
	a.configSelectedIdx = 0
	a.configExpandedIdx = -1

	result, _ := a.handleConfigKey(keyType(tea.KeyDown))
	app := result.(App)
	assert.Equal(t, 1, app.configSelectedIdx)

	result, _ = app.handleConfigKey(keyType(tea.KeyUp))
	app = result.(App)
	assert.Equal(t, 0, app.configSelectedIdx)
}

// ── tab-completion for /config and /set ─────────────────────────────────────

func TestTryArgComplete_ConfigSection(t *testing.T) {
	a := newAppForTest(t, nil)
	result, ok := a.tryArgComplete("/config sand")
	if !ok {
		t.Fatal("expected completion")
	}
	assert.Contains(t, result, "/config sandbox ")
}

func TestTryArgComplete_SetPath(t *testing.T) {
	a := newAppForTest(t, nil)
	result, ok := a.tryArgComplete("/set constraints.t")
	if !ok {
		t.Fatal("expected completion")
	}
	assert.Contains(t, result, "/set constraints.timeout ")
}

// ── modified badge in View ──────────────────────────────────────────────────

func TestModifiedBadge_ShownWhenModified(t *testing.T) {
	a := newAppForTest(t, nil)
	a.recipeModified = true
	view := a.View()
	assert.Contains(t, view, "[modified]")
}

func TestModifiedBadge_HiddenWhenClean(t *testing.T) {
	a := newAppForTest(t, nil)
	a.recipeModified = false
	view := a.View()
	assert.NotContains(t, view, "[modified]")
}

// ── configPathDefaults tests ─────────────────────────────────────────────────

func TestConfigPathDefaults_AllPathsHaveDefaults(t *testing.T) {
	// Every config path should have a default description.
	for path := range configPaths {
		_, ok := configPathDefaults[path]
		assert.True(t, ok, "missing default for config path %q", path)
	}
}

func TestConfigPathDefaults_RenderInScalarView(t *testing.T) {
	rec := &recipe.Recipe{}
	fields := buildScalarFields("constraints", rec)
	out := renderConfigOverlay(
		[]configSection{{Name: "constraints", Kind: "scalar"}},
		0, 0, false, 80,
		nil, 0,
		fields, 0, "", false, "",
	)
	// Unset fields should show their default values.
	assert.Contains(t, out, "(default: 5m)")
	assert.Contains(t, out, "(default: unlimited)")
}

func TestSummarizeSandbox_ShowsDefaults(t *testing.T) {
	rec := &recipe.Recipe{}
	s := summarizeSandbox(rec)
	assert.Contains(t, s, "filesystem=unrestricted")
	assert.Contains(t, s, "network=full")
}

func TestSummarizeContextSummarization_ShowsDefault(t *testing.T) {
	rec := &recipe.Recipe{}
	s := summarizeContextSummarization(rec)
	assert.Contains(t, s, "built-in prompt")
}

// ── section descriptions tests ───────────────────────────────────────────────

func TestSectionDescriptions_AllSectionsHaveDescriptions(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	for _, sec := range sections {
		assert.NotEmpty(t, sec.Desc, "section %q missing Desc", sec.Name)
		assert.NotEmpty(t, sec.DetailDesc, "section %q missing DetailDesc", sec.Name)
	}
}

func TestSectionDescriptions_NavViewShowsDescForSelected(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	// Render with first section selected.
	out := renderConfigOverlay(sections, 0, -1, false, 80, nil, 0, nil, 0, "", false, "")
	// The selected section (models) should show its brief description.
	assert.Contains(t, out, sectionDescriptions["models"].Brief)
}

func TestSectionDescriptions_ExpandedViewShowsDetail(t *testing.T) {
	rec := recipe.Default()
	sections := buildConfigSections(rec, nil, nil)
	fields := buildScalarFields("constraints", rec)
	// Expand constraints (index 5).
	out := renderConfigOverlay(sections, 5, 5, false, 80, nil, 0, fields, 0, "", false, "")
	assert.Contains(t, out, sectionDescriptions["constraints"].Detail)
}

// ── text editing tests ───────────────────────────────────────────────────────

func TestHandleConfigTextKey_CtrlSSavesSystemPrompt(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = cloneRecipe(a.recipe)
	a.configOverlayActive = true
	a.configSections = buildConfigSections(a.sessionRecipe, a.adapters, a.disabledTools)
	// Expand system-prompt (index 1).
	a.configExpandedIdx = 1
	a.configTextEditing = true
	a.configTextArea.SetValue("New system prompt content")

	result, _ := a.handleConfigTextKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	app := result.(App)
	assert.Equal(t, "New system prompt content", app.sessionRecipe.SystemPrompt)
	assert.True(t, app.recipeModified)
	assert.False(t, app.configTextEditing)
}

func TestHandleConfigTextKey_EscapeCancelsEditing(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = cloneRecipe(a.recipe)
	a.configOverlayActive = true
	a.configSections = buildConfigSections(a.sessionRecipe, a.adapters, a.disabledTools)
	a.configExpandedIdx = 1
	a.configTextEditing = true
	a.configTextArea.SetValue("Unsaved content")

	result, _ := a.handleConfigTextKey(tea.KeyMsg{Type: tea.KeyEsc})
	app := result.(App)
	assert.False(t, app.configTextEditing)
	// Original prompt should be unchanged.
	assert.Equal(t, a.sessionRecipe.SystemPrompt, app.sessionRecipe.SystemPrompt)
}

func TestHandleConfigTextKey_CtrlDSavesContextSummarization(t *testing.T) {
	a := newAppForTest(t, nil)
	a.sessionRecipe = cloneRecipe(a.recipe)
	a.configOverlayActive = true
	a.configSections = buildConfigSections(a.sessionRecipe, a.adapters, a.disabledTools)
	// context-summarization is index 12.
	a.configExpandedIdx = 12
	a.configTextEditing = true
	a.configTextArea.SetValue("Custom summarization prompt")

	result, _ := a.handleConfigTextKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	app := result.(App)
	assert.Equal(t, "Custom summarization prompt", app.sessionRecipe.SummarizationPrompt)
	assert.True(t, app.recipeModified)
}

func TestRenderConfigOverlay_TextEditingShowsTextArea(t *testing.T) {
	sections := []configSection{{Name: "system-prompt", Kind: "text", DetailDesc: "test detail"}}
	out := renderConfigOverlay(sections, 0, 0, false, 80, nil, 0, nil, 0, "",
		true, "  [textarea content here]")
	assert.Contains(t, out, "[textarea content here]")
	assert.Contains(t, out, "Ctrl+S = save")
}

func TestRenderConfigOverlay_TextPreviewShown(t *testing.T) {
	sections := []configSection{{Name: "system-prompt", Kind: "text", DetailDesc: "test detail"}}
	out := renderConfigOverlay(sections, 0, 0, false, 80, nil, 0, nil, 0, "Hello world",
		false, "")
	assert.Contains(t, out, "Hello world")
	assert.Contains(t, out, "Enter = edit")
}
