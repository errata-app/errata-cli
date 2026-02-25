// Package hooks provides lifecycle event hooks for the agentic loop.
//
// Hooks are user-configured shell commands that execute at specific points
// in the session or tool-use lifecycle. They support environment variable
// substitution, glob-based tool matching, timeout enforcement, and optional
// output injection back into the model context.
//
// Only "command" action hooks are supported. All hook execution is sequential
// in definition order. No LLM-based hooks — intentionally omitted to avoid
// contaminating evaluation.
package hooks

import (
	"bytes"
	"context"
	"os/exec"
	"path"
	"strings"
	"time"
)

// ─── Event types ────────────────────────────────────────────────────────────

// Event identifies a lifecycle point where hooks can fire.
type Event string

const (
	SessionStart   Event = "session_start"
	PrePrompt      Event = "pre_prompt"
	PostResponse   Event = "post_response"
	PreToolUse     Event = "pre_tool_use"
	PostToolUse    Event = "post_tool_use"
	ContextCompact Event = "context_compact"
	SessionEnd     Event = "session_end"
)

// ─── Hook ───────────────────────────────────────────────────────────────────

// Hook is a parsed, ready-to-execute lifecycle hook.
type Hook struct {
	Name         string
	Event        Event
	Matcher      string        // tool name or glob; "" = match all events of type
	Command      string        // shell command to execute
	Timeout      time.Duration // 0 = no timeout (30s default applied at run time)
	InjectOutput bool          // if true, stdout is returned for context injection
	Enabled      bool
}

// HookResult captures the outcome of a single hook execution.
type HookResult struct {
	Name     string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	TimedOut bool
}

// ─── Matching ───────────────────────────────────────────────────────────────

// MatchesEvent reports whether h should fire for the given event and tool name.
// toolName is relevant only for PreToolUse / PostToolUse events; pass "" otherwise.
func MatchesEvent(h Hook, event Event, toolName string) bool {
	if !h.Enabled {
		return false
	}
	if h.Event != event {
		return false
	}
	if h.Matcher == "" {
		return true // no matcher → matches all events of this type
	}
	// Matcher is relevant only for tool-use events.
	if event != PreToolUse && event != PostToolUse {
		return true
	}
	// Try exact match first, then glob.
	if h.Matcher == toolName {
		return true
	}
	ok, _ := path.Match(h.Matcher, toolName)
	return ok
}

// ─── Execution ──────────────────────────────────────────────────────────────

const defaultTimeout = 30 * time.Second

// Env carries environment variables for hook command expansion.
type Env map[string]string

// Run executes all matching hooks sequentially in definition order.
// env provides variable substitutions for the command string (e.g. $TOOL_NAME).
// Returns results for each hook that ran.
func Run(ctx context.Context, hooks []Hook, event Event, toolName string, env Env) []HookResult {
	var results []HookResult
	for _, h := range hooks {
		if !MatchesEvent(h, event, toolName) {
			continue
		}
		results = append(results, runOne(ctx, h, env))
	}
	return results
}

// runOne executes a single hook command.
func runOne(ctx context.Context, h Hook, env Env) HookResult {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	cmdStr := expandEnv(h.Command, env)

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", cmdStr)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	dur := time.Since(start)

	result := HookResult{
		Name:     h.Name,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: dur,
	}

	if cmdCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.ExitCode = -1
	} else if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}

	return result
}

// expandEnv replaces $VAR_NAME placeholders in cmd with values from env.
func expandEnv(cmd string, env Env) string {
	for k, v := range env {
		cmd = strings.ReplaceAll(cmd, "$"+k, v)
	}
	return cmd
}

// ─── Context-based delivery ─────────────────────────────────────────────────

type hooksKey struct{}

// WithHooks returns a context carrying the hook list.
func WithHooks(ctx context.Context, h []Hook) context.Context {
	return context.WithValue(ctx, hooksKey{}, h)
}

// HooksFromContext retrieves hooks from ctx. Returns nil when absent.
func HooksFromContext(ctx context.Context) []Hook {
	h, _ := ctx.Value(hooksKey{}).([]Hook)
	return h
}
