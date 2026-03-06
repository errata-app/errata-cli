package pkg_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPkgPackages_NoInternalImports ensures that public packages under pkg/
// do not import any internal/ packages. This is the contract that makes them
// safe for external repos to vendor or go-get.
func TestPkgPackages_NoInternalImports(t *testing.T) {
	pkgs := []string{
		"github.com/suarezc/errata/pkg/recipe",
		"github.com/suarezc/errata/pkg/recipestore",
	}

	for _, pkg := range pkgs {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
		}
		for dep := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			if strings.Contains(dep, "suarezc/errata/internal/") {
				t.Errorf("%s imports internal package %s", pkg, dep)
			}
		}
	}
}
