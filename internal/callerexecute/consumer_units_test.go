package callerexecute

import (
	"errors"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestCallerUnitsRuntimeRejectsStoreCurrentOldPack(t *testing.T) {
	fixture := newWorkerHarness(t, 1)
	current, _, err := currentJobAuthority(t.Context(), fixture.state, fixture.worker.registry, fixture.state.repo.Name)
	if err != nil || current == nil {
		t.Fatalf("current positive authority=%v err=%v", current != nil, err)
	}
	fixture.state.resolver.SourceLanePolicy = current.stored.SourceLanePolicy
	reader := &PublicationReader{state: fixture.state, adapters: fixture.worker.registry}
	if _, err := reader.boundResolver(t.Context(), current.stored); err != nil {
		t.Fatalf("current reader positive=%v", err)
	}
	original := slices.Clone(fixture.state.resolver.ResolverPacks)
	for _, test := range []struct {
		name  string
		packs []store.ResolverCatalogPack
	}{
		{"old_pack", []store.ResolverCatalogPack{{Name: resolvermaterialize.GoModulePackName, Version: "1.1.1"}, {Name: resolvermaterialize.GRPCGeneratedPackName, Version: "1.1.1"}}},
		{"missing_unit_pack", nil},
		{"duplicate_pack", []store.ResolverCatalogPack{original[0], original[0]}},
		{"foreign_pack", []store.ResolverCatalogPack{original[0], {Name: resolvermaterialize.ThriftGeneratedPackName, Version: resolvermaterialize.PackVersion}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture.state.resolver.ResolverPacks = test.packs
			got, inactive, err := currentJobAuthority(t.Context(), fixture.state, fixture.worker.registry, fixture.state.repo.Name)
			if err != nil || inactive || got != nil {
				t.Fatalf("stale store-current worker authority=%v inactive=%v err=%v", got, inactive, err)
			}
			if _, err := reader.boundResolver(t.Context(), current.stored); !errors.Is(err, errPublicationReadTransition) {
				t.Fatalf("stale store-current reader admission=%v", err)
			}
		})
	}
}
