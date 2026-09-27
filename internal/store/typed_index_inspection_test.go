package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestTypedIndexObsoleteInspection(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "inspection")
	raw := f.enqueue(t, "first")
	chunk := f.claim(t)
	w, err := s.BeginTypedIndex(ctx, chunk)
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, executed bool) {
		t.Helper()
		readctx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 3})
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.InspectTypedIndexAttempt(readctx, w.AttemptDigest)
		if err != nil || string(typedTestJSON(t, got.Parent)) != string(raw) || got.PlanningDigest != w.RootDigest || got.AttemptDigest != w.AttemptDigest || got.ChunkIdentity != chunk.Identity || got.LeaseDigest != GenerationLeaseTokenDigest(chunk.LeaseToken) || (got.PlanDigest != "") != executed {
			t.Fatalf("inspection: %+v %v", got, err)
		}
		if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 3}) {
			t.Fatalf("inspection read accounting: %+v %v", counts, err)
		}
	}
	check(t, false)
	a, _ := f.seal(t, chunk)
	check(t, true)
	for _, mode := range []string{"cancel", "profile", "head", "delete", "readd"} {
		t.Run(mode, func(t *testing.T) {
			switch mode {
			case "cancel":
				err = s.CancelTypedIndex(ctx, f.repo, a.Digest())
			case "profile":
				_, err = s.InstallTypedProfile(ctx, f.repo, f.profile, f.intent.UniverseDigest, 1)
			case "head":
				err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now())
			case "delete":
				if err = s.SetRepoDeleting(ctx, f.repo, true); err == nil {
					err = s.DeleteRepo(ctx, f.repo)
				}
			case "readd":
				err = s.UpsertRepo(ctx, Repo{Name: f.repo})
			}
			if err != nil {
				t.Fatal(err)
			}
			check(t, true)
			if _, err := s.BeginTypedIndex(ctx, chunk); err == nil {
				t.Fatal("obsolete custody regained live admission")
			}
		})
	}
	if _, err = s.InspectTypedIndexAttempt(ctx, typedDigest([]byte("missing"))); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestTypedIndexInspectionRelationsAndRaces(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "inspection-relations")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	w, err := s.BeginTypedIndex(ctx, chunk)
	if err != nil {
		t.Fatal(err)
	}
	admission, _ := f.seal(t, chunk)
	attemptID := typedID(string(TypedIndexAttempts), w.AttemptDigest)
	parentID := typedID(string(TypedIndexRequests), w.RootDigest)
	requestID := typedID(string(TypedIndexRequests), admission.Digest())
	planID := typedID(string(TypedIndexPlans), w.RootDigest)
	for _, tc := range []struct {
		name, sql string
		id        any
	}{
		{"attempt projection", `UPDATE $rid SET request_root=NONE RETURN NONE;`, attemptID},
		{"attempt body", `UPDATE $rid SET body='{}' RETURN NONE;`, attemptID},
		{"parent projection", `UPDATE $rid SET is_parent=false RETURN NONE;`, parentID},
		{"parent repository", `UPDATE $rid SET repository='example.invalid/other' RETURN NONE;`, parentID},
		{"request epoch", `UPDATE $rid SET body=$changed RETURN NONE;`, requestID},
		{"plan identity", `UPDATE $rid SET body=$changed RETURN NONE;`, planID},
		{"plan missing", `DELETE $rid RETURN NONE;`, planID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Save and restore the exact test-owned record around the selection race.
			var kind TypedIndexControlKind
			var key string
			switch tc.id {
			case attemptID:
				kind, key = TypedIndexAttempts, w.AttemptDigest
			case parentID:
				kind, key = TypedIndexRequests, w.RootDigest
			case requestID:
				kind, key = TypedIndexRequests, admission.Digest()
			default:
				kind, key = TypedIndexPlans, w.RootDigest
			}
			body, err := s.typedReadControl(ctx, kind, key)
			if err != nil {
				t.Fatal(err)
			}
			changed := ""
			if tc.name == "request epoch" {
				var r typedIndexRequest
				_ = typedDecode(body, typedindex.MaxRequestBytes+1024, &r)
				r.SourceEpoch++
				changed, _ = typedEncode(r, typedindex.MaxRequestBytes+1024)
			}
			if tc.name == "plan identity" {
				var p typedIndexPlan
				_ = typedDecode(body, maxTypedControlBytes, &p)
				p.Digest = typedDigest([]byte("different"))
				changed, _ = typedEncode(p, maxTypedControlBytes)
			}
			_, err = s.inspectTypedIndexAttempt(ctx, w.AttemptDigest, func() {
				if err := s.typedWrite(ctx, tc.sql, map[string]any{"rid": tc.id, "changed": changed}, 1); err != nil {
					t.Fatal(err)
				}
			})
			if err == nil {
				t.Fatal("changed observation accepted")
			}
			vars := map[string]any{"rid": tc.id, "body": body, "repository": f.repo, "root": w.RootDigest, "parent": kind == TypedIndexRequests && key == w.RootDigest, "state": ""}
			if vars["parent"] == true {
				vars["state"] = "live"
			}
			restoreSQL := `UPSERT $rid SET body=$body,repository=$repository,request_root=$root,control_key=record::id($rid) RETURN NONE;`
			if kind == TypedIndexRequests {
				restoreSQL = `UPSERT $rid SET body=$body,repository=$repository,request_root=$root,control_key=record::id($rid),is_parent=$parent,custody_state=$state RETURN NONE;`
			}
			if err := s.typedWrite(ctx, restoreSQL, vars, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := s.InspectTypedIndexAttempt(ctx, w.AttemptDigest); err != nil {
				t.Fatalf("restored positive control: %v", err)
			}
		})
	}
	original, err := s.typedReadControl(ctx, TypedIndexAttempts, w.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	var saved typedIndexAttempt
	if typedDecode(original, maxTypedControlBytes, &saved) != nil {
		t.Fatal("fixture decode")
	}
	for _, tc := range []struct {
		name                    string
		parent, preflight, want bool
	}{
		{"parent cannot execute", true, false, false}, {"old parent beside sealed plan", true, true, true}, {"successor retry preflight", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := saved
			if tc.parent {
				changed.Request = changed.Root
			}
			if tc.preflight {
				changed.Stage = TypedPreflight
				changed.States = [5]string{"running", "pending", "pending", "pending", "pending"}
			}
			body, _ := typedEncode(changed, maxTypedControlBytes)
			if err := s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": attemptID, "body": body}, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := s.InspectTypedIndexAttempt(ctx, w.AttemptDigest); (err == nil) != tc.want {
				t.Fatalf("inspect %v", err)
			}
			if tc.parent && tc.preflight {
				successorBody, err := s.typedReadControl(ctx, TypedIndexRequests, admission.Digest())
				if err != nil {
					t.Fatal(err)
				}
				if err := s.typedWrite(ctx, `DELETE $rid RETURN NONE;`, map[string]any{"rid": requestID}, 1); err != nil {
					t.Fatal(err)
				}
				if _, err := s.InspectTypedIndexAttempt(ctx, w.AttemptDigest); err == nil {
					t.Fatal("old parent hid missing plan successor")
				}
				if err := s.typedWrite(ctx, `CREATE $rid SET repository=$repository,request_root=$root,control_key=record::id($rid),is_parent=false,custody_state='',body=$body RETURN NONE;`, map[string]any{"rid": requestID, "repository": f.repo, "root": w.RootDigest, "body": successorBody}, 1); err != nil {
					t.Fatal(err)
				}
				if _, err := s.InspectTypedIndexAttempt(ctx, w.AttemptDigest); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.typedWrite(ctx, `UPDATE $rid SET body=$body RETURN NONE;`, map[string]any{"rid": attemptID, "body": original}, 1); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := s.RetryGenerationChunk(ctx, chunk, "execution_failed", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimGenerationChunk(ctx, GenerationResourceTypedIndex, "inspection-retry")
	if err != nil || next == nil {
		t.Fatalf("claim retry: %+v %v", next, err)
	}
	work, err := s.BeginTypedIndex(ctx, *next)
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := s.InspectTypedIndexAttempt(ctx, work.AttemptDigest)
	if err != nil || inspected.Stage != TypedPreflight || inspected.RequestDigest != admission.Digest() {
		t.Fatalf("actual successor retry: %+v %v", inspected, err)
	}

}

func TestTypedIndexSixTableCensus(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "census")
	f.enqueue(t, "one")
	chunk := f.claim(t)
	a, p := f.seal(t, chunk)
	b := f.bundle(t, a, p)
	f.advancePublication(t, chunk, b)
	if _, err := s.PublishTypedIndex(ctx, chunk, typedindex.PublicationPointer{}, b); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []TypedIndexControlKind{TypedIndexRequests, TypedIndexAttempts, TypedIndexPlans, TypedIndexIntents, TypedIndexStates, TypedIndexCurrents} {
		t.Run(string(kind), func(t *testing.T) {
			readctx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{StoreReadAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			page, err := s.ScanTypedIndexControls(readctx, kind, "", 1)
			if err != nil || len(page.Rows) != 1 {
				t.Fatalf("page: %+v %v", page, err)
			}
			if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{StoreReadAttempts: 1}) {
				t.Fatalf("census read accounting: %+v %v", counts, err)
			}
			key := page.Rows[0].ID
			if !typedDigestControlKind(kind) {
				if key != f.repo {
					t.Fatal("repository cursor")
				}
				if _, err := s.ScanTypedIndexRootChildren(ctx, chunk.Generation, kind, "", 1); err == nil {
					t.Fatal("repository table accepted root scan")
				}
			}
			for _, mutate := range []func(*TypedIndexControl){
				func(r *TypedIndexControl) { r.Repository = "" }, func(r *TypedIndexControl) { r.ID = "bad" }, func(r *TypedIndexControl) { r.Body = "{}" }, func(r *TypedIndexControl) { r.Body += " " },
			} {
				bad := page.Rows[0]
				mutate(&bad)
				if validateTypedCensusControl(ctx, kind, bad) == nil {
					t.Fatal("malformed census control accepted")
				}
			}

			switch kind {
			case TypedIndexIntents:
				var intent TypedIndexIntent
				_ = typedDecode(page.Rows[0].Body, maxTypedIntentBytes, &intent)
				for _, mutate := range []func(*TypedIndexIntent){func(v *TypedIndexIntent) { v.ProfileEpoch = 0 }, func(v *TypedIndexIntent) { v.ProfileDigest = typedDigest([]byte("wrong")) }, func(v *TypedIndexIntent) { v.ProfileJSON = " " + v.ProfileJSON }, func(v *TypedIndexIntent) { v.Desired = "bad" }} {
					bad := page.Rows[0]
					v := intent
					mutate(&v)
					bad.Body, _ = typedEncode(v, maxTypedIntentBytes)
					if validateTypedCensusControl(ctx, kind, bad) == nil {
						t.Fatal("bad profile identity accepted")
					}
				}
			case TypedIndexCurrents:
				current, _ := decodeTypedIndexCurrent(page.Rows[0].Body)
				for _, mutate := range []func(*typedIndexCurrent){func(v *typedIndexCurrent) { v.Pointer.Binding.ToolsDigest = "bad" }, func(v *typedIndexCurrent) { v.Pointer.Binding.Source.Incarnation = "../bad" }, func(v *typedIndexCurrent) { v.Pointer.Binding.Source.Commit = strings.Repeat("A", 40) }, func(v *typedIndexCurrent) { v.AttemptDigest = "bad" }, func(v *typedIndexCurrent) { v.Pointer.RootDigest = "sha256:" + strings.Repeat("A", 64) }} {
					bad := page.Rows[0]
					v := current
					mutate(&v)
					bad.Body, _ = typedEncode(v, maxTypedControlBytes)
					if validateTypedCensusControl(ctx, kind, bad) == nil {
						t.Fatal("bad current scalar accepted")
					}
				}
			}

			for _, after := range []string{"", key} {
				statement, vars := typedControlScanStatement(kind, "", after, 64)
				statement = strings.TrimSuffix(statement, ";") + " EXPLAIN FULL"
				if after == "" {
					requireRetentionExplain(t, ctx, s, statement, vars, "TableScan", string(kind))
					continue
				}
				results, err := surrealdb.Query[any](ctx, s.db, statement, vars)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range *results {
					if r.Error != nil {
						t.Fatal(r.Error.Message)
					}
				}
				scans := 0
				for _, op := range retentionPlanOperators(results) {
					switch op.operator {
					case "SelectProject", "Compute":
					case "DynamicScan":
						scans++
						if op.attrs["limit"] != "$scan_limit" || op.attrs["source"] != "type::record(...)" {
							t.Fatalf("unbounded range: %+v", op)
						}
					default:
						t.Fatalf("unexpected range operator: %+v", op)
					}
				}
				if scans != 1 {
					t.Fatalf("range scans: %d", scans)
				}
			}

			if _, err := s.ScanTypedIndexControls(ctx, kind, "not/a/cursor\x00", 1); err == nil {
				t.Fatal("invalid cursor accepted")
			}
			if _, err := s.ScanTypedIndexControls(ctx, kind, "", 65); err == nil {
				t.Fatal("oversized page accepted")
			}
		})
	}
	// Repository-keyed rows remain enumerable after deletion and across the
	// 64-row boundary; no repository listing or current intent is used.
	for _, kind := range []TypedIndexControlKind{TypedIndexIntents, TypedIndexStates, TypedIndexCurrents} {
		original, err := s.ScanTypedIndexControls(ctx, kind, "", 64)
		if err != nil {
			t.Fatal(err)
		}
		row := original.Rows[0]
		for n := 0; n < 65; n++ {
			repo := fmt.Sprintf("example.invalid/z%02d", n)
			body := row.Body
			switch kind {
			case TypedIndexIntents:
				var v TypedIndexIntent
				_ = typedDecode(body, maxTypedIntentBytes, &v)
				v.Repository = repo
				body, _ = typedEncode(v, maxTypedIntentBytes)
			case TypedIndexCurrents:
				v, _ := decodeTypedIndexCurrent(body)
				v.Pointer.Binding.Source.Repository = repo
				body, _ = typedEncode(v, maxTypedControlBytes)
			}
			if err := s.typedWrite(ctx, `CREATE $rid SET repository=$repository,body=$body RETURN NONE;`, map[string]any{"rid": typedID(string(kind), repo), "repository": repo, "body": body}, 1); err != nil {
				t.Fatal(err)
			}
		}
		page, err := s.ScanTypedIndexControls(ctx, kind, "", 64)
		if err != nil || len(page.Rows) != 64 || page.Next == "" {
			t.Fatalf("boundary %+v %v", page, err)
		}
		tail, err := s.ScanTypedIndexControls(ctx, kind, page.Next, 64)
		if err != nil || len(tail.Rows) != 2 || tail.Next != "" {
			t.Fatalf("tail %+v %v", tail, err)
		}
		// Corrupt sentinel: a page must refuse rather than hide malformed overflow.
		if err := s.typedWrite(ctx, `UPDATE $rid SET body='{}' RETURN NONE;`, map[string]any{"rid": typedID(string(kind), tail.Rows[0].ID)}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ScanTypedIndexControls(ctx, kind, "", 64); !errors.Is(err, typedindex.Invalid) {
			t.Fatalf("bad sentinel: %v", err)
		}
	}
}
