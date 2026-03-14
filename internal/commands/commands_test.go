package commands_test

import (
	"strings"
	"testing"

	"github.com/errata-app/errata-cli/internal/commands"
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

func TestAll_ContainsExit(t *testing.T) {
	for _, c := range commands.All {
		if c.Name == "/exit" {
			return
		}
	}
	t.Error("/exit not found in All")
}
