package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func seedInvestigation(t *testing.T, s *store.Surreal) (*store.Investigation, *store.Revision) {
	t.Helper()
	ctx := context.Background()
	investigation, err := s.CreateInvestigation(ctx, store.Investigation{
		Referent:    "grpc:/example.payment.v1.PaymentService/Authorize",
		ClaimFamily: "consumer-census", Title: "Payment migration", Owner: "user:owner",
	})
	if err != nil {
		t.Fatalf("create investigation: %v", err)
	}
	revision, err := s.PutRevision(ctx, store.Revision{
		InvestigationID: investigation.ID, Seq: 1,
		NormalizedQuestion: "which consumers call Authorize?",
		DecisionSought:     "approve migration inventory",
		DeclaredUniverse:   "repo:example/consumer@abc",
		SnapshotPolicy:     "exact commit", BuildConfiguration: "linux/amd64",
		PackSelection: "grpc-go@1", EnumerationMethod: "bounded repository list",
		Creator: "user:owner",
	})
	if err != nil {
		t.Fatalf("put revision: %v", err)
	}
	investigation.CurrentRevisionID = revision.ID
	investigation.Lifecycle = "active"
	investigation, err = s.UpdateInvestigation(ctx, *investigation)
	if err != nil {
		t.Fatalf("activate investigation: %v", err)
	}
	return investigation, revision
}

func testRunRequest(revisionID string) store.RunRequest {
	return store.RunRequest{
		RevisionID: revisionID, IdempotencyKey: "request-1",
		ResolvedInputIdentities: []string{"repo:example/consumer@abc"},
		RequestedSnapshot:       "example/consumer@abc",
		InputManifestDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RequestedPackVersions:   []string{"grpc-go@1"}, CreatedBy: "user:owner",
	}
}

func completeInvestigationCoverage(t *testing.T, units int) string {
	t.Helper()
	coverage, err := store.CanonicalInvestigationCoverageLedger(store.InvestigationCoverageLedger{
		EligibleUnits: units,
		Analyzed:      units,
	})
	if err != nil {
		t.Fatalf("canonical investigation coverage: %v", err)
	}
	return coverage
}

func advanceToPublishing(t *testing.T, s *store.Surreal, runID string) store.InvestigationRunLease {
	t.Helper()
	ctx := context.Background()
	lease, err := s.AcquireInvestigationRunLease(ctx, runID, "worker:one")
	if err != nil {
		t.Fatalf("acquire run lease: %v", err)
	}
	for _, row := range []struct {
		state  store.RunState
		reason string
	}{
		{store.RunEnumerating, "scope frozen"},
		{store.RunAnalyzing, "enumeration complete"},
		{store.RunPublishing, "analysis complete"},
	} {
		if _, err := s.AdvanceInvestigationRun(ctx, *lease, row.state, row.reason); err != nil {
			t.Fatalf("append %s: %v", row.state, err)
		}
		got, err := s.GetRun(ctx, runID)
		if err != nil || got.State != row.state {
			t.Fatalf("state after %s = %+v, %v", row.state, got, err)
		}
	}
	return *lease
}

func putPublishedArtifact(t *testing.T, s *store.Surreal, runID string) *store.RunArtifact {
	t.Helper()
	lease := advanceToPublishing(t, s, runID)
	artifact, err := s.PublishInvestigationRun(context.Background(), lease, store.RunArtifact{
		Scope: "investigation:test", RunID: runID, TerminalStatus: store.RunPublished,
		SnapshotManifest: "sha256:snapshot", InputManifest: "sha256:input",
		CoverageLedger: completeInvestigationCoverage(t, 1), FactReferences: []string{"fact:one"},
		EligibilityResult: "eligible:false:blocker",
	})
	if err != nil {
		t.Fatalf("put artifact: %v", err)
	}
	return artifact
}

func TestInvestigationRunStateAndIdempotentCreation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, revision := seedInvestigation(t, s)
	request := testRunRequest(revision.ID)

	const callers = 24
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := s.CreateRun(ctx, request)
			if err != nil {
				errs <- err
				return
			}
			ids <- run.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreateRun: %v", err)
		}
	}
	var runID string
	for id := range ids {
		if runID == "" {
			runID = id
		}
		if id != runID {
			t.Fatalf("idempotent creates returned %q and %q", runID, id)
		}
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil || run.State != store.RunQueued || run.Seq != 1 {
		t.Fatalf("initial run = %+v, %v", run, err)
	}
	events, err := s.ListRunEvents(ctx, runID)
	if err != nil || len(events) != 1 || events[0].NewState != store.RunQueued {
		t.Fatalf("initial events = %+v, %v", events, err)
	}

	if _, err := s.AppendRunEvent(ctx, runID, store.RunEvent{
		NewState: store.RunPublishing, Actor: "worker:one", Reason: "skip states",
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("skipped transition error = %v, want ErrConflict", err)
	}
	lease := advanceToPublishing(t, s, runID)
	if _, err := s.AppendRunEvent(ctx, runID, store.RunEvent{
		NewState: store.RunFailed, Actor: "worker:late", Reason: "late failure",
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("terminal transition error = %v, want ErrConflict", err)
	}

	changed := request
	changed.RequestedSnapshot = "example/consumer@def"
	if _, err := s.CreateRun(ctx, changed); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("idempotency reuse error = %v, want ErrConflict", err)
	}
	artifact, err := s.PublishInvestigationRun(ctx, lease, store.RunArtifact{
		Scope: "investigation:test", RunID: runID,
		SnapshotManifest: "sha256:snapshot", InputManifest: "sha256:input",
		CoverageLedger: completeInvestigationCoverage(t, 1), FactReferences: []string{"fact:one"},
		EligibilityResult: "eligible:false:blocker",
	})
	if err != nil {
		t.Fatalf("publish artifact: %v", err)
	}
	again, err := s.PutRunArtifact(ctx, *artifact)
	if err != nil || again.ID != artifact.ID {
		t.Fatalf("artifact exact retry = %+v, %v", again, err)
	}
}

func TestInvestigationImmutableRecordsAndSupersession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	investigation, revision := seedInvestigation(t, s)
	run, err := s.CreateRun(ctx, testRunRequest(revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	artifact := putPublishedArtifact(t, s, run.ID)

	decision, err := s.PutDecision(ctx, store.Decision{
		InvestigationID: investigation.ID, ClaimScope: "consumer-census",
		Conclusion: "no_decision", RevisionID: revision.ID, RunArtifactID: artifact.ID,
		EligibilityResult: "eligible:false:blocker", VisibleScope: "repo:example/consumer",
		Authority: "user:owner", AuthoritySource: "investigation owner",
		Rationale: "needs review",
	})
	if err != nil {
		t.Fatalf("put decision: %v", err)
	}
	disposition, err := s.PutDisposition(ctx, store.Disposition{
		InvestigationID: investigation.ID, Category: "will_migrate",
		SubjectKind: "consumer", SubjectID: "consumer:one", Actor: "user:owner",
		AuthorityBasis: "subject owner", Rationale: "scheduled",
	})
	if err != nil {
		t.Fatalf("put disposition: %v", err)
	}
	baseline, err := s.PutBaselineDesignation(ctx, store.BaselineDesignation{
		InvestigationID: investigation.ID, ClaimScope: "consumer-census",
		WorkflowScope: "migration", RevisionID: revision.ID, RunArtifactID: artifact.ID,
		Authority: "user:owner", Rationale: "accepted inventory",
	})
	if err != nil {
		t.Fatalf("put baseline: %v", err)
	}
	watch, err := s.CreateWatch(ctx, store.Watch{Owner: "user:owner", Enabled: true})
	if err != nil {
		t.Fatalf("create watch: %v", err)
	}
	watchRevision, err := s.PutWatchRevision(ctx, store.WatchRevision{
		WatchID: watch.ID, Seq: 1, InvestigationID: investigation.ID, RevisionID: revision.ID,
		TypedQuery: "claim:consumer-census", TriggerPolicy: "on-publish",
		Coalescing: "one-per-run", NoiseControls: "material-only", Creator: "user:owner",
	})
	if err != nil {
		t.Fatalf("put watch revision: %v", err)
	}
	events, err := s.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}

	rows := []struct {
		name string
		edit func() error
	}{
		{name: "revision", edit: func() error {
			changed := *revision
			changed.NormalizedQuestion = "edited in place"
			changed.ContentDigest = ""
			_, err := s.PutRevision(ctx, changed)
			return err
		}},
		{name: "run request", edit: func() error {
			changed := testRunRequest(revision.ID)
			changed.InputManifestDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			_, err := s.CreateRun(ctx, changed)
			return err
		}},
		{name: "run event", edit: func() error {
			changed := events[len(events)-1]
			changed.Reason = "edited in place"
			changed.Digest = ""
			_, err := s.AppendRunEvent(ctx, run.ID, changed)
			return err
		}},
		{name: "run artifact", edit: func() error {
			changed := *artifact
			changed.DiagnosticData = "edited in place"
			_, err := s.PutRunArtifact(ctx, changed)
			return err
		}},
		{name: "decision", edit: func() error {
			changed := *decision
			changed.Rationale = "edited in place"
			changed.ContentDigest = ""
			_, err := s.PutDecision(ctx, changed)
			return err
		}},
		{name: "disposition", edit: func() error {
			changed := *disposition
			changed.Rationale = "edited in place"
			changed.ContentDigest = ""
			_, err := s.PutDisposition(ctx, changed)
			return err
		}},
		{name: "baseline", edit: func() error {
			changed := *baseline
			changed.Rationale = "edited in place"
			changed.ContentDigest = ""
			_, err := s.PutBaselineDesignation(ctx, changed)
			return err
		}},
		{name: "watch revision", edit: func() error {
			changed := *watchRevision
			changed.TypedQuery = "edited in place"
			changed.ContentDigest = ""
			_, err := s.PutWatchRevision(ctx, changed)
			return err
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if err := row.edit(); err == nil {
				t.Fatal("in-place immutable edit succeeded")
			}
		})
	}

	revision2, err := s.PutRevision(ctx, store.Revision{
		InvestigationID: investigation.ID, Seq: 2,
		NormalizedQuestion: "which consumers still call Authorize?",
		DecisionSought:     revision.DecisionSought, DeclaredUniverse: revision.DeclaredUniverse,
		SnapshotPolicy: revision.SnapshotPolicy, BuildConfiguration: revision.BuildConfiguration,
		PackSelection: revision.PackSelection, EnumerationMethod: revision.EnumerationMethod,
		Creator: "user:owner",
	})
	if err != nil || revision2.ID == revision.ID {
		t.Fatalf("revision correction = %+v, %v", revision2, err)
	}
	decision2 := *decision
	decision2.ID, decision2.ContentDigest, decision2.CreatedAt = "", "", time.Time{}
	decision2.Supersedes, decision2.Rationale = decision.ID, "review completed"
	if _, err := s.PutDecision(ctx, decision2); err != nil {
		t.Fatalf("decision supersession: %v", err)
	}
	disposition2 := *disposition
	disposition2.ID, disposition2.ContentDigest, disposition2.CreatedAt = "", "", time.Time{}
	disposition2.Supersedes, disposition2.Rationale = disposition.ID, "rescheduled"
	if _, err := s.PutDisposition(ctx, disposition2); err != nil {
		t.Fatalf("disposition supersession: %v", err)
	}
	baseline2 := *baseline
	baseline2.ID, baseline2.ContentDigest, baseline2.CreatedAt = "", "", time.Time{}
	baseline2.Supersedes, baseline2.Rationale = baseline.ID, "new acceptance"
	if _, err := s.PutBaselineDesignation(ctx, baseline2); err != nil {
		t.Fatalf("baseline supersession: %v", err)
	}
	watchRevision2, err := s.PutWatchRevision(ctx, store.WatchRevision{
		WatchID: watch.ID, Seq: 2, InvestigationID: investigation.ID, RevisionID: revision2.ID,
		TypedQuery: "claim:consumer-census revision:2", TriggerPolicy: "on-publish",
		Coalescing: "one-per-run", NoiseControls: "material-only", Creator: "user:owner",
	})
	if err != nil {
		t.Fatalf("watch revision correction: %v", err)
	}
	watch.CurrentRevisionID = watchRevision2.ID
	watch.Owner = "user:new-owner"
	if _, err := s.UpdateWatch(ctx, *watch); err != nil {
		t.Fatalf("mutable watch update: %v", err)
	}
	changedInvestigation := *investigation
	changedInvestigation.Referent = "grpc:/other.Service/Method"
	if _, err := s.UpdateInvestigation(ctx, changedInvestigation); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("investigation referent edit = %v, want ErrConflict", err)
	}
}
