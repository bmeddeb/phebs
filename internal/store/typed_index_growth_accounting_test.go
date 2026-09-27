//go:build darwin || linux

package store

import (
	"context"
	"strings"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

func TestTypedIndexGrowthReleaseAccounting(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 10, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	hash := typedDigest([]byte("fixture"))
	a := typedIndexAttempt{ChunkIdentity: hash, Root: hash, Request: hash, Lease: hash, Stage: TypedPreflight, States: [5]string{"running", "pending", "pending", "pending", "pending"}}
	a.Growth = &TypedIndexGrowth{PlanningDigest: hash, AttemptDigest: hash, ChunkID: "chunk", ChunkIdentity: hash, LeaseDigest: hash, State: "held", Spec: typedTestGrowthSpec()}
	body, _ := typedEncode(a, maxTypedControlBytes)
	selected := TypedIndexGrowthRelease{row: TypedIndexControl{ID: hash, StoredKey: hash, Repository: "example.invalid/accounting", Root: hash, Body: body, GrowthKey: typedGrowthKey}}
	native.call = func(_ context.Context, req *connection.RPCRequest) (any, error) {
		statement, ok := req.Params[0].(string)
		if !ok || strings.Count(statement, "UPDATE ") != 1 || strings.Contains(statement, "DELETE ") {
			t.Errorf("release mutation recipe changed")
		}
		snap, err := controller.Snapshot()
		if err != nil || snap.Transactions != 1 || snap.Rows != 1 || snap.MaximumRows != 1 {
			t.Errorf("one supplied operand: %+v %v", snap, err)
		}
		return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
	}
	if err := (&Surreal{db: db, accounting: owner}).ReleaseTypedIndexGrowth(ctx, selected); err != nil {
		t.Fatal(err)
	}
	if native.calls != 1 {
		t.Fatalf("SDK calls %d", native.calls)
	}
}
