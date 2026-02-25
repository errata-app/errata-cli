package sandbox

import (
	"context"
	"strings"
	"testing"
)

// ─── Config.Active ────────────────────────────────────────────────────────────

func TestConfig_Active_Unrestricted(t *testing.T) {
	cases := []Config{
		{},
		{Filesystem: "unrestricted"},
		{Filesystem: "unrestricted", Network: "full"},
		{Network: "full"},
	}
	for _, c := range cases {
		if c.Active() {
			t.Errorf("expected Active()=false for %+v", c)
		}
	}
}

func TestConfig_Active_Restricted(t *testing.T) {
	cases := []Config{
		{Filesystem: "project_only"},
		{Filesystem: "read_only"},
		{Network: "none"},
		{Filesystem: "project_only", Network: "none"},
		{Filesystem: "read_only", Network: "none"},
	}
	for _, c := range cases {
		if !c.Active() {
			t.Errorf("expected Active()=true for %+v", c)
		}
	}
}

// ─── Context round-trip ───────────────────────────────────────────────────────

func TestWithConfig_RoundTrip(t *testing.T) {
	want := Config{
		Filesystem:  "project_only",
		Network:     "none",
		ProjectRoot: "/tmp/myproject",
	}
	ctx := WithConfig(context.Background(), want)
	got, ok := ConfigFromContext(ctx)
	if !ok {
		t.Fatal("ConfigFromContext returned ok=false")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestConfigFromContext_Missing(t *testing.T) {
	_, ok := ConfigFromContext(context.Background())
	if ok {
		t.Error("expected ok=false for context without sandbox config")
	}
}

// ─── BuildCmd — inactive config ───────────────────────────────────────────────

func TestBuildCmd_InactiveConfig(t *testing.T) {
	cfg := Config{Filesystem: "unrestricted", Network: "full"}
	cmd := BuildCmd(context.Background(), cfg, "sh", "-c", "echo hi")
	// Should be a plain sh invocation, not sandbox-exec or bwrap.
	if len(cmd.Args) == 0 {
		t.Fatal("cmd.Args is empty")
	}
	if !strings.HasSuffix(cmd.Args[0], "sh") {
		t.Errorf("expected executable to be sh, got %q", cmd.Args[0])
	}
}

func TestBuildCmd_ZeroConfig(t *testing.T) {
	cfg := Config{}
	cmd := BuildCmd(context.Background(), cfg, "sh", "-c", "echo hi")
	if len(cmd.Args) == 0 {
		t.Fatal("cmd.Args is empty")
	}
	if !strings.HasSuffix(cmd.Args[0], "sh") {
		t.Errorf("expected executable to be sh for zero Config, got %q", cmd.Args[0])
	}
}

// ─── BuildCmd — active config (platform-specific) ────────────────────────────

func TestBuildCmd_ActiveConfig_ExecutableSet(t *testing.T) {
	if !Available {
		t.Skip("OS-level sandboxing not available on this platform")
	}
	cfg := Config{Filesystem: "read_only"}
	cmd := BuildCmd(context.Background(), cfg, "sh", "-c", "echo hi")
	if len(cmd.Args) == 0 {
		t.Fatal("cmd.Args is empty")
	}
	// On darwin the executable should be sandbox-exec; on Linux bwrap.
	exe := cmd.Args[0]
	if !strings.Contains(exe, "sandbox-exec") && !strings.Contains(exe, "bwrap") {
		t.Errorf("expected sandbox-exec or bwrap, got %q", exe)
	}
}

// ─── Darwin-specific profile tests ───────────────────────────────────────────

// These tests call the unexported buildProfile which only exists in sandbox_darwin.go.
// On non-darwin builds this file is excluded so the tests below only compile on darwin.
