package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func ledgerSnapshot(
	investigation *store.Investigation,
	revision *store.Revision,
	artifact *store.RunArtifact,
	facts ...store.ConsumerEdgeFact,
) store.ConsumerSnapshot {
	snapshot := consumerSnapshotFixture(artifact.ID, facts...)
	snapshot.InvestigationID = investigation.ID
	snapshot.RevisionID = revision.ID
	snapshot.SourceSnapshot = artifact.SnapshotManifest
	return snapshot
}

func TestInvestigationConsumerLedgerDiffAndArtifactSweep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)

	firstRun, err := s.CreateRun(ctx, testRunRequest(revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	firstArtifact := putPublishedArtifact(t, s, firstRun.ID)
	first, err := s.RecordConsumerSnapshot(ctx, ledgerSnapshot(
		investigation,
		revision,
		firstArtifact,
		consumerFact("fact:one", "edge:old"),
	))
	if err != nil {
		t.Fatalf("record first snapshot: %v", err)
	}
	if first.Comparison.Comparable ||
		first.Comparison.NonComparabilityCauses[0] != "no_prior_snapshot" {
		t.Fatalf("first comparison = %+v", first.Comparison)
	}

	secondRequest := testRunRequest(revision.ID)
	secondRequest.IdempotencyKey = "request-2"
	secondRun, err := s.CreateRun(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondArtifact := putPublishedArtifactWithFacts(t, s, secondRun.ID, []string{"fact:two"})
	second, err := s.RecordConsumerSnapshot(ctx, ledgerSnapshot(
		investigation,
		revision,
		secondArtifact,
		consumerFact("fact:two", "edge:new"),
	))
	if err != nil {
		t.Fatalf("record second snapshot: %v", err)
	}
	if !second.Comparison.Comparable || len(second.Comparison.Deltas) != 2 {
		t.Fatalf("second comparison = %+v", second.Comparison)
	}
	ledger, err := s.ListConsumerLedgerAs(ctx, "user:owner", investigation.ID)
	if err != nil || len(ledger) != 2 {
		t.Fatalf("ledger = %+v, %v", ledger, err)
	}
	if ledger[0].LogicalRelationshipID != "edge:new" || !ledger[0].Active ||
		ledger[1].LogicalRelationshipID != "edge:old" || ledger[1].Active ||
		ledger[1].LastRemovedArtifactID != secondArtifact.ID {
		t.Fatalf("ledger state = %+v", ledger)
	}
	if _, err := s.ListConsumerLedgerAs(ctx, "user:stranger", investigation.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unauthorized ledger read = %v, want ErrNotFound", err)
	}

	owner, err := s.PutRunArtifactRetentionOwner(ctx, store.RunArtifactRetentionOwner{
		ArtifactID: firstArtifact.ID, Kind: store.RunArtifactOwnerInvestigation,
		OwnerID: investigation.ID, AuthorizedBy: "worker:one",
		Reason: "active investigation run artifact",
	})
	if err != nil {
		t.Fatalf("read publication owner: %v", err)
	}
	if _, err := s.ReleaseRunArtifactRetentionOwner(
		ctx,
		owner.Key,
		"user:owner",
		"ledger survival test",
	); err != nil {
		t.Fatalf("release artifact owner: %v", err)
	}
	if swept, err := s.SweepRunArtifacts(ctx, time.Now().UTC().Add(time.Hour)); err != nil || swept != 1 {
		t.Fatalf("sweep artifact = %d, %v", swept, err)
	}
	if _, err := s.GetRunArtifact(ctx, firstArtifact.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("swept artifact read = %v, want ErrNotFound", err)
	}
	afterSweep, err := s.ListConsumerLedgerAs(ctx, "user:owner", investigation.ID)
	if err != nil || len(afterSweep) != 2 ||
		afterSweep[1].FirstSeenArtifactID != firstArtifact.ID {
		t.Fatalf("ledger after sweep = %+v, %v", afterSweep, err)
	}
}

func putPublishedArtifactWithFacts(
	t *testing.T,
	s *store.Surreal,
	runID string,
	facts []string,
) *store.RunArtifact {
	t.Helper()
	lease := advanceToPublishing(t, s, runID)
	artifact, err := s.PublishInvestigationRun(context.Background(), lease, store.RunArtifact{
		Scope: "investigation:test", RunID: runID,
		SnapshotManifest:  "sha256:snapshot:" + runID,
		InputManifest:     "sha256:input",
		CoverageLedger:    completeInvestigationCoverage(t, 1),
		FactReferences:    facts,
		EligibilityResult: "eligible:false:blocker",
	})
	if err != nil {
		t.Fatalf("put artifact: %v", err)
	}
	return artifact
}
