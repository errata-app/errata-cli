package hooks_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/hooks"
)

// ─── MatchesEvent ───────────────────────────────────────────────────────────

func TestMatchesEvent_ExactMatch(t *testing.T) {
	h := hooks.Hook{Name: "h1", Event: hooks.PostToolUse, Matcher: "bash", Enabled: true}
	assert.True(t, hooks.MatchesEvent(h, hooks.PostToolUse, "bash"))
	assert.False(t, hooks.MatchesEvent(h, hooks.PostToolUse, "read_file"))
}

func TestMatchesEvent_GlobMatch(t *testing.T) {
	h := hooks.Hook{Name: "h1", Event: hooks.PostToolUse, Matcher: "edit_*", Enabled: true}
	assert.True(t, hooks.MatchesEvent(h, hooks.PostToolUse, "edit_file"))
	assert.False(t, hooks.MatchesEvent(h, hooks.PostToolUse, "read_file"))
}

func TestMatchesEvent_NoMatcher(t *testing.T) {
	h := hooks.Hook{Name: "h1", Event: hooks.PostToolUse, Matcher: "", Enabled: true}
	assert.True(t, hooks.MatchesEvent(h, hooks.PostToolUse, "anything"))
}

func TestMatchesEvent_WrongEvent(t *testing.T) {
	h := hooks.Hook{Name: "h1", Event: hooks.PostToolUse, Matcher: "bash", Enabled: true}
	assert.False(t, hooks.MatchesEvent(h, hooks.PreToolUse, "bash"))
}

func TestMatchesEvent_Disabled(t *testing.T) {
	h := hooks.Hook{Name: "h1", Event: hooks.PostToolUse, Matcher: "bash", Enabled: false}
	assert.False(t, hooks.MatchesEvent(h, hooks.PostToolUse, "bash"))
}

func TestMatchesEvent_NonToolEvent_IgnoresMatcher(t *testing.T) {
	h := hooks.Hook{Name: "h1", Event: hooks.SessionStart, Matcher: "bash", Enabled: true}
	assert.True(t, hooks.MatchesEvent(h, hooks.SessionStart, ""))
}

// ─── Run ────────────────────────────────────────────────────────────────────

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell tests require sh")
	}
}

func TestRun_BasicCommand(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{Name: "echo", Event: hooks.SessionStart, Command: "echo hello", Enabled: true},
	}
	results := hooks.Run(context.Background(), h, hooks.SessionStart, "", nil)
	require.Len(t, results, 1)
	assert.Equal(t, "echo", results[0].Name)
	assert.Equal(t, "hello\n", results[0].Stdout)
	assert.Equal(t, 0, results[0].ExitCode)
	assert.False(t, results[0].TimedOut)
}

func TestRun_EnvSubstitution(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{Name: "env", Event: hooks.PostToolUse, Matcher: "bash", Command: "echo $TOOL_NAME", Enabled: true},
	}
	env := hooks.Env{"TOOL_NAME": "bash"}
	results := hooks.Run(context.Background(), h, hooks.PostToolUse, "bash", env)
	require.Len(t, results, 1)
	assert.Equal(t, "bash\n", results[0].Stdout)
}

func TestRun_NonZeroExit(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{Name: "fail", Event: hooks.SessionStart, Command: "exit 42", Enabled: true},
	}
	results := hooks.Run(context.Background(), h, hooks.SessionStart, "", nil)
	require.Len(t, results, 1)
	assert.Equal(t, 42, results[0].ExitCode)
}

func TestRun_Timeout(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{
			Name:    "slow",
			Event:   hooks.SessionStart,
			Command: "sleep 10",
			Timeout: 100 * time.Millisecond,
			Enabled: true,
		},
	}
	results := hooks.Run(context.Background(), h, hooks.SessionStart, "", nil)
	require.Len(t, results, 1)
	assert.True(t, results[0].TimedOut)
	assert.Equal(t, -1, results[0].ExitCode)
}

func TestRun_SequentialOrder(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{Name: "first", Event: hooks.SessionStart, Command: "echo 1", Enabled: true},
		{Name: "second", Event: hooks.SessionStart, Command: "echo 2", Enabled: true},
	}
	results := hooks.Run(context.Background(), h, hooks.SessionStart, "", nil)
	require.Len(t, results, 2)
	assert.Equal(t, "first", results[0].Name)
	assert.Equal(t, "second", results[1].Name)
}

func TestRun_DisabledHooksSkipped(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{Name: "on", Event: hooks.SessionStart, Command: "echo on", Enabled: true},
		{Name: "off", Event: hooks.SessionStart, Command: "echo off", Enabled: false},
	}
	results := hooks.Run(context.Background(), h, hooks.SessionStart, "", nil)
	require.Len(t, results, 1)
	assert.Equal(t, "on", results[0].Name)
}

func TestRun_NoMatchingHooks(t *testing.T) {
	h := []hooks.Hook{
		{Name: "h1", Event: hooks.PostToolUse, Matcher: "bash", Enabled: true},
	}
	results := hooks.Run(context.Background(), h, hooks.PreToolUse, "read_file", nil)
	assert.Empty(t, results)
}

func TestRun_InjectOutput(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{
			Name:         "vet",
			Event:        hooks.PostToolUse,
			Matcher:      "edit_file",
			Command:      "echo 'all good'",
			InjectOutput: true,
			Enabled:      true,
		},
	}
	results := hooks.Run(context.Background(), h, hooks.PostToolUse, "edit_file", nil)
	require.Len(t, results, 1)
	// Caller checks h.InjectOutput and reads result.Stdout.
	assert.Equal(t, "all good\n", results[0].Stdout)
}

func TestRun_Stderr(t *testing.T) {
	skipOnWindows(t)
	h := []hooks.Hook{
		{Name: "err", Event: hooks.SessionStart, Command: "echo oops >&2", Enabled: true},
	}
	results := hooks.Run(context.Background(), h, hooks.SessionStart, "", nil)
	require.Len(t, results, 1)
	assert.Equal(t, "oops\n", results[0].Stderr)
}

// ─── Context delivery ───────────────────────────────────────────────────────

func TestHooksFromContext_Present(t *testing.T) {
	h := []hooks.Hook{{Name: "test", Event: hooks.SessionStart, Enabled: true}}
	ctx := hooks.WithHooks(context.Background(), h)
	got := hooks.HooksFromContext(ctx)
	require.Len(t, got, 1)
	assert.Equal(t, "test", got[0].Name)
}

func TestHooksFromContext_Absent(t *testing.T) {
	got := hooks.HooksFromContext(context.Background())
	assert.Nil(t, got)
}
