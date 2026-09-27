package store

import (
	"errors"
	"fmt"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexDispositionAtomicHistory(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "disposition")
	f.enqueue(t, "one")
	c := f.claim(t)
	explained, explainErr := surrealdb.Query[any](ctx, s.db, typedCollectionAttemptsSQL+" EXPLAIN FULL", map[string]any{"root": c.Generation, "after": "", "attempt_limit": 65})
	if explainErr != nil {
		t.Fatal(explainErr)
	}
	for _, result := range *explained {
		if result.Error != nil {
			t.Fatal(result.Error)
		}
	}
	operators := retentionPlanOperators(explained)
	if len(operators) != 3 || operators[0].operator != "SelectProject" || operators[1].operator != "Compute" || operators[2].operator != "IndexScan" || fmt.Sprint(operators[2].attrs["index"]) != "typed_index_attempt_root" || fmt.Sprint(operators[2].attrs["limit"]) != "$attempt_limit" {
		t.Fatalf("unbounded production disposition plan: %+v", operators)
	}
	accounted, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 100})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.InspectTypedIndexDisposition(accounted, c)
	counts, accountingErr := ledger.Finish()
	if accountingErr != nil || counts.StoreReadAttempts != 11 || counts.StoreWriteAttempts != 0 {
		t.Fatalf("fresh read cost: %+v %v", counts, accountingErr)
	}
	if err != nil || initial.State() != TypedIndexFresh {
		t.Fatal(initial.State(), err)
	}
	work, err := s.BeginTypedIndex(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireFreshTypedIndexGrowth(ctx, c, typedTestGrowthSpec(), initial); !errors.Is(err, typedindex.Stale) {
		t.Fatalf("history growth race accepted: %v", err)
	}
	fresh, err := s.InspectTypedIndexDisposition(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireFreshTypedIndexGrowth(ctx, c, typedTestGrowthSpec(), TypedIndexDisposition{}); err == nil {
		t.Fatal("zero snapshot accepted")
	}
	// Remove a selected row and restore it: neither disappearance nor a changed
	// body may be adopted by the final history fence.
	row := fresh.rows[0]
	for _, change := range []string{"body", "projection", "delete"} {
		t.Run(change, func(t *testing.T) {
			query := `UPDATE $row SET body=$body RETURN NONE;`
			vars := map[string]any{"row": typedID(string(TypedIndexAttempts), row.ID), "body": row.Body + " "}
			if change == "projection" {
				query = `UPDATE $row SET request_root=$other RETURN NONE;`
				vars["other"] = typedDigest([]byte("other"))
			}
			if change == "delete" {
				query = `DELETE $row RETURN NONE;`
			}
			if e := s.typedWrite(ctx, query, vars, 1); e != nil {
				t.Fatal(e)
			}
			if _, e := s.AcquireFreshTypedIndexGrowth(ctx, c, typedTestGrowthSpec(), fresh); e == nil {
				t.Fatal("stale snapshot accepted")
			}
			if e := s.typedWrite(ctx, `UPSERT $row SET repository=$repo,request_root=$root,control_key=$key,body=$body RETURN NONE;`, map[string]any{"row": typedID(string(TypedIndexAttempts), row.ID), "repo": row.Repository, "root": row.Root, "key": row.StoredKey, "body": row.Body}, 1); e != nil {
				t.Fatal(e)
			}
		})
	}
	if _, err = s.AcquireFreshTypedIndexGrowth(ctx, c, typedTestGrowthSpec(), fresh); err != nil {
		t.Fatal("restored positive", err)
	}
	fresh, err = s.InspectTypedIndexDisposition(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireFreshTypedIndexGrowth(ctx, c, typedTestGrowthSpec(), fresh); err != nil {
		t.Fatal("same lease reopen", err)
	}
	f.custodyInputs(t, c)
	if err = s.AdvanceTypedIndex(ctx, c, TypedPreflight); err != nil {
		t.Fatal(err)
	}
	interrupted, err := s.InspectTypedIndexDisposition(ctx, c)
	if err != nil || interrupted.State() != TypedIndexInterrupted {
		t.Fatal(interrupted.State(), err)
	}
	if _, err = s.AcquireFreshTypedIndexGrowth(ctx, c, typedTestGrowthSpec(), interrupted); err == nil {
		t.Fatal("interrupted snapshot accepted")
	}
	if err = s.ReleaseGenerationChunk(ctx, c, "shutdown"); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "new-worker")
	if err != nil || next == nil {
		t.Fatal(next, err)
	}
	if _, err = s.BeginTypedIndex(ctx, *next); err != nil {
		t.Fatal(err)
	} // overwrites last-state
	interrupted, err = s.InspectTypedIndexDisposition(ctx, *next)
	if err != nil || interrupted.State() != TypedIndexInterrupted {
		t.Fatal("history disappeared behind new last-state", interrupted.State(), err)
	}
	held, err := s.InspectTypedIndexGrowthRelease(ctx, work.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTypedIndexGrowth(ctx, held); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcquireFreshTypedIndexGrowth(ctx, *next, typedTestGrowthSpec(), interrupted); err == nil {
		t.Fatal("released old growth enabled native replay")
	}
}

func TestTypedIndexDispositionPublishedAndFences(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "disposition-published")
	f.enqueue(t, "one")
	c := f.claim(t)
	a, p := f.seal(t, c)
	b := f.bundle(t, a, p)
	f.advancePublication(t, c, b)
	if _, e := s.PublishTypedIndex(ctx, c, typedindex.PublicationPointer{}, b); e != nil {
		t.Fatal(e)
	}
	// The publication committed, but the scheduler died before settlement.
	if e := s.ReleaseGenerationChunk(ctx, c, "shutdown"); e != nil {
		t.Fatal(e)
	}
	next, e := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "new-worker")
	if e != nil || next == nil {
		t.Fatal(next, e)
	}
	accounted, ledger, e := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 100})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.InspectTypedIndexDisposition(accounted, *next)
	counts, accountingErr := ledger.Finish()
	if accountingErr != nil || counts.StoreReadAttempts != 18 || counts.StoreWriteAttempts != 0 {
		t.Fatalf("published read cost: %+v %v", counts, accountingErr)
	}
	if e != nil || d.State() != TypedIndexAlreadyPublished {
		t.Fatal(d.State(), e)
	}
	if _, e = s.AcquireFreshTypedIndexGrowth(ctx, *next, typedTestGrowthSpec(), d); e == nil {
		t.Fatal("publication enabled replay")
	}
	// Actual point/range change after the observation must fail its final fence.
	_, e = s.inspectTypedIndexDisposition(ctx, *next, func() {
		if x := s.typedWrite(ctx, `DELETE $row RETURN NONE;`, map[string]any{"row": typedID(string(TypedIndexCurrents), f.repo)}, 1); x != nil {
			t.Fatal(x)
		}
	})
	if !errors.Is(e, typedindex.Stale) {
		t.Fatalf("current race: %v", e)
	}
	if e = s.SetRepoIndexed(ctx, f.repo, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.InspectTypedIndexDisposition(ctx, *next); e == nil {
		t.Fatal("obsolete authority classified fresh")
	}
}
