//go:build linux

package t421

import (
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestLinuxEpochIndexRecipeBindingContinuity(t *testing.T) {
	want := []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/private/git", Environment: []string{"HOME=/private"}}, {Role: "surreal", Path: "/private/surreal"}, {Role: "zoekt-git-index", Path: "/private/zoekt", RuntimeRecipe: dispatchadmission.ProductionLinuxSerialIndexV1}}
	if !sameExecutionToolBindings(want, want) || sameExecutionToolBindings(nil, nil) {
		t.Fatal("closed native binding shape changed")
	}
	for _, test := range []struct {
		name string
		edit func([]dispatchadmission.ProductionToolBinding)
	}{
		{"missing-recipe", func(v []dispatchadmission.ProductionToolBinding) { v[2].RuntimeRecipe = "" }},
		{"foreign-recipe", func(v []dispatchadmission.ProductionToolBinding) { v[2].RuntimeRecipe = "foreign" }},
		{"role", func(v []dispatchadmission.ProductionToolBinding) { v[2].Role = "git" }},
		{"path", func(v []dispatchadmission.ProductionToolBinding) { v[2].Path = "/private/replaced" }},
		{"environment", func(v []dispatchadmission.ProductionToolBinding) { v[0].Environment = []string{"HOME=/foreign"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := slices.Clone(want)
			test.edit(changed)
			if sameExecutionToolBindings(changed, want) || sameExecutionToolBindings(want, changed) {
				t.Fatal("changed or missing resource-issued binding accepted")
			}
		})
	}
	missing := slices.Clone(want)
	missing[2].RuntimeRecipe = ""
	if sameExecutionToolBindings(missing, missing) {
		t.Fatal("native missing-recipe fallback accepted")
	}
	if _, tools, _, err := (&linuxEpochCoordinator{}).tools(t.Context(), 1); err == nil || len(tools) != 0 {
		t.Fatal("unissued owner produced runtime recipe")
	}
}
