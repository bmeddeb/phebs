//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"testing"
)

func indexGCTestRecord(producer uint32) ProductionBootstrap {
	record := foregroundGitTestRecord(producer)
	record.Tools[0].RuntimeRecipe = ProductionLinuxForegroundGitV2
	record.Tools[2].RuntimeRecipe = ProductionLinuxForegroundGitV2
	record.Tools[2].Environment = append(record.Tools[2].Environment, "GOGC=50")
	return record
}

func TestProductionLinuxIndexGCClosedRecipe(t *testing.T) {
	for _, producer := range []uint32{2, 3, 4} {
		record := indexGCTestRecord(producer)
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			if !errors.Is(record.validate(), ErrProductionBootstrap) {
				t.Fatal("foreign host admitted GC recipe")
			}
			continue
		}
		if err := record.validate(); err != nil {
			t.Fatal("closed index GC recipe refused", producer, err)
		}
		args := serialIndexTestArgs(record.Tools[2].Path)
		got, err := productionIndexRuntimeArgs(SiteIndexBuild, record.Tools[2], args)
		if err != nil || len(got) != 10 || got[8] != "-parallelism=1" || got[9] != args[8] {
			t.Fatal("GC recipe changed HEAD workload", got, err)
		}
		// Engine HOLD/RELEASE authority and maxima must survive the new recipe.
		record.Workspace = engineMeasurementTestRecord(producer).Workspace
		record.EngineMeasurements = engineMeasurementMaximum(producer)
		if err := record.validate(); err != nil {
			t.Fatal("GC recipe lost engine coordination", err)
		}
	}
}

func TestProductionLinuxIndexGCRefusesDriftBeforeStart(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"missing", func(r *ProductionBootstrap) {
			r.Tools[2].Environment = r.Tools[2].Environment[:len(r.Tools[2].Environment)-1]
		}},
		{"duplicate", func(r *ProductionBootstrap) { r.Tools[2].Environment = append(r.Tools[2].Environment, "GOGC=50") }},
		{"disabled", func(r *ProductionBootstrap) { r.Tools[2].Environment[len(r.Tools[2].Environment)-1] = "GOGC=off" }},
		{"default", func(r *ProductionBootstrap) { r.Tools[2].Environment[len(r.Tools[2].Environment)-1] = "GOGC=100" }},
		{"alternate", func(r *ProductionBootstrap) { r.Tools[2].Environment[len(r.Tools[2].Environment)-1] = "GOGC=050" }},
		{"empty", func(r *ProductionBootstrap) { r.Tools[2].Environment[len(r.Tools[2].Environment)-1] = "GOGC=" }},
		{"memory-limit", func(r *ProductionBootstrap) {
			r.Tools[2].Environment = append(r.Tools[2].Environment, "GOMEMLIMIT=1GiB")
		}},
		{"unpaired", func(r *ProductionBootstrap) { r.Tools[0].RuntimeRecipe = ProductionLinuxForegroundGitV1 }},
		{"old-recipe", func(r *ProductionBootstrap) { r.Tools[2].RuntimeRecipe = ProductionLinuxForegroundGitV1 }},
		{"absent-recipe", func(r *ProductionBootstrap) { r.Tools[2].RuntimeRecipe = "" }},
		{"git-gc", func(r *ProductionBootstrap) { r.Tools[0].Environment = append(r.Tools[0].Environment, "GOGC=50") }},
		{"surreal-gc", func(r *ProductionBootstrap) { r.Tools[1].Environment = append(r.Tools[1].Environment, "GOGC=50") }},
		{"ordinary", func(r *ProductionBootstrap) { r.SemanticMode = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := indexGCTestRecord(2)
			test.edit(&record)
			if !errors.Is(record.validate(), ErrProductionBootstrap) {
				t.Fatal("changed GC recipe admitted")
			}
		})
	}
	for _, value := range []string{"", "GOGC=off", "GOGC=100", "GOGC=050", "GOGC=50\n"} {
		t.Run("start-"+value, func(t *testing.T) {
			tool := indexGCTestRecord(2).Tools[2]
			tool.Environment = tool.Environment[:len(tool.Environment)-1]
			if value != "" {
				tool.Environment = append(tool.Environment, value)
			}
			args := serialIndexTestArgs(tool.Path)
			command := &exec.Cmd{Path: tool.Path, Args: slices.Clone(args)}
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			client := &Client{ctx: ctx, cancel: cancel, changed: make(chan struct{})}
			lifetime := &ProductionLifetime{program: ProgramPhebs, semanticMode: ProductionSemanticV3, client: client, tools: map[string]ProductionToolBinding{tool.Role: tool}}
			_, err := startProductionCommandWithEnv(t.Context(), lifetime, SiteIndexBuild, tool.Role, command, nil)
			if !errors.Is(err, ErrProductionBootstrap) || command.Process != nil || !slices.Equal(command.Args, args) || !errors.Is(client.err, ErrProductionBootstrap) || ctx.Err() == nil {
				t.Fatal("changed GC setting reached native Start or lost sticky refusal", err)
			}
		})
	}
	for _, record := range []ProductionBootstrap{serialIndexTestRecord(2), foregroundGitTestRecord(2)} {
		if indexGCSelected(record.Tools[2].Environment) || runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && record.validate() != nil {
			t.Fatal("historical recipe changed")
		}
	}
}
