//go:build linux

package typedexecutor

import (
	"context"
	"encoding/json"
	"errors"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/lifecycle"
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
	executionFailed, capacityRefused := false, false
	r.Report = func(_ Outcome, err error) {
		executionFailed = err != nil
		capacityRefused = errors.Is(err, lifecycle.ErrPressureRefusal) || errors.Is(err, lifecycle.ErrCapacityUnavailable)
	}
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
		count := -1
		if begins != nil {
			count = *begins
		}
		t.Fatalf("turn: settle=%d begins=%d lookups=%d lease_unchanged=%t execution_failed=%t capacity_refused=%t", settled, count, *lookups, selected.LeaseToken == f.chunk.LeaseToken, executionFailed, capacityRefused)
	}
	if _, err = f.s.ResolveTypedIndexCurrentCustody(ctx, f.chunk.Repository); err != nil {
		t.Fatal("real publication", err)
	}
	if _, err = f.s.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("settlement stranded growth", err)
	}
	before, err := f.s.GetGenerationSchedule(ctx, f.chunk.Repository, store.TypedIndexScheduleStage)
	if err != nil || before.Status != store.GenerationScheduleSettled || before.Succeeded != 1 {
		t.Fatal("published schedule not settled", before, err)
	}
	if err = r.Coordinator(ctx, store.Job{Kind: store.JobTypedIndex, Target: f.chunk.Repository}); err != nil {
		t.Fatal("published duplicate coordinator", err)
	}
	after, err := f.s.GetGenerationSchedule(ctx, f.chunk.Repository, store.TypedIndexScheduleStage)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("published duplicate changed schedule", err)
	}
	next, err := f.s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "published-warm")
	if err != nil && !errors.Is(err, store.ErrNotFound) || next != nil || *lookups != 1 || *begins != 1 {
		t.Fatal("published duplicate replay", next, err)
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
	prepared, prepareErr := f.c.Prepare(ctx, f.chunk, f.source, f.raw)
	if prepareErr != nil {
		t.Fatal(prepareErr)
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
	journal := filepath.Join(f.c.config.Workspace, prepared.Manifest.Identity.RelativeName(), prepared.Inputs.Name+".typed-container.json")
	if err = os.WriteFile(journal, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = r.Reconcile(ctx); err == nil {
		t.Fatal("invalid native journal accepted")
	}
	rows, err = surrealdb.Query[[]struct {
		Status string `json:"status"`
	}](ctx, db, `SELECT status FROM $row`, map[string]any{"row": rid})
	if err != nil || rows == nil || len(*rows) != 1 || len((*rows)[0].Result) != 1 || (*rows)[0].Result[0].Status != "running" {
		t.Fatalf("native census failure reaped lease: %v %v", rows, err)
	}
	if err = os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	oldSocket, oldImage := f.c.config.Socket, f.c.config.Image
	f.c.config.Socket = filepath.Join(t.TempDir(), "unavailable.sock")
	f.c.config.Image = "sha256:" + strings.Repeat("a", 64)
	id := prepared.Manifest.Identity
	rootName, e := typedsandbox.HostScratchRootName(id.PlanningDigest, id.AttemptDigest)
	if e != nil {
		t.Fatal(e)
	}
	record := struct {
		Schema, Name, ContainerID, DaemonID, ImageID, Socket, Inputs, Controls string
		Control                                                                typedsandbox.ControlIdentity
		Allowance                                                              typedsandbox.Allowance
		Scratch                                                                *typedsandbox.ScratchAuthority `json:",omitempty"`
	}{
		Schema: "phebs-typed-container-owner-v3", Name: "phebs-typed-index-" + strings.Repeat("a", 32), DaemonID: "fixture", ImageID: f.c.config.Image, Socket: f.c.config.Socket,
		Inputs: strings.TrimSuffix(journal, ".typed-container.json"), Controls: filepath.Join(filepath.Dir(journal), "controls-plan"),
		Control:   typedsandbox.ControlIdentity{PlanningDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest, RequestDigest: id.PlanningDigest, Phase: typedsandbox.ControlPlan, SealDigest: "sha256:" + strings.Repeat("c", 64), Device: 1, Inode: 1},
		Allowance: typedsandbox.Allowance{Schema: "phebs-typed-allowance-v1", PlanningDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest, BootID: "00000000-0000-0000-0000-000000000000", TimeDevice: 1, TimeInode: 1, Start: 1, Deadline: 1 + int64(typedsandbox.WallLimit)},
		Scratch:   &typedsandbox.ScratchAuthority{Source: typedsandbox.HostScratchBase + "/" + rootName + "/scratch", DeviceMajor: 7, BlockSize: 4096, Blocks: 100, Inodes: typedsandbox.ScratchInodes, ImageBytes: typedsandbox.ScratchBytes / 4096 * 4096},
	}
	for _, fault := range []string{"canonical", "wrong scratch", "wrong request"} {
		saved := record
		scratch := *record.Scratch
		if fault == "wrong scratch" {
			scratch.Source = typedsandbox.HostScratchBase + "/wrong/scratch"
			record.Scratch = &scratch
		}
		if fault == "wrong request" {
			record.Control.Phase = typedsandbox.ControlExecute
			record.Control.RequestDigest = "sha256:" + strings.Repeat("d", 64)
			record.Controls = filepath.Join(filepath.Dir(journal), "controls-execute")
		}
		raw, e := json.Marshal(record)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(journal, raw, 0600); e != nil {
			t.Fatal(e)
		}
		if fault == "canonical" {
			if e = f.c.censusOwnership(ctx); e != nil {
				t.Fatal("canonical interrupted census", e)
			}
		} else {
			if e = r.Reconcile(ctx); e == nil {
				t.Fatal("mismatched native identity accepted", fault)
			}
			rows, e := surrealdb.Query[[]struct {
				Status string `json:"status"`
			}](ctx, db, `SELECT status FROM $row`, map[string]any{"row": rid})
			if e != nil || rows == nil || len(*rows) != 1 || len((*rows)[0].Result) != 1 || (*rows)[0].Result[0].Status != "running" {
				t.Fatal("mismatched identity reaped lease", fault, e)
			}
		}
		record = saved
	}
	if err = os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	f.c.config.Socket, f.c.config.Image = oldSocket, oldImage
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

func TestTypedRuntimeCheckedNoReplay(t *testing.T) {
	endpoint := testServer(t)
	for _, purpose := range []typedindex.Purpose{typedindex.Canary, typedindex.DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			ctx := t.Context()
			f, w, prior := checkedFixture(t, endpoint, "check_reuse_"+strings.ReplaceAll(string(purpose), "-", "_"), purpose)
			_, begins := installNeutralNative(t, f, w, "")
			r, lookups := runtimeFixture(t, f)
			if e := r.Class().Handle(ctx, f.chunk, generationscheduler.TypedIndexBudget()); e != nil {
				t.Fatal(e)
			}
			if e := r.Class().Handle(ctx, f.chunk, generationscheduler.TypedIndexBudget()); e != nil {
				t.Fatal("same lease reuse", e)
			}
			if *begins != 1 || *lookups != 1 {
				t.Fatal("same lease replay", *begins, *lookups)
			}
			// Crash after the checked CAS but before settlement, discard the controller
			// and make the external bundle source unavailable. Exact reuse needs neither.
			if e := f.s.ReleaseGenerationChunk(ctx, f.chunk, "lost checked reply"); e != nil {
				t.Fatal(e)
			}
			next, e := f.s.ClaimGenerationChunk(ctx, store.GenerationResourceTypedIndex, "check-retry")
			if e != nil || next == nil {
				t.Fatal(e)
			}
			fresh, e := New(f.c.config)
			if e != nil {
				t.Fatal(e)
			}
			fresh.native = f.c.native
			fresh.observeHost = f.c.observeHost
			r.controller = fresh
			if e = r.Reconcile(ctx); e != nil {
				t.Fatal("checked recovery", e)
			}
			if e = os.Rename(f.source, f.source+"-unavailable"); e != nil {
				t.Fatal(e)
			}
			r.bundle = func(context.Context, typedindex.Admission) (string, []byte, error) {
				t.Fatal("checked reuse looked up bundle")
				return "", nil, ErrHeld
			}
			before, e := f.s.ScanTypedIndexRootChildren(ctx, f.chunk.Generation, store.TypedIndexAttempts, "", 64)
			if e != nil {
				t.Fatal(e)
			}
			if e = r.Class().Handle(ctx, *next, generationscheduler.TypedIndexBudget()); e != nil {
				t.Fatal("new lease reuse", e)
			}
			after, e := f.s.ScanTypedIndexRootChildren(ctx, f.chunk.Generation, store.TypedIndexAttempts, "", 64)
			if e != nil || len(before.Rows) != len(after.Rows) || len(after.Rows) != 1 {
				t.Fatal("reuse created attempt", before, after, e)
			}
			if *begins != 1 || *lookups != 1 {
				t.Fatal("checked repeated child/copy")
			}
			if _, e = fresh.Execute(ctx, *next, f.source, f.raw); e == nil {
				t.Fatal("direct checked replay")
			}
			if *begins != 1 {
				t.Fatal("direct checked replay began native work")
			}
			if e = f.s.CompleteGenerationChunk(ctx, *next); e != nil {
				t.Fatal(e)
			}
			if e = r.Class().AfterSettlement(ctx, *next); e != nil {
				t.Fatal(e)
			}
			current, e := f.s.ResolveTypedIndexCurrent(ctx, f.chunk.Repository)
			if e != nil || current != prior {
				t.Fatal("reuse current", e)
			}
		})
	}
}
