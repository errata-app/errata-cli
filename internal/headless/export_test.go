package headless

import "github.com/suarezc/errata/internal/recipe"

// Test-only exports for internal functions.

func RecipeName(rec *recipe.Recipe) string { return recipeName(rec) }
func Truncate(s string, max int) string    { return truncate(s, max) }

// CreateModelWorkDirs wraps createModelWorkDirs for testing.
// Signature: (projectDir, baseDir, adapters) → (dirs, base, cleanup, err)
var CreateModelWorkDirs = createModelWorkDirs
var DiffWorktree = diffWorktree
