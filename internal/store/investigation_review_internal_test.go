package store

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func reviewProjectionFixture() reviewProjectionInput {
	fact := ConsumerEdgeFact{
		FactID: "fact:new", LogicalRelationshipID: "edge:new",
		Predicate: "CALLS_OPERATION", SubjectKind: "source_occurrence",
		SubjectID: "repo:a/client.go:10", ObjectKind: "grpc_operation",
		ObjectID: "/shop.Cart/Get",
	}
	return reviewProjectionInput{
		Principal:        "user:owner",
		InvestigationID:  "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		SourceSnapshotID: "ics_snapshot",
		Comparison: ConsumerComparison{
			ComparisonID: "iccmp_fixture", LeftArtifactID: "ira_left",
			RightArtifactID: "ira_right", Comparable: true,
			RuleID: "consumer-edge-comparability", RuleVersion: "1.0",
			RuleDigest: "sha256:comparison", NonComparabilityCauses: []string{},
			PerSideCoverageOnly: false,
			Deltas: []ConsumerDelta{
				{
					LogicalRelationshipID: "edge:new", Change: "added",
					Cause: "relationship_added_traced", After: &fact,
				},
			},
		},
		Coverage: InvestigationCoverageLedger{
			SchemaVersion: investigationCoverageSchemaVersion,
			EligibleUnits: 2, Analyzed: 1, Failed: 1,
			Failures: []InvestigationCoverageFailure{
				{Unit: "repo:b", Code: "ANALYSIS_FAILED", Retryable: true},
			},
		},
		Facts: []ConsumerEdgeFact{fact},
		Rule: ReviewProjectionRule{
			ID: "grpc-review", Version: "1.0", ExpiresAfter: 24 * time.Hour,
		},
		HumanRecordStateDigest: "sha256:human-state",
		UnresolvedAttribution: []ReviewAttributionCondition{
			{
				FactID: "fact:new", SubjectKind: "consumer_relationship",
				SubjectID: "edge:new", Hop: "owner", ReasonCode: "OWNER_UNKNOWN",
			},
		},
		EvaluatedAt: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
	}
}

func TestReviewProjectionDeterministicQueuesAndIdentity(t *testing.T) {
	input := reviewProjectionFixture()
	first, err := deriveReviewItems(input)
	if err != nil {
		t.Fatal(err)
	}
	input.EvaluatedAt = input.EvaluatedAt.Add(time.Hour)
	second, err := deriveReviewItems(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("items = %d, %d; want three queues", len(first), len(second))
	}
	if !slices.IsSortedFunc(first, func(left, right ReviewItem) int {
		return strings.Compare(left.ID, right.ID)
	}) {
		t.Fatalf("review items are not in canonical identity order: %+v", first)
	}
	gotQueues := make([]ReviewQueue, len(first))
	for i := range first {
		gotQueues[i] = first[i].Queue
		if first[i].ID != second[i].ID ||
			first[i].ProjectionKey != second[i].ProjectionKey {
			t.Fatalf("identity changed for identical delta: %+v / %+v", first[i], second[i])
		}
	}
	slices.Sort(gotQueues)
	wantQueues := []ReviewQueue{
		ReviewQueueCoverageRegression,
		ReviewQueueNewConsumers,
		ReviewQueueUnresolvedAttribution,
	}
	slices.Sort(wantQueues)
	if !slices.Equal(gotQueues, wantQueues) {
		t.Fatalf("queues = %v, want %v", gotQueues, wantQueues)
	}
}

func TestReviewItemLifecycleIsRuleDerived(t *testing.T) {
	items, err := deriveReviewItems(reviewProjectionFixture())
	if err != nil {
		t.Fatal(err)
	}
	item := items[0]
	if got := item.LifecycleAt(item.EvaluatedAt); got != "open" {
		t.Fatalf("initial lifecycle = %q", got)
	}
	supersededAt := item.EvaluatedAt.Add(time.Hour)
	item.SupersededBy = "irp_successor"
	item.SupersededAt = &supersededAt
	item, err = normalizeReviewItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if got := item.LifecycleAt(supersededAt); got != "superseded" {
		t.Fatalf("superseded lifecycle = %q", got)
	}
	item.SupersededBy = ""
	item.SupersededAt = nil
	if got := item.LifecycleAt(item.ExpiresAt); got != "expired" {
		t.Fatalf("expired lifecycle = %q", got)
	}
}

func TestReviewProjectionHasNoHandCreationMethod(t *testing.T) {
	storeType := reflect.TypeOf((*InvestigationReviewStore)(nil)).Elem()
	for i := 0; i < storeType.NumMethod(); i++ {
		name := storeType.Method(i).Name
		if name == "CreateReviewItem" || name == "PutReviewItem" {
			t.Fatalf("hand-creation method exists: %s", name)
		}
	}
}

func TestReviewCursorCanonicalRoundTrip(t *testing.T) {
	cursor := ReviewCursor{
		AcknowledgedItemIDs:    []string{"iri_b", "iri_a", "iri_b"},
		LastViewedComparisonID: "iccmp_fixture",
	}
	normalized, encoded, err := normalizeReviewCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(normalized.AcknowledgedItemIDs, []string{"iri_a", "iri_b"}) {
		t.Fatalf("acknowledgements = %v", normalized.AcknowledgedItemIDs)
	}
	decoded, err := ParseReviewCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*decoded, normalized) {
		t.Fatalf("round trip = %+v, want %+v", *decoded, normalized)
	}
	if _, err := ParseReviewCursor(encoded + `{}`); err == nil {
		t.Fatal("trailing JSON unexpectedly accepted")
	}
}

func TestFirstConsumerSnapshotDoesNotInventCoverageRegression(t *testing.T) {
	input := reviewProjectionFixture()
	input.Comparison = firstComparison(ConsumerSnapshot{
		Principal: "user:owner", InvestigationID: input.InvestigationID,
		ArtifactID: "ira_right",
		Semantics: ConsumerSnapshotSemantics{
			VisibilityProjection: "visibility:owner:1",
		},
	})
	input.Coverage = InvestigationCoverageLedger{
		SchemaVersion: investigationCoverageSchemaVersion,
		EligibleUnits: 1, Analyzed: 1,
		Failures: []InvestigationCoverageFailure{},
	}
	input.UnresolvedAttribution = []ReviewAttributionCondition{}
	items, err := deriveReviewItems(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("first snapshot projected items = %+v", items)
	}
}
