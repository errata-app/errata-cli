package headless

import "github.com/suarezc/errata/internal/recipe"

// Test-only exports for internal functions.

func RecipeName(rec *recipe.Recipe) string { return recipeName(rec) }
func Truncate(s string, max int) string    { return truncate(s, max) }

var CreateModelWorkDirs = createModelWorkDirs
var DiffWorktree = diffWorktree
