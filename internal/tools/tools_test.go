package tools_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/tools"
)

// ─── JSONSchemaProps ─────────────────────────────────────────────────────────

func TestJSONSchemaProps_RoundTrip(t *testing.T) {
	def := tools.ToolDef{
		Name:        "test_tool",
		Description: "A test tool",
		Properties: map[string]tools.ToolParam{
			"path":    {Type: "string", Description: "File path"},
			"content": {Type: "string", Description: "File content"},
			"limit":   {Type: "integer", Description: "Max lines"},
		},
		Required: []string{"path", "content"},
	}

	props, required := def.JSONSchemaProps()

	// Verify all properties are present with correct type and description.
	assert.Len(t, props, 3)
	for name, p := range def.Properties {
		prop, ok := props[name]
		require.True(t, ok, "property %q should be present", name)
		m, mOK := prop.(map[string]any)
		require.True(t, mOK, "property %q should be map[string]any", name)
		assert.Equal(t, p.Type, m["type"])
		assert.Equal(t, p.Description, m["description"])
	}

	// Verify required is a copy, not a reference.
	assert.Equal(t, []string{"path", "content"}, required)
	required[0] = "mutated"
	assert.Equal(t, "path", def.Required[0], "JSONSchemaProps must return a copy of Required")
}

func TestJSONSchemaProps_Empty(t *testing.T) {
	def := tools.ToolDef{Name: "empty"}
	props, required := def.JSONSchemaProps()
	assert.Empty(t, props)
	assert.Empty(t, required)
}

// ─── ActiveDefinitions ───────────────────────────────────────────────────────

func TestActiveDefinitions_NilDisabled_ReturnsAll(t *testing.T) {
	all := tools.ActiveDefinitions(nil)
	assert.Equal(t, tools.Definitions, all)
}

func TestActiveDefinitions_EmptyDisabled_ReturnsAll(t *testing.T) {
	all := tools.ActiveDefinitions(map[string]bool{})
	assert.Equal(t, tools.Definitions, all)
}

func TestActiveDefinitions_DisablesOneToolByName(t *testing.T) {
	disabled := map[string]bool{tools.BashToolName: true}
	active := tools.ActiveDefinitions(disabled)
	for _, d := range active {
		assert.NotEqual(t, tools.BashToolName, d.Name, "bash should be excluded")
	}
	assert.Len(t, active, len(tools.Definitions)-1)
}

func TestActiveDefinitions_DisablesMultipleTools(t *testing.T) {
	disabled := map[string]bool{
		tools.BashToolName:   true,
		tools.SearchCodeName: true,
	}
	active := tools.ActiveDefinitions(disabled)
	assert.Len(t, active, len(tools.Definitions)-2)
	for _, d := range active {
		assert.NotEqual(t, tools.BashToolName, d.Name)
		assert.NotEqual(t, tools.SearchCodeName, d.Name)
	}
}

// ─── WithActiveTools / ActiveToolsFromContext ─────────────────────────────────

func TestActiveToolsFromContext_DefaultWhenNilContext(t *testing.T) {
	ctx := context.Background()
	got := tools.ActiveToolsFromContext(ctx)
	assert.Equal(t, tools.Definitions, got)
}

func TestWithActiveTools_RoundTrip(t *testing.T) {
	subset := tools.Definitions[:2]
	ctx := tools.WithActiveTools(context.Background(), subset)
	got := tools.ActiveToolsFromContext(ctx)
	assert.Equal(t, subset, got)
}

func TestActiveToolsFromContext_EmptySliceReturnsEmpty(t *testing.T) {
	// A context carrying an empty slice means zero active tools.
	ctx := tools.WithActiveTools(context.Background(), []tools.ToolDef{})
	got := tools.ActiveToolsFromContext(ctx)
	assert.Empty(t, got)
}

func TestActiveToolsFromContext_NilDoesNotStore(t *testing.T) {
	// WithActiveTools(ctx, nil) is a no-op — ActiveToolsFromContext returns Definitions.
	ctx := tools.WithActiveTools(context.Background(), nil)
	got := tools.ActiveToolsFromContext(ctx)
	assert.Equal(t, tools.Definitions, got)
}

// ─── ToolsForRole ─────────────────────────────────────────────────────────────

func TestToolsForRole_Explorer(t *testing.T) {
	defs := tools.ToolsForRole(tools.RoleExplorer, tools.Definitions)
	names := toolNames(defs)
	assert.Contains(t, names, tools.ReadToolName)
	assert.Contains(t, names, tools.ListDirToolName)
	assert.Contains(t, names, tools.SearchFilesName)
	assert.Contains(t, names, tools.SearchCodeName)
	assert.Contains(t, names, tools.WebFetchToolName)
	assert.Contains(t, names, tools.WebSearchToolName)
	// Explorer must not include write or bash tools.
	assert.NotContains(t, names, tools.WriteToolName)
	assert.NotContains(t, names, tools.EditToolName)
	assert.NotContains(t, names, tools.BashToolName)
	assert.NotContains(t, names, tools.SpawnAgentToolName)
}

func TestToolsForRole_Planner(t *testing.T) {
	defs := tools.ToolsForRole(tools.RolePlanner, tools.Definitions)
	names := toolNames(defs)
	assert.Contains(t, names, tools.ReadToolName)
	assert.Contains(t, names, tools.BashToolName)
	// Planner must not include write tools.
	assert.NotContains(t, names, tools.WriteToolName)
	assert.NotContains(t, names, tools.EditToolName)
	assert.NotContains(t, names, tools.SpawnAgentToolName)
}

func TestToolsForRole_Coder(t *testing.T) {
	// Coder returns parentDefs unchanged.
	parent := tools.Definitions
	defs := tools.ToolsForRole(tools.RoleCoder, parent)
	assert.Equal(t, parent, defs)
}

func TestToolsForRole_Full_AliasForCoder(t *testing.T) {
	parent := tools.Definitions
	coder := tools.ToolsForRole(tools.RoleCoder, parent)
	full := tools.ToolsForRole(tools.RoleFull, parent)
	assert.Equal(t, coder, full)
}

func TestToolsForRole_UnknownRole_DefaultsToCoder(t *testing.T) {
	parent := tools.Definitions
	defs := tools.ToolsForRole("mystery-role", parent)
	assert.Equal(t, parent, defs)
}

// ─── Sub-agent context helpers ────────────────────────────────────────────────

func TestSubagentDepth_RoundTrip(t *testing.T) {
	ctx := tools.WithSubagentDepth(context.Background(), 3)
	assert.Equal(t, 3, tools.SubagentDepthFromContext(ctx))
}

func TestSubagentDepth_DefaultZero(t *testing.T) {
	assert.Equal(t, 0, tools.SubagentDepthFromContext(context.Background()))
}

func TestSubagentDispatcher_RoundTrip(t *testing.T) {
	called := false
	var d tools.SubagentDispatcher = func(_ context.Context, _ map[string]string) (string, []tools.FileWrite, string) {
		called = true
		return "ok", nil, ""
	}
	ctx := tools.WithSubagentDispatcher(context.Background(), d)
	got := tools.SubagentDispatcherFromContext(ctx)
	require.NotNil(t, got)
	text, _, _ := got(context.Background(), nil)
	assert.True(t, called)
	assert.Equal(t, "ok", text)
}

func TestSubagentDispatcher_NilWhenAbsent(t *testing.T) {
	got := tools.SubagentDispatcherFromContext(context.Background())
	assert.Nil(t, got)
}

// ─── Context function round-trips ───────────────────────────────────────────

func TestWithBashPrefixes_RoundTrip(t *testing.T) {
	prefixes := []string{"go *", "npm *"}
	ctx := tools.WithBashPrefixes(context.Background(), prefixes)
	got := tools.BashPrefixesFromContext(ctx)
	assert.Equal(t, prefixes, got)
}

func TestBashPrefixesFromContext_NilWhenAbsent(t *testing.T) {
	got := tools.BashPrefixesFromContext(context.Background())
	assert.Nil(t, got)
}

func TestWithMCPDispatchers_RoundTrip(t *testing.T) {
	d := map[string]tools.MCPDispatcher{
		"search": func(args map[string]string) string { return "found" },
	}
	ctx := tools.WithMCPDispatchers(context.Background(), d)
	got := tools.MCPDispatchersFromContext(ctx)
	assert.NotNil(t, got)
	assert.Equal(t, "found", got["search"](nil))
}

func TestMCPDispatchersFromContext_NilWhenAbsent(t *testing.T) {
	got := tools.MCPDispatchersFromContext(context.Background())
	assert.Nil(t, got)
}

func TestWithSeed_RoundTrip(t *testing.T) {
	ctx := tools.WithSeed(context.Background(), 12345)
	seed, ok := tools.SeedFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, int64(12345), seed)
}

func TestSeedFromContext_FalseWhenAbsent(t *testing.T) {
	_, ok := tools.SeedFromContext(context.Background())
	assert.False(t, ok)
}

func TestWithSeed_ZeroValue(t *testing.T) {
	ctx := tools.WithSeed(context.Background(), 0)
	seed, ok := tools.SeedFromContext(ctx)
	assert.True(t, ok, "zero seed should still be present")
	assert.Equal(t, int64(0), seed)
}

// ─── MaxSteps context ──────────────────────────────────────────────────────

func TestWithMaxSteps_RoundTrip(t *testing.T) {
	ctx := tools.WithMaxSteps(context.Background(), 42)
	assert.Equal(t, 42, tools.MaxStepsFromContext(ctx))
}

func TestMaxStepsFromContext_ZeroWhenAbsent(t *testing.T) {
	assert.Equal(t, 0, tools.MaxStepsFromContext(context.Background()))
}

// ─── DefinitionsAllowed ─────────────────────────────────────────────────────

func TestDefinitionsAllowed_AllowlistOnly(t *testing.T) {
	allowlist := []string{tools.ReadToolName, tools.BashToolName}
	got := tools.DefinitionsAllowed(allowlist, nil)
	names := toolNames(got)
	assert.Len(t, got, 2)
	assert.Contains(t, names, tools.ReadToolName)
	assert.Contains(t, names, tools.BashToolName)
}

func TestDefinitionsAllowed_AllowlistPlusDisabled(t *testing.T) {
	allowlist := []string{tools.ReadToolName, tools.BashToolName}
	disabled := map[string]bool{tools.BashToolName: true}
	got := tools.DefinitionsAllowed(allowlist, disabled)
	assert.Len(t, got, 1)
	assert.Equal(t, tools.ReadToolName, got[0].Name)
}

func TestDefinitionsAllowed_NilAllowlist_UsesAll(t *testing.T) {
	disabled := map[string]bool{tools.BashToolName: true}
	got := tools.DefinitionsAllowed(nil, disabled)
	names := toolNames(got)
	assert.NotContains(t, names, tools.BashToolName)
	assert.NotEmpty(t, got)
}

func TestDefinitionsAllowed_NilAllowlistReturnsAll(t *testing.T) {
	got := tools.DefinitionsAllowed(nil, nil)
	assert.Equal(t, tools.Definitions, got)
}

func TestDefinitionsAllowed_EmptyAllowlistReturnsNone(t *testing.T) {
	got := tools.DefinitionsAllowed([]string{}, nil)
	assert.Empty(t, got)
}

func TestDefinitionsAllowed_EmptyAllowlistPlusDisabled(t *testing.T) {
	// Empty allowlist already yields zero tools; disabled is irrelevant.
	got := tools.DefinitionsAllowed([]string{}, map[string]bool{tools.BashToolName: true})
	assert.Empty(t, got)
}

func TestDefinitionsAllowed_InvalidNames(t *testing.T) {
	got := tools.DefinitionsAllowed([]string{"nonexistent_tool"}, nil)
	assert.Empty(t, got)
}

// ─── FilterDefs ─────────────────────────────────────────────────────────────

func TestFilterDefs_PreservesOrder(t *testing.T) {
	defs := []tools.ToolDef{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	disabled := map[string]bool{"b": true}
	got := tools.FilterDefs(defs, disabled)
	assert.Equal(t, []string{"a", "c"}, toolNames(got))
}

func TestFilterDefs_NilDisabled(t *testing.T) {
	defs := []tools.ToolDef{{Name: "a"}, {Name: "b"}}
	got := tools.FilterDefs(defs, nil)
	assert.Len(t, got, 2)
}

// toolNames extracts tool name strings from a ToolDef slice.
func toolNames(defs []tools.ToolDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}
