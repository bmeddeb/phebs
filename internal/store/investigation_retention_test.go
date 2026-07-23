package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func seedPublishedExtractionRun(t *testing.T, s *store.Surreal, repo, commit, extractor string) *store.ExtractionRun {
	t.Helper()
	ctx := context.Background()
	seedEvidenceRepo(t, s, repo, commit)
	run, err := s.BeginExtractionRun(ctx, repo, commit, "proto-contract", extractor)
	if err != nil {
		t.Fatalf("begin extraction run: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, testCoverage(0, 0)); err != nil {
		t.Fatalf("publish extraction run: %v", err)
	}
	return run
}

func supersedeExtractionRun(t *testing.T, s *store.Surreal, repo, commit, extractor string) *store.ExtractionRun {
	t.Helper()
	run, err := s.BeginExtractionRun(context.Background(), repo, commit, "proto-contract", extractor)
	if err != nil {
		t.Fatalf("begin replacement extraction run: %v", err)
	}
	if err := s.PublishExtractionRun(context.Background(), run.ID, testCoverage(0, 0)); err != nil {
		t.Fatalf("publish replacement extraction run: %v", err)
	}
	return run
}

func putPinnedArtifact(t *testing.T, s *store.Surreal, extractionRunID, scope string) (*store.Investigation, *store.Revision, *store.RunArtifact) {
	t.Helper()
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)
	run, err := s.CreateRun(ctx, testRunRequest(revision.ID))
	if err != nil {
		t.Fatalf("create investigation run: %v", err)
	}
	lease := advanceToPublishing(t, s, run.ID)
	artifact, err := s.PublishInvestigationRun(ctx, lease, store.RunArtifact{
		Scope: scope, RunID: run.ID, TerminalStatus: store.RunPublished,
		SnapshotManifest: "sha256:snapshot", InputManifest: "sha256:input",
		CoverageLedger: completeInvestigationCoverage(t, 1), FactReferences: []string{"fact:one"},
		PinReferences: []string{extractionRunID}, EligibilityResult: "eligible:true",
	})
	if err != nil {
		t.Fatalf("put pinned artifact: %v", err)
	}
	return investigation, revision, artifact
}

func retainInvestigationArtifact(t *testing.T, s *store.Surreal, artifactID, investigationID string) *store.RunArtifactRetentionOwner {
	t.Helper()
	owner, err := s.PutRunArtifactRetentionOwner(context.Background(), store.RunArtifactRetentionOwner{
		ArtifactID: artifactID, Kind: store.RunArtifactOwnerInvestigation,
		OwnerID: investigationID, AuthorizedBy: "worker:one",
		Reason: "active investigation run artifact",
	})
	if err != nil {
		t.Fatalf("retain artifact: %v", err)
	}
	return owner
}

func TestInvestigationRetentionArtifactPinsAndAuthorizedOwnerBlockSweep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit := "github.com/investigation/retention", "aaaaaaaa"
	oldRun := seedPublishedExtractionRun(t, s, repo, commit, "proto-contract@1")
	investigation, _, artifact := putPinnedArtifact(t, s, oldRun.ID, "investigation:retention")
	owner := retainInvestigationArtifact(t, s, artifact.ID, investigation.ID)

	supersedeExtractionRun(t, s, repo, commit, "proto-contract@2")
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 0 {
		t.Fatalf("artifact-pinned evidence sweep = %d, %v; want 0", n, err)
	}
	if n, err := s.SweepRunArtifacts(ctx, time.Now().UTC().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("owner-protected artifact sweep = %d, %v; want 0", n, err)
	}
	if _, err := s.GetRunArtifact(ctx, artifact.ID); err != nil {
		t.Fatalf("protected artifact unavailable: %v", err)
	}

	release, err := s.ReleaseRunArtifactRetentionOwner(ctx, owner.Key, "user:owner", "investigation archived")
	if err != nil || release.OwnerKey != owner.Key {
		t.Fatalf("release owner = %+v, %v", release, err)
	}
	if _, err := s.PutRunArtifactRetentionOwner(ctx, *owner); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("released owner reacquisition = %v, want ErrConflict", err)
	}
	if n, err := s.SweepRunArtifacts(ctx, time.Now().UTC().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("released artifact sweep = %d, %v; want 1", n, err)
	}
	if _, err := s.GetRunArtifact(ctx, artifact.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("collected artifact read = %v, want ErrNotFound", err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 1 {
		t.Fatalf("newly unpinned evidence sweep = %d, %v; want 1", n, err)
	}
}

func TestInvestigationRetentionPolicyOverridesOwners(t *testing.T) {
	for _, policy := range []store.RunArtifactOverridePolicy{
		store.RunArtifactOverrideRevocation,
		store.RunArtifactOverrideMandatoryDeletion,
		store.RunArtifactOverrideLegalPolicy,
		store.RunArtifactOverrideApprovedRetention,
	} {
		t.Run(string(policy), func(t *testing.T) {
			s := newTestStore(t)
			repo, commit := "github.com/investigation/"+string(policy), "bbbbbbbb"
			extractionRun := seedPublishedExtractionRun(t, s, repo, commit, "proto-contract@1")
			investigation, _, artifact := putPinnedArtifact(t, s, extractionRun.ID, "investigation:"+string(policy))
			retainInvestigationArtifact(t, s, artifact.ID, investigation.ID)

			override, err := s.PutRunArtifactRetentionOverride(context.Background(), store.RunArtifactRetentionOverride{
				ArtifactID: artifact.ID, Policy: policy, AuthorizedBy: "policy:operator",
				Reason: "approved policy exception",
			})
			if err != nil || override.Policy != policy {
				t.Fatalf("put override = %+v, %v", override, err)
			}
			if n, err := s.SweepRunArtifacts(context.Background(), time.Now().UTC().Add(-time.Hour)); err != nil || n != 1 {
				t.Fatalf("override sweep = %d, %v; want immediate collection", n, err)
			}
			if _, err := s.GetRunArtifact(context.Background(), artifact.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("overridden artifact read = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestInvestigationRetentionBaselineDesignationOwnsArtifact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	extractionRun := seedPublishedExtractionRun(t, s, "github.com/investigation/baseline", "cccccccc", "proto-contract@1")
	investigation, revision, artifact := putPinnedArtifact(t, s, extractionRun.ID, "investigation:baseline")
	baseline, err := s.PutBaselineDesignation(ctx, store.BaselineDesignation{
		InvestigationID: investigation.ID, ClaimScope: "consumer-census", WorkflowScope: "migration",
		RevisionID: revision.ID, RunArtifactID: artifact.ID,
		Authority: "user:owner", Rationale: "accepted evidence baseline",
	})
	if err != nil {
		t.Fatalf("put baseline: %v", err)
	}
	if _, err := s.PutBaselineDesignation(ctx, *baseline); err != nil {
		t.Fatalf("baseline exact retry: %v", err)
	}
	if n, err := s.SweepRunArtifacts(ctx, time.Now().UTC().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("baseline-owned artifact sweep = %d, %v; want 0", n, err)
	}
}

func TestInvestigationRetentionPublicationAndPinsAreAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, revision := seedInvestigation(t, s)
	run, err := s.CreateRun(ctx, testRunRequest(revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	lease := advanceToPublishing(t, s, run.ID)
	candidate := store.RunArtifact{
		Scope: "investigation:atomic", RunID: run.ID, TerminalStatus: store.RunPublished,
		SnapshotManifest: "sha256:snapshot", InputManifest: "sha256:input",
		CoverageLedger: completeInvestigationCoverage(t, 1), PinReferences: []string{"missing-extraction-run"},
		EligibilityResult: "eligible:false:missing-evidence",
	}
	id, err := store.ComputeRunArtifactID(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInvestigationRun(ctx, lease, candidate); !errors.Is(err, store.ErrConflict) &&
		!errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("unavailable pin publication = %v, want ErrConflict or ErrLeaseLost", err)
	}
	if _, err := s.GetRunArtifact(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("partially published artifact read = %v, want ErrNotFound", err)
	}
}

func TestInvestigationRetentionArtifactSweepReleasesOnlyItsOwnPins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit := "github.com/investigation/pin-isolation", "dddddddd"
	oldRun := seedPublishedExtractionRun(t, s, repo, commit, "proto-contract@1")
	investigation, _, artifact := putPinnedArtifact(t, s, oldRun.ID, "investigation:pin-isolation")
	owner := retainInvestigationArtifact(t, s, artifact.ID, investigation.ID)
	if _, err := s.ReleaseRunArtifactRetentionOwner(ctx, owner.Key, "user:owner", "test collection"); err != nil {
		t.Fatalf("release investigation owner: %v", err)
	}
	if err := s.PinRun(ctx, oldRun.ID, "independent-retention-owner"); err != nil {
		t.Fatalf("independent pin: %v", err)
	}
	supersedeExtractionRun(t, s, repo, commit, "proto-contract@2")
	if n, err := s.SweepRunArtifacts(ctx, time.Now().UTC().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("unowned artifact sweep = %d, %v; want 1", n, err)
	}
	if _, err := s.GetRunArtifact(ctx, artifact.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("artifact read after sweep = %v, want ErrNotFound", err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 0 {
		t.Fatalf("independently pinned evidence sweep = %d, %v; want 0", n, err)
	}
}
