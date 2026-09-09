//go:build darwin || linux

package dispatchadmission

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestIndexOfferAdmittedEnvironment(t *testing.T) {
	base := productionTestRecord().Tools[2].Environment
	selected := append(slices.Clone(base), IndexOfferEnvironment+"=v1", "ZOEKT_DISABLE_CATFILE_BATCH=true")
	for _, test := range []struct {
		name, role, semantic string
		environment          []string
		valid                bool
	}{
		{"legacy", "zoekt-git-index", "", base, true},
		{"selected", "zoekt-git-index", ProductionSemanticV3, selected, true},
		{"legacy selected flag", "zoekt-git-index", "", selected, false},
		{"wrong role", "git", ProductionSemanticV3, selected, false},
		{"missing pair", "zoekt-git-index", ProductionSemanticV3, selected[:len(selected)-1], false},
		{"duplicate", "zoekt-git-index", ProductionSemanticV3, append(slices.Clone(selected), IndexOfferEnvironment+"=v1"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validProductionToolEnvironment(test.environment, test.role, test.semantic); got != test.valid {
				t.Fatal(got)
			}
		})
	}
	for _, mode := range []string{"selected", "missing", "catfile"} {
		t.Run(mode, func(t *testing.T) {
			record := productionTestRecord()
			tool := record.Tools[2]
			tool.Environment = slices.Clone(selected)
			if mode == "missing" {
				tool.Environment = base
			}
			if mode == "catfile" {
				tool.Environment[len(tool.Environment)-1] = "ZOEKT_DISABLE_CATFILE_BATCH=false"
			}
			controller, client, server := paired(t, productionTestConfig(record))
			setPipedTestRuntime(t, &ProductionLifetime{program: ProgramPhebs, semanticMode: ProductionSemanticV3, client: client, tools: map[string]ProductionToolBinding{"zoekt-git-index": tool}})
			command := exec.CommandContext(t.Context(), "/bin/sh", "-c", `test "$PHEBS_T422_INDEX_OFFERS" = v1 && test "$ZOEKT_DISABLE_CATFILE_BATCH" = true`)
			command.Env = []string{"PHEBS_T422_INDEX_OFFERS=untrusted"}
			err := RunProduction(t.Context(), SiteIndexBuild, command)
			if mode == "selected" {
				if err != nil || command.ProcessState == nil || !command.ProcessState.Success() {
					t.Fatal(err)
				}
				if strings.Contains(strings.Join(command.Env, "\n"), "untrusted") {
					t.Fatal("ambient mode survived")
				}
				if snapshot := finishPair(t, controller, client, server); snapshot.Attempts != 1 {
					t.Fatal(snapshot)
				}
			} else {
				if err == nil || command.Process != nil || client.Context().Err() == nil {
					t.Fatal("invalid mode reached native start", err)
				}
				if err := <-server; err == nil {
					t.Fatal("refusal not sticky")
				}
			}
		})
	}
}
