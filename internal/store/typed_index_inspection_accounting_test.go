//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

func TestTypedIndexInspectionAccounting(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 10, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	s := &Surreal{db: db, accounting: owner}
	h := typedDigest([]byte("fixture"))
	source, err := typedSource(typedSourceRecord{Name: "example.invalid/accounting", Commit: strings.Repeat("a", 40), Incarnation: strings.Repeat("b", 32), Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := typedindex.Request{Schema: typedindex.RequestSchema, Action: typedindex.Plan, Source: source, Provider: typedindex.ProviderID, ProfileName: "reduced", ProfileEpoch: 1, ProfileDigest: h, ConfigDigest: h, ToolsDigest: h, UniverseDigest: h, BundleDigest: h, PolicyDigest: h, IdempotencyKey: "one"}
	raw, _ := typedEncode(r, typedindex.MaxRequestBytes)
	root := typedDigest([]byte(raw))
	body, _ := typedEncode(typedIndexRequest{Raw: raw, Root: root, SourceEpoch: 1}, typedindex.MaxRequestBytes+1024)
	parent := TypedIndexControl{ID: root, StoredKey: root, Repository: source.Repository, Root: root, Parent: true, State: "live", Body: body}
	a := typedIndexAttempt{ChunkIdentity: h, Root: root, Request: root, Lease: h, Stage: TypedPreflight, States: [5]string{"running", "pending", "pending", "pending", "pending"}}
	abody, _ := typedEncode(a, maxTypedControlBytes)
	attempt := TypedIndexControl{ID: h, StoredKey: h, Repository: source.Repository, Root: root, Body: abody}
	var calls int
	native.call = func(_ context.Context, req *connection.RPCRequest) (any, error) {
		calls++
		statement, ok := req.Params[0].(string)
		if !ok || strings.Contains(statement, "UPDATE ") || strings.Contains(statement, "DELETE ") || strings.Contains(statement, "CREATE ") {
			t.Fatal("inspection submitted write")
		}
		if calls == 1 {
			return []surrealdb.QueryResult[any]{{Status: "OK", Result: []TypedIndexControl{attempt}}}, nil
		}
		if calls == 2 {
			return []surrealdb.QueryResult[any]{{Status: "OK", Result: []TypedIndexControl{}}}, nil
		}
		o := typedAttemptObservation{Attempt: []TypedIndexControl{attempt}, Parent: []TypedIndexControl{parent}, Request: []TypedIndexControl{parent}, Successor: []TypedIndexControl{parent}}
		return []surrealdb.QueryResult[any]{{Status: "OK", Result: []typedAttemptObservation{o}}}, nil
	}
	if _, err := s.InspectTypedIndexAttempt(ctx, h); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("SDK calls %d", calls)
	}
	if snap, err := controller.Snapshot(); err != nil || snap.Transactions != 0 || snap.Rows != 0 || snap.MaximumRows != 0 {
		t.Fatalf("read-only recipes: %+v %v", snap, err)
	}
	// Each global page uses exactly one SDK read; no fake zero-row mutation.
	calls = 0
	native.call = func(_ context.Context, _ *connection.RPCRequest) (any, error) {
		calls++
		return []surrealdb.QueryResult[any]{{Status: "OK", Result: []TypedIndexControl{}}}, nil
	}
	for _, kind := range []TypedIndexControlKind{TypedIndexRequests, TypedIndexAttempts, TypedIndexPlans, TypedIndexIntents, TypedIndexStates, TypedIndexCurrents} {
		if _, err := s.ScanTypedIndexControls(ctx, kind, "", 64); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 6 {
		t.Fatalf("census SDK calls %d", calls)
	}
	if snap, err := controller.Snapshot(); err != nil || snap.Transactions != 0 || snap.Rows != 0 {
		t.Fatalf("census recipes: %+v %v", snap, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.InspectTypedIndexAttempt(canceled, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	if calls != 6 {
		t.Fatal("canceled inspection reached SDK")
	}
}
