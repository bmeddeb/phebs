package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexCensusLimits(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "census-limits")
	raw := f.enqueue(t, "original")
	root := typedDigest(raw)
	body, err := s.typedReadControl(ctx, TypedIndexRequests, root)
	if err != nil {
		t.Fatal(err)
	}
	var stored typedIndexRequest
	var parent typedindex.Request
	if typedDecode(body, typedindex.MaxRequestBytes+1024, &stored) != nil || typedDecode(stored.Raw, typedindex.MaxRequestBytes, &parent) != nil {
		t.Fatal("fixture parent")
	}
	addRequest := func(r typedindex.Request, planning string, isParent bool) string {
		t.Helper()
		b := typedTestJSON(t, r)
		key := typedDigest(b)
		state := ""
		if isParent {
			planning, state = key, "collecting"
		}
		record, e := typedEncode(typedIndexRequest{Raw: string(b), Root: planning, SourceEpoch: stored.SourceEpoch}, typedindex.MaxRequestBytes+1024)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$key,is_parent=$parent,custody_state=$state,body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), key), "repo": f.repo, "root": planning, "key": key, "parent": isParent, "state": state, "body": record}, 1); e != nil {
			t.Fatal(e)
		}
		return key
	}
	// The maximal retained repository contains64 parents and64 successors.
	roots := []string{root}
	for n := range 64 {
		r := parent
		planning := root
		if n != 0 {
			r.IdempotencyKey = fmt.Sprintf("parent-%d", n)
			planning = addRequest(r, "", true)
			roots = append(roots, planning)
		}
		r.Action, r.ParentRequestDigest, r.PlanDigest = typedindex.Execute, planning, typedDigest([]byte(fmt.Sprintf("plan-%d", n)))
		addRequest(r, planning, false)
	}
	chunk := f.claim(t)
	work, err := s.BeginTypedIndex(ctx, chunk)
	if err != nil {
		t.Fatal(err)
	}
	attemptBody, err := s.typedReadControl(ctx, TypedIndexAttempts, work.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	addAttempt := func(n int) string {
		t.Helper()
		id := typedDigest([]byte(fmt.Sprintf("retained-attempt-%d", n)))
		if e := s.typedWrite(ctx, `CREATE ONLY $rid SET repository=$repo,request_root=$root,control_key=$key,body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), id), "repo": f.repo, "root": root, "key": id, "body": attemptBody}, 1); e != nil {
			t.Fatal(e)
		}
		return id
	}
	for n := range 63 {
		addAttempt(n)
	}
	check := func() {
		t.Helper()
		rctx, ledger, e := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 2})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.InspectTypedIndexRetainedLimits(rctx, f.repo, root); e != nil {
			t.Fatal(e)
		}
		got, e := ledger.Finish()
		if e != nil || got != (readaccounting.Counts{StoreReadAttempts: 2}) {
			t.Fatalf("reads %+v %v", got, e)
		}
	}
	check()
	requireRetentionExplain(t, ctx, s, typedRootAdmissionSQL+" EXPLAIN FULL", map[string]any{"repository": f.repo, "scan_limit": 129}, "IndexScan", "typed_index_request_repository")
	requireRetentionExplain(t, ctx, s, typedAttemptAdmissionSQL+" EXPLAIN FULL", map[string]any{"root": root, "scan_limit": 65}, "IndexScan", "typed_index_attempt_root")
	extra := addAttempt(64)
	if e := s.InspectTypedIndexRetainedLimits(ctx, f.repo, root); !errors.Is(e, typedindex.Capacity) {
		t.Fatal("65 attempts", e)
	}
	if e := s.typedWrite(ctx, `DELETE $rid RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexAttempts), extra)}, 1); e != nil {
		t.Fatal(e)
	}
	check()
	r := parent
	r.IdempotencyKey = "overflow-parent"
	extra = addRequest(r, "", true)
	if e := s.InspectTypedIndexRetainedLimits(ctx, f.repo, root); !errors.Is(e, typedindex.Capacity) {
		t.Fatal("65 parents", e)
	}
	if e := s.typedWrite(ctx, `DELETE $rid RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), extra)}, 1); e != nil {
		t.Fatal(e)
	}
	check()
	if e := s.InspectTypedIndexRetainedLimits(ctx, "example.invalid/foreign", root); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign root", e)
	}
	if e := s.InspectTypedIndexRetainedLimits(ctx, f.repo, typedDigest([]byte("missing"))); !errors.Is(e, ErrNotFound) {
		t.Fatal("missing parent", e)
	}
	bad := roots[1]
	if e := s.typedWrite(ctx, `UPDATE $rid SET is_parent=NONE RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), bad)}, 1); e != nil {
		t.Fatal(e)
	}
	if e := s.InspectTypedIndexRetainedLimits(ctx, f.repo, root); !errors.Is(e, typedindex.Invalid) {
		t.Fatal("malformed discriminator", e)
	}
	if e := s.typedWrite(ctx, `UPDATE $rid SET is_parent=true RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), bad)}, 1); e != nil {
		t.Fatal(e)
	}
	check()
	if err = s.DeleteRepo(ctx, f.repo); err != nil {
		t.Fatal(err)
	}
	check() // Historical custody is still bounded after repository deletion.
}
