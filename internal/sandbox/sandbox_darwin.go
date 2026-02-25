//go:build darwin

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	sandboxExecPath string
	initOnce        sync.Once
)

func init() {
	initOnce.Do(func() {
		path, err := exec.LookPath("sandbox-exec")
		if err == nil {
			sandboxExecPath = path
			Available = true
		}
	})
}

// buildProfile generates an SBPL (Sandbox Profile Language) profile string.
//
// We use allow-by-default style so arbitrary development toolchains (go, git,
// npm, etc.) work without whitelisting every binary. Only the restricted
// operations are explicitly denied.
func buildProfile(cfg Config) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")

	switch cfg.Filesystem {
	case "project_only":
		root := cfg.ProjectRoot
		if root == "" {
			root, _ = os.Getwd()
		}
		// Deny all writes, then re-allow project root and temp directories.
		// More-specific subpath rules take precedence over the blanket deny.
		b.WriteString("(deny file-write*)\n")
		b.WriteString("(allow file-write* (literal \"/dev/null\"))\n")
		b.WriteString("(allow file-write* (subpath \"/private/tmp\"))\n")
		b.WriteString("(allow file-write* (subpath \"/var/tmp\"))\n")
		b.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))\n", root))
	case "read_only":
		// No writes anywhere except temp (needed by compilers, go toolchain, etc.)
		b.WriteString("(deny file-write*)\n")
		b.WriteString("(allow file-write* (literal \"/dev/null\"))\n")
		b.WriteString("(allow file-write* (subpath \"/private/tmp\"))\n")
		b.WriteString("(allow file-write* (subpath \"/var/tmp\"))\n")
	}

	if cfg.Network == "none" {
		b.WriteString("(deny network*)\n")
	}

	return b.String()
}

func buildCmd(ctx context.Context, cfg Config, name string, args ...string) *exec.Cmd {
	if !Available || !cfg.Active() {
		return exec.CommandContext(ctx, name, args...)
	}
	profile := buildProfile(cfg)
	sbArgs := []string{"-p", profile, "--", name}
	sbArgs = append(sbArgs, args...)
	return exec.CommandContext(ctx, sandboxExecPath, sbArgs...)
}
