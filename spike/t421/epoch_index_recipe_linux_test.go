//go:build linux

package t421

import (
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestLinuxEpochIndexRecipeBindingContinuity(t *testing.T) {
	want := []dispatchadmission.ProductionToolBinding{{Role: "git", Path: "/private/git", Environment: []string{"HOME=/private"}, RuntimeRecipe: dispatchadmission.ProductionLinuxForegroundGitV2}, {Role: "surreal", Path: "/private/surreal"}, {Role: "zoekt-git-index", Path: "/private/zoekt", Environment: []string{"GOGC=50"}, RuntimeRecipe: dispatchadmission.ProductionLinuxForegroundGitV2}}
	if !sameExecutionToolBindings(want, want, "") || sameExecutionToolBindings(nil, nil, "") {
		t.Fatal("closed native binding shape changed")
	}
	for _, test := range []struct {
		name string
		edit func([]dispatchadmission.ProductionToolBinding)
	}{
		{"missing-recipe", func(v []dispatchadmission.ProductionToolBinding) { v[2].RuntimeRecipe = "" }},
		{"missing-git-recipe", func(v []dispatchadmission.ProductionToolBinding) { v[0].RuntimeRecipe = "" }},
		{"foreign-recipe", func(v []dispatchadmission.ProductionToolBinding) { v[2].RuntimeRecipe = "foreign" }},
		{"old-recipe", func(v []dispatchadmission.ProductionToolBinding) {
			v[2].RuntimeRecipe = dispatchadmission.ProductionLinuxForegroundGitV1
		}},
		{"gc-drift", func(v []dispatchadmission.ProductionToolBinding) { v[2].Environment = []string{"GOGC=100"} }},
		{"role", func(v []dispatchadmission.ProductionToolBinding) { v[2].Role = "git" }},
		{"path", func(v []dispatchadmission.ProductionToolBinding) { v[2].Path = "/private/replaced" }},
		{"environment", func(v []dispatchadmission.ProductionToolBinding) { v[0].Environment = []string{"HOME=/foreign"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := slices.Clone(want)
			test.edit(changed)
			if sameExecutionToolBindings(changed, want, "") || sameExecutionToolBindings(want, changed, "") {
				t.Fatal("changed or missing resource-issued binding accepted")
			}
		})
	}
	missing := slices.Clone(want)
	missing[2].RuntimeRecipe = ""
	if sameExecutionToolBindings(missing, missing, "") {
		t.Fatal("native missing-recipe fallback accepted")
	}
	if _, tools, _, err := (&linuxEpochCoordinator{}).tools(t.Context(), 1); err == nil || len(tools) != 0 {
		t.Fatal("unissued owner produced runtime recipe")
	}
}

func TestLinuxEpochCheckpointRecipeBindingContinuity(t *testing.T) {
	_, record, _ := epochEngineLaunchTestRecord(3, dispatchadmission.EngineMeasurementCheckpointV2)
	want := record.Tools
	if !sameExecutionToolBindings(want, want, dispatchadmission.EngineMeasurementCheckpointV2) ||
		sameExecutionToolBindings(want, want, "") || sameExecutionToolBindings(want, want, "unknown") {
		t.Fatal("V3 tool bindings manufactured their own measurement selection")
	}
	for _, index := range []int{0, 2} {
		changed := slices.Clone(want)
		changed[index].RuntimeRecipe = dispatchadmission.ProductionLinuxForegroundGitV2
		if sameExecutionToolBindings(changed, want, dispatchadmission.EngineMeasurementCheckpointV2) ||
			sameExecutionToolBindings(want, changed, dispatchadmission.EngineMeasurementCheckpointV2) ||
			sameExecutionToolBindings(changed, changed, dispatchadmission.EngineMeasurementCheckpointV2) {
			t.Fatal("mismatched checkpoint runtime accepted", index)
		}
	}
	changed := slices.Clone(want)
	changed[1].Path += "-replaced"
	if sameExecutionToolBindings(changed, want, dispatchadmission.EngineMeasurementCheckpointV2) {
		t.Fatal("unchanged recipe hid changed native image path")
	}
}
