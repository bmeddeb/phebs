package api

import (
	"context"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

type relationshipCoverageFixture struct {
	repositories []string
	coverage     RelationshipProofCoverage
}

func (fixture *relationshipCoverageFixture) RootCoverage(
	_ context.Context,
	repositories []string,
) (*RelationshipProofCoverage, error) {
	fixture.repositories = slices.Clone(repositories)
	value := fixture.coverage
	value.Roots = slices.Clone(value.Roots)
	return &value, nil
}

func TestWorkbenchRelationshipCoverageBindsExactRootDigest(t *testing.T) {
	fixture := &relationshipCoverageFixture{coverage: RelationshipProofCoverage{
		SchemaVersion: "phebs-relationship-proof-coverage-v1", State: "exact",
		ExactRootCount: 2, Digest: "sha256:exact-root-set",
		Roots: []RelationshipRootReceipt{
			{Repository: "example.com/acme/one", State: "complete", Generation: "sha256:one"},
			{Repository: "example.com/acme/two", State: "complete", Generation: "sha256:two"},
		},
	}}
	coverage, err := workbenchRelationshipCoverage(t.Context(), fixture, []store.ChangeBriefContractSelection{
		{Repository: "example.com/acme/two"},
		{Repository: "example.com/acme/one"},
		{Repository: "example.com/acme/two"},
	})
	if err != nil || coverage.Digest != "sha256:exact-root-set" ||
		!slices.Equal(fixture.repositories, []string{"example.com/acme/one", "example.com/acme/two"}) {
		t.Fatalf("workbench relationship coverage = %+v repositories=%v err=%v", coverage, fixture.repositories, err)
	}
	cursor := workbenchImpactCursor{}
	if err := bindWorkbenchImpactDigest(
		"relationship coverage", &cursor.RelationshipDigest, coverage.Digest, false,
	); err != nil || cursor.RelationshipDigest != coverage.Digest {
		t.Fatalf("relationship cursor digest = %q, %v", cursor.RelationshipDigest, err)
	}
	if err := bindWorkbenchImpactDigest(
		"relationship coverage", &cursor.RelationshipDigest, "sha256:changed", true,
	); err == nil {
		t.Fatal("changed relationship root set did not invalidate the Workbench cursor")
	}
}
