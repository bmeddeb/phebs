package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestInvestigationReviewProjectionLifecycleAndCursor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)
	rule := store.ReviewProjectionRule{
		ID: "grpc-review", Version: "1.0", ExpiresAfter: 24 * time.Hour,
	}

	firstRun, err := s.CreateRun(ctx, testRunRequest(revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	firstArtifact := putPublishedArtifact(t, s, firstRun.ID)
	firstSnapshot, err := s.RecordConsumerSnapshot(ctx, ledgerSnapshot(
		investigation,
		revision,
		firstArtifact,
		consumerFact("fact:one", "edge:old"),
	))
	if err != nil {
		t.Fatal(err)
	}
	firstItems, err := s.MaterializeReviewProjection(
		ctx,
		store.ReviewProjectionRequest{
			Principal: "user:owner", SourceSnapshotID: firstSnapshot.ID,
			Rule: rule, HumanRecordStateDigest: "sha256:human-state-1",
			UnresolvedAttribution: []store.ReviewAttributionCondition{},
		},
	)
	if err != nil || len(firstItems) != 0 {
		t.Fatalf("first projection = %+v, %v", firstItems, err)
	}

	secondRequest := testRunRequest(revision.ID)
	secondRequest.IdempotencyKey = "review-request-2"
	secondRun, err := s.CreateRun(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondArtifact := putPublishedArtifactWithCoverageFacts(
		t,
		s,
		secondRun.ID,
		store.InvestigationCoverageLedger{
			EligibleUnits: 2, Analyzed: 1, Failed: 1,
			Failures: []store.InvestigationCoverageFailure{
				{Unit: "repo:b", Code: "ANALYSIS_FAILED", Retryable: true},
			},
		},
		[]string{"fact:two"},
	)
	secondSnapshot, err := s.RecordConsumerSnapshot(ctx, ledgerSnapshot(
		investigation,
		revision,
		secondArtifact,
		consumerFact("fact:two", "edge:new"),
	))
	if err != nil {
		t.Fatal(err)
	}
	projectionRequest := store.ReviewProjectionRequest{
		Principal: "user:owner", SourceSnapshotID: secondSnapshot.ID,
		Rule: rule, HumanRecordStateDigest: "sha256:human-state-2",
		UnresolvedAttribution: []store.ReviewAttributionCondition{
			{
				FactID: "fact:two", SubjectKind: "consumer_relationship",
				SubjectID: "edge:new", Hop: "owner", ReasonCode: "OWNER_UNKNOWN",
			},
		},
	}
	secondItems, err := s.MaterializeReviewProjection(ctx, projectionRequest)
	if err != nil || len(secondItems) != 3 {
		t.Fatalf("second projection = %+v, %v", secondItems, err)
	}
	repeated, err := s.MaterializeReviewProjection(ctx, projectionRequest)
	if err != nil || len(repeated) != len(secondItems) {
		t.Fatalf("repeated projection = %+v, %v", repeated, err)
	}
	for i := range secondItems {
		if secondItems[i].ID != repeated[i].ID {
			t.Fatalf("projection identity drift = %+v / %+v", secondItems, repeated)
		}
	}
	if _, err := s.PutReviewCursor(ctx, "user:owner", investigation.ID, store.ReviewCursor{
		AcknowledgedItemIDs:    []string{secondItems[0].ID},
		LastViewedComparisonID: secondSnapshot.Comparison.ComparisonID,
	}); err != nil {
		t.Fatalf("put review cursor: %v", err)
	}
	views, err := s.ListReviewItemsAs(
		ctx,
		"user:owner",
		investigation.ID,
		secondSnapshot.CreatedAt.Add(time.Hour),
	)
	if err != nil || len(views) != 3 {
		t.Fatalf("review views = %+v, %v", views, err)
	}
	acknowledged := 0
	for _, view := range views {
		if view.Lifecycle != "open" {
			t.Fatalf("initial lifecycle = %+v", views)
		}
		if view.Acknowledged {
			acknowledged++
		}
	}
	if acknowledged != 1 {
		t.Fatalf("acknowledged views = %d, want 1", acknowledged)
	}

	thirdRequest := testRunRequest(revision.ID)
	thirdRequest.IdempotencyKey = "review-request-3"
	thirdRun, err := s.CreateRun(ctx, thirdRequest)
	if err != nil {
		t.Fatal(err)
	}
	thirdArtifact := putPublishedArtifactWithFacts(
		t,
		s,
		thirdRun.ID,
		[]string{"fact:three"},
	)
	thirdSnapshot, err := s.RecordConsumerSnapshot(ctx, ledgerSnapshot(
		investigation,
		revision,
		thirdArtifact,
		consumerFact("fact:three", "edge:later"),
	))
	if err != nil {
		t.Fatal(err)
	}
	thirdItems, err := s.MaterializeReviewProjection(
		ctx,
		store.ReviewProjectionRequest{
			Principal: "user:owner", SourceSnapshotID: thirdSnapshot.ID,
			Rule: rule, HumanRecordStateDigest: "sha256:human-state-3",
			UnresolvedAttribution: []store.ReviewAttributionCondition{},
		},
	)
	if err != nil || len(thirdItems) != 1 {
		t.Fatalf("third projection = %+v, %v", thirdItems, err)
	}
	views, err = s.ListReviewItemsAs(
		ctx,
		"user:owner",
		investigation.ID,
		thirdSnapshot.CreatedAt.Add(time.Hour),
	)
	if err != nil || len(views) != 4 {
		t.Fatalf("superseded views = %+v, %v", views, err)
	}
	lifecycles := make([]string, len(views))
	for i := range views {
		lifecycles[i] = views[i].Lifecycle
	}
	slices.Sort(lifecycles)
	if !slices.Equal(lifecycles, []string{"open", "superseded", "superseded", "superseded"}) {
		t.Fatalf("lifecycles = %v", lifecycles)
	}
	if _, err := s.ListReviewItemsAs(
		ctx,
		"user:stranger",
		investigation.ID,
		time.Now(),
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unauthorized review read = %v, want ErrNotFound", err)
	}
}

func putPublishedArtifactWithCoverageFacts(
	t *testing.T,
	s *store.Surreal,
	runID string,
	coverage store.InvestigationCoverageLedger,
	facts []string,
) *store.RunArtifact {
	t.Helper()
	canonical, err := store.CanonicalInvestigationCoverageLedger(coverage)
	if err != nil {
		t.Fatalf("coverage ledger: %v", err)
	}
	lease := advanceToPublishing(t, s, runID)
	artifact, err := s.PublishInvestigationRun(context.Background(), lease, store.RunArtifact{
		Scope: "investigation:test", RunID: runID,
		SnapshotManifest:  "sha256:snapshot:" + runID,
		InputManifest:     "sha256:input",
		CoverageLedger:    canonical,
		FactReferences:    facts,
		EligibilityResult: "eligible:false:blocker",
	})
	if err != nil {
		t.Fatalf("put artifact: %v", err)
	}
	return artifact
}
