package store_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func guidedRequest(key string) store.GuidedInvestigationRequest {
	return store.GuidedInvestigationRequest{
		IdempotencyKey:     key,
		PreviewDigest:      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Referent:           "grpc:/example.payment.v1.PaymentService/Authorize",
		ClaimFamily:        "consumer-census",
		Title:              "Payment migration",
		NormalizedQuestion: "which consumers call Authorize?",
		DecisionSought:     "approve migration inventory",
		DeclaredUniverse:   `{"repositories":["github.com/example/consumer"]}`,
		SnapshotPolicy:     "exact indexed commits",
		BuildConfiguration: "linux/amd64",
		PackSelection:      "grpc-go@1",
		EnumerationMethod:  "authorized repository list",
		ResolvedInputIdentities: []string{
			"repo:github.com/example/consumer@aaaaaaaa",
		},
		RequestedSnapshot:     `{"repositories":[{"name":"github.com/example/consumer","commit":"aaaaaaaa"}]}`,
		InputManifestDigest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RequestedPackVersions: []string{"grpc-go@1"},
		Principal:             authzOwner,
	}
}

func TestInvestigationGuidedCreationIsConcurrentAndQueueIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	request := guidedRequest("guided-concurrent")

	const callers = 24
	results := make(chan *store.GuidedInvestigationCreation, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := s.CreateGuidedInvestigation(ctx, request)
			if err != nil {
				errs <- err
				return
			}
			results <- created
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent guided creation: %v", err)
	}
	var want *store.GuidedInvestigationCreation
	for got := range results {
		if want == nil {
			want = got
			continue
		}
		if got.Investigation.ID != want.Investigation.ID ||
			got.Revision.ID != want.Revision.ID || got.Run.ID != want.Run.ID {
			t.Fatalf("idempotent creation returned competing identities:\nwant=%+v\ngot=%+v", want, got)
		}
	}
	if want == nil || want.Investigation.Lifecycle != "active" ||
		want.Investigation.CurrentRevisionID != want.Revision.ID ||
		want.Run.State != store.RunQueued {
		t.Fatalf("guided creation = %+v", want)
	}
	jobs, err := s.ListJobs(ctx, store.JobInvestigate, store.StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Target != want.Run.ID {
		t.Fatalf("investigation jobs = %+v, want one slot for %s", jobs, want.Run.ID)
	}

	changed := request
	changed.Title = "different frozen request"
	if _, err := s.CreateGuidedInvestigation(ctx, changed); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("changed idempotent request = %v, want ErrConflict", err)
	}
}

func TestInvestigationRunLeaseRetriesCancellationAndLateWorkerFence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateGuidedInvestigation(ctx, guidedRequest("guided-cancel"))
	if err != nil {
		t.Fatal(err)
	}
	runID := created.Run.ID
	first, err := s.AcquireInvestigationRunLease(ctx, runID, "worker:first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceInvestigationRun(ctx, *first, store.RunEnumerating, "scope frozen"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryInvestigationRun(ctx, *first, "transient catalog error", 2)
	if err != nil || retry.Attempt != 2 || retry.NewState != store.RunQueued {
		t.Fatalf("retry event = %+v, %v", retry, err)
	}
	if _, err := s.AdvanceInvestigationRun(ctx, *first, store.RunAnalyzing, "late old attempt"); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old lease advance = %v, want ErrLeaseLost", err)
	}
	second, err := s.AcquireInvestigationRunLease(ctx, runID, "worker:second")
	if err != nil || second.Attempt != 2 {
		t.Fatalf("second lease = %+v, %v", second, err)
	}
	if _, err := s.RetryInvestigationRun(ctx, *second, "another transient error", 2); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("retry past bound = %v, want ErrConflict", err)
	}
	for _, step := range []store.RunState{store.RunEnumerating, store.RunAnalyzing, store.RunPublishing} {
		if _, err := s.AdvanceInvestigationRun(ctx, *second, step, "advance"); err != nil {
			t.Fatalf("advance %s: %v", step, err)
		}
	}
	if _, err := s.CancelInvestigationRunAs(ctx, authzReader, runID, "not mine"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reader cancel = %v, want ErrNotFound", err)
	}
	if _, err := s.CancelInvestigationRunAs(ctx, authzOwner, runID, "question withdrawn"); err != nil {
		t.Fatalf("owner cancel: %v", err)
	}
	late := store.RunArtifact{
		Scope: "investigation:late", RunID: runID,
		SnapshotManifest: "snapshot", InputManifest: "input",
		CoverageLedger: completeInvestigationCoverage(t, 1), FactReferences: []string{"fact:late"},
		EligibilityResult: "ineligible:canceled",
	}
	if _, err := s.PublishInvestigationRun(ctx, *second, late); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("late publication = %v, want ErrLeaseLost", err)
	}
	run, err := s.GetRunAs(ctx, authzOwner, runID)
	if err != nil || run.State != store.RunCanceled {
		t.Fatalf("canceled run = %+v, %v", run, err)
	}
	if _, err := s.GetRunArtifactForRunAs(ctx, authzOwner, runID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("canceled run artifact = %v, want ErrNotFound", err)
	}
}

func TestInvestigationRunStaleLeaseRecoveryIsTimeAndAttemptFenced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateGuidedInvestigation(
		ctx,
		guidedRequest("guided-stale-recovery"),
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AcquireInvestigationRunLease(
		ctx,
		created.Run.ID,
		"worker:first",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceInvestigationRun(
		ctx,
		*first,
		store.RunEnumerating,
		"scope frozen",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequeueStaleInvestigationRun(
		ctx,
		created.Run.ID,
		"worker:recovery",
		first.AcquiredAt.Add(-time.Second),
		"not stale",
		3,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("live lease recovery = %v, want ErrConflict", err)
	}
	requeued, err := s.RequeueStaleInvestigationRun(
		ctx,
		created.Run.ID,
		"worker:recovery",
		first.AcquiredAt.Add(time.Second),
		"worker disappeared",
		3,
	)
	if err != nil || requeued.Attempt != 2 ||
		requeued.PriorState != store.RunEnumerating ||
		requeued.NewState != store.RunQueued {
		t.Fatalf("stale recovery = %+v, %v", requeued, err)
	}
	if _, err := s.AdvanceInvestigationRun(
		ctx,
		*first,
		store.RunAnalyzing,
		"late first worker",
	); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("recovered worker retained lease: %v", err)
	}
	second, err := s.AcquireInvestigationRunLease(
		ctx,
		created.Run.ID,
		"worker:second",
	)
	if err != nil || second.Attempt != 2 {
		t.Fatalf("second lease = %+v, %v", second, err)
	}
}

func TestInvestigationPartialFailurePublishesOnlyCoverageDiagnostics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateGuidedInvestigation(ctx, guidedRequest("guided-partial-failure"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireInvestigationRunLease(ctx, created.Run.ID, "worker:failure")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []store.RunState{store.RunEnumerating, store.RunAnalyzing} {
		if _, err := s.AdvanceInvestigationRun(ctx, *lease, step, "advance"); err != nil {
			t.Fatalf("advance %s: %v", step, err)
		}
	}
	coverage, err := store.CanonicalInvestigationCoverageLedger(store.InvestigationCoverageLedger{
		EligibleUnits: 13, Analyzed: 10, Partial: 2, Failed: 1,
		Failures: []store.InvestigationCoverageFailure{
			{Unit: "repo:github.com/example/consumer@aaaaaaaa", Code: "EXTRACTOR_FAILED", Retryable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := s.FailInvestigationRun(ctx, *lease, store.RunArtifact{
		Scope: "investigation:partial-failure", RunID: created.Run.ID,
		SnapshotManifest: "snapshot", InputManifest: "input",
		CoverageLedger:    coverage,
		EligibilityResult: "eligible:false:UNITS_FAILED,UNITS_PARTIAL",
		DiagnosticData:    "bounded extractor failure",
	}, "analysis exhausted bounded retries")
	if err != nil {
		t.Fatalf("fail run: %v", err)
	}
	if artifact.TerminalStatus != store.RunFailed || len(artifact.FactReferences) != 0 ||
		len(artifact.PinReferences) != 0 || !strings.Contains(artifact.CoverageLedger, `"partial":2`) ||
		!strings.Contains(artifact.CoverageLedger, `"failed":1`) {
		t.Fatalf("failed artifact = %+v", artifact)
	}
	read, err := s.GetRunArtifactForRunAs(ctx, authzOwner, created.Run.ID)
	if err != nil || read.ID != artifact.ID {
		t.Fatalf("read failed artifact = %+v, %v", read, err)
	}
	run, err := s.GetRunAs(ctx, authzOwner, created.Run.ID)
	if err != nil || run.State != store.RunFailed {
		t.Fatalf("failed run = %+v, %v", run, err)
	}
	if _, err := s.PublishInvestigationRun(ctx, *lease, store.RunArtifact{
		Scope: "investigation:late-after-failure", RunID: created.Run.ID,
		SnapshotManifest: "snapshot", InputManifest: "input",
		CoverageLedger: coverage, EligibilityResult: "ineligible",
	}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("publish after failure = %v, want ErrLeaseLost", err)
	}
}
