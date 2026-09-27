//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

// Store-only drainage assertions are synthetic trusted-controller facts. These
// tests create no physical owner and prove neither native nor filesystem cleanup.
func collectionFixture(t *testing.T, s *Surreal, name string) (*typedFixture, GenerationChunk, string, string) {
	t.Helper()
	f := newTypedFixture(t, s, name)
	raw := f.enqueue(t, "one")
	chunk := f.claim(t)
	a, _ := f.seal(t, chunk)
	if err := s.FailGenerationChunk(t.Context(), chunk, "finished"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelTypedIndex(t.Context(), f.repo, a.Digest()); err != nil {
		t.Fatal(err)
	}
	growth, err := s.GetTypedIndexGrowth(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.InspectTypedIndexGrowthRelease(t.Context(), growth.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTypedIndexGrowth(t.Context(), selected); err != nil {
		t.Fatal(err)
	}
	root := typedDigest(raw)
	retirement, err := s.InspectTypedIndexRetirement(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginTypedIndexRetirement(t.Context(), retirement); err != nil {
		t.Fatal(err)
	}
	return f, chunk, root, a.Digest()
}

func TestTypedIndexCollection(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f, chunk, root, successor := collectionFixture(t, s, "collection")
	inspect := func() TypedIndexCollection {
		t.Helper()
		rctx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 7})
		if err != nil {
			t.Fatal(err)
		}
		selected, err := s.InspectTypedIndexCollection(rctx, root)
		counts, finish := ledger.Finish()
		if err != nil || finish != nil || counts.StoreReadAttempts != 7 {
			t.Fatalf("inspect %+v %v %v", counts, err, finish)
		}
		return selected
	}
	selected := inspect()
	if len(selected.Attempts()) != 1 {
		t.Fatal(selected.Attempts())
	}
	for _, ids := range [][]string{nil, {typedDigest([]byte("wrong"))}, {selected.Attempts()[0], selected.Attempts()[0]}} {
		if err := s.CollectDrainedTypedIndexControls(ctx, selected, ids); !errors.Is(err, typedindex.Invalid) {
			t.Fatal("wrong drainage assertion", err)
		}
	}
	// Every mutation below must invalidate the exact prior snapshot; restoration
	// proves the same positive selection remains valid rather than vacuous refusal.
	for _, tc := range []struct {
		name, sql string
		vars      map[string]any
		undo      string
		undoVars  map[string]any
	}{
		{"root state", `UPDATE $rid SET custody_state='live' RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), root)}, `UPDATE $rid SET custody_state='collecting' RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), root)}},
		{"root body", `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), root), "body": selected.observation.Retirement.RootBody + " "}, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), root), "body": selected.observation.Retirement.RootBody}},
		{"plan", `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexPlans), root), "body": selected.observation.Plan[0].Body + " "}, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexPlans), root), "body": selected.observation.Plan[0].Body}},
		{"state", `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexStates), f.repo), "body": selected.observation.State[0].Body + " "}, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexStates), f.repo), "body": selected.observation.State[0].Body}},
		{"attempt", `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), selected.Attempts()[0]), "body": selected.observation.Attempts[0].Body + " "}, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), selected.Attempts()[0]), "body": selected.observation.Attempts[0].Body}},
		{"running", `UPDATE $rid SET status='running' RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(chunk)}, `UPDATE $rid SET status='failed' RETURN NONE;`, map[string]any{"rid": generationChunkRecordID(chunk)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.typedWrite(ctx, tc.sql, tc.vars, 1); err != nil {
				t.Fatal(err)
			}
			if err := s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); !errors.Is(err, typedindex.Stale) {
				t.Fatal("stale snapshot", err)
			}
			if err := s.typedWrite(ctx, tc.undo, tc.undoVars, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := s.InspectTypedIndexCollection(ctx, root); err != nil {
				t.Fatal("restored positive", err)
			}
		})
	}
	originalIntent := selected.observation.Retirement.Intent
	var intent TypedIndexIntent
	if err := typedDecode(originalIntent, maxTypedIntentBytes, &intent); err != nil {
		t.Fatal(err)
	}
	intent.Canceled = false
	active, _ := typedEncode(intent, maxTypedIntentBytes)
	if err := s.typedWrite(ctx, `UPDATE $intent SET body=$body RETURN NONE;`, map[string]any{"intent": typedID(string(TypedIndexIntents), f.repo), "body": active}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InspectTypedIndexCollection(ctx, root); !errors.Is(err, typedindex.Stale) {
		t.Fatal("active desired", err)
	}
	if err := s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); !errors.Is(err, typedindex.Stale) {
		t.Fatal("desired changed", err)
	}
	if err := s.typedWrite(ctx, `UPDATE $intent SET body=$body RETURN NONE;`, map[string]any{"intent": typedID(string(TypedIndexIntents), f.repo), "body": originalIntent}, 1); err != nil {
		t.Fatal(err)
	}
	// A released holder restored to held must refuse inspection and old deletion.
	attempt := selected.observation.Attempts[0]
	var a typedIndexAttempt
	if err := typedDecode(attempt.Body, maxTypedControlBytes, &a); err != nil {
		t.Fatal(err)
	}
	a.Growth.State = "held"
	held, _ := typedEncode(a, maxTypedControlBytes)
	if err := s.typedWrite(ctx, `UPDATE $rid SET body=$body,growth_key='typed-index' RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), attempt.ID), "body": held}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InspectTypedIndexCollection(ctx, root); !errors.Is(err, typedindex.Stale) {
		t.Fatal("held growth", err)
	}
	if err := s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); !errors.Is(err, typedindex.Stale) {
		t.Fatal("holder race", err)
	}
	if err := s.typedWrite(ctx, `UPDATE $rid SET body=$body,growth_key=NONE RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), attempt.ID), "body": attempt.Body}, 1); err != nil {
		t.Fatal(err)
	}
	selected = inspect()
	// Actual concurrent collectors must never leave a partially deleted graph.
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()) })
	}
	wg.Wait()
	if errs[0] != nil && errs[1] != nil {
		t.Fatalf("both collectors failed: %v", errs)
	}
	if err := s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); !errors.Is(err, typedindex.Stale) {
		t.Fatal("completed selection reused", err)
	}
	for _, item := range []struct {
		kind TypedIndexControlKind
		key  string
	}{{TypedIndexAttempts, attempt.ID}, {TypedIndexPlans, root}, {TypedIndexRequests, successor}, {TypedIndexStates, f.repo}} {
		rows, err := s.typedRelationPoint(ctx, item.kind, item.key)
		if err != nil || len(rows) != 0 {
			t.Fatal("dangling derived control", item, rows, err)
		}
	}
	after, err := s.InspectTypedIndexCollection(ctx, root)
	if err != nil || len(after.Attempts()) != 0 {
		t.Fatal("tombstone replay", err)
	}
	if err = s.CollectDrainedTypedIndexControls(ctx, after, nil); err != nil {
		t.Fatal("empty replay", err)
	}
	got, err := s.typedRead(ctx, string(TypedIndexIntents), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	intent.Canceled = true
	intent.Desired = ""
	want, _ := typedEncode(intent, maxTypedIntentBytes)
	if got != want {
		t.Fatal("precious intent altered", got, want)
	}
	var parent typedIndexRequest
	for _, row := range selected.observation.Requests {
		if row.Parent {
			_ = typedDecode(row.Body, typedindex.MaxRequestBytes+1024, &parent)
		}
	}
	if _, err = s.EnqueueTypedIndex(ctx, f.repo, []byte(parent.Raw)); !errors.Is(err, typedindex.Stale) {
		t.Fatal("tombstone readmission", err)
	}
}

func TestTypedIndexCollectionBoundaryAndAccounting(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f, _, root, _ := collectionFixture(t, s, "collection-boundary")
	selected, err := s.InspectTypedIndexCollection(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	original := selected.observation.Attempts[0]
	for n := range 64 {
		id := typedDigest([]byte(fmt.Sprintf("extra-%d", n)))
		var a typedIndexAttempt
		_ = typedDecode(original.Body, maxTypedControlBytes, &a)
		a.Custody.AttemptDigest = id
		a.Growth.AttemptDigest = id
		body, _ := typedEncode(a, maxTypedControlBytes)
		if err = s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$key,body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), id), "repo": f.repo, "root": root, "key": id, "body": body}, 1); err != nil {
			t.Fatal(err)
		}
		if n == 62 {
			selected, err = s.InspectTypedIndexCollection(ctx, root)
			if err != nil || len(selected.Attempts()) != 64 {
				t.Fatal("64", err)
			}
		}
		if n == 63 {
			if _, err = s.InspectTypedIndexCollection(ctx, root); !errors.Is(err, typedindex.Capacity) {
				t.Fatal("65", err)
			}
			if err = s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); !errors.Is(err, typedindex.Stale) {
				t.Fatal("new child race", err)
			}
			if err = s.typedWrite(ctx, `DELETE $rid RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), id)}, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct{ sql, index, limit string }{{typedCollectionRequestsSQL, "typed_index_request_root", "$request_limit"}, {typedCollectionAttemptsSQL, "typed_index_attempt_root", "$attempt_limit"}} {
		result, e := surrealdb.Query[any](ctx, s.db, tc.sql+" EXPLAIN FULL", map[string]any{"root": root, "after": "", "request_limit": 3, "attempt_limit": 65})
		if e != nil {
			t.Fatal(e)
		}
		scans := 0
		for _, op := range retentionPlanOperators(result) {
			if strings.Contains(op.operator, "Sort") || op.operator == "UnionIndexScan" {
				t.Fatal("unbounded plan", op)
			}
			if op.operator == "IndexScan" {
				scans++
				if fmt.Sprint(op.attrs["index"]) != tc.index || fmt.Sprint(op.attrs["limit"]) != tc.limit {
					t.Fatal("physical bound", op)
				}
			}
		}
		if scans != 1 {
			t.Fatal("scan count", scans)
		}
	}
	// The actual SDK accounting wrapper observes one transaction with exactly68
	// supplied mutation operands:64 attempts, plan, successor, state and intent.
	accountingCtx, owner, controller := storeAccountingFixture(t, 10, 2)
	db, native := storeAccountingDB(t, accountingCtx, owner)
	native.call = func(_ context.Context, req *connection.RPCRequest) (any, error) {
		statement, _ := req.Params[0].(string)
		if !strings.Contains(statement, "IF $collection != $expected") || strings.Contains(statement, "DELETE typed_index_request") {
			t.Fatal("mutation recipe")
		}
		snapshot, e := controller.Snapshot()
		if e != nil || snapshot.Transactions != 1 || snapshot.Rows != 68 || snapshot.MaximumRows != 68 {
			t.Fatal(snapshot, e)
		}
		return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
	}
	if err = (&Surreal{db: db, accounting: owner}).CollectDrainedTypedIndexControls(accountingCtx, selected, selected.Attempts()); err != nil {
		t.Fatal(err)
	}
	if native.calls != 1 {
		t.Fatal(native.calls)
	}
	if err = s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); err != nil {
		t.Fatal(err)
	}
}

func TestTypedIndexCollectionPreservesReplacement(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f, _, oldRoot, _ := collectionFixture(t, s, "collection-replacement")
	raw := f.enqueue(t, "replacement")
	chunk := f.claim(t)
	admission, plan := f.seal(t, chunk)
	bundle := f.bundle(t, admission, plan)
	f.advancePublication(t, chunk, bundle)
	pointer, err := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteGenerationChunk(ctx, chunk); err != nil {
		t.Fatal(err)
	}
	currentRoot := typedDigest(raw)
	if _, err = s.InspectTypedIndexCollection(ctx, currentRoot); !errors.Is(err, typedindex.Stale) {
		t.Fatal("current protected", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET custody_state='collecting' RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), currentRoot)}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InspectTypedIndexCollection(ctx, currentRoot); !errors.Is(err, typedindex.Stale) {
		t.Fatal("current collecting protected", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET custody_state='live' RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), currentRoot)}, 1); err != nil {
		t.Fatal(err)
	}
	selected, err := s.InspectTypedIndexCollection(ctx, oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	currentBefore, err := s.typedRead(ctx, string(TypedIndexCurrents), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := s.typedRead(ctx, string(TypedIndexStates), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	intentBefore, err := s.typedRead(ctx, string(TypedIndexIntents), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexCurrents), f.repo), "body": currentBefore + " "}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); !errors.Is(err, typedindex.Stale) {
		t.Fatal("current race", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexCurrents), f.repo), "body": currentBefore}, 1); err != nil {
		t.Fatal(err)
	}
	// Discovery itself repeats the selected state/owner in its final snapshot.
	_, err = s.inspectTypedIndexCollection(ctx, oldRoot, func() {
		if e := s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexStates), f.repo), "body": stateBefore + " "}, 1); e != nil {
			t.Fatal(e)
		}
	})
	if !errors.Is(err, typedindex.Stale) {
		t.Fatal("selection state race", err)
	}
	if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexStates), f.repo), "body": stateBefore}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind TypedIndexControlKind
		want string
	}{{TypedIndexCurrents, currentBefore}, {TypedIndexStates, stateBefore}, {TypedIndexIntents, intentBefore}} {
		got, e := s.typedRead(ctx, string(tc.kind), f.repo)
		if e != nil || got != tc.want {
			t.Fatal("replacement changed", tc.kind, e)
		}
	}
	got, err := s.ResolveTypedIndexCurrent(ctx, f.repo)
	if err != nil || got != pointer {
		t.Fatal("prior current lost", err)
	}
	// The unrelated current's held growth was never deleted or released.
	held, err := s.GetTypedIndexGrowth(ctx)
	if err != nil || held.PlanningDigest != currentRoot {
		t.Fatal("unrelated holder", err)
	}
}

func TestTypedIndexCollectionHistoricalAndMalformed(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	for _, mode := range []string{"restore", "deleted", "pre-growth"} {
		t.Run(mode, func(t *testing.T) {
			var f *typedFixture
			var root string
			if mode == "pre-growth" {
				f = newTypedFixture(t, s, "collection-"+mode)
				root = typedDigest(f.enqueue(t, "one"))
				if err := s.CancelTypedIndex(ctx, f.repo, root); err != nil {
					t.Fatal(err)
				}
				r, err := s.InspectTypedIndexRetirement(ctx, root)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.BeginTypedIndexRetirement(ctx, r); err != nil {
					t.Fatal(err)
				}
			} else {
				f, _, root, _ = collectionFixture(t, s, "collection-"+mode)
			}
			if mode == "restore" {
				raw, err := s.typedRead(ctx, string(TypedIndexIntents), f.repo)
				if err != nil {
					t.Fatal(err)
				}
				var intent TypedIndexIntent
				_ = typedDecode(raw, maxTypedIntentBytes, &intent)
				intent.Canceled = false
				intent.RestoreRequired = true
				raw, err = typedEncode(intent, maxTypedIntentBytes)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexIntents), f.repo), "body": raw}, 1); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "deleted" {
				if err := s.DeleteRepo(ctx, f.repo); err != nil {
					t.Fatal(err)
				}
			}
			selected, err := s.InspectTypedIndexCollection(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "pre-growth" {
				plan := selected.observation.Plan[0]
				if err = s.typedWrite(ctx, `DELETE $rid RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexPlans), root)}, 1); err != nil {
					t.Fatal(err)
				}
				if _, err = s.InspectTypedIndexCollection(ctx, root); !errors.Is(err, typedindex.Invalid) {
					t.Fatal("orphan successor", err)
				}
				if err = s.typedWrite(ctx, `CREATE ONLY $rid SET body=$body,repository=$repo,request_root=$root,control_key=$root RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexPlans), root), "body": plan.Body, "repo": f.repo, "root": root}, 1); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.CollectDrainedTypedIndexControls(ctx, selected, selected.Attempts()); err != nil {
				t.Fatal(err)
			}
			if mode == "restore" {
				raw, e := s.typedRead(ctx, string(TypedIndexIntents), f.repo)
				if e != nil {
					t.Fatal(e)
				}
				var intent TypedIndexIntent
				if typedDecode(raw, maxTypedIntentBytes, &intent) != nil || !intent.RestoreRequired || intent.Canceled || intent.Desired != "" {
					t.Fatal("restore intent lost")
				}
			}
			after, err := s.InspectTypedIndexCollection(ctx, root)
			if err != nil || len(after.Attempts()) != 0 {
				t.Fatal("empty retained tombstone", err)
			}
			if err = s.CollectDrainedTypedIndexControls(ctx, after, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
