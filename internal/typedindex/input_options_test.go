package typedindex

import (
	"slices"
	"testing"
)

func TestProviderDiscriminant(t *testing.T) {
	if ModuleProviderID == ProviderID || ImportProviderID == ProviderID || ModuleProviderID == ImportProviderID {
		t.Fatalf("provider discriminants collide: %q %q %q", ProviderID, ModuleProviderID, ImportProviderID)
	}
	if ModuleProviderID != "go-module-scip-v1" || ImportProviderID != "imported-artifact-scip-v1" {
		t.Fatalf("unexpected provider id constant: %q %q", ModuleProviderID, ImportProviderID)
	}
	if ProviderID != "bazel-rules-go-scip-v1" {
		t.Fatalf("frozen Bazel ProviderID changed: %q", ProviderID)
	}
}

func TestKnownProvider(t *testing.T) {
	for _, ok := range []string{ProviderID, ModuleProviderID, ImportProviderID} {
		if !KnownProvider(ok) {
			t.Fatalf("KnownProvider(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"", " ", "  " + ProviderID, ProviderID + " ", ProviderID + "\n", "\t" + ModuleProviderID,
		"BAZEL-RULES-GO-SCIP-V1", "Go-Module-Scip-V1", "IMPORTED-ARTIFACT-SCIP-V1",
		"bazel-rules-go-scip", "bazel-rules-go-scip-v2", "go-module-scip-v0", "go-module-scip-v2",
		"imported-artifact-scip-v0", "imported-artifact-scip-v2", "go-module-scip", "imported-artifact-scip",
		"unknown", "module", "import", "bazel",
	} {
		if KnownProvider(bad) {
			t.Fatalf("KnownProvider(%q) = true, want false", bad)
		}
	}
}

func TestProviderOrder(t *testing.T) {
	want := []string{ProviderID, ModuleProviderID, ImportProviderID}
	got := ProviderOrder()
	if !slices.Equal(got, want) {
		t.Fatalf("ProviderOrder() = %v, want %v", got, want)
	}
	if got[0] != ProviderID {
		t.Fatalf("Bazel must order first even when unavailable, got %q", got[0])
	}
	// The returned slice must be a copy: mutating it cannot disturb package state.
	got[0] = "mutated"
	got = got[:0]
	_ = got
	if ProviderOrder()[0] != ProviderID {
		t.Fatal("ProviderOrder() returned an alias to internal state")
	}
	for _, id := range ProviderOrder() {
		if !KnownProvider(id) {
			t.Fatalf("ordered provider %q is not a known provider", id)
		}
	}
}

func TestProviderDescriptors(t *testing.T) {
	ds := ProviderDescriptors()
	if len(ds) != 3 {
		t.Fatalf("len(ProviderDescriptors()) = %d, want 3", len(ds))
	}
	order := ProviderOrder()
	for i, d := range ds {
		if d.Provider != order[i] {
			t.Fatalf("descriptor[%d].Provider = %q, want %q", i, d.Provider, order[i])
		}
	}
	// Bazel descriptor is byte-identical to the existing single Describe().
	if ds[0] != Describe() {
		t.Fatalf("Bazel descriptor %#v != Describe() %#v", ds[0], Describe())
	}
	if !ds[0].PlanningContract {
		t.Fatal("Bazel descriptor lost its planning contract")
	}
	// The unimplemented module and import providers claim nothing.
	for _, d := range ds[1:] {
		if d.PlanningContract || d.ExecutionAvailable || d.Tests || d.Implementations || d.GeneratedDocuments {
			t.Fatalf("unimplemented provider %q claims a capability: %#v", d.Provider, d)
		}
		if d.Provider != ModuleProviderID && d.Provider != ImportProviderID {
			t.Fatalf("unexpected non-Bazel descriptor provider %q", d.Provider)
		}
	}
	// Nothing is registered: no descriptor may claim execution availability.
	for _, d := range ds {
		if d.ExecutionAvailable {
			t.Fatalf("descriptor %q claims execution availability", d.Provider)
		}
	}
	// Descriptors are independent state: mutating one returned slice must not
	// disturb a later ProviderDescriptors() result. This would fail if the
	// function ever returned a shared/cached slice instead of a fresh one.
	ds[0].PlanningContract = false
	ds[0].ExecutionAvailable = true
	ds[1].PlanningContract = true
	fresh := ProviderDescriptors()
	if !fresh[0].PlanningContract || fresh[0].ExecutionAvailable {
		t.Fatal("mutating a returned Bazel descriptor disturbed a later ProviderDescriptors() result")
	}
	if fresh[1].PlanningContract {
		t.Fatal("mutating a returned module descriptor disturbed a later ProviderDescriptors() result")
	}
	if fresh[0] != Describe() {
		t.Fatalf("a later Bazel descriptor %#v != Describe() %#v", fresh[0], Describe())
	}
}
