package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/spike/t421"
)

func TestProjectT421FinalCatalogMatchesFrozenLogicalRevisions(t *testing.T) {
	plan, err := t421.BuildPlan("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := t421.BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for index, revision := range []struct {
		name, version string
		logicalB      bool
	}{
		{name: "a", version: "a-v1"},
		{name: "b", version: "b-v1", logicalB: true},
		{name: "a-return", version: "a-return-v1"},
	} {
		t.Run(revision.name, func(t *testing.T) {
			catalog := cloneT421FinalParityCatalog(corpus.Catalog)
			catalog.Authority.Version = revision.version
			if revision.logicalB {
				catalog.Services[len(catalog.Services)/2].DisplayName += "-b"
			}
			logicalSHA256, err := servicecatalogv3.NormalizedCatalogLogicalDigest(ctx, catalog)
			if err != nil {
				t.Fatal(err)
			}
			got, err := projectT421FinalCatalog(ctx, logicalSHA256, catalog)
			if err != nil {
				t.Fatal(err)
			}
			want := plan.Revisions.Logical[index]
			if got.CatalogLogicalSHA256 != want.CatalogLogicalSHA256 ||
				got.SemanticSHA256 != want.SemanticSHA256 {
				t.Fatalf("revision digests differ: got=(%s,%s) want=(%s,%s)",
					got.CatalogLogicalSHA256, got.SemanticSHA256,
					want.CatalogLogicalSHA256, want.SemanticSHA256)
			}
			if got.CatalogSource != (t421FinalCatalogSource{
				Schema: want.CatalogSource.Schema, Records: want.CatalogSource.Records,
				Bytes: want.CatalogSource.Bytes, SHA256: want.CatalogSource.SHA256,
			}) {
				t.Fatalf("catalog source differs: got=%+v want=%+v", got.CatalogSource, want.CatalogSource)
			}
			for _, identity := range []struct {
				name string
				got  t421FinalSetIdentity
				want t421.SetIdentity
			}{
				{name: "catalog", got: got.Catalog, want: want.Catalog},
				{name: "memberships", got: got.MembershipSet, want: want.Memberships},
				{name: "placements", got: got.Placements, want: want.Placements},
				{name: "unowned prefixes", got: got.UnownedPrefixes, want: want.UnownedPrefixes},
				{name: "service queries", got: got.ServiceQueries, want: want.ServiceQueries},
			} {
				if identity.got != (t421FinalSetIdentity{
					Records: identity.want.Records, FramedBytes: identity.want.FramedBytes,
					SHA256: identity.want.SHA256,
				}) {
					t.Fatalf("%s identity differs: got=%+v want=%+v", identity.name, identity.got, identity.want)
				}
			}
		})
	}
}

func cloneT421FinalParityCatalog(input servicecatalog.Catalog) servicecatalog.Catalog {
	result := input
	result.Services = slices.Clone(input.Services)
	for index := range result.Services {
		result.Services[index].Successors = slices.Clone(input.Services[index].Successors)
	}
	result.Memberships = slices.Clone(input.Memberships)
	result.Unowned = slices.Clone(input.Unowned)
	if input.Override != nil {
		override := *input.Override
		result.Override = &override
	}
	return result
}
