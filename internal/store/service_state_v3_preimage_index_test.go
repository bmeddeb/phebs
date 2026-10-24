package store

import (
	"strings"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestServiceStateV3PreimagePointNativeIndex(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	plan, changes, _ := serviceStateV3HeadroomFixture(t, serviceStateV3Activate, 1)
	prior := changes[0].prior
	content := serviceStateContent(prior)
	content["visible_from"] = uint64(1)
	content["snapshot_revision"] = plan.SummaryControlRevision
	content["snapshot_digest"] = plan.SummaryDigest
	created, err := surrealdb.Query[any](ctx, s.db,
		"CREATE service_state_v3_preimage CONTENT $content RETURN NONE;", map[string]any{"content": content})
	if err != nil || created == nil || len(*created) != 1 || (*created)[0].Status != "OK" {
		t.Fatalf("seed preimage: %v / %+v", err, created)
	}
	for _, test := range []struct {
		name     string
		key      string
		revision uint64
		preserve bool
		want     int
	}{
		{"selected", prior.ServiceKey, plan.SummaryControlRevision, true, 1},
		{"missing-key", prior.ServiceKey + "-missing", plan.SummaryControlRevision, true, 0},
		{"other-snapshot", prior.ServiceKey, plan.SummaryControlRevision + 1, true, 0},
		{"unselected", prior.ServiceKey, plan.SummaryControlRevision, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			vars := map[string]any{
				"repository": plan.Repository, "preserve": test.preserve,
				"selector": map[string]any{"state_control_revision": test.revision},
				"update":   map[string]any{"content": map[string]any{"service_key": test.key}},
			}
			rows, err := surrealdb.Query[[]serviceStateV3TargetRec](ctx, s.db,
				serviceStateV3PreimagePointSQL+"SELECT * FROM $prior_rows;", vars)
			if err != nil || rows == nil || len(*rows) != 4 {
				t.Fatalf("native lookup: %v / %+v", err, rows)
			}
			for _, result := range *rows {
				if result.Status != "OK" || result.Error != nil {
					t.Fatalf("native statement: %+v", result)
				}
			}
			last := (*rows)[3].Result
			if len(last) != test.want || test.want == 1 &&
				(last[0].StateDigest != prior.StateDigest || last[0].ControlRevision != prior.ControlRevision || last[0].SnapshotDigest != plan.SummaryDigest) {
				t.Fatalf("native selected preimage changed: %+v", last)
			}
			if !test.preserve {
				return
			}
			explain := strings.Replace(serviceStateV3PreimagePointSQL, "LIMIT 2", "LIMIT 2 EXPLAIN FULL", 1)
			result, err := surrealdb.Query[any](ctx, s.db, explain+"RETURN $prior_rows;", vars)
			if err != nil || result == nil {
				t.Fatal("native explain", err)
			}
			for _, statement := range *result {
				if statement.Status != "OK" || statement.Error != nil {
					t.Fatalf("native explain statement: %+v", statement)
				}
			}
			indexes := 0
			for _, op := range retentionPlanOperators(result) {
				if op.operator == "TableScan" {
					t.Fatal("per-service lookup scans all retained preimages")
				}
				if op.operator == "IndexScan" {
					indexes++
					if op.attrs["index"] != "service_state_v3_preimage_snapshot" || op.attrs["limit"] != "2" {
						t.Fatalf("wrong preimage index/bound: %+v", op)
					}
				}
			}
			if indexes != 1 {
				t.Fatalf("native point lookup used %d index scans", indexes)
			}
		})
	}
	// Execute the production fragment repeatedly in one native FOR scope so
	// index planning cannot accidentally reuse the first iteration's key.
	vars := map[string]any{
		"repository": plan.Repository, "preserve": true,
		"selector": map[string]any{"state_control_revision": plan.SummaryControlRevision},
		"updates": []map[string]any{
			{"content": map[string]any{"service_key": prior.ServiceKey}, "want": 1},
			{"content": map[string]any{"service_key": prior.ServiceKey + "-missing"}, "want": 0},
			{"content": map[string]any{"service_key": prior.ServiceKey}, "want": 1},
		},
	}
	results, err := surrealdb.Query[any](ctx, s.db,
		"FOR $update IN $updates {"+serviceStateV3PreimagePointSQL+`
			IF array::len($prior_rows) != $update.want {
				THROW 'preimage lookup reused another iteration key';
			};
		}; RETURN true;`, vars)
	if err != nil || results == nil {
		t.Fatal("native loop bindings", err)
	}
	for _, result := range *results {
		if result.Status != "OK" || result.Error != nil {
			t.Fatalf("native loop binding statement: %+v", result)
		}
	}
}
