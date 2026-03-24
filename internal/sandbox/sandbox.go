// Package sandbox provides OS-level process sandboxing for bash subprocesses
// spawned by Errata's agentic tool loop.
//
// Each call to BuildCmd creates a new, isolated subprocess — models do not share
// a sandbox process. Subagents inherit the parent model's sandbox settings via
// Go context propagation.
//
// Platform support:
//   - macOS: sandbox-exec with SBPL profiles (built-in, zero install)
//   - Linux: bwrap (bubblewrap) if on PATH; application-level fallback otherwise
//   - Windows/other: application-level only (cwd confinement + warning)
package sandbox

import (
	"context"
	"os/exec"
)

// Config describes the OS-level sandbox applied to each bash subprocess.
// Zero-value Config (all empty strings) means unrestricted — BuildCmd returns
// a plain exec.Cmd.
type Config struct {
	// Filesystem controls write access:
	//   "" or "unrestricted" — no restrictions
	//   "project_only"       — writes restricted to ProjectRoot (and /tmp)
	//   "read_only"          — no writes except to /tmp
	Filesystem string

	// Network controls outbound access:
	//   "" or "full" — no restrictions
	//   "none"       — all network calls blocked at OS level
	Network string

	// ProjectRoot is the absolute path models may write to in project_only mode.
	// "" means use os.Getwd() at exec time.
	ProjectRoot string

	// AllowLocalFetch permits web_fetch to target localhost URLs.
	AllowLocalFetch bool
}

// Active reports whether cfg requires any OS-level restriction.
// BuildCmd is a no-op pass-through when Active() returns false.
func (cfg Config) Active() bool {
	return cfg.Filesystem == "project_only" ||
		cfg.Filesystem == "read_only" ||
		cfg.Network == "none"
}

// Available reports whether OS-level process sandboxing can be applied on this
// platform. Set at package init in the platform-specific files.
var Available bool

// ─── Context helpers ─────────────────────────────────────────────────────────

type configKey struct{}

// WithConfig stores cfg in ctx so that ExecuteBash can retrieve it.
func WithConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}

// ConfigFromContext retrieves the Config stored by WithConfig.
// ok is false if no config was stored.
func ConfigFromContext(ctx context.Context) (Config, bool) {
	v, ok := ctx.Value(configKey{}).(Config)
	return v, ok
}

// ─── BuildCmd ────────────────────────────────────────────────────────────────

// BuildCmd creates an *exec.Cmd that executes name+args under the sandbox
// described by cfg. On macOS this wraps with sandbox-exec; on Linux with bwrap.
// On unsupported platforms or when the OS tool is unavailable, the subprocess
// runs with cmd.Dir = cfg.ProjectRoot only (application-level confinement).
//
// The implementation lives in sandbox_darwin.go and sandbox_other.go.
func BuildCmd(ctx context.Context, cfg Config, name string, args ...string) *exec.Cmd {
	return buildCmd(ctx, cfg, name, args...)
}
