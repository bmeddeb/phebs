package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestServiceStateV3MigrationDefinitionBatches(t *testing.T) {
	for _, recipe := range []struct {
		name, source string
		count        int
	}{
		{"preflight", serviceStateV3PreflightSchema, 3},
		{"state", serviceStateV3Schema, 74},
		{"optional", serviceStateV3SnapshotOptionalSchema, 1},
		{"snapshot", serviceStateV3SnapshotSchema, 48},
	} {
		t.Run(recipe.name, func(t *testing.T) {
			if count, err := schemaBatchDefinitionCount(recipe.source); err != nil || count != recipe.count {
				t.Fatalf("definitions=%d, error=%v; want %d", count, err, recipe.count)
			}
			s, conn := restoreClearScript(t, []restoreClearStep{{contains: "BEGIN;\n" + recipe.source + "\nCOMMIT;"}}, nil)
			if err := s.applySchemaBatch(t.Context(), recipe.source, "state test "); err != nil || conn.calls != 1 {
				t.Fatalf("batch calls=%d error=%v", conn.calls, err)
			}
		})
	}
}

func TestServiceStateV3MigrationSchemaOrder(t *testing.T) {
	for _, outcome := range []string{"success", "current", "unowned", "preflight_failure", "schema_failure", "marker_failure"} {
		t.Run(outcome, func(t *testing.T) {
			steps := []restoreClearStep{
				{contains: "SELECT version FROM $rid", rows: []any{}},
				{contains: "BEGIN;\n" + serviceStateV3PreflightSchema + "\nCOMMIT;"},
				{contains: "RETURN [{ count:", rows: []map[string]any{{"count": 0}}},
				{contains: "BEGIN;\n" + serviceStateV3Schema + "\nCOMMIT;"},
				{contains: "UPSERT $rid SET version = IF"},
			}
			failure := errors.New("native state migration failure")
			want := error(nil)
			switch outcome {
			case "current":
				steps[0].rows = []map[string]any{{"version": serviceStateV3SchemaMigrationVersion}}
				steps = steps[:1]
			case "unowned":
				steps[2].rows = []map[string]any{{"count": 1}}
				steps = steps[:3]
			case "preflight_failure", "schema_failure", "marker_failure":
				index := slices.Index([]string{"", "preflight_failure", "", "schema_failure", "marker_failure"}, outcome)
				steps[index].err = failure
				steps, want = steps[:index+1], failure
			}
			s, conn := restoreClearScript(t, steps, nil)
			err := s.migrateServiceStateV3Schema(t.Context())
			if conn.calls != len(steps) || (outcome == "unowned" && (err == nil || !strings.Contains(err.Error(), "unowned pre-migration rows"))) ||
				(outcome != "unowned" && !errors.Is(err, want)) {
				t.Fatalf("schema calls=%d error=%v; want %d/%v", conn.calls, err, len(steps), want)
			}
		})
	}
}

func TestServiceStateV3MigrationBackfillPages(t *testing.T) {
	for _, count := range []int{0, 1, 511, 512, 513, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var steps []restoreClearStep
			for offset := 0; offset < count; offset += 512 {
				ids := make([]models.RecordID, min(count-offset, 512))
				for index := range ids {
					ids[index] = models.NewRecordID("service_state_v3_current", uint64(offset+index))
				}
				steps = append(steps,
					restoreClearStep{contains: serviceStateV3VisibleFromSelection, rows: ids},
					restoreClearStep{contains: serviceStateV3VisibleFromWrite, ids: ids},
				)
			}
			steps = append(steps, restoreClearStep{contains: serviceStateV3VisibleFromSelection, rows: []models.RecordID{}})
			s, conn := restoreClearScript(t, steps, nil)
			if err := s.backfillServiceStateV3VisibleFrom(t.Context()); err != nil || conn.calls != len(steps) {
				t.Fatalf("backfill calls=%d error=%v; want %d", conn.calls, err, len(steps))
			}
		})
	}
}

func TestServiceStateV3MigrationBackfillFailure(t *testing.T) {
	ids := []models.RecordID{models.NewRecordID("service_state_v3_current", []any{"native", uint64(1)})}
	failure := errors.New("lost migration response")
	for _, test := range []struct {
		name  string
		steps []restoreClearStep
		want  error
	}{
		{"canceled_empty", []restoreClearStep{{contains: serviceStateV3VisibleFromSelection, rows: []models.RecordID{}, cancel: true}}, context.Canceled},
		{"canceled_page", []restoreClearStep{{contains: serviceStateV3VisibleFromSelection, rows: ids, cancel: true}}, context.Canceled},
		{"lost_read", []restoreClearStep{{contains: serviceStateV3VisibleFromSelection, err: failure}}, failure},
		{"lost_write_no_retry", []restoreClearStep{
			{contains: serviceStateV3VisibleFromSelection, rows: ids},
			{contains: serviceStateV3VisibleFromWrite, ids: ids, err: failure},
		}, failure},
		{"committed_prefix_then_cancel", []restoreClearStep{
			{contains: serviceStateV3VisibleFromSelection, rows: ids},
			{contains: serviceStateV3VisibleFromWrite, ids: ids, cancel: true},
		}, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, conn := restoreClearScript(t, test.steps, cancel)
			if err := s.backfillServiceStateV3VisibleFrom(ctx); !errors.Is(err, test.want) || conn.calls != len(test.steps) {
				t.Fatalf("backfill calls=%d error=%v; want %d/%v", conn.calls, err, len(test.steps), test.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (*Surreal)(nil).backfillServiceStateV3VisibleFrom(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled backfill reached SDK: %v", err)
	}
}

func TestServiceStateV3MigrationBackfillCensus(t *testing.T) {
	overflow := make([]models.RecordID, 513)
	for index := range overflow {
		overflow[index] = models.NewRecordID("service_state_v3_current", uint64(index))
	}
	for _, test := range []struct {
		name    string
		results any
	}{
		{"nil", nil},
		{"absent", []any{}},
		{"duplicate", []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}, {Status: "OK", Result: []any{}}}},
		{"unknown_status", []surrealdb.QueryResult[any]{{Status: "unknown", Result: []any{}}}},
		{"native_error", []surrealdb.QueryResult[any]{{Status: "ERR", Result: "failed"}}},
		{"null", []surrealdb.QueryResult[any]{{Status: "OK"}}},
		{"overflow", []surrealdb.QueryResult[any]{{Status: "OK", Result: overflow}}},
		{"other_table", []surrealdb.QueryResult[any]{{Status: "OK", Result: []models.RecordID{models.NewRecordID("repo", "a")}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, script := restoreClearScript(t, nil, nil)
			raw := restoreClearRaw(t, test.results)
			conn := &restoreStateV3TestConnection{Connection: script, response: &raw}
			db, err := surrealdb.FromConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(context.Background()) })
			if err := (&Surreal{db: db}).backfillServiceStateV3VisibleFrom(t.Context()); err == nil || len(conn.writes) != 0 {
				t.Fatalf("malformed census reached write: %v, %v", conn.writes, err)
			}
		})
	}
}

func TestServiceStateV3MigrationSnapshotOrder(t *testing.T) {
	for _, outcome := range []string{"success", "current", "latch_failure", "optional_failure", "backfill_failure", "definition_failure", "marker_failure", "format_refusal"} {
		t.Run(outcome, func(t *testing.T) {
			steps := []restoreClearStep{
				{contains: "SELECT version FROM $rid", rows: []any{}},
				{contains: "UPDATE $rid SET version = IF"},
				{contains: "BEGIN;\n" + serviceStateV3SnapshotOptionalSchema + "\nCOMMIT;"},
				{contains: serviceStateV3VisibleFromSelection, rows: []models.RecordID{}},
				{contains: "BEGIN;\n" + serviceStateV3SnapshotSchema + "\nCOMMIT;"},
				{contains: "UPSERT $rid SET version = IF"},
			}
			definition := serviceStateV3SnapshotSchema
			failure := errors.New("native migration failure")
			want := error(nil)
			switch outcome {
			case "current":
				steps[0].rows = []map[string]any{{"version": serviceStateV3SnapshotSchemaMigrationVersion}}
				steps = steps[:2] // The existing compatibility latch still runs.
			case "latch_failure", "optional_failure", "backfill_failure", "definition_failure", "marker_failure":
				index := slices.Index([]string{"", "latch_failure", "optional_failure", "backfill_failure", "definition_failure", "marker_failure"}, outcome)
				steps[index].err = failure
				steps, want = steps[:index+1], failure
			case "format_refusal":
				definition = "THROW 'not a definition recipe';"
				steps = steps[:4]
			}
			s, conn := restoreClearScript(t, steps, nil)
			err := s.migrateServiceStateV3SnapshotSchemaWithDefinition(t.Context(), definition)
			if conn.calls != len(steps) || (outcome == "format_refusal" && err == nil) ||
				(outcome != "format_refusal" && !errors.Is(err, want)) {
				t.Fatalf("snapshot calls=%d error=%v; want %d/%v", conn.calls, err, len(steps), want)
			}
		})
	}
}

func TestServiceStateV3MigrationBackfillNative(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	s, err := OpenLocalMemory(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := s.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	observed, conn := restoreClearNativeObserved(ctx, t, dataDir)
	query := func(sql string, vars map[string]any) {
		t.Helper()
		if _, err := surrealdb.Query[any](ctx, s.db, sql, vars); err != nil {
			t.Fatal(err)
		}
	}
	query(serviceStateV3SnapshotOptionalSchema, nil)
	_, changes, _ := serviceStateV3HeadroomFixture(t, serviceStateV3Reconcile, 1)
	ids := make([]models.RecordID, 514)
	rows := make([]map[string]any, len(ids))
	for index := range rows {
		ids[index] = models.NewRecordID("service_state_v3_current", fmt.Sprintf("legacy-%04d", index))
		rows[index] = serviceStateContent(changes[0].update.State)
		rows[index]["service_key"] = fmt.Sprintf("legacy-%04d", index)
	}
	ids[0] = models.NewRecordID("service_state_v3_current", uint64(17))
	ids[1] = models.NewRecordID("service_state_v3_current", []any{"legacy", uint64(1)})
	for index := range rows {
		rows[index]["id"] = ids[index]
	}
	rows[513]["visible_from"] = uint64(7)
	for offset := 0; offset < len(rows); offset += 512 {
		query("FOR $row IN $rows { CREATE $row.id CONTENT $row; };", map[string]any{"rows": rows[offset:min(offset+512, len(rows))]})
	}
	failure := errors.New("lost first committed backfill reply")
	conn.after = func() error { return failure }
	if err := observed.backfillServiceStateV3VisibleFrom(ctx); !errors.Is(err, failure) || !slices.Equal(conn.writes, []int{512}) {
		t.Fatalf("first prefix: %v, writes=%v", err, conn.writes)
	}
	if err := observed.backfillServiceStateV3VisibleFrom(ctx); err != nil || !slices.Equal(conn.writes, []int{512, 1}) {
		t.Fatalf("resume: %v, writes=%v", err, conn.writes)
	}
	query("FOR $rid IN $ids { UPDATE $rid UNSET visible_from; };", map[string]any{"ids": ids[:2]})
	conn.before = func(ctx context.Context, _ map[string]any) error {
		_, err := surrealdb.Query[any](ctx, s.db, "UPDATE $rid SET visible_from = 9", map[string]any{"rid": ids[0]})
		return err
	}
	if err := observed.backfillServiceStateV3VisibleFrom(ctx); err == nil || !strings.Contains(err.Error(), "visible revision page changed") {
		t.Fatalf("changed native page did not refuse: %v", err)
	}
	if err := observed.backfillServiceStateV3VisibleFrom(ctx); err != nil || !slices.Equal(conn.writes, []int{512, 1, 2, 1}) {
		t.Fatalf("retry after refusal: %v, writes=%v", err, conn.writes)
	}
	result, err := surrealdb.Query[[]struct {
		ID      models.RecordID `json:"id"`
		Visible uint64          `json:"visible_from"`
	}](ctx, s.db, "SELECT id, visible_from FROM service_state_v3_current", nil)
	if err != nil || result == nil || len(*result) != 1 || len((*result)[0].Result) != len(ids) {
		t.Fatalf("retained rows: %+v, %v", result, err)
	}
	counts := map[uint64]int{}
	for _, row := range (*result)[0].Result {
		counts[row.Visible]++
	}
	if counts[1] != 512 || counts[7] != 1 || counts[9] != 1 {
		t.Fatalf("backfill replaced an existing value or lost a legacy row: %v", counts)
	}
}
