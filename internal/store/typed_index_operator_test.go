package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexOperatorReadonlyAndExpectedRevision(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "operator")
	before, err := s.ReadTypedIndexOperator(ctx, f.repo)
	if err != nil || before.Revision == "" || before.Status.Desired != "" {
		t.Fatal(before, err)
	}
	request := typedindex.NewManagedRequest(before.Source, before.Profile, before.ProfileEpoch, before.UniverseDigest, typedindex.Canary)
	raw := typedTestJSON(t, request)
	if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, "sha256:"+strings.Repeat("b", 64), raw); !errors.Is(err, typedindex.Stale) {
		t.Fatal("stale preview accepted", err)
	}
	for range 2 {
		if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, raw); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.ReadTypedIndexOperator(ctx, f.repo)
	if err != nil || after.Revision != before.Revision || after.Coordinator != StatusPending {
		t.Fatal(after, err)
	}
	chunk := f.claim(t)
	if _, err = s.BeginTypedIndex(ctx, chunk); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, raw); !errors.Is(err, typedindex.Stale) {
		t.Fatal("old HEAD accepted", err)
	}
	stale, err := s.ReadTypedIndexOperator(ctx, f.repo)
	if err != nil || !stale.Status.Stale || stale.DesiredFresh || stale.Status.Stage != "" {
		t.Fatal("old active stage crossed HEAD", stale, err)
	}
	repo := "example.invalid/uninitialized-operator"
	if err = s.UpsertRepo(ctx, Repo{Name: repo}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRepoIndexed(ctx, repo, strings.Repeat("a", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	// Remove optional identity to reproduce legacy/restored state. Read must not mint it.
	_, err = storeQuery[any](ctx, s.accounting, s.db, "UPDATE $repo SET typed_incarnation=NONE, typed_source_epoch=NONE RETURN NONE", map[string]any{"repo": repoID(repo)}, storeWrite(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadTypedIndexOperator(ctx, repo); !errors.Is(err, typedindex.Unprepared) {
		t.Fatal("read initialized source", err)
	}
	row, err := s.GetRepo(ctx, repo)
	if err != nil || row.TypedIncarnation != "" || row.TypedSourceEpoch != 0 {
		t.Fatal("read mutated source", row, err)
	}
}

func TestTypedIndexOperatorQueuedScheduleAndEarlyFailure(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	for _, tc := range []struct {
		name                 string
		publication, newHEAD bool
	}{
		{"absent", false, false},
		{"published", true, false},
		{"replacing-stale", true, true},
	} {
		f := newTypedFixture(t, s, "operator-queued-"+tc.name)
		if tc.publication {
			f.enqueue(t, "old-publication")
			chunk := f.claim(t)
			admission, plan := f.seal(t, chunk)
			bundle := f.bundle(t, admission, plan)
			f.advancePublication(t, chunk, bundle)
			if _, err := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteGenerationChunk(ctx, chunk); err != nil {
				t.Fatal(err)
			}
			// The coordinator for this test-only directly executed publication is still pending.
			if _, err := s.CancelPendingJobs(ctx, JobTypedIndex, f.repo); err != nil {
				t.Fatal(err)
			}
		}
		if tc.newHEAD {
			if err := s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		before, err := s.ReadTypedIndexOperator(ctx, f.repo)
		if err != nil {
			t.Fatal(err)
		}
		request := typedindex.NewManagedRequest(before.Source, before.Profile, before.ProfileEpoch, before.UniverseDigest, typedindex.Canary)
		raw := typedTestJSON(t, request)
		if err = s.CheckTypedIndexPreview(ctx, f.repo, before.Revision, raw); err != nil {
			t.Fatal(err)
		}
		if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, raw); err != nil {
			t.Fatal(err)
		}
		coordinator, err := s.ClaimJob(ctx, JobTypedIndex, "coordinator")
		if err != nil || coordinator == nil {
			t.Fatal(coordinator, err)
		}
		if err = s.SetJobStatus(ctx, *coordinator, StatusRunning, ""); err != nil {
			t.Fatal(err)
		}
		spec, err := s.TypedIndexSchedule(ctx, f.repo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.EnqueueGenerationSchedule(ctx, spec); err != nil {
			t.Fatal(err)
		}
		if err = s.SetJobStatus(ctx, *coordinator, StatusDone, ""); err != nil {
			t.Fatal(err)
		}
		for _, expanded := range []bool{false, true} {
			if expanded {
				if _, err = s.ExpandGenerationSchedule(ctx, f.repo, spec.Stage, spec.Generation); err != nil {
					t.Fatal(err)
				}
			}
			queued, err := s.ReadTypedIndexOperator(ctx, f.repo)
			if err != nil || queued.Coordinator != StatusDone || queued.Status.Stage != "" || queued.Schedule == nil || queued.Schedule.Status != GenerationScheduleActive || !queued.DesiredFresh {
				t.Fatal(queued, err)
			}
			if (queued.Status.Current != nil) != (tc.publication && !tc.newHEAD) {
				t.Fatal("last publication lost", queued)
			}
			if queued.Status.Stale != tc.newHEAD {
				t.Fatal("stale current must remain separate from fresh desired", queued)
			}
		}
		chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "worker")
		if err != nil || chunk == nil {
			t.Fatal(chunk, err)
		}
		running, err := s.ReadTypedIndexOperator(ctx, f.repo)
		if err != nil || !running.DesiredFresh || running.Schedule == nil || running.Schedule.Running != 1 || running.Status.Stage != "" {
			t.Fatal("claimed queue lost", running, err)
		}
		if err = s.FailGenerationChunk(ctx, *chunk, "before BeginTypedIndex"); err != nil {
			t.Fatal(err)
		}
		failed, err := s.ReadTypedIndexOperator(ctx, f.repo)
		if err != nil || failed.Status.Stage != "" || failed.Schedule == nil || failed.Schedule.Failed != 1 || failed.Schedule.Status != GenerationScheduleSettled {
			t.Fatal(failed, err)
		}
		if err = s.CheckTypedIndexPreview(ctx, f.repo, before.Revision, raw); !errors.Is(err, ErrTypedIndexRequestRecorded) {
			t.Fatal("recorded canary preview", err)
		}
		// Same bytes remain a transport confirmation, never a second coordinator.
		if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, raw); err != nil {
			t.Fatal(err)
		}
		pending, err := s.queuePendingIDs(ctx, JobTypedIndex, f.repo, "")
		if err != nil || len(pending) != 0 {
			t.Fatal(pending, err)
		}
		dry := typedTestJSON(t, typedindex.NewManagedRequest(before.Source, before.Profile, before.ProfileEpoch, before.UniverseDigest, typedindex.DryRun))
		if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, dry); err != nil {
			t.Fatal(err)
		}
		if err = s.CheckTypedIndexPreview(ctx, f.repo, before.Revision, raw); !errors.Is(err, ErrTypedIndexRequestRecorded) {
			t.Fatal("superseded purpose preview", err)
		}
		if _, err = s.CancelPendingJobs(ctx, JobTypedIndex, f.repo); err != nil {
			t.Fatal(err)
		}
		// Failure after BeginTypedIndex is also consumed, while another purpose
		// remains available. Returning to dry-run cannot be repaired by refresh.
		dryChunk := f.claim(t)
		if _, err = s.BeginTypedIndex(ctx, dryChunk); err != nil {
			t.Fatal(err)
		}
		started, err := s.ReadTypedIndexOperator(ctx, f.repo)
		if err != nil || !started.DesiredFresh || started.Status.Stage != TypedPreflight || started.Status.Stale != tc.newHEAD {
			t.Fatal("fresh attempt hidden by prior navigation", started, err)
		}
		if err = s.FailTypedIndex(ctx, dryChunk, typedindex.ExecutionFailed); err != nil {
			t.Fatal(err)
		}
		if err = s.FailGenerationChunk(ctx, dryChunk, "execution_failed"); err != nil {
			t.Fatal(err)
		}
		if err = s.CheckTypedIndexPreview(ctx, f.repo, before.Revision, dry); !errors.Is(err, ErrTypedIndexRequestRecorded) {
			t.Fatal("failed attempt preview", err)
		}
		if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, dry); err != nil {
			t.Fatal("failed exact transport confirmation", err)
		}
		publish := typedTestJSON(t, typedindex.NewManagedRequest(before.Source, before.Profile, before.ProfileEpoch, before.UniverseDigest, typedindex.Publish))
		if err = s.CheckTypedIndexPreview(ctx, f.repo, before.Revision, publish); err != nil {
			t.Fatal("fresh purpose refused", err)
		}
		if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, publish); err != nil {
			t.Fatal(err)
		}
		if err = s.CheckTypedIndexPreview(ctx, f.repo, before.Revision, dry); !errors.Is(err, ErrTypedIndexRequestRecorded) {
			t.Fatal("round-trip purpose preview", err)
		}
		if _, err = s.CancelPendingJobs(ctx, JobTypedIndex, f.repo); err != nil {
			t.Fatal(err)
		}
	}
}
