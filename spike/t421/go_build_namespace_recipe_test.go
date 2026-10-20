package t421

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestLinuxGoBuildHelperReferenceInventory(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	path, module, version, sum, recipe, err := referenceToolRoleForSchema(linuxGoBuildRole, linuxGoBuildRecipe, commit)
	if err != nil || path != "github.com/bmeddeb/phebs/spike/t421/cmd/go-build-helper" || module != "" || version != "" || sum != "" || recipe != "" {
		t.Fatal("fixed helper role", err)
	}
	for _, test := range []struct{ role, schema, commit string }{
		{linuxGoBuildRole, "", commit}, {linuxGoBuildRole, PlanV3Schema, commit}, {linuxInputTreeRole, linuxGoBuildRecipe, commit},
		{linuxGoBuildRole, linuxInputTreeRecipe, commit}, {"phebs", linuxGoBuildRecipe, commit}, {linuxGoBuildRole, linuxGoBuildRecipe, "invalid"},
	} {
		if _, _, _, _, _, err := referenceToolRoleForSchema(test.role, test.schema, test.commit); err == nil {
			t.Fatal("helper widened historical or other recipe", test)
		}
	}
	if knownPlanSchema(linuxGoBuildRecipe) {
		t.Fatal("helper recipe became a ceremony plan")
	}
	info := &debug.BuildInfo{GoVersion: runtime.Version(), Path: path, Main: debug.Module{Path: "github.com/bmeddeb/phebs"}, Settings: []debug.BuildSetting{
		{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"}, {Key: "GOOS", Value: runtime.GOOS}, {Key: "GOARCH", Value: runtime.GOARCH},
		{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: "false"},
	}}
	if validateReferenceBuildInfoForSchema(info, linuxGoBuildRole, linuxGoBuildRecipe, path, commit, "", "", "", nil) != nil {
		t.Fatal("exact helper identity refused")
	}
	info.Settings[len(info.Settings)-1].Value = "true"
	if validateReferenceBuildInfoForSchema(info, linuxGoBuildRole, linuxGoBuildRecipe, path, commit, "", "", "", nil) == nil {
		t.Fatal("dirty helper admitted")
	}
	info.Settings[len(info.Settings)-1].Value = "false"
	info.Settings[len(info.Settings)-2].Value = strings.Repeat("a", 40)
	if validateReferenceBuildInfoForSchema(info, linuxGoBuildRole, linuxGoBuildRecipe, path, commit, "", "", "", nil) == nil {
		t.Fatal("different helper source admitted")
	}
}
