package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func durableOutcome(
	scope store.ExtractionScope,
	disposition store.DomainOutcomeDisposition,
	extractor string,
) store.ExtractionDomainOutcome {
	generation := store.ExtractionGenerationIdentity{
		Extractor:        extractor,
		InventoryPolicy:  "gitlink-boundary-v2",
		DependencyDigest: candidateDigest('9'),
	}
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	return store.ExtractionDomainOutcome{
		Scope:         scope,
		Disposition:   disposition,
		Generation:    generation,
		ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt: `{"schema":"` +
			store.ExtractionOutcomeReceiptSchema + `"}`,
	}
}

func TestExtractionDomainOutcomePublicationAndLatestOnlyLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/outcomes"
	commit := strings.Repeat("1", 40)
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://github.com/acme/outcomes.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	scope := store.ExtractionScope{
		Repository: repository, Commit: commit, Domain: "proto-contract",
	}

	run, err := s.BeginExtractionRun(ctx, scope, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	published := durableOutcome(
		scope, store.DomainOutcomePublished, run.Extractor,
	)
	published.RunID = run.ID
	wrong := published
	wrong.Scope.Domain = "grpc-consumer"
	if err := s.PublishExtractionRunWithOutcome(
		ctx, run.ID, testCoverage(0, 0), wrong,
	); err == nil {
		t.Fatal("mismatched outcome published a staged run")
	}
	if err := s.PublishExtractionRunWithOutcome(
		ctx, run.ID, testCoverage(0, 0), published,
	); err != nil {
		t.Fatalf("publish with outcome: %v", err)
	}
	got, err := s.LatestExtractionDomainOutcome(ctx, scope)
	if err != nil || got.Disposition != store.DomainOutcomePublished ||
		got.RunID != run.ID ||
		got.Generation.Digest !=
			store.ComputeExtractionGenerationDigest(got.Generation) {
		t.Fatalf("published outcome = %+v, %v", got, err)
	}

	refusal := durableOutcome(
		scope, store.DomainOutcomeTerminalGenerationRefusal, "2.0.0",
	)
	if err := s.RecordExtractionDomainOutcome(ctx, refusal); err != nil {
		t.Fatalf("record refusal: %v", err)
	}
	visible, err := s.LatestPublishedRun(ctx, scope)
	if err != nil || visible.ID != run.ID {
		t.Fatalf("prior publication disturbed = %+v, %v", visible, err)
	}
	got, err = s.LatestExtractionDomainOutcome(ctx, scope)
	if err != nil ||
		got.Disposition != store.DomainOutcomeTerminalGenerationRefusal ||
		got.Generation.Extractor != "2.0.0" {
		t.Fatalf("latest-only refusal = %+v, %v", got, err)
	}

	nextCommit := strings.Repeat("2", 40)
	setCandidateIndexedState(t, ctx, s, repository, nextCommit, nil, nil)
	nextScope := scope
	nextScope.Commit = nextCommit
	if _, err := s.LatestExtractionDomainOutcome(
		ctx, nextScope,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("advanced scope outcome = %v, want ErrNotFound", err)
	}
	if err := s.DeleteRepo(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestExtractionDomainOutcome(
		ctx, scope,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted repository outcome = %v, want ErrNotFound", err)
	}
}

func TestExtractionDomainOutcomeRejectsPublishedAndOwnerDowngrades(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/outcome-fence"
	commit := strings.Repeat("8", 40)
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://github.com/acme/outcome-fence.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	scope := store.ExtractionScope{
		Repository: repository, Commit: commit, Domain: "proto-contract",
	}
	run, err := s.BeginExtractionRun(ctx, scope, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	published := durableOutcome(
		scope, store.DomainOutcomePublished, run.Extractor,
	)
	published.RunID = run.ID
	if err := s.PublishExtractionRunWithOutcome(
		ctx, run.ID, testCoverage(0, 0), published,
	); err != nil {
		t.Fatal(err)
	}

	retryable := published
	retryable.Disposition = store.DomainOutcomeRetryableFailure
	if err := s.RecordExtractionDomainOutcome(
		ctx, retryable,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("published attempt downgrade = %v, want ErrConflict", err)
	}
	retryable.RunID = ""
	if err := s.RecordExtractionDomainOutcome(
		ctx, retryable,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unstarted settled downgrade = %v, want ErrConflict", err)
	}
	current, err := s.LatestExtractionDomainOutcome(ctx, scope)
	if err != nil ||
		current.Disposition != store.DomainOutcomePublished ||
		current.RunID != run.ID {
		t.Fatalf("published outcome after downgrade attempts = %+v, %v",
			current, err)
	}

	retryable.Generation.Extractor = "2.0.0"
	retryable.Generation.Digest =
		store.ComputeExtractionGenerationDigest(retryable.Generation)
	if err := s.RecordExtractionDomainOutcome(ctx, retryable); err != nil {
		t.Fatalf("new-generation retryable outcome: %v", err)
	}
	current, err = s.LatestExtractionDomainOutcome(ctx, scope)
	if err != nil ||
		current.Disposition != store.DomainOutcomeRetryableFailure ||
		current.Generation.Extractor != "2.0.0" {
		t.Fatalf("new-generation retryable = %+v, %v", current, err)
	}
}

func TestExtractionDomainOutcomeRejectsRetiredAttemptWriter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/outcome-owner-fence"
	commit := strings.Repeat("9", 40)
	if err := s.UpsertRepo(ctx, store.Repo{
		Name:     repository,
		CloneURL: "https://github.com/acme/outcome-owner-fence.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	scope := store.ExtractionScope{
		Repository: repository, Commit: commit, Domain: "proto-contract",
	}
	retired, err := s.BeginExtractionRun(ctx, scope, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AbortExtractionRun(ctx, retired.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.BeginExtractionRun(ctx, scope, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	outcome := durableOutcome(
		scope, store.DomainOutcomeRetryableFailure, retired.Extractor,
	)
	outcome.RunID = retired.ID
	if err := s.RecordExtractionDomainOutcome(
		ctx, outcome,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("retired attempt outcome = %v, want ErrConflict", err)
	}
	attempt, err := s.LatestExtractionAttempt(ctx, scope)
	if err != nil || attempt.RunID != current.ID || attempt.Status != "staged" {
		t.Fatalf("current attempt changed = %+v, %v", attempt, err)
	}
	if _, err := s.LatestExtractionDomainOutcome(
		ctx, scope,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("retired attempt created outcome: %v", err)
	}
}

func TestCandidateControlTerminalRepairAdvancesSameDigestAndClearsOutcome(
	t *testing.T,
) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "github.com/acme/control-repair"
	commit := candidateCommit('3')
	if err := s.UpsertRepo(ctx, store.Repo{
		Name:     repository,
		CloneURL: "https://github.com/acme/control-repair.git",
	}); err != nil {
		t.Fatal(err)
	}
	setCandidateIndexedState(t, ctx, s, repository, commit, nil, nil)
	publication := candidatePublication(repository, commit, "")
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	pointer, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil || pointer.ControlRevision != 1 {
		t.Fatalf("initial pointer = %+v, %v", pointer, err)
	}

	scope := store.ExtractionScope{
		Repository: repository, Commit: commit, Domain: "proto-contract",
	}
	refusal := durableOutcome(
		scope, store.DomainOutcomeTerminalGenerationRefusal, "1.0.0",
	)
	refusal.Generation.CandidateManifestDigest = pointer.ManifestDigest
	refusal.Generation.CandidatePolicyDigest = pointer.PolicyDigest
	refusal.Generation.CandidateControlRevision = pointer.ControlRevision
	refusal.Generation.InventoryPolicy =
		"candidate-manifest-v4-" + strings.Repeat("b", 64)
	refusal.Generation.Digest =
		store.ComputeExtractionGenerationDigest(refusal.Generation)
	refusal.CandidateControlFailure = true
	if err := s.RecordExtractionDomainOutcome(ctx, refusal); err != nil {
		t.Fatalf("record control refusal: %v", err)
	}
	needed, err := s.CandidateControlRepairNeeded(
		ctx, *pointer,
	)
	if err != nil || !needed {
		t.Fatalf("repair needed = %v, %v", needed, err)
	}
	stalePointer := *pointer
	stalePointer.HeadCommit = strings.Repeat("d", 40)
	needed, err = s.CandidateControlRepairNeeded(ctx, stalePointer)
	if err != nil || needed {
		t.Fatalf("stale-scope repair needed = %v, %v", needed, err)
	}
	jobs, err := s.ListJobs(ctx, store.JobCandidate, store.StatusPending)
	if err != nil || len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("candidate repair fanout = %+v, %v", jobs, err)
	}

	publication.ControlRevision = pointer.ControlRevision + 1
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatalf("same-digest strict repair: %v", err)
	}
	repaired, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil || repaired.ControlRevision != 2 ||
		repaired.ManifestDigest != pointer.ManifestDigest {
		t.Fatalf("repaired pointer = %+v, %v", repaired, err)
	}
	if _, err := s.LatestExtractionDomainOutcome(
		ctx, scope,
	); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("repaired control outcome = %v, want ErrNotFound", err)
	}
	needed, err = s.CandidateControlRepairNeeded(
		ctx, *repaired,
	)
	if err != nil || needed {
		t.Fatalf("repair still needed = %v, %v", needed, err)
	}
	extractionJobs, err := s.ListJobs(
		ctx, store.JobExtract, store.StatusPending,
	)
	if err != nil || len(extractionJobs) != 1 {
		t.Fatalf("repair extraction fanout = %+v, %v", extractionJobs, err)
	}

	// An ordinary exact-pointer retry repairs fan-out but never invents a
	// strict control transition.
	publication.ControlRevision = 0
	publication.PublishedAt = time.Time{}
	if err := s.PublishCandidateManifest(ctx, publication); err != nil {
		t.Fatal(err)
	}
	exact, err := s.GetCandidateManifestPublication(ctx, repository)
	if err != nil || exact.ControlRevision != repaired.ControlRevision {
		t.Fatalf("exact retry revision = %+v, %v", exact, err)
	}
}
