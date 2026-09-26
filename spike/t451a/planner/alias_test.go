package planner

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfiguredAliasPackageCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       string
		rootInputs []string
		wantError  string
	}{
		{
			name:       "non-Go dependency alias",
			kind:       "constraint_value",
			rootInputs: []string{"@@//common:lib", "@@//tool:alias"},
		},
		{
			name:       "requested alias has no package",
			kind:       "constraint_value",
			rootInputs: []string{"@@//tool:alias"},
			wantError:  "requested root has no package-load unit",
		},
		{
			name:       "Go dependency projection missing behind alias",
			kind:       "go_library",
			rootInputs: []string{"@@//common:lib", "@@//tool:alias"},
			wantError:  "configured go_library has no package-load unit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq, aq, projections := syntheticPlan(t)
			root := canonicalLabel(Roots()[0])
			cq = bytes.Replace(cq, cqTarget(root, "alias", "@@//common:lib"), cqTarget(root, "alias", tc.rootInputs...), 1)
			cq = append(cq, cqTarget("@@//tool:alias", "alias", "@@//tool:actual")...)
			cq = append(cq, cqTarget("@@//tool:actual", tc.kind)...)
			plan, err := Assemble(cq, aq, projections)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got %v, want %s", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Units) != 1 || len(plan.Documents) != 1 {
				t.Fatal("non-Go alias changed package or document membership")
			}
			found := false
			for _, target := range plan.Targets {
				if target.Label == "@@//tool:alias" {
					found = true
					if len(target.Units) != 0 {
						t.Fatal("non-Go alias acquired a package-load unit")
					}
				}
				if target.Label == root && len(target.Units) != 1 {
					t.Fatal("requested Go alias lost its package-load unit")
				}
			}
			if !found {
				t.Fatal("non-Go alias missing from configured universe")
			}
		})
	}
}
