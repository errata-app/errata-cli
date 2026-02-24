package commands_test

import (
	"strings"
	"testing"

	"github.com/suarezc/errata/internal/commands"
)

func TestAll_NamesStartWithSlash(t *testing.T) {
	for _, c := range commands.All {
		if !strings.HasPrefix(c.Name, "/") {
			t.Errorf("command %q does not start with '/'", c.Name)
		}
	}
}

func TestAll_NamesUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, c := range commands.All {
		if seen[c.Name] {
			t.Errorf("duplicate command name: %q", c.Name)
		}
		seen[c.Name] = true
	}
}

func TestAll_DescriptionsNonEmpty(t *testing.T) {
	for _, c := range commands.All {
		if strings.TrimSpace(c.Desc) == "" {
			t.Errorf("command %q has empty description", c.Name)
		}
	}
}

func TestWeb_ExcludesTUIOnly(t *testing.T) {
	web := commands.Web()
	for _, c := range web {
		if c.TUIOnly {
			t.Errorf("Web() returned TUIOnly command %q", c.Name)
		}
	}
}

func TestWeb_SubsetOfAll(t *testing.T) {
	allNames := make(map[string]bool)
	for _, c := range commands.All {
		allNames[c.Name] = true
	}
	for _, c := range commands.Web() {
		if !allNames[c.Name] {
			t.Errorf("Web() returned unknown command %q", c.Name)
		}
	}
}

func TestWeb_SmallerThanAll(t *testing.T) {
	// At least /exit is TUIOnly, so Web() must be strictly smaller.
	if len(commands.Web()) >= len(commands.All) {
		t.Errorf("expected Web() (%d) to be smaller than All (%d)",
			len(commands.Web()), len(commands.All))
	}
}

func TestAll_ExitIsTUIOnly(t *testing.T) {
	for _, c := range commands.All {
		if c.Name == "/exit" && !c.TUIOnly {
			t.Error("/exit should be TUIOnly")
		}
	}
}
