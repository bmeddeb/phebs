package store_test

import (
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func consumerSnapshotFixture(artifactID string, facts ...store.ConsumerEdgeFact) store.ConsumerSnapshot {
	return store.ConsumerSnapshot{
		Principal:       "user:owner",
		InvestigationID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		RevisionID:      "ivr_fixture_1",
		ArtifactID:      artifactID,
		SourceSnapshot:  "repo@example",
		Semantics: store.ConsumerSnapshotSemantics{
			ClaimIdentity:          "claim:grpc-consumer",
			FactSchema:             "contract-edge-v1",
			RuleIdentity:           "grpc-go@1",
			ExtractorIdentity:      "grpcgo@1",
			AdapterIdentity:        "source@1",
			IdentitySemantics:      "grpc-operation+source-occurrence-v1",
			DeclaredUniverse:       []string{"repo:a", "repo:b"},
			EnumerationMethod:      "authorized-repository-manifest-v1",
			SnapshotPolicy:         "exact-commit",
			BuildConfiguration:     "linux/amd64",
			ExternalInputSemantics: "none-v1",
			VisibilityProjection:   "visibility:owner:1",
			AnalysisComplete:       true,
			InputsFresh:            true,
		},
		Facts: facts,
	}
}

func consumerFact(factID, logicalID string) store.ConsumerEdgeFact {
	return store.ConsumerEdgeFact{
		FactID: factID, LogicalRelationshipID: logicalID,
		Predicate:   "CALLS_OPERATION",
		SubjectKind: "source_occurrence", SubjectID: "repo:a/client.go:10",
		ObjectKind: "grpc_operation", ObjectID: "/shop.Cart/Get",
	}
}

func TestConsumerSnapshotComparableTracedDelta(t *testing.T) {
	left := consumerSnapshotFixture("ira_left", consumerFact("fact:left", "edge:old"))
	right := consumerSnapshotFixture("ira_right", consumerFact("fact:right", "edge:new"))
	comparison, err := store.CompareConsumerSnapshots(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Comparable || comparison.PerSideCoverageOnly ||
		len(comparison.NonComparabilityCauses) != 0 || len(comparison.Deltas) != 2 {
		t.Fatalf("comparison = %+v", comparison)
	}
	if comparison.Deltas[0].LogicalRelationshipID != "edge:new" ||
		comparison.Deltas[0].Change != "added" ||
		comparison.Deltas[0].Cause != "relationship_added_traced" ||
		comparison.Deltas[1].LogicalRelationshipID != "edge:old" ||
		comparison.Deltas[1].Change != "removed" ||
		comparison.Deltas[1].Cause != "relationship_removed_traced" {
		t.Fatalf("deltas = %+v", comparison.Deltas)
	}
}

func TestConsumerSnapshotProhibitedCausesNeverRenderRemoval(t *testing.T) {
	base := consumerSnapshotFixture("ira_left", consumerFact("fact:left", "edge:old"))
	tests := []struct {
		name      string
		wantCause string
		mutate    func(*store.ConsumerSnapshot)
	}{
		{
			name: "authorization", wantCause: "authorization_changed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.VisibilityProjection = "visibility:owner:2"
			},
		},
		{
			name: "identity", wantCause: "schema_or_identity_changed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.IdentitySemantics = "grpc-operation-v2"
			},
		},
		{
			name: "pack rule", wantCause: "pack_or_rule_changed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.RuleIdentity = "grpc-go@2"
			},
		},
		{
			name: "scope expanded", wantCause: "scope_expanded",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.DeclaredUniverse = append(
					snapshot.Semantics.DeclaredUniverse,
					"repo:c",
				)
			},
		},
		{
			name: "scope narrowed", wantCause: "scope_narrowed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.DeclaredUniverse = []string{"repo:a"}
			},
		},
		{
			name: "enumeration", wantCause: "enumeration_changed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.EnumerationMethod = "catalog-v2"
			},
		},
		{
			name: "build", wantCause: "build_configuration_changed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.BuildConfiguration = "darwin/arm64"
			},
		},
		{
			name: "external metadata", wantCause: "external_metadata_changed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.ExternalInputSemantics = "catalog-v2"
			},
		},
		{
			name: "analysis failed", wantCause: "analysis_failed",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.AnalysisComplete = false
			},
		},
		{
			name: "stale input", wantCause: "input_stale",
			mutate: func(snapshot *store.ConsumerSnapshot) {
				snapshot.Semantics.InputsFresh = false
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			right := base
			right.ArtifactID = "ira_right_" + test.name
			right.Semantics.DeclaredUniverse = slices.Clone(base.Semantics.DeclaredUniverse)
			right.Facts = []store.ConsumerEdgeFact{}
			test.mutate(&right)
			comparison, err := store.CompareConsumerSnapshots(base, right)
			if err != nil {
				t.Fatal(err)
			}
			if comparison.Comparable || !comparison.PerSideCoverageOnly ||
				len(comparison.Deltas) != 0 ||
				!slices.Contains(comparison.NonComparabilityCauses, test.wantCause) {
				t.Fatalf("comparison = %+v", comparison)
			}
		})
	}
}
