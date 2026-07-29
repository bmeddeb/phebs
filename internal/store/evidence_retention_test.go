package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func addEvidenceSweepProgress(total *EvidenceSweepProgress, step EvidenceSweepProgress) {
	total.RunsMarkedDeleting += step.RunsMarkedDeleting
	total.RunsDeleted += step.RunsDeleted
	total.AssociationRowsDeleted += step.AssociationRowsDeleted
	total.AssertionRowsDeleted += step.AssertionRowsDeleted
	total.AtomRowsDeleted += step.AtomRowsDeleted
	total.RetentionPhasesAdvanced += step.RetentionPhasesAdvanced
}

// sweepEvidenceRun preserves the old one-call test vocabulary while driving
// the production one-step API until it either completes one logical run or
// proves the store drained. Tests that exercise crash boundaries call the
// production method directly.
func sweepEvidenceRun(
	ctx context.Context, s *Surreal, now time.Time, staleStagedAfter time.Duration,
) (int, error) {
	for stepCount := 0; stepCount < 100_000; stepCount++ {
		step, err := s.SweepEvidence(ctx, now, staleStagedAfter)
		if err != nil {
			return 0, err
		}
		if step.RunsDeleted > 1 {
			return 0, fmt.Errorf("invalid logical run count %d", step.RunsDeleted)
		}
		if step.RunsDeleted == 1 {
			return 1, nil
		}
		if !step.DidWork() {
			return 0, nil
		}
	}
	return 0, errors.New("evidence sweep did not complete within the test step bound")
}

type evidenceRetentionState struct {
	Status string `json:"status"`
	Phase  string `json:"retention_phase"`
}

func readEvidenceRetentionState(t *testing.T, s *Surreal, runID string) (evidenceRetentionState, bool) {
	t.Helper()
	results, err := surrealdb.Query[[]evidenceRetentionState](t.Context(), s.db,
		"SELECT status, retention_phase FROM $rid",
		map[string]any{"rid": extractionRunID(runID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0], true
		}
	}
	return evidenceRetentionState{}, false
}

func seedT205Run(
	t *testing.T, s *Surreal, repo, commit, domain string, facts int,
) *ExtractionRun {
	t.Helper()
	run, err := beginExtractionRun(s, t.Context(), repo, commit, domain, "t205-retention-v1")
	if err != nil {
		t.Fatal(err)
	}
	atoms, associations, assertions := t201EvidenceBatch(run, 0, facts)
	if facts > 0 {
		if err := s.AddEvidence(t.Context(), run.ID, atoms, associations, assertions); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PublishExtractionRun(t.Context(), run.ID, t203Coverage(facts)); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestT205PinnedRunNeverEntersDeleting(t *testing.T) {
	s := newRunnerStore(t)
	const (
		repo   = "synthetic.invalid/t205-pinned"
		commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		domain = "t20-retention"
	)
	if err := s.UpsertRepo(t.Context(), Repo{Name: repo, CloneURL: "file:///synthetic/t205-pinned.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(t.Context(), repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	pinned := seedT205Run(t, s, repo, commit, domain, 1)
	if err := s.PinRun(t.Context(), pinned.ID, "t205-proof"); err != nil {
		t.Fatal(err)
	}
	current := seedT205Run(t, s, repo, commit, domain, 1)

	progress, err := s.SweepEvidence(t.Context(), time.Now().UTC(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if progress.DidWork() {
		t.Fatalf("pinned-only sweep made progress: %+v", progress)
	}
	state, found := readEvidenceRetentionState(t, s, pinned.ID)
	if !found || state.Status != "superseded" || state.Phase != "" {
		t.Fatalf("pinned run entered retention state: %+v, found=%v", state, found)
	}
	latest, err := latestPublishedRun(s, t.Context(), repo, domain)
	if err != nil || latest.ID != current.ID {
		t.Fatalf("current publication = %+v, %v", latest, err)
	}
}

func TestT205ChunkBoundariesResumeAndPreserveSharedAtoms(t *testing.T) {
	s := newRunnerStore(t)
	const (
		repo   = "synthetic.invalid/t205-resume"
		commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		domain = "t20-retention"
		facts  = evidenceSweepRowBatchSize + 88
	)
	if err := s.UpsertRepo(t.Context(), Repo{Name: repo, CloneURL: "file:///synthetic/t205-resume.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(t.Context(), repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	old := seedT205Run(t, s, repo, commit, domain, facts)
	current := seedT205Run(t, s, repo, commit, domain, facts)

	// Each call is deliberately made through a fresh wrapper carrying no
	// in-memory cursor. Every committed return is therefore a simulated crash
	// boundary whose only continuation authority is the stored status/phase.
	var total EvidenceSweepProgress
	for stepCount := 0; stepCount < 16 && total.RunsDeleted == 0; stepCount++ {
		resumer := &Surreal{db: s.db}
		step, err := resumer.SweepEvidence(t.Context(), time.Now().UTC(), 0)
		if err != nil {
			t.Fatalf("resume step %d: %v", stepCount, err)
		}
		if !step.DidWork() {
			t.Fatalf("resume step %d made no progress", stepCount)
		}
		if step.AssociationRowsDeleted > evidenceSweepRowBatchSize ||
			step.AssertionRowsDeleted > evidenceSweepRowBatchSize ||
			(step.AssociationRowsDeleted > 0 && step.AssertionRowsDeleted > 0) {
			t.Fatalf("step %d exceeded one physical chunk: %+v", stepCount, step)
		}
		addEvidenceSweepProgress(&total, step)
		if stepCount == 0 {
			if err := s.applySchema(t.Context()); err != nil {
				t.Fatalf("schema reapply at first crash boundary: %v", err)
			}
		}

		latest, latestErr := latestPublishedRun(s, t.Context(), repo, domain)
		if latestErr != nil || latest.ID != current.ID {
			t.Fatalf("step %d resurrected old evidence: %+v, %v", stepCount, latest, latestErr)
		}
		if total.RunsDeleted == 0 {
			state, found := readEvidenceRetentionState(t, s, old.ID)
			if !found || state.Status != "deleting" ||
				(state.Phase != "associations" && state.Phase != "assertions" &&
					state.Phase != "finalize") {
				t.Fatalf("step %d durable continuation = %+v, found=%v", stepCount, state, found)
			}
		}
	}
	if total.RunsMarkedDeleting != 1 || total.RunsDeleted != 1 ||
		total.AssociationRowsDeleted != facts || total.AssertionRowsDeleted != facts ||
		total.AtomRowsDeleted != 0 || total.RetentionPhasesAdvanced != 2 {
		t.Fatalf("resumable sweep totals = %+v", total)
	}
	if _, found := readEvidenceRetentionState(t, s, old.ID); found {
		t.Fatal("completed deleting run still exists")
	}
	counts := t203StoredCounts(t, t.Context(), s, current.ID)
	if counts.Associations != facts || counts.Assertions != facts || counts.Atoms != facts {
		t.Fatalf("current shared evidence changed: %+v", counts)
	}
	atoms, _, _ := t201EvidenceBatch(current, 0, 1)
	resolved, err := s.ResolveEvidence(t.Context(), repo, current.ID, atoms[0].ID)
	if err != nil || len(resolved.Occurrences) != 1 {
		t.Fatalf("shared atom did not survive: %+v, %v", resolved, err)
	}
}

func TestT205AssociationChunkCardinalityAlwaysUsesAnArray(t *testing.T) {
	s := newRunnerStore(t)
	const (
		repo   = "synthetic.invalid/t205-cardinality"
		commit = "cccccccccccccccccccccccccccccccccccccccc"
	)
	if err := s.UpsertRepo(t.Context(), Repo{
		Name: repo, CloneURL: "file:///synthetic/t205-cardinality.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(t.Context(), repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, facts := range []int{0, 1} {
		domain := fmt.Sprintf("t20-retention-cardinality-%d", facts)
		old := seedT205Run(t, s, repo, commit, domain, facts)
		seedT205Run(t, s, repo, commit, domain, facts)
		deleted, err := sweepEvidenceRun(t.Context(), s, time.Now().UTC(), 0)
		if err != nil || deleted != 1 {
			t.Fatalf("%d-row sweep = %d, %v", facts, deleted, err)
		}
		if _, found := readEvidenceRetentionState(t, s, old.ID); found {
			t.Fatalf("%d-row swept run still exists", facts)
		}
	}
}

func TestT205RetentionStepRejectsInvalidDurableState(t *testing.T) {
	for _, candidate := range []evidenceSweepCandidateRec{
		{Status: "deleting"},
		{Status: "deleting", Phase: "unknown"},
		{Status: "aborted", Phase: "associations"},
		{Status: "published"},
	} {
		if _, err := evidenceSweepStepSQL(candidate); err == nil {
			t.Fatalf("candidate %+v was accepted", candidate)
		}
	}
}
