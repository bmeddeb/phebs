package store_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func newTestStore(t *testing.T) *store.Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed (https://surrealdb.com/install)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	s, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func TestSchemaIdempotent(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	for i := range 2 { // second open re-applies the schema over persisted data
		s, err := store.OpenLocal(ctx, dir)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

func TestRepoCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	repos := []store.Repo{
		{Name: "github.com/foo/bar", CloneURL: "https://github.com/foo/bar.git",
			DefaultBranch: "main", IsPublic: true, PushedAt: &now,
			Metadata: map[string]any{"stars": int64(7)}},
		{Name: "example.com/baz", CloneURL: "https://example.com/baz.git", IsFork: true},
	}
	for _, r := range repos {
		if err := s.UpsertRepo(ctx, r); err != nil {
			t.Fatalf("upsert %s: %v", r.Name, err)
		}
	}

	got, err := s.GetRepo(ctx, "github.com/foo/bar")
	if err != nil {
		t.Fatal(err)
	}
	if got.CloneURL != repos[0].CloneURL || !got.IsPublic || got.DefaultBranch != "main" {
		t.Errorf("GetRepo = %+v, want fields of %+v", got, repos[0])
	}
	if got.PushedAt == nil || !got.PushedAt.Equal(now) {
		t.Errorf("PushedAt = %v, want %v", got.PushedAt, now)
	}

	// upsert same name = update in place, not a duplicate
	repos[0].DefaultBranch = "trunk"
	if err := s.UpsertRepo(ctx, repos[0]); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListRepos len = %d, want 2", len(all))
	}
	if all[1].DefaultBranch != "trunk" { // ordered by name: example.com first
		t.Errorf("updated DefaultBranch = %q, want trunk", all[1].DefaultBranch)
	}

	if err := s.DeleteRepo(ctx, "example.com/baz"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRepo(ctx, "example.com/baz"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete, err = %v, want ErrNotFound", err)
	}
}

func TestJobLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, kind := range []store.JobKind{store.JobSync, store.JobIndex} {
		t.Run(string(kind), func(t *testing.T) {
			job, err := s.CreateJob(ctx, kind, "target-1")
			if err != nil {
				t.Fatalf("CreateJob: %v", err)
			}
			if job.ID == "" || job.Status != store.StatusPending {
				t.Fatalf("created job = %+v, want id set and pending", job)
			}

			pending, err := s.ListJobs(ctx, kind, store.StatusPending)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || pending[0].Target != "target-1" {
				t.Fatalf("pending = %+v, want the created job", pending)
			}

			if err := s.SetJobStatus(ctx, job.ID, store.StatusRunning, ""); err != nil {
				t.Fatalf("to running: %v", err)
			}
			if err := s.SetJobStatus(ctx, job.ID, store.StatusFailed, "boom"); err != nil {
				t.Fatalf("to failed: %v", err)
			}

			failed, err := s.ListJobs(ctx, kind, store.StatusFailed)
			if err != nil {
				t.Fatal(err)
			}
			if len(failed) != 1 || failed[0].Error != "boom" || failed[0].FinishedAt == nil {
				t.Fatalf("failed job = %+v, want error recorded and finished_at set", failed)
			}

			if err := s.SetJobStatus(ctx, string(kind)+":nope", store.StatusDone, ""); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("unknown id err = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestClaimJobConcurrent is the T1.3 AC: N concurrent pollers drain the
// queue through the shipped ClaimJob with zero double-claims.
func TestClaimJobConcurrent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const jobs, workers = 100, 5

	for i := range jobs {
		if _, err := s.CreateJob(ctx, store.JobIndex, fmt.Sprintf("repo-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	claimed := map[string]string{}
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(who string) {
			defer wg.Done()
			for {
				job, err := s.ClaimJob(ctx, store.JobIndex, who)
				if errors.Is(err, store.ErrNotFound) {
					return
				}
				if err != nil {
					t.Errorf("%s: %v", who, err)
					return
				}
				if job.Status != store.StatusClaimed || job.ClaimedBy != who {
					t.Errorf("claimed job = %+v, want status claimed by %s", job, who)
				}
				mu.Lock()
				if prev, dup := claimed[job.ID]; dup {
					t.Errorf("double claim: %s by %s and %s", job.ID, prev, who)
				}
				claimed[job.ID] = who
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(claimed) != jobs {
		t.Errorf("claimed %d unique jobs, want %d", len(claimed), jobs)
	}
	if left, _ := s.ListJobs(ctx, store.JobIndex, store.StatusPending); len(left) != 0 {
		t.Errorf("%d jobs still pending", len(left))
	}
}

func TestJobStatusEnumEnforced(t *testing.T) {
	s := newTestStore(t)
	job, err := s.CreateJob(context.Background(), store.JobSync, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(context.Background(), job.ID, "bogus", ""); err == nil {
		t.Error("bogus status accepted; schema ASSERT should reject it")
	}
}
