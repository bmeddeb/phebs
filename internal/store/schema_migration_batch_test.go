package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	connectionhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

type migrationBatchStep struct {
	query   string
	prefix  bool
	results []surrealdb.QueryResult[any]
}

type migrationBatchConnection struct {
	*failingOpenConnection
	steps       []migrationBatchStep
	calls       int
	failAt      int
	failure     error
	cancelAfter int
	cancel      context.CancelFunc
	marker      models.RecordID
}

func (c *migrationBatchConnection) Send(ctx context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method != "query" || len(params) != 2 || c.calls >= len(c.steps) {
		return nil, errors.New("unexpected migration submission")
	}
	query, ok := params[0].(string)
	step := c.steps[c.calls]
	if !ok || (!step.prefix && query != step.query) || (step.prefix && !strings.HasPrefix(query, step.query)) {
		return nil, errors.New("migration statement order or bytes changed")
	}
	if strings.Contains(query, "FROM $rid") {
		vars, ok := params[1].(map[string]any)
		if !ok || !reflect.DeepEqual(vars["rid"], c.marker) {
			return nil, errors.New("migration marker identity changed")
		}
	}
	index := c.calls
	c.calls++
	if index == c.failAt {
		return nil, c.failure
	}
	encoded, err := surrealcbor.New().Marshal(step.results)
	if err != nil {
		return nil, err
	}
	if c.calls == c.cancelAfter {
		c.cancel()
	}
	raw := cbor.RawMessage(encoded)
	return &connection.RPCResponse[cbor.RawMessage]{Result: &raw}, nil
}

func TestSchemaMigrationBatchOrderAndFailure(t *testing.T) {
	for _, migration := range []struct {
		name, preflight, definitions, version string
		marker                                models.RecordID
		migrate                               func(*Surreal, context.Context) error
		verify                                bool
	}{
		{"catalog", "BEGIN;\n" + serviceCatalogV3PreflightSchema + "\nCOMMIT;", serviceCatalogV3Schema,
			serviceCatalogV3SchemaMigrationVersion, serviceCatalogV3SchemaMigrationID(), (*Surreal).migrateServiceCatalogV3Schema, true},
		{"lifecycle", "BEGIN;\n" + serviceCatalogV3LifecyclePreflightSchema + "\nCOMMIT;", serviceCatalogV3LifecycleSchema,
			serviceCatalogV3LifecycleSchemaMigrationVersion, serviceCatalogV3LifecycleSchemaMigrationID(), (*Surreal).migrateServiceCatalogV3LifecycleSchema, true},
		{"selector", "\nDEFINE TABLE IF NOT EXISTS service_runtime_selector SCHEMALESS;", serviceRuntimeSelectorSchema,
			serviceRuntimeSelectorSchemaMigrationVersion, serviceRuntimeSelectorSchemaMigrationID(), (*Surreal).migrateServiceRuntimeSelectorSchema, false},
	} {
		t.Run(migration.name, func(t *testing.T) {
			for _, outcome := range []string{"success", "existing_marker", "unknown_marker", "duplicate_marker", "unowned_rows", "preflight_transport", "preflight_native", "schema_transport", "commit_native", "marker_transport", "cancel_before", "cancel_after_probe"} {
				t.Run(outcome, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					okRows := func(rows any) []surrealdb.QueryResult[any] {
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}
					}
					markerRows := []map[string]any{{"version": migration.version}}
					definitions, err := schemaBatchDefinitionCount(migration.definitions)
					if err != nil {
						t.Fatal(err)
					}
					schemaResults := make([]surrealdb.QueryResult[any], definitions+2)
					for i := range schemaResults {
						schemaResults[i].Status = "OK"
					}
					steps := []migrationBatchStep{
						{query: "SELECT version FROM $rid", results: okRows([]any{})},
						{query: migration.preflight, results: okRows(nil)},
						{query: "RETURN [{ count:", prefix: true, results: okRows([]map[string]any{{"count": 0}})},
						{query: "BEGIN;\n" + migration.definitions + "\nCOMMIT;", results: schemaResults},
						{query: "\nBEGIN;\nLET $current = (SELECT version FROM $rid LIMIT 1)[0].version;", prefix: true, results: okRows(nil)},
					}
					if migration.verify {
						steps = append(steps, migrationBatchStep{query: "SELECT version FROM $rid", results: okRows(markerRows)})
					}
					transportFailure := errors.New("migration transport unavailable")
					conn := &migrationBatchConnection{
						failingOpenConnection: &failingOpenConnection{Connection: connectionhttp.New(&connection.Config{Unmarshaler: surrealcbor.New()})},
						steps:                 steps, failAt: -1, failure: transportFailure, cancel: cancel, marker: migration.marker,
					}
					wantCalls, wantError := len(steps), ""
					switch outcome {
					case "existing_marker", "unknown_marker", "duplicate_marker":
						switch outcome {
						case "unknown_marker":
							markerRows[0]["version"] = "unsupported"
							wantError = "unsupported marker"
						case "duplicate_marker":
							markerRows = append(markerRows, markerRows[0])
							wantError = "duplicate marker"
						}
						conn.steps[0].results = okRows(markerRows)
						wantCalls = 1
					case "unowned_rows":
						conn.steps[2].results = okRows([]map[string]any{{"count": 1}})
						wantCalls, wantError = 3, "unowned pre-migration rows"
					case "preflight_transport", "schema_transport", "marker_transport":
						conn.failAt = map[string]int{"preflight_transport": 1, "schema_transport": 3, "marker_transport": 4}[outcome]
						wantCalls, wantError = conn.failAt+1, "migration transport unavailable"
					case "preflight_native":
						conn.steps[1].results = []surrealdb.QueryResult[any]{{Status: "ERR", Result: "native preflight refusal"}}
						wantCalls, wantError = 2, "native preflight refusal"
					case "commit_native":
						conn.steps[3].results[definitions+1] = surrealdb.QueryResult[any]{Status: "ERR", Result: "native commit refusal"}
						wantCalls, wantError = 4, "native commit refusal"
					case "cancel_before":
						cancel()
						wantCalls, wantError = 0, "context canceled"
					case "cancel_after_probe":
						conn.cancelAfter = 3
						wantCalls, wantError = 3, "context canceled"
					}
					db, err := surrealdb.FromConnection(t.Context(), conn)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := db.Close(t.Context()); err != nil {
							t.Error(err)
						}
					})
					err = migration.migrate(&Surreal{db: db}, ctx)
					if conn.calls != wantCalls || (wantError == "" && err != nil) ||
						(wantError != "" && (err == nil || !strings.Contains(err.Error(), wantError))) {
						t.Fatalf("calls=%d want=%d error=%v want=%q", conn.calls, wantCalls, err, wantError)
					}
					if conn.failAt >= 0 && !errors.Is(err, transportFailure) {
						t.Fatalf("lost transport error: %v", err)
					}
				})
			}
		})
	}
}
