//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

func TestTypedIndexControlRelations(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "relations")
	if err := s.InspectTypedIndexControlRelations(ctx, TypedIndexIntents, f.repo); err != nil {
		t.Fatal("empty desired", err)
	}
	raw := f.enqueue(t, "one")
	root := typedDigest(raw)
	chunk := f.claim(t)
	work, err := s.BeginTypedIndex(ctx, chunk)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InspectTypedIndexControlRelations(ctx, TypedIndexRequests, root); err != nil {
		t.Fatal("unplanned parent", err)
	}
	if err = s.InspectTypedIndexControlRelations(ctx, TypedIndexAttempts, work.AttemptDigest); err != nil {
		t.Fatal("preflight", err)
	}
	admission, plan := f.seal(t, chunk)
	bundle := f.bundle(t, admission, plan)
	f.advancePublication(t, chunk, bundle)
	if _, err = s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, bundle); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		kind  TypedIndexControlKind
		key   string
		reads uint64
	}{
		{TypedIndexRequests, root, 3}, {TypedIndexRequests, admission.Digest(), 3}, {TypedIndexPlans, root, 2}, {TypedIndexAttempts, work.AttemptDigest, 3}, {TypedIndexStates, f.repo, 4}, {TypedIndexCurrents, f.repo, 4}, {TypedIndexIntents, f.repo, 4},
	}
	check := func() {
		t.Helper()
		for _, tc := range cases {
			rctx, ledger, e := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: tc.reads})
			if e != nil {
				t.Fatal(e)
			}
			e = s.InspectTypedIndexControlRelations(rctx, tc.kind, tc.key)
			counts, finish := ledger.Finish()
			if e != nil || finish != nil || counts.StoreReadAttempts != tc.reads {
				t.Fatalf("%s %s reads=%+v error=%v finish=%v", tc.kind, tc.key, counts, e, finish)
			}
		}
	}
	check()
	// Save complete scalar/body records once; each negative restores a positive.
	records := map[TypedIndexControlKind]TypedIndexControl{}
	for _, tc := range cases {
		rows, e := s.typedRelationPoint(ctx, tc.kind, tc.key)
		if e != nil || len(rows) != 1 {
			t.Fatal(e)
		}
		records[tc.kind] = rows[0]
	}
	restore := func(t *testing.T, kind TypedIndexControlKind, row TypedIndexControl) {
		t.Helper()
		statement := `UPSERT $rid SET body=$body,repository=$repository`
		if typedDigestControlKind(kind) {
			statement += `,control_key=$key,request_root=$root`
		}
		if kind == TypedIndexRequests {
			statement += `,is_parent=$parent,custody_state=$state`
		}
		if kind == TypedIndexAttempts {
			if row.GrowthKey == "" {
				statement += `,growth_key=NONE`
			} else {
				statement += `,growth_key=$growth`
			}
		}
		e := s.typedWrite(ctx, statement+` RETURN NONE;`, map[string]any{"rid": typedID(string(kind), row.ID), "body": row.Body, "repository": row.Repository, "key": row.StoredKey, "root": row.Root, "parent": row.Parent, "state": row.State, "growth": row.GrowthKey}, 1)
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		name     string
		selected TypedIndexControlKind
		key      string
		changed  TypedIndexControlKind
		mutate   func(*TypedIndexControl)
		missing  bool
	}{
		{name: "state missing attempt", selected: TypedIndexStates, key: f.repo, changed: TypedIndexAttempts, missing: true},
		{name: "current incomplete attempt", selected: TypedIndexCurrents, key: f.repo, changed: TypedIndexAttempts, mutate: func(r *TypedIndexControl) {
			var a typedIndexAttempt
			_ = typedDecode(r.Body, maxTypedControlBytes, &a)
			a.Stage = TypedValidation
			a.States = [5]string{"complete", "complete", "complete", "running", "pending"}
			r.Body, _ = typedEncode(a, maxTypedControlBytes)
		}},
		{name: "state foreign repository", selected: TypedIndexStates, key: f.repo, changed: TypedIndexAttempts, mutate: func(r *TypedIndexControl) { r.Repository = "example.invalid/other" }},
		{name: "current wrong source", selected: TypedIndexCurrents, key: f.repo, changed: TypedIndexCurrents, mutate: func(r *TypedIndexControl) {
			v, _ := decodeTypedIndexCurrent(r.Body)
			v.Pointer.Binding.Source.Commit = strings.Repeat("b", 40)
			r.Body, _ = typedEncode(v, maxTypedControlBytes)
		}},
		{name: "intent wrong epoch", selected: TypedIndexIntents, key: f.repo, changed: TypedIndexIntents, mutate: func(r *TypedIndexControl) {
			var v TypedIndexIntent
			_ = typedDecode(r.Body, maxTypedIntentBytes, &v)
			v.ProfileEpoch++
			r.Body, _ = typedEncode(v, maxTypedIntentBytes)
		}},
		{name: "plan missing successor", selected: TypedIndexPlans, key: root, changed: TypedIndexRequests, missing: true},
		{name: "intent missing desired", selected: TypedIndexIntents, key: f.repo, changed: TypedIndexRequests, missing: true},
		{name: "request missing plan", selected: TypedIndexRequests, key: admission.Digest(), changed: TypedIndexPlans, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := records[tc.changed]
			after := before
			if tc.missing {
				if e := s.typedWrite(ctx, `DELETE $rid RETURN NONE;`, map[string]any{"rid": typedID(string(tc.changed), before.ID)}, 1); e != nil {
					t.Fatal(e)
				}
			} else {
				tc.mutate(&after)
				restore(t, tc.changed, after)
			}
			if e := s.InspectTypedIndexControlRelations(ctx, tc.selected, tc.key); e == nil {
				t.Fatal("broken relation accepted")
			}
			restore(t, tc.changed, before)
			if e := s.InspectTypedIndexControlRelations(ctx, tc.selected, tc.key); e != nil {
				t.Fatal("restored positive", e)
			}
		})
	}
	t.Run("collecting parent associations", func(t *testing.T) {
		rows, e := s.typedRelationPoint(ctx, TypedIndexRequests, root)
		if e != nil || len(rows) != 1 {
			t.Fatal(e)
		}
		parent := rows[0]
		originalIntent := records[TypedIndexIntents]
		t.Cleanup(func() {
			restore(t, TypedIndexRequests, parent)
			restore(t, TypedIndexIntents, originalIntent)
		})
		collecting := parent
		collecting.State = "collecting"
		restore(t, TypedIndexRequests, collecting)
		for _, tc := range cases {
			e = s.InspectTypedIndexControlRelations(ctx, tc.kind, tc.key)
			if tc.kind == TypedIndexCurrents || tc.kind == TypedIndexIntents {
				if !errors.Is(e, typedindex.Invalid) {
					t.Fatal("active pointer to collecting parent", tc.kind, e)
				}
			} else if e != nil {
				t.Fatal("historical collecting relation", tc.kind, e)
			}
		}
		for _, mode := range []string{"canceled", "restore"} {
			var intent TypedIndexIntent
			if e = typedDecode(originalIntent.Body, maxTypedIntentBytes, &intent); e != nil {
				t.Fatal(e)
			}
			intent.Canceled, intent.RestoreRequired = mode == "canceled", mode == "restore"
			changed := originalIntent
			changed.Body, e = typedEncode(intent, maxTypedIntentBytes)
			if e != nil {
				t.Fatal(e)
			}
			restore(t, TypedIndexIntents, changed)
			if e = s.InspectTypedIndexControlRelations(ctx, TypedIndexIntents, f.repo); e != nil {
				t.Fatal("inactive desired collecting", mode, e)
			}
		}
	})
	check()
	for _, tc := range cases {
		t.Run("race/"+string(tc.kind)+"/"+tc.key, func(t *testing.T) {
			rows, e := s.typedRelationPoint(ctx, tc.kind, tc.key)
			if e != nil {
				t.Fatal(e)
			}
			before := rows[0]
			e = s.inspectTypedIndexControlRelations(ctx, tc.kind, tc.key, func() { changed := before; changed.Body += " "; restore(t, tc.kind, changed) })
			if !errors.Is(e, typedindex.Stale) {
				t.Fatal("selected replacement", e)
			}
			restore(t, tc.kind, before)
			if e = s.InspectTypedIndexControlRelations(ctx, tc.kind, tc.key); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		selected TypedIndexControlKind
		key      string
		changed  TypedIndexControlKind
	}{
		{"state attempt", TypedIndexStates, f.repo, TypedIndexAttempts},
		{"request plan", TypedIndexRequests, admission.Digest(), TypedIndexPlans},
		{"intent desired", TypedIndexIntents, f.repo, TypedIndexRequests},
	} {
		t.Run("relation race/"+tc.name, func(t *testing.T) {
			before := records[tc.changed]
			e := s.inspectTypedIndexControlRelations(ctx, tc.selected, tc.key, func() {
				changed := before
				changed.Body += " "
				restore(t, tc.changed, changed)
			})
			if !errors.Is(e, typedindex.Stale) {
				t.Fatal("dependent replacement", e)
			}
			restore(t, tc.changed, before)
			if e = s.InspectTypedIndexControlRelations(ctx, tc.selected, tc.key); e != nil {
				t.Fatal("restored positive", e)
			}
		})
	}
	parentRows, err := s.typedRelationPoint(ctx, TypedIndexRequests, root)
	if err != nil || len(parentRows) != 1 {
		t.Fatal(err)
	}
	if err = s.typedWrite(ctx, `DELETE $rid RETURN NONE;`, map[string]any{"rid": typedID(string(TypedIndexRequests), root)}, 1); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []TypedIndexControlKind{TypedIndexPlans, TypedIndexRequests} {
		key := root
		if kind == TypedIndexRequests {
			key = admission.Digest()
		}
		if err = s.InspectTypedIndexControlRelations(ctx, kind, key); !errors.Is(err, typedindex.Invalid) {
			t.Fatal("missing parent", kind, err)
		}
	}
	restore(t, TypedIndexRequests, parentRows[0])
	check()
	// Source/profile liveness is not historical relation authority.
	if err = s.CancelTypedIndex(ctx, f.repo, admission.Digest()); err != nil {
		t.Fatal(err)
	}
	check()
	if err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("c", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	check()
	if err = s.SetRepoDeleting(ctx, f.repo, true); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteRepo(ctx, f.repo); err != nil {
		t.Fatal(err)
	}
	retained := cases[:0]
	for _, tc := range cases {
		if tc.kind == TypedIndexCurrents || tc.kind == TypedIndexIntents {
			if err = s.InspectTypedIndexControlRelations(ctx, tc.kind, tc.key); !errors.Is(err, ErrNotFound) {
				t.Fatal("deleted repository authority retained", tc.kind, err)
			}
			continue
		}
		retained = append(retained, tc)
	}
	cases = retained
	check()
	if err = s.UpsertRepo(ctx, Repo{Name: f.repo}); err != nil {
		t.Fatal(err)
	}
	check()
	if err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("d", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if err = s.InspectTypedIndexControlRelations(ctx, tc.kind, tc.key); err != nil {
			t.Fatal("profile replacement", tc.kind, err)
		}
	}
	if err = s.InspectTypedIndexControlRelations(ctx, TypedIndexAttempts, typedDigest([]byte("missing"))); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.InspectTypedIndexControlRelations(ctx, "injected", root); !errors.Is(err, typedindex.Invalid) {
		t.Fatal(err)
	}
}

func TestTypedIndexControlRelationsAccounting(t *testing.T) {
	ctx, owner, controller := storeAccountingFixture(t, 10, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	s := &Surreal{db: db, accounting: owner}
	h := typedDigest([]byte("fixture"))
	tool := typedindex.Tool{Version: "1", Digest: h}
	def := typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: h, ImageDigest: h}
	p, err := typedindex.DecodeProfile(ctx, typedTestJSON(t, def))
	if err != nil {
		t.Fatal(err)
	}
	source, err := typedSource(typedSourceRecord{Name: "example.invalid/accounting", Commit: strings.Repeat("a", 40), Incarnation: strings.Repeat("b", 32), Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	r := typedindex.NewRequest(source, p, 1, h, "one")
	raw := string(typedTestJSON(t, r))
	root := typedDigest([]byte(raw))
	body, _ := typedEncode(typedIndexRequest{Raw: raw, Root: root, SourceEpoch: 1}, typedindex.MaxRequestBytes+1024)
	parent := TypedIndexControl{ID: root, StoredKey: root, Repository: source.Repository, Root: root, Parent: true, State: "live", Body: body}
	intent := TypedIndexIntent{Repository: source.Repository, ProfileJSON: string(typedTestJSON(t, def)), ProfileDigest: p.Digest(), ProfileEpoch: 1, UniverseDigest: h, Desired: root}
	ib, _ := typedEncode(intent, maxTypedIntentBytes)
	selected := TypedIndexControl{ID: source.Repository, Repository: source.Repository, Body: ib}
	calls := 0
	native.call = func(_ context.Context, req *connection.RPCRequest) (any, error) {
		calls++
		statement, ok := req.Params[0].(string)
		if !ok || strings.Contains(statement, "UPDATE ") || strings.Contains(statement, "CREATE ") || strings.Contains(statement, "DELETE ") {
			t.Fatal("not read-only")
		}
		var result any
		switch calls {
		case 1:
			result = []TypedIndexControl{selected}
		case 2:
			result = []TypedIndexControl{parent}
		case 3:
			result = []TypedIndexControl{}
		case 4:
			result = []typedControlRelations{{Selected: []TypedIndexControl{selected}, Relations: typedAttemptObservation{Parent: []TypedIndexControl{parent}, Request: []TypedIndexControl{parent}, Successor: []TypedIndexControl{parent}}}}
		default:
			t.Fatal("unbounded calls")
		}
		return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
	}
	if err = s.InspectTypedIndexControlRelations(ctx, TypedIndexIntents, source.Repository); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal(calls)
	}
	if snap, e := controller.Snapshot(); e != nil || snap.Transactions != 0 || snap.Rows != 0 {
		t.Fatal(snap, e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = s.InspectTypedIndexControlRelations(canceled, TypedIndexIntents, source.Repository); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal("canceled reached SDK")
	}
}
