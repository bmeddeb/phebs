package store

import (
	"context"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestSchemaUpgradeDeletesInvestigationsAndPreservesProofPins(t *testing.T) {
	ctx := t.Context()
	s, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	tables := []string{
		"investigation", "investigation_revision", "investigation_run",
		"investigation_run_event", "investigation_run_artifact",
		"investigation_artifact_owner", "investigation_artifact_owner_release",
		"investigation_artifact_retention_override", "investigation_decision",
		"investigation_disposition", "investigation_baseline_designation",
		"investigation_grant", "investigation_cursor", "investigation_creation",
		"investigation_consumer_snapshot", "investigation_consumer_edge_ledger",
		"investigation_review_projection", "investigation_review_item",
		"investigation_dossier", "investigation_watch", "investigation_watch_revision",
		"investigation_workbench_mutation", "investigation_workbench_disposition",
		"investigation_change_brief", "investigation_run_job",
	}
	for _, table := range tables {
		results, err := surrealdb.Query[any](ctx, s.db,
			"DEFINE TABLE IF NOT EXISTS "+table+" SCHEMALESS; CREATE "+table+":legacy CONTENT {value: true};", nil)
		if err != nil || results == nil || len(*results) != 2 {
			t.Fatalf("seed %s: results=%v err=%v", table, results, err)
		}
		for _, result := range *results {
			if result.Status != "OK" || result.Error != nil {
				t.Fatalf("seed %s: %+v", table, result)
			}
		}
	}
	seed := `
CREATE evidence_pin:investigation CONTENT {kind: 'investigation-artifact:artifact', pin_key: 'investigation', run_id: 'run'};
CREATE evidence_pin:proof CONTENT {kind: 'proof-bundle:proof', pin_key: 'proof', run_id: 'run'};
CREATE lifecycle_cursor:investigation CONTENT {key: 'owner:investigations', cursor: '', revision: 1, updated_at: time::now()};
CREATE lifecycle_cursor:jobs CONTENT {key: 'owner:durable-jobs', cursor: '{"kind":7,"phase":"count"}', revision: 1, updated_at: time::now()};
DELETE store_migration:retired_investigation_tables;`
	if results, err := surrealdb.Query[any](ctx, s.db, seed, nil); err != nil || results == nil || len(*results) != 5 {
		t.Fatalf("seed shared rows: results=%v err=%v", results, err)
	}

	for pass := 0; pass < 2; pass++ {
		if err := s.applySchema(ctx); err != nil {
			t.Fatalf("schema upgrade pass %d: %v", pass, err)
		}
		results, err := surrealdb.Query[struct {
			Tables map[string]string `json:"tables"`
		}](ctx, s.db, "INFO FOR DB;", nil)
		if err != nil || results == nil || len(*results) != 1 || (*results)[0].Status != "OK" {
			t.Fatalf("table census pass %d: results=%v err=%v", pass, results, err)
		}
		for _, table := range tables {
			if _, exists := (*results)[0].Result.Tables[table]; exists {
				t.Fatalf("retired table %s survived upgrade pass %d", table, pass)
			}
		}
		pins, err := surrealdb.Query[[]struct {
			Kind string `json:"kind"`
		}](ctx, s.db, "SELECT kind FROM evidence_pin ORDER BY kind;", nil)
		if err != nil || pins == nil || len(*pins) != 1 || len((*pins)[0].Result) != 1 ||
			(*pins)[0].Result[0].Kind != "proof-bundle:proof" {
			t.Fatalf("unrelated proof pin changed on pass %d: pins=%v err=%v", pass, pins, err)
		}
		cursors, err := surrealdb.Query[[]any](ctx, s.db,
			"SELECT id FROM lifecycle_cursor WHERE key IN ['owner:investigations', 'owner:durable-jobs'];", nil)
		if err != nil || cursors == nil || len(*cursors) != 1 || len((*cursors)[0].Result) != 0 {
			t.Fatalf("retired lifecycle cursors survived pass %d: rows=%v err=%v", pass, cursors, err)
		}
	}
}
