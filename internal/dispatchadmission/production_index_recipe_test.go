//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func serialIndexTestRecord(producer uint32) ProductionBootstrap {
	record := productionStoreTestRecord()
	record.SemanticMode, record.InputSHA256 = ProductionSemanticV3, [32]byte{1}
	record.Producer.ID, record.Store.Producer = producer, producer
	record.Limits.Producers, record.Limits.Phases = 12, 15
	record.Store.Phases = productionStorePhases(producer)
	record.Store.Calls, record.Store.Transactions = storeaccounting.MaximumCalls, storeaccounting.MaximumTransactions
	record.Control.Phases = nil
	for phase := uint32(1); phase <= 15; phase++ {
		if record.Store.Phases&(1<<(phase-1)) != 0 {
			record.Control.Phases = append(record.Control.Phases, phase)
		}
	}
	record.Phase, record.Control.InitialPhase, record.Store.Phase = record.Control.Phases[0], record.Control.Phases[0], record.Control.Phases[0]
	record.Control.OwnerControl, record.Control.MaximumPhases = true, len(record.Control.Phases)
	record.Tools[2].Environment = append(record.Tools[2].Environment, IndexOfferEnvironment+"=v1", "ZOEKT_DISABLE_CATFILE_BATCH=true")
	record.Tools[2].RuntimeRecipe = ProductionLinuxSerialIndexV1
	return record
}

func TestProductionLinuxSerialIndexBootstrap(t *testing.T) {
	for _, producer := range []uint32{2, 3, 4} {
		record := serialIndexTestRecord(producer)
		err := record.validate()
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			if !errors.Is(err, ErrProductionBootstrap) {
				t.Fatal("foreign platform admitted Linux recipe", err)
			}
			continue
		}
		if err != nil || record.Workspace != nil {
			t.Fatal("existing server context or workspace scope changed", producer, err)
		}
	}
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"unknown", func(r *ProductionBootstrap) { r.Tools[2].RuntimeRecipe = "unknown" }},
		{"wrong-role", func(r *ProductionBootstrap) { r.Tools[0].RuntimeRecipe = ProductionLinuxSerialIndexV1 }},
		{"author", func(r *ProductionBootstrap) { r.Program = ProgramCorpusAuthor }},
		{"ordinary", func(r *ProductionBootstrap) { r.SemanticMode = "" }},
		{"missing-store", func(r *ProductionBootstrap) { r.Store = nil }},
		{"wrong-producer", func(r *ProductionBootstrap) { r.Producer.ID = 5 }},
		{"wrong-phase", func(r *ProductionBootstrap) { r.Phase = 3 }},
		{"control-shape", func(r *ProductionBootstrap) { r.Control.MaximumPhases++ }},
		{"no-owner-control", func(r *ProductionBootstrap) { r.Control.OwnerControl = false }},
		{"missing-index-mode", func(r *ProductionBootstrap) {
			r.Tools[2].Environment = r.Tools[2].Environment[:len(r.Tools[2].Environment)-2]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := serialIndexTestRecord(2)
			test.edit(&record)
			if !errors.Is(record.validate(), ErrProductionBootstrap) {
				t.Fatal("foreign recipe context admitted")
			}
		})
	}
}

func TestProductionIndexRecipeAbsentKeepsLegacyBytesAndArgs(t *testing.T) {
	tool := productionTestRecord().Tools[2]
	legacy := struct {
		Role        string
		Path        string
		Environment []string
	}{tool.Role, tool.Path, tool.Environment}
	raw, err := json.Marshal(tool)
	old, oldErr := json.Marshal(legacy)
	if err != nil || oldErr != nil || !bytes.Equal(raw, old) {
		t.Fatal("absent recipe changed legacy tool bytes", err, oldErr)
	}
	args := []string{tool.Path, "ordinary", "-parallelism=4"}
	got, err := productionIndexRuntimeArgs(999, tool, args)
	if err != nil || !reflect.DeepEqual(got, args) || &got[0] != &args[0] {
		t.Fatal("absent recipe altered ordinary args or allocated", got, err)
	}
}

func serialIndexTestArgs(path string) []string {
	return []string{path, "-index", "/private/stage", "-incremental=false", "-submodules=false", "-file_limit=2097152", "-shard_limit=104857600", "-max_trigram_count=20000", "/private/repository.git"}
}

func TestProductionLinuxSerialIndexArgvAndNoStartRefusals(t *testing.T) {
	tool := serialIndexTestRecord(2).Tools[2]
	args := serialIndexTestArgs(tool.Path)
	got, err := productionIndexRuntimeArgs(SiteIndexBuild, tool, args)
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		want := append(slices.Clone(args[:8]), "-parallelism=1", args[8])
		if err != nil || !reflect.DeepEqual(got, want) || &got[0] == &args[0] {
			t.Fatal("fixed serial recipe missing or shares caller argv", got, err)
		}
		got[0] = "caller-mutation"
		if args[0] != tool.Path {
			t.Fatal("selected argv mutated original")
		}
	} else if !errors.Is(err, ErrProductionBootstrap) {
		t.Fatal("foreign platform selected Linux args")
	}
	for _, test := range []struct {
		name string
		edit func([]string) []string
	}{
		{"missing", func(a []string) []string { return a[:8] }},
		{"extra", func(a []string) []string { return append(a, "extra") }},
		{"duplicate-serial", func(a []string) []string { return append(a[:8], "-parallelism=1", a[8]) }},
		{"parallel-four", func(a []string) []string { a[7] = "-parallelism=4"; return a }},
		{"parallel-split", func(a []string) []string { return append(a[:8], "-parallelism", "1", a[8]) }},
		{"double-dash", func(a []string) []string { a[7] = "--parallelism=1"; return a }},
		{"separator", func(a []string) []string { a[7] = "--"; return a }},
		{"branches", func(a []string) []string { return append(a[:8], "-branches=HEAD", a[8]) }},
		{"relative-stage", func(a []string) []string { a[2] = "stage"; return a }},
		{"unclean-stage", func(a []string) []string { a[2] = "/private/../stage"; return a }},
		{"relative-repo", func(a []string) []string { a[8] = "repo.git"; return a }},
		{"unclean-repo", func(a []string) []string { a[8] = "/private/../repo.git"; return a }},
		{"wrong-limit", func(a []string) []string { a[6] = "-shard_limit=209715200"; return a }},
		{"duplicate-option", func(a []string) []string { a[7] = a[6]; return a }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := test.edit(serialIndexTestArgs(tool.Path))
			command := &exec.Cmd{Path: tool.Path, Args: slices.Clone(bad)}
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			client := &Client{ctx: ctx, cancel: cancel, changed: make(chan struct{})}
			lifetime := &ProductionLifetime{program: ProgramPhebs, semanticMode: ProductionSemanticV3, client: client, tools: map[string]ProductionToolBinding{tool.Role: tool}}
			_, err := startProductionCommandWithEnv(t.Context(), lifetime, SiteIndexBuild, tool.Role, command, nil)
			if !errors.Is(err, ErrProductionBootstrap) || !errors.Is(client.err, ErrProductionBootstrap) ||
				command.Process != nil || !reflect.DeepEqual(command.Args, bad) || ctx.Err() == nil {
				t.Fatal("invalid recipe reached Start, mutated args or lost sticky refusal", err)
			}
		})
	}
	for _, site := range []uint32{0, SiteSyncGit, SiteSurrealEngine} {
		if _, err := productionIndexRuntimeArgs(site, tool, args); !errors.Is(err, ErrProductionBootstrap) {
			t.Fatal("foreign site accepted serial index recipe", site)
		}
	}
}
