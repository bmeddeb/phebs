package main

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/config"
)

// TestCallerMapPackIdentityAndRecipeFrozen pins the exact T47.2a frozen tuple:
// the stable pack identity `phebs.grpc.caller.go` (docs/PROTO_GRPC_PACK_CARDS.md,
// "Exact Go gRPC callers") and the fixed extractor set that implements it. The
// declaration extractor travels with the caller extractor on purpose, so
// resolved callers keep their repository-committed declaration lineage without
// depending on the experimental protobuf umbrella.
func TestCallerMapPackIdentityAndRecipeFrozen(t *testing.T) {
	if callerMapPackID != "phebs.grpc.caller.go" {
		t.Fatalf("caller map pack id = %q, want phebs.grpc.caller.go", callerMapPackID)
	}
	recipe := callerMapRecipe()
	want := []struct{ domain, version string }{
		{"proto-contract", "3.0.0"},
		{"grpc-caller", "1.5.0"},
	}
	if len(recipe) != len(want) {
		t.Fatalf("caller map recipe has %d extractors, want %d", len(recipe), len(want))
	}
	for index, expected := range want {
		if recipe[index].Domain() != expected.domain || recipe[index].Version() != expected.version {
			t.Fatalf("caller map recipe[%d] = %s@%s, want %s@%s",
				index, recipe[index].Domain(), recipe[index].Version(),
				expected.domain, expected.version)
		}
	}
}

// TestCallerMapRecipeStaysUnboundUntilReleased pins the T47.5 handoff: the
// recipe is deliberately absent from packRecipes until the same PR that records
// its first passing signed released record binds it. A verified released record
// for the caller-map pack must therefore still refuse startup as an unbound
// release, so Caller Map cannot reach ordinary activation through a record
// alone.
func TestCallerMapRecipeStaysUnboundUntilReleased(t *testing.T) {
	if _, bound := packRecipes[callerMapPackID]; bound {
		t.Fatal("callerMapRecipe must not be bound before its first released record (T47.5 owns the binding)")
	}
	dir := t.TempDir()
	public, release := writeReleasedRecord(t, dir, callerMapPackID, "key-1")
	bindReleaseLoad(t, release)
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
	if _, err := releasedExtractors(context.Background(), cfg); err == nil {
		t.Fatal("a verified released caller-map record without its in-tree binding must refuse startup")
	}
}

// TestCallerMapRegistrationFollowsExtractorSet pins the T47.3 registration
// semantics at the exact registry boundary: the Caller Map surface is enabled
// by whichever admission supplied a caller-adapter domain — the released
// recipe or the experimental-dark switches — and never by evidence that cannot
// produce a caller pair. Thrift-field- or Kafka-only admissions must stay
// visibly unavailable.
func TestCallerMapRegistrationFollowsExtractorSet(t *testing.T) {
	released, err := callerexecute.NewRegistry(mergeExtractors(nil, callerMapRecipe()))
	if err != nil {
		t.Fatalf("released registry: %v", err)
	}
	if !released.Enabled() {
		t.Fatal("a released caller-map recipe must enable the Caller Map surface")
	}
	adapters := released.Adapters()
	if len(adapters) != 1 || adapters[0] != (callerexecute.Adapter{
		Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc",
	}) {
		t.Fatalf("released adapters = %+v, want grpc-caller@1.5.0/grpc", adapters)
	}
	domains := released.CandidateDomains()
	if len(domains) != 2 ||
		domains[0].Domain != "grpc-caller" || domains[0].Version != "1.5.0" ||
		domains[1].Domain != "proto-contract" || domains[1].Version != "3.0.0" {
		t.Fatalf("released candidate domains = %+v, want grpc-caller@1.5.0 then proto-contract@3.0.0", domains)
	}

	for _, testCase := range []struct {
		name       string
		proto      bool
		thrift     bool
		thriftFld  bool
		kafka      bool
		wantEnable bool
	}{
		{name: "proto umbrella only", proto: true, wantEnable: true},
		{name: "thrift umbrella only", thrift: true, wantEnable: true},
		{name: "thrift-field only", thriftFld: true, wantEnable: false},
		{name: "kafka only", kafka: true, wantEnable: false},
		{name: "thrift-field and kafka", thriftFld: true, kafka: true, wantEnable: false},
		{name: "all off", wantEnable: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			registry, err := callerexecute.NewRegistry(mergeExtractors(
				evidenceExtractors(testCase.proto, testCase.thrift, testCase.thriftFld, testCase.kafka),
				nil,
			))
			if err != nil {
				t.Fatalf("dark registry: %v", err)
			}
			if registry.Enabled() != testCase.wantEnable {
				t.Fatalf("enabled = %v, want %v (adapters %+v)",
					registry.Enabled(), testCase.wantEnable, registry.Adapters())
			}
		})
	}
}

// TestMergeExtractorsCallerMapRecipeGovernsMixedProvenance proves the
// mixed-provenance answer for the T48.4b finding: when the experimental
// protobuf umbrella and the released caller-map recipe both name the
// declaration and caller domains, the released recipe governs and the merged
// set carries exactly one extractor per domain. A single caller adapter means a
// single expected pair per candidate leaf: no duplicate generation identity and
// no mixed dark/released evidence plane.
func TestMergeExtractorsCallerMapRecipeGovernsMixedProvenance(t *testing.T) {
	dark := evidenceExtractors(true, false, false, false)
	merged := mergeExtractors(dark, callerMapRecipe())
	counts := map[string]int{}
	versions := map[string]string{}
	for _, extractor := range merged {
		counts[extractor.Domain()]++
		versions[extractor.Domain()] = extractor.Version()
	}
	if counts["proto-contract"] != 1 || versions["proto-contract"] != "3.0.0" {
		t.Fatalf("proto-contract = %d at %q, want exactly one at 3.0.0 (merged %v)",
			counts["proto-contract"], versions["proto-contract"], merged)
	}
	if counts["grpc-caller"] != 1 || versions["grpc-caller"] != "1.5.0" {
		t.Fatalf("grpc-caller = %d at %q, want exactly one at 1.5.0 (merged %v)",
			counts["grpc-caller"], versions["grpc-caller"], merged)
	}
	registry, err := callerexecute.NewRegistry(merged)
	if err != nil {
		t.Fatalf("mixed registry: %v", err)
	}
	adapters := registry.Adapters()
	if len(adapters) != 1 || adapters[0] != (callerexecute.Adapter{
		Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc",
	}) {
		t.Fatalf("mixed adapters = %+v, want exactly grpc-caller@1.5.0/grpc", adapters)
	}
}
