package servicecatalogv3

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestSourceGenerationDigestV2UsesStableSourceTuple(t *testing.T) {
	commit := strings.Repeat("c", 40)
	census := rawDigest([]byte("source census"))
	operator := servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "owner", Version: "v1"}
	base := Binding{Repository: "example/catalog", Source: Source{
		Kind: servicecatalog.SourceOperator, Path: "/tmp/catalog.json", Commit: commit,
		CensusDigest: census, FileCount: 4, AcceptedFileCount: 3, UnownedFileCount: 1,
	}, Authority: operator}

	digestFor := func(t *testing.T, binding Binding) string {
		t.Helper()
		catalog := acceptedCatalog(1, false)
		catalog.Authority = binding.Authority
		generation, err := Build(binding, catalog)
		if err != nil {
			t.Fatal(err)
		}
		value, err := SourceGenerationDigest(generation.Root)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	want := digestFor(t, base)
	if want != "sha256:513bf82d06308b7f5f8f1d0a10edfb5b5f3fed04bd34d54131d82daee492e5a5" {
		t.Fatalf("v2 source digest = %q", want)
	}
	for name, binding := range map[string]Binding{
		"path and complement counts": func() Binding {
			value := base
			value.Source.Path = "/tmp/other.json"
			value.Source.AcceptedFileCount = 2
			value.Source.UnownedFileCount = 2
			return value
		}(),
		"committed source": func() Binding {
			value := base
			value.Authority = servicecatalog.Authority{Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: commit}
			value.Source.Kind = servicecatalog.SourceCommitted
			return value
		}(),
		"legacy source": func() Binding {
			value := base
			legacy := rawDigest([]byte("legacy"))
			value.Authority = servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: servicecatalog.AnalysisUnitV1AuthorityID, Version: legacy}
			value.Source.Kind = servicecatalog.SourceAnalysisUnitV1
			value.Source.Path = ""
			value.Source.LegacyDigest = legacy
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if got := digestFor(t, binding); got != want {
				t.Fatalf("source metadata changed digest: got %q want %q", got, want)
			}
		})
	}

	for name, binding := range map[string]Binding{
		"repository": func() Binding {
			value := base
			value.Repository = "example/other"
			return value
		}(),
		"commit": func() Binding {
			value := base
			value.Source.Commit = strings.Repeat("d", 40)
			return value
		}(),
		"census": func() Binding {
			value := base
			value.Source.CensusDigest = rawDigest([]byte("other census"))
			return value
		}(),
		"file count": func() Binding {
			value := base
			value.Source.FileCount = 5
			value.Source.AcceptedFileCount = 4
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if got := digestFor(t, binding); got == want {
				t.Fatalf("source %s did not change digest", name)
			}
		})
	}
}

func TestLegacyRootCompatibilityAndRebuild(t *testing.T) {
	catalog := acceptedCatalog(3, false)
	binding := testBinding(catalog.Authority)
	legacy, err := build(binding, catalog, RootSchema)
	if err != nil {
		t.Fatal(err)
	}
	current, err := Build(binding, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Root.Schema != RootSchema || current.Root.Schema != RootSchemaV2 {
		t.Fatalf("root schemas = %q, %q", legacy.Root.Schema, current.Root.Schema)
	}
	if !reflect.DeepEqual(legacy.Members, current.Members) ||
		!reflect.DeepEqual(legacy.Root.ServiceMembers, current.Root.ServiceMembers) ||
		!reflect.DeepEqual(legacy.Root.PlacementMembers, current.Root.PlacementMembers) {
		t.Fatal("root schema change rewrote reusable member bytes")
	}
	legacyRaw, err := EncodeRoot(legacy.Root)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := DecodeRoot(legacyRaw)
	if err != nil || !reflect.DeepEqual(opened, legacy.Root) {
		t.Fatalf("legacy root round-trip = %+v, %v", opened, err)
	}
	legacySource, err := SourceGenerationDigest(legacy.Root)
	if err != nil {
		t.Fatal(err)
	}
	if legacySource != "sha256:b00a86449bab0ca4ff28cf684a89983586b6dd0ad60290305e10f8cb863e38ea" {
		t.Fatalf("legacy source digest = %q", legacySource)
	}
	currentSource, err := SourceGenerationDigest(current.Root)
	if err != nil {
		t.Fatal(err)
	}
	if currentSource == legacySource {
		t.Fatal("v2 root reused the legacy source-generation domain")
	}
	for name, generation := range map[string]Generation{"legacy": legacy, "v2": current} {
		t.Run(name, func(t *testing.T) {
			rebuilt, err := Rebuild(generation.Root, catalog)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rebuilt, generation) {
				t.Fatal("rebuild did not preserve exact generation")
			}
		})
	}
	invalid := current.Root
	invalid.Schema = "phebs-service-catalog-v3-root-v3"
	if _, err := Rebuild(invalid, catalog); err == nil {
		t.Fatal("unknown root schema was rebuilt")
	}
}
