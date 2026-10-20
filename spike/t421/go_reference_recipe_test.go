package t421

import "testing"

func TestLinuxGoReferenceHelperRoleIsolation(t *testing.T) {
	const commit = "0123456789012345678901234567890123456789"
	path, module, version, sum, recipe, err := referenceToolRoleForSchema(linuxGoReferenceRole, linuxGoReferenceRecipe, commit)
	if err != nil || path != "github.com/bmeddeb/phebs/spike/t421/cmd/go-reference-helper" || module != "" || version != "" || sum != "" || recipe != "" {
		t.Fatal("distinct reference helper role refused", err)
	}
	for _, test := range []struct{ role, schema, source string }{
		{linuxGoReferenceRole, "", commit}, {linuxGoReferenceRole, PlanV3Schema, commit},
		{linuxGoReferenceRole, linuxGoBuildRecipe, commit}, {linuxGoBuildRole, linuxGoReferenceRecipe, commit},
		{linuxInputTreeRole, linuxGoReferenceRecipe, commit}, {"phebs", linuxGoReferenceRecipe, commit},
		{linuxGoReferenceRole, linuxGoReferenceRecipe, "invalid"},
	} {
		if _, _, _, _, _, err := referenceToolRoleForSchema(test.role, test.schema, test.source); err == nil {
			t.Fatal("reference helper widened another recipe", test)
		}
	}
	if knownPlanSchema(linuxGoReferenceRecipe) {
		t.Fatal("private helper became a ceremony plan")
	}
}
