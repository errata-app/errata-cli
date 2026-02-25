//go:build !darwin

package sandbox

import (
	"context"
	"log"
	"os"
	"os/exec"
	"sync"
)

var (
	bwrapPath string
	initOnce  sync.Once
)

func init() {
	initOnce.Do(func() {
		path, err := exec.LookPath("bwrap")
		if err == nil {
			bwrapPath = path
			Available = true
		}
		// Windows / BSD / other: Available stays false; app-level only.
	})
}

func buildCmd(ctx context.Context, cfg Config, name string, args ...string) *exec.Cmd {
	if !cfg.Active() {
		return exec.CommandContext(ctx, name, args...)
	}

	if !Available {
		log.Printf("sandbox: OS-level process sandboxing unavailable on this platform " +
			"(install bwrap for full protection on Linux). " +
			"Applying application-level confinement only.")
		cmd := exec.CommandContext(ctx, name, args...)
		// Best-effort: confine working directory to project root so relative
		// paths cannot escape it.
		if cfg.ProjectRoot != "" {
			cmd.Dir = cfg.ProjectRoot
		}
		return cmd
	}

	// ── bwrap (bubblewrap) on Linux ──────────────────────────────────────────
	root := cfg.ProjectRoot
	if root == "" {
		root, _ = os.Getwd()
	}

	bwArgs := []string{
		// Bind the entire host filesystem read-only as the baseline.
		"--ro-bind", "/", "/",
		// Re-bind /tmp read-write so compilers and toolchains can use it.
		"--bind", "/tmp", "/tmp",
		// Mount /dev and /proc so standard tools work.
		"--dev", "/dev",
		"--proc", "/proc",
	}

	if cfg.Filesystem == "project_only" {
		// Overlay the project root with a read-write bind so the model can
		// write there but nowhere else on the real filesystem.
		bwArgs = append(bwArgs, "--bind", root, root)
	}
	// read_only: the baseline --ro-bind / / is already read-only everywhere
	// except /tmp; no additional rules needed.

	if cfg.Network == "none" {
		bwArgs = append(bwArgs, "--unshare-net")
	}

	bwArgs = append(bwArgs, "--")
	bwArgs = append(bwArgs, name)
	bwArgs = append(bwArgs, args...)
	return exec.CommandContext(ctx, bwrapPath, bwArgs...)
}
