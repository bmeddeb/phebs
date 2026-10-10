package main

import (
	"slices"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
)

func TestMergedMetadataNamesTheMergePin(t *testing.T) {
	meta := canonicalMetadata("v0.2.7", "example", "github.com/example/repo", "full-commit", []string{".", "api"})
	want := []string{"t472-merge", "--remote", "github.com/example/repo", "--pin", "full-commit", "--module-rels", ".,api"}
	if !slices.Equal(meta.GetToolInfo().GetArguments(), want) {
		t.Fatalf("merged metadata arguments = %q, want %q", meta.GetToolInfo().GetArguments(), want)
	}
}

func TestVersionSkewCountsOnlyUnboundInRepoReferences(t *testing.T) {
	const (
		defined = "scip-go gomod example.com/api aaa `example.com/api/pb`/Client#Call()."
		skewed  = "scip-go gomod example.com/api bbb `example.com/api/pb`/Client#Call()."
		foreign = "scip-go gomod example.com/other bbb `example.com/other/pb`/Client#Call()."
	)
	definition := int32(scip.SymbolRole_Definition)
	docs := []*scip.Document{
		{RelativePath: "api/pb/client.go", Occurrences: []*scip.Occurrence{
			{Symbol: defined, SymbolRoles: definition},
		}},
		{RelativePath: "client/call.go", Occurrences: []*scip.Occurrence{
			{Symbol: defined}, // binds exactly
			{Symbol: skewed},  // same symbol, other module version
			{Symbol: foreign}, // not an in-repo symbol
			{Symbol: "local 1"},
		}},
	}
	count, sample := versionSkew(docs)
	if count != 1 || len(sample) != 1 || sample[0] != "client/call.go "+skewed {
		t.Fatalf("versionSkew = %d %q, want the one skewed reference", count, sample)
	}
}
