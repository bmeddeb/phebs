//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

func TestTypedIndexCollectionBatch(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f, chunk, root, successor := collectionFixture(t, s, "collection-batch")
	initial, err := s.InspectTypedIndexCollection(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	original := initial.observation.Attempts[0]
	// All64 bodies are valid selected controls. Physical absence is a synthetic
	// trusted-controller assertion here; the Linux lifecycle test owns that proof.
	for n := 0; n < 63; n++ {
		id := typedDigest([]byte(fmt.Sprintf("batch-extra-%d", n)))
		var a typedIndexAttempt
		if typedDecode(original.Body, maxTypedControlBytes, &a) != nil {
			t.Fatal("decode")
		}
		a.Custody.AttemptDigest = id
		a.Growth.AttemptDigest = id
		body, e := typedEncode(a, maxTypedControlBytes)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$key,body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), id), "repo": f.repo, "root": root, "key": id, "body": body}, 1); e != nil {
			t.Fatal(e)
		}
	}
	selected, err := s.InspectTypedIndexCollection(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Attempts()) != 64 {
		t.Fatal("fixture", len(selected.Attempts()))
	}
	if _, err = s.CollectDrainedTypedIndexControlsBatch(ctx, selected, nil); !errors.Is(err, typedindex.Invalid) {
		t.Fatal("incomplete physical assertion", err)
	}
	// A late scheduler change invalidates the exact complete snapshot atomically.
	if err = s.typedWrite(ctx, `UPDATE $rid SET status='running' RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(chunk)}, 1); err != nil {
		t.Fatal(err)
	}
	if got, e := s.CollectDrainedTypedIndexControlsBatch(ctx, selected, selected.Attempts()); !errors.Is(e, typedindex.Stale) || got.Mutations != 0 {
		t.Fatal("running race", got, e)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET status='failed' RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(chunk)}, 1); err != nil {
		t.Fatal(err)
	}
	total, turns := 0, 0
	for len(selected.Attempts()) > 0 {
		before := len(selected.Attempts())
		// Exact wrapper accounting, not merely the returned summary, stays <=16.
		want := min(12, before)
		selectedState := len(selected.observation.StateOwner) == 1 && containsBatchAttempt(selected.Attempts()[:want], selected.observation.StateOwner[0].ID)
		if selectedState {
			want++
		}
		if before <= 12 {
			want += 3
		} // plan, successor and inactive intent
		ac, owner, meter := storeAccountingFixture(t, 10, 2)
		db, native := storeAccountingDB(t, ac, owner)
		native.call = func(_ context.Context, req *connection.RPCRequest) (any, error) {
			q, _ := req.Params[0].(string)
			if !strings.Contains(q, "IF $collection != $expected") {
				t.Fatal("missing complete fence")
			}
			snap, e := meter.Snapshot()
			if e != nil || snap.Transactions != 1 || snap.Rows != uint64(want) || snap.MaximumRows > 16 {
				t.Fatalf("accounting %+v want%d %v", snap, want, e)
			}
			return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
		}
		mock, e := (&Surreal{db: db, accounting: owner}).CollectDrainedTypedIndexControlsBatch(ac, selected, selected.Attempts())
		if e != nil || mock.Mutations != want {
			t.Fatal(mock, e)
		}
		got, e := s.CollectDrainedTypedIndexControlsBatch(ctx, selected, selected.Attempts())
		if e != nil || got.Mutations != want || got.Mutations > 16 || got.More != (before > 12) {
			t.Fatal("batch", got, want, e)
		}
		total += got.Mutations
		turns++
		if _, e = s.CollectDrainedTypedIndexControlsBatch(ctx, selected, selected.Attempts()); !errors.Is(e, typedindex.Stale) {
			t.Fatal("stale replay", e)
		}
		// Restart from fresh durable state only; no in-memory completed-ID list.
		selected, e = s.InspectTypedIndexCollection(ctx, root)
		if e != nil {
			t.Fatal("coherent prefix", e)
		}
		if len(selected.Attempts()) != max(0, before-12) {
			t.Fatal("progress")
		}
		for _, kind := range []TypedIndexControlKind{TypedIndexPlans, TypedIndexRequests} {
			key := root
			if kind == TypedIndexRequests {
				key = successor
			}
			rows, e := s.typedRelationPoint(ctx, kind, key)
			if e != nil || len(rows) != boolRows(got.More) {
				t.Fatal("reference retention", kind, e)
			}
		}
	}
	if total != 68 || turns != 6 {
		t.Fatal("total/turns", total, turns)
	}
	replay, e := s.CollectDrainedTypedIndexControlsBatch(ctx, selected, nil)
	if e != nil || replay.Mutations != 0 || replay.More {
		t.Fatal("empty replay", replay, e)
	}
	rows, e := s.typedRelationPoint(ctx, TypedIndexRequests, root)
	if e != nil || len(rows) != 1 || rows[0].State != "collecting" {
		t.Fatal("tombstone", e)
	}
}
func containsBatchAttempt(ids []string, id string) bool {
	for _, s := range ids {
		if s == id {
			return true
		}
	}
	return false
}
func boolRows(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestTypedIndexCollectionBatchConcurrent(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	_, _, root, _ := collectionFixture(t, s, "batch-race")
	selected, e := s.InspectTypedIndexCollection(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var results [2]TypedIndexCollectionBatch
	var errs [2]error
	for i := range results {
		wg.Go(func() {
			results[i], errs[i] = s.CollectDrainedTypedIndexControlsBatch(ctx, selected, selected.Attempts())
		})
	}
	wg.Wait()
	successful := 0
	for i, e := range errs {
		if e == nil {
			successful++
			if results[i].Mutations != 5 {
				t.Fatal(results[i])
			}
		} else if results[i].Mutations != 0 {
			t.Fatal(results[i], e)
		}
	}
	if successful != 1 {
		t.Fatal("exactly one writer", errs)
	}
	after, e := s.InspectTypedIndexCollection(ctx, root)
	if e != nil || len(after.Attempts()) != 0 {
		t.Fatal(e)
	}
}

func TestTypedIndexCollectionBatchSixteenOperands(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f, _, root, _ := collectionFixture(t, s, "batch-sixteen")
	selected, e := s.InspectTypedIndexCollection(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	original := selected.observation.Attempts[0]
	for n := range 11 {
		id := typedDigest([]byte(fmt.Sprintf("sixteen-%d", n)))
		var a typedIndexAttempt
		if typedDecode(original.Body, maxTypedControlBytes, &a) != nil {
			t.Fatal("decode")
		}
		a.Custody.AttemptDigest = id
		a.Growth.AttemptDigest = id
		body, e := typedEncode(a, maxTypedControlBytes)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$key,body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), id), "repo": f.repo, "root": root, "key": id, "body": body}, 1); e != nil {
			t.Fatal(e)
		}
	}
	selected, e = s.InspectTypedIndexCollection(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	ac, owner, meter := storeAccountingFixture(t, 10, 2)
	db, native := storeAccountingDB(t, ac, owner)
	native.call = func(_ context.Context, _ *connection.RPCRequest) (any, error) {
		snap, e := meter.Snapshot()
		if e != nil || snap.Transactions != 1 || snap.Rows != 16 || snap.MaximumRows != 16 {
			t.Fatal(snap, e)
		}
		return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
	}
	if _, e = (&Surreal{db: db, accounting: owner}).CollectDrainedTypedIndexControlsBatch(ac, selected, selected.Attempts()); e != nil {
		t.Fatal(e)
	}
	got, e := s.CollectDrainedTypedIndexControlsBatch(ctx, selected, selected.Attempts())
	if e != nil || got.Mutations != 16 || got.More {
		t.Fatal(got, e)
	}
}
