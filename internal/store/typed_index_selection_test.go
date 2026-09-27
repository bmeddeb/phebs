//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

func TestTypedIndexReadSelectionAccounting(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 10, 1)
	db, native := storeAccountingDB(t, ctx, owner)
	s := &Surreal{db: db, accounting: owner}
	calls := 0
	native.call = func(_ context.Context, req *connection.RPCRequest) (any, error) {
		calls++
		sql := req.Params[0].(string)
		if strings.Contains(sql, "UPDATE ") || strings.Contains(sql, "UPSERT ") || strings.Contains(sql, "CREATE ") {
			t.Fatal("query initialized source", sql)
		}
		if strings.Contains(sql, "typed_incarnation") {
			return []surrealdb.QueryResult[any]{{Status: "OK", Result: []typedSourceRecord{{Name: "example.invalid/reader", Commit: strings.Repeat("a", 40)}}}}, nil
		}
		return []surrealdb.QueryResult[any]{{Status: "OK", Result: []TypedIndexControl{}}}, nil
	}
	if _, e := s.ReadTypedIndexIntent(ctx, "example.invalid/reader"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("unselected is not one read", calls)
	}
	if _, e := s.ReadTypedIndexCurrentCustody(ctx, "example.invalid/reader"); !errors.Is(e, typedindex.Unprepared) {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal("uninitialized source did extra work", calls)
	}
	snap, e := controller.Snapshot()
	if e != nil || snap.Transactions != 0 || snap.Rows != 0 {
		t.Fatal("query write accounting", snap, e)
	}
}
func TestTypedIndexReadSelectionActualStore(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "read-selection")
	intent, e := s.ReadTypedIndexIntent(ctx, f.repo)
	if e != nil || intent.ProfileDigest != f.profile.Digest() {
		t.Fatal(intent, e)
	}
	f.enqueue(t, "one")
	chunk := f.claim(t)
	a, p := f.seal(t, chunk)
	bundle := f.bundle(t, a, p)
	f.advancePublication(t, chunk, bundle)
	pointer, e := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle)
	if e != nil {
		t.Fatal(e)
	}
	readctx, ledger, e := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 6})
	if e != nil {
		t.Fatal(e)
	}
	current, e := s.ReadTypedIndexCurrentCustody(readctx, f.repo)
	counts, countErr := ledger.Finish()
	if countErr != nil || counts.StoreReadAttempts != 6 || counts.StoreWriteAttempts != 0 {
		t.Fatal(counts, countErr)
	}
	if e != nil || current.Pointer != pointer {
		t.Fatal(current, e)
	}
	// Restored/legacy optional identity is genuinely absent, not malformed. Reads
	// preserve it exactly; the existing mutation getter remains able to initialize.
	if e = s.typedWrite(ctx, `UPDATE $repo UNSET typed_incarnation, typed_source_epoch RETURN NONE;`, map[string]any{"repo": repoID(f.repo)}, 1); e != nil {
		t.Fatal(e)
	}
	readctx, ledger, e = readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 2})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadTypedIndexIntent(readctx, f.repo); e != nil {
		t.Fatal("selection depended on source", e)
	}
	if _, e = s.ReadTypedIndexCurrentCustody(readctx, f.repo); !errors.Is(e, typedindex.Unprepared) {
		t.Fatal(e)
	}
	counts, countErr = ledger.Finish()
	if countErr != nil || counts.StoreReadAttempts != 2 || counts.StoreWriteAttempts != 0 {
		t.Fatal("restored reads", counts, countErr)
	}
	rows, e := storeQuery[[]typedSourceRecord](ctx, s.accounting, s.db, "SELECT "+typedSourceProjection+" FROM $repo", map[string]any{"repo": repoID(f.repo)}, storeRead())
	if e != nil {
		t.Fatal(e)
	}
	got := firstDomainRows(rows)
	if len(got) != 1 || got[0].HasIncarnation || got[0].HasEpoch {
		t.Fatal("read minted source", got)
	}
	for _, mutation := range []string{
		"SET typed_source_epoch=1",
		"SET typed_incarnation='invalid', typed_source_epoch=1",
		"SET indexed_commit_hash='invalid'",
	} {
		if e = s.typedWrite(ctx, "UPDATE $repo "+mutation+" RETURN NONE;", map[string]any{"repo": repoID(f.repo)}, 1); e != nil {
			t.Fatal(e)
		}
		if _, e = s.ReadTypedIndexCurrentCustody(ctx, f.repo); !errors.Is(e, typedindex.Invalid) {
			t.Fatal("malformed source treated unprepared", mutation, e)
		}
		if e = s.typedWrite(ctx, "UPDATE $repo SET indexed_commit_hash=$commit, typed_incarnation=NONE, typed_source_epoch=NONE RETURN NONE;", map[string]any{"repo": repoID(f.repo), "commit": current.Parent.Request().Source.Commit}, 1); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.GetTypedSource(ctx, f.repo); e != nil {
		t.Fatal("mutation initialization changed", e)
	}
	// A malformed selected intent cannot be mistaken for an absent one.
	raw, e := s.typedRead(ctx, "typed_index_intent", f.repo)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"", "{}"} {
		if e = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_intent", f.repo), "body": bad}, 1); e != nil {
			t.Fatal(e)
		}
		if _, e = s.ReadTypedIndexIntent(ctx, f.repo); e == nil || errors.Is(e, ErrNotFound) {
			t.Fatal("corrupt selection treated absent", e)
		}
	}
	if e = s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": typedID("typed_index_intent", f.repo), "body": raw}, 1); e != nil {
		t.Fatal(e)
	}
}
