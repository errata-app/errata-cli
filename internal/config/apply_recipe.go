package config

import (
	"github.com/errata-app/errata-cli/pkg/recipe"
)

// ApplyRecipe overlays recipe model settings onto cfg.
// Only ActiveModels is set from the recipe — all other recipe-derived
// settings are read directly from the recipe at run time.
func ApplyRecipe(r *recipe.Recipe, cfg *Config) {
	if len(r.Models) > 0 {
		cfg.ActiveModels = r.Models
	}
}
