//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

func runtimeFixture(t *testing.T, f fixture) (*Runtime, *int) {
	t.Helper()
	lookups := 0
	r, err := NewRuntime(f.c, func(_ context.Context, parent typedindex.Admission) (string, []byte, error) {
		lookups++
		if parent.Digest() != f.chunk.Generation {
			t.Fatal("wrong immutable bundle selection")
		}
		return f.source, f.raw, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, &lookups
}
func TestTypedRuntimeSchedulerTurn(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "runtime_success")
	ctx := t.Context()
	r, lookups := runtimeFixture(t, f)
	if err := r.Coordinator(ctx, store.Job{Kind: store.JobTypedIndex, Target: f.chunk.Repository}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ReleaseGenerationChunk(ctx, f.chunk, "fixture startup"); err != nil {
		t.Fatal(err)
	}
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	scheduler, err := r.Scheduler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	configuration := scheduler.Classes[store.GenerationResourceTypedIndex]
	handle, settle := configuration.Handle, configuration.AfterSettlement
	var begins *int
	var selected store.GenerationChunk
	settled := 0
	configuration.Handle = func(ctx context.Context, c store.GenerationChunk, b generationscheduler.Budget) error {
		selected = c
		actual := f
		actual.chunk = c
		_, begins = installNeutralNative(t, actual, w, "")
		return handle(ctx, c, b)
	}
	configuration.AfterSettlement = func(ctx context.Context, c store.GenerationChunk) error {
		err := settle(ctx, c)
		settled++
		cancel()
		return err
	}
	scheduler.Classes[store.GenerationResourceTypedIndex] = configuration
	scheduler.Report = func(err error) { t.Errorf("scheduler: %v", err) }
	if err = scheduler.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if settled != 1 || begins == nil || *begins != 1 || *lookups != 1 || selected.LeaseToken == f.chunk.LeaseToken {
		t.Fatalf("turn: settle=%d begin=%v lookups=%d", settled, begins, *lookups)
	}
	if _, err = f.s.ResolveTypedIndexCurrentCustody(ctx, f.chunk.Repository); err != nil {
		t.Fatal("real publication", err)
	}
	if _, err = f.s.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("settlement stranded growth", err)
	}
}
func TestTypedRuntimeCrossLeaseNoReplay(t *testing.T) {
	endpoint := testServer(t)
	for _, name := range []string{"planning", "overwritten-state", "published"} {
		t.Run(name, func(t *testing.T) {
			f, w := completeFixture(t, endpoint, "runtime_"+name)
			ctx := t.Context()
			r, lookups := runtimeFixture(t, f)
			_, begins := installNeutralNative(t, f, w, "")
			if name == "published" {
				if _, err := f.c.Execute(ctx, f.chunk, f.source, f.raw); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.c.Prepare(ctx, f.chunk, f.source, f.raw); err != nil {
					t.Fatal(err)
				}
				if err := f.s.AdvanceTypedIndex(ctx, f.chunk, store.TypedPreflight); err != nil {
					t.Fatal(err)
				}
			}
			before := *begins
			if err := f.s.ReleaseGenerationChunk(ctx, f.chunk, "hard death"); err != nil {
				t.Fatal(err)
			}
			next, err := f.s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "replacement")
			if err != nil || next == nil {
				t.Fatal(next, err)
			}
			if name == "overwritten-state" {
				if _, err = f.s.BeginTypedIndex(ctx, *next); err != nil {
					t.Fatal(err)
				}
			}
			// Fresh controller rebuilds state from durable rows, not old memory. Native
			// observation hooks are the same explicit neutral seam; no worker is run.
			fresh, err := New(f.c.config)
			if err != nil {
				t.Fatal(err)
			}
			fresh.native = f.c.native
			fresh.observeHost = f.c.observeHost
			r.controller = fresh
			if err = r.Reconcile(ctx); err != nil {
				t.Fatal("restart reconcile", err)
			}
			err = r.Class().Handle(ctx, *next, generationscheduler.TypedIndexBudget())
			if name == "published" {
				if err != nil {
					t.Fatal("published recovery", err)
				}
			} else if !store.IsTerminal(err) {
				t.Fatal("interrupted root retried", err)
			}
			if *begins != before || *lookups != 0 {
				t.Fatal("new lease repeated execution")
			}
			if _, err = fresh.Execute(ctx, *next, f.source, f.raw); err == nil {
				t.Fatal("direct Execute bypassed history")
			}
			if *begins != before {
				t.Fatal("direct call minted allowance")
			}
			if name == "published" {
				err = f.s.CompleteGenerationChunk(ctx, *next)
			} else {
				err = f.s.FailGenerationChunk(ctx, *next, "interrupted")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Class().AfterSettlement(ctx, *next); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestTypedRuntimeTransientBeforeNative(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "runtime_transient")
	r, _ := runtimeFixture(t, f)
	ctx := t.Context()
	_, begins := installNeutralNative(t, f, w, "")
	closed, openErr := store.Open(ctx, endpoint, "root", "fixture", "executor", "runtime_transient")
	if openErr != nil {
		t.Fatal(openErr)
	}
	if closeErr := closed.Close(ctx); closeErr != nil {
		t.Fatal(closeErr)
	}
	original := f.c.config.Store
	f.c.config.Store = closed
	failed := r.Coordinator(ctx, store.Job{Kind: store.JobTypedIndex, Target: f.chunk.Repository})
	if failed == nil || store.IsTerminal(failed) {
		t.Fatal("transient coordinator read lost work", failed)
	}
	failed = r.Class().Handle(ctx, f.chunk, generationscheduler.TypedIndexBudget())
	if failed == nil || store.IsTerminal(failed) || *begins != 0 {
		t.Fatal("transient disposition read lost work", failed)
	}
	f.c.config.Store = original
	r.bundle = func(context.Context, typedindex.Admission) (string, []byte, error) {
		return "", nil, errors.New("temporary bundle service failure")
	}
	err := r.Class().Handle(ctx, f.chunk, generationscheduler.TypedIndexBudget())
	if err == nil || store.IsTerminal(err) || *begins != 0 {
		t.Fatal("pre-native transient lost", err)
	}
	r.bundle = func(context.Context, typedindex.Admission) (string, []byte, error) { return f.source, f.raw, nil }
	if err = r.Class().Handle(ctx, f.chunk, generationscheduler.TypedIndexBudget()); err != nil {
		t.Fatal("restored positive", err)
	}
	if *begins != 1 {
		t.Fatal("wrong allowance count")
	}
}

func TestTypedRuntimeCensusBeforeStaleReap(t *testing.T) {
	endpoint := testServer(t)
	f, w := completeFixture(t, endpoint, "runtime_stale")
	ctx := t.Context()
	_, begins := installNeutralNative(t, f, w, "")
	r, _ := runtimeFixture(t, f)
	if _, err := f.c.Prepare(ctx, f.chunk, f.source, f.raw); err != nil {
		t.Fatal(err)
	}
	if err := f.s.AdvanceTypedIndex(ctx, f.chunk, store.TypedPreflight); err != nil {
		t.Fatal(err)
	}
	db, err := surrealdb.FromEndpointURLString(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if _, err = db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err = db.Use(ctx, "executor", "runtime_stale"); err != nil {
		t.Fatal(err)
	}
	rid := models.NewRecordID("generation_schedule_chunk", f.chunk.ID)
	if _, err = surrealdb.Query[any](ctx, db, `UPDATE $row SET heartbeat_at=time::now()-1h RETURN NONE;`, map[string]any{"row": rid}); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(f.c.config.Workspace, "unknown")
	if err = os.WriteFile(unknown, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = r.Reconcile(ctx); err == nil {
		t.Fatal("unknown custody accepted")
	}
	rows, err := surrealdb.Query[[]struct {
		Status string `json:"status"`
	}](ctx, db, `SELECT status FROM $row`, map[string]any{"row": rid})
	if err != nil || rows == nil || len(*rows) != 1 || len((*rows)[0].Result) != 1 || (*rows)[0].Result[0].Status != "running" {
		t.Fatalf("census failure reaped lease: %v %v", rows, err)
	}
	if err = os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	if err = r.Reconcile(ctx); err != nil {
		t.Fatal("restored census/reap", err)
	}
	next, err := f.s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "reaped-replacement")
	if err != nil || next == nil {
		t.Fatal(next, err)
	}
	if err = r.Class().Handle(ctx, *next, generationscheduler.TypedIndexBudget()); !store.IsTerminal(err) {
		t.Fatal("reaped planning replayed", err)
	}
	if *begins != 0 {
		t.Fatal("new allowance after stale recovery")
	}
}

func TestTypedRuntimeCancellationSettlement(t *testing.T) {
	endpoint := testServer(t)
	for _, sourceCancel := range []bool{false, true} {
		name := "shutdown"
		if sourceCancel {
			name = "source_cancel"
		}
		t.Run(name, func(t *testing.T) {
			f, w := completeFixture(t, endpoint, "runtime_"+name)
			r, _ := runtimeFixture(t, f)
			_, begins := installNeutralNative(t, f, w, "")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			run := f.c.native.run
			f.c.native.run = func(ctx context.Context, o typedsandbox.Options, a typedsandbox.ScratchAuthority) (typedsandbox.Result, error) {
				result, err := run(ctx, o, a)
				if sourceCancel {
					if e := f.s.CancelTypedIndex(t.Context(), f.chunk.Repository, o.Control.RequestDigest); e != nil {
						t.Fatal(e)
					}
				} else {
					cancel()
				}
				return result, err
			}
			err := r.Class().Handle(ctx, f.chunk, generationscheduler.TypedIndexBudget())
			if err == nil || *begins != 1 {
				t.Fatal("cancel did not stop turn", err)
			}
			if e := f.s.ReleaseGenerationChunk(t.Context(), f.chunk, "canceled"); e != nil {
				t.Fatal(e)
			}
			if e := r.Class().AfterSettlement(t.Context(), f.chunk); e != nil {
				t.Fatal("old lease cleanup", e)
			}
			if _, e := f.s.GetTypedIndexGrowth(t.Context()); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("canceled holder retained", e)
			}
			next, e := f.s.ClaimGenerationChunk(t.Context(), store.GenerationResourceTypedIndex, "after-cancel")
			if e != nil || next == nil {
				t.Fatal(next, e)
			}
			if e = r.Class().Handle(t.Context(), *next, generationscheduler.TypedIndexBudget()); e == nil {
				t.Fatal("canceled root re-executed")
			}
			if *begins != 1 {
				t.Fatal("cancellation refreshed allowance")
			}
		})
	}
}
