//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

func TestBuildProfile_ProjectOnly(t *testing.T) {
	cfg := Config{Filesystem: "project_only", ProjectRoot: "/Users/user/myproject"}
	p := buildProfile(cfg)
	if !strings.Contains(p, "(deny file-write*)") {
		t.Error("expected deny file-write*")
	}
	if !strings.Contains(p, `"/Users/user/myproject"`) {
		t.Errorf("expected project root in profile, got:\n%s", p)
	}
	if strings.Contains(p, "(deny network*)") {
		t.Error("network:full should not add deny network*")
	}
}

func TestBuildProfile_ReadOnly(t *testing.T) {
	cfg := Config{Filesystem: "read_only"}
	p := buildProfile(cfg)
	if !strings.Contains(p, "(deny file-write*)") {
		t.Error("expected deny file-write*")
	}
	// No project-root allow rule should appear.
	if strings.Contains(p, "(allow file-write* (subpath \"/Users") {
		t.Error("read_only should not allow writes to a project path")
	}
	// Temp dirs should still be allowed.
	if !strings.Contains(p, "/private/tmp") {
		t.Error("expected /private/tmp allow in read_only profile")
	}
}

func TestBuildProfile_NetworkNone(t *testing.T) {
	cfg := Config{Network: "none"}
	p := buildProfile(cfg)
	if !strings.Contains(p, "(deny network*)") {
		t.Error("expected deny network* for network:none")
	}
	// No filesystem deny when filesystem is unrestricted.
	if strings.Contains(p, "(deny file-write*)") {
		t.Error("unexpected deny file-write* for unrestricted filesystem")
	}
}

func TestBuildProfile_Combined(t *testing.T) {
	cfg := Config{
		Filesystem:  "project_only",
		Network:     "none",
		ProjectRoot: "/tmp/proj",
	}
	p := buildProfile(cfg)
	if !strings.Contains(p, "(deny file-write*)") {
		t.Error("missing deny file-write*")
	}
	if !strings.Contains(p, "(deny network*)") {
		t.Error("missing deny network*")
	}
	if !strings.Contains(p, `"/tmp/proj"`) {
		t.Error("missing project root allow rule")
	}
}
