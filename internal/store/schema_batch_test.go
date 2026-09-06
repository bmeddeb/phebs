package store

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	connectionhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
)

func TestSchemaBatchTrustedRecipes(t *testing.T) {
	for _, test := range []struct {
		name        string
		definitions string
		count       int
	}{
		{"base", schema, 488},
		{"API pre-migration", apiKeyCapabilityPreMigrationSchema, 1},
		{"API capability", apiKeyCapabilitySchema, 2},
		{"evidence pre-migration", evidencePreMigrationSchema, 2},
		{"evidence index", evidenceIndexes, 4},
		{"catalog v3 preflight", serviceCatalogV3PreflightSchema, 4},
		{"catalog v3", serviceCatalogV3Schema, 27},
		{"catalog v3 lifecycle preflight", serviceCatalogV3LifecyclePreflightSchema, 3},
		{"catalog v3 lifecycle", serviceCatalogV3LifecycleSchema, 28},
		{"runtime selector", serviceRuntimeSelectorSchema, 16},
	} {
		t.Run(test.name, func(t *testing.T) {
			count, err := schemaBatchDefinitionCount(test.definitions)
			if err != nil || count != test.count {
				t.Fatalf("trusted source format/count = %d, %v", count, err)
			}
			t.Logf("definitions=%d bytes=%d wrapped_bytes=%d", count, len(test.definitions), len(test.definitions)+15)
			failure := errors.New("test transport failure")
			conn := newSchemaBatchTestConnection(failure)
			db, err := surrealdb.FromConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			s := &Surreal{db: db}
			err = s.applySchemaBatch(t.Context(), test.definitions, test.name+" ")
			if !errors.Is(err, failure) || len(conn.queries) != 1 ||
				conn.queries[0] != "BEGIN;\n"+test.definitions+"\nCOMMIT;" {
				t.Fatalf("one unchanged, unretried submission = %v, %v", conn.queries, err)
			}
			if err := db.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSchemaBatchSourceShapeRefusals(t *testing.T) {
	const valid = "DEFINE TABLE IF NOT EXISTS batch_test SCHEMALESS;"
	for _, test := range []struct {
		name, definitions string
	}{
		{"empty", "\n-- comment only\n"},
		{"too many", strings.Repeat(valid+"\n", 513)},
		{"data statement", valid + "\nDELETE batch_test;"},
		{"indented data statement", valid + "\n  DELETE batch_test;"},
		{"inline extra statement", valid + " DELETE batch_test;"},
		{"unclosed", strings.TrimSuffix(valid, ";")},
		{"missing terminator before define", strings.TrimSuffix(valid, ";") + "\n" + valid},
		{"nested begin", "BEGIN;\n" + valid + "\nCOMMIT;"},
		{"unsupported definition", "DEFINE FUNCTION IF NOT EXISTS fn::test() {};"},
		{"unclosed event", "DEFINE EVENT IF NOT EXISTS test ON batch_test\n  THEN {\n    THROW 'test'"},
		{"nested terminator", "DEFINE EVENT IF NOT EXISTS test ON batch_test\n  THEN {\n    THROW 'test';\n  };"},
		{"concurrent index", "DEFINE INDEX IF NOT EXISTS test ON batch_test FIELDS value CONCURRENTLY;"},
		{"deferred index", "DEFINE INDEX IF NOT EXISTS test ON batch_test FIELDS value DEFER;"},
		{"async event", "DEFINE EVENT IF NOT EXISTS test ON batch_test ASYNC THEN {};"},
		{"table view", "DEFINE TABLE IF NOT EXISTS view AS SELECT * FROM batch_test;"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var absent *Surreal
			if err := absent.applySchemaBatch(t.Context(), test.definitions, "test "); err == nil {
				t.Fatal("unsupported source format reached the SDK")
			}
		})
	}
	if count, err := schemaBatchDefinitionCount(strings.Repeat(valid+"\n", 512)); err != nil || count != 512 {
		t.Fatalf("exact definition ceiling = %d, %v", count, err)
	}
}

func TestSchemaBatchCancellationAndFailureStopBeforeMigrations(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var absent *Surreal
	if err := absent.applySchema(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled before first batch = %v", err)
	}
	failure := errors.New("test first schema transaction failure")
	conn := newSchemaBatchTestConnection(failure)
	db, err := surrealdb.FromConnection(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openConnected(t.Context(), db, "user", "pass", "ns", "db"); !errors.Is(err, failure) || !strings.Contains(err.Error(), "apply schema:") {
		t.Fatalf("failed initial transaction = %v", err)
	}
	if !conn.closed || len(conn.queries) != 1 || conn.queries[0] != "BEGIN;\n"+schema+"\nCOMMIT;" {
		t.Fatalf("failed Open retried or reached migrations: closed=%v queries=%d", conn.closed, len(conn.queries))
	}
}

type schemaBatchTestConnection struct {
	*failingOpenConnection
	queries []string
	failure error
}

func newSchemaBatchTestConnection(failure error) *schemaBatchTestConnection {
	return &schemaBatchTestConnection{
		failingOpenConnection: &failingOpenConnection{
			Connection: connectionhttp.New(&connection.Config{}),
		},
		failure: failure,
	}
}

func (conn *schemaBatchTestConnection) Send(
	_ context.Context, method string, params ...any,
) (*connection.RPCResponse[cbor.RawMessage], error) {
	if method != "query" || len(params) != 2 {
		return nil, errors.New("unexpected schema test RPC")
	}
	query, ok := params[0].(string)
	if !ok {
		return nil, errors.New("unexpected schema test query type")
	}
	conn.queries = append(conn.queries, query)
	return nil, conn.failure
}

func TestSchemaBatchNativeAtomicityAndSelfHealing(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	directory := t.TempDir()
	s, endpoint, closeStore := schemaBatchNativeStore(ctx, t, directory)
	t.Cleanup(func() { closeStore() })
	// The direct SDK check establishes the pinned server's complete result
	// shape; the pure transport test binds applySchemaBatch to these exact bytes.
	results, err := surrealdb.Query[any](ctx, s.db, "BEGIN;\n"+schema+"\nCOMMIT;", nil)
	if err != nil || results == nil || len(*results) != 490 {
		t.Fatalf("fresh complete schema batch: results=%v err=%v", results, err)
	}
	for index, result := range *results {
		if result.Status != "OK" || result.Error != nil {
			t.Fatalf("fresh schema statement %d failed: %+v", index, result)
		}
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("ordinary five-batch schema and migrations: %v", err)
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("ordinary schema reapply: %v", err)
	}
	pristine := schemaBatchNativeMetadata(ctx, t, s)
	if err := s.UpsertRepo(ctx, Repo{
		Name: "example.com/acme/schema-batch", CloneURL: "https://example.com/acme/schema-batch.git",
	}); err != nil {
		t.Fatal(err)
	}
	// Make an early missing index, a weakened overwrite field, and a missing
	// guard visible before a deterministic late failure. Another immutable
	// writer guard stays installed and must survive both rollback paths.
	schemaBatchNativeQuery(ctx, t, s, `
REMOVE INDEX repo_name ON TABLE repo;
DEFINE FIELD OVERWRITE kind ON evidence_pin TYPE any;
REMOVE EVENT caller_leaf_outcome_writer_v1 ON TABLE caller_leaf_outcome;
REMOVE INDEX investigation_watch_revision_identity ON TABLE investigation_watch_revision;
CREATE investigation_watch_revision:one CONTENT { watch_id: 'duplicate', seq: 1 };
CREATE investigation_watch_revision:two CONTENT { watch_id: 'duplicate', seq: 1 };
DELETE $marker;`, map[string]any{"marker": candidateControlRevisionMigrationID()})
	beforeFailure := schemaBatchNativeMetadata(ctx, t, s)
	assertUnchanged := func(current *Surreal) {
		t.Helper()
		if got := schemaBatchNativeMetadata(ctx, t, current); got != beforeFailure {
			t.Fatal("failed batch leaked schema/index/event changes")
		}
		rows, err := surrealdb.Query[[]map[string]any](ctx, current.db,
			"SELECT * FROM $marker", map[string]any{"marker": candidateControlRevisionMigrationID()})
		if err != nil || len(firstDomainRows(rows)) != 0 {
			t.Fatalf("failed first batch reached a later migration: %v", err)
		}
		if repo, err := current.GetRepo(ctx, "example.com/acme/schema-batch"); err != nil ||
			repo.CloneURL != "https://example.com/acme/schema-batch.git" {
			t.Fatalf("schema batch changed populated data: %+v, %v", repo, err)
		}
	}
	// This is an actual production batch failure, not a production callback or
	// test-only bypass of index validation. Its late unique index cannot build.
	if err := s.applySchema(ctx); err == nil {
		t.Fatal("late duplicate-data index build unexpectedly succeeded")
	}
	assertUnchanged(s)
	fresh := schemaBatchNativeConnection(ctx, t, endpoint)
	assertUnchanged(fresh)
	if err := fresh.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Remove only the fixture's duplicate so every definition can succeed,
	// then inject a server-side THROW after the complete body. This raw SQL is
	// deliberately test-only: the production trusted-source guard refuses it.
	schemaBatchNativeQuery(ctx, t, s, "DELETE investigation_watch_revision:two;", nil)
	if _, err := surrealdb.Query[any](ctx, s.db,
		"BEGIN;\n"+schema+"\nTHROW 'schema-batch-rollback-test';\nCOMMIT;", nil,
	); err == nil || !strings.Contains(err.Error(), "schema-batch-rollback-test") {
		t.Fatalf("deterministic server rollback = %v", err)
	}
	assertUnchanged(s)
	fresh = schemaBatchNativeConnection(ctx, t, endpoint)
	assertUnchanged(fresh)
	if err := fresh.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	closeStore()
	s, _, closeStore = schemaBatchNativeStore(ctx, t, directory)
	assertUnchanged(s)
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("self-heal populated store after rollback/reopen: %v", err)
	}
	if got := schemaBatchNativeMetadata(ctx, t, s); got != pristine {
		t.Fatal("ordinary reapply did not restore exact index/field/event metadata")
	}
	if repo, err := s.GetRepo(ctx, "example.com/acme/schema-batch"); err != nil ||
		repo.CloneURL != "https://example.com/acme/schema-batch.git" {
		t.Fatalf("self-healing changed existing data: %+v, %v", repo, err)
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("post-repair idempotent reapply: %v", err)
	}
}

func schemaBatchNativeStore(ctx context.Context, t *testing.T, directory string) (*Surreal, string, func()) {
	t.Helper()
	runtime, stop, err := startLocal(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup is installed before connecting, so an authentication assertion
	// cannot strand the supervised child. Each returned owner closes once.
	var s *Surreal
	closed := false
	closeStore := func() {
		if closed {
			return
		}
		closed = true
		if s != nil {
			_ = s.Close(context.Background())
		}
		stop()
	}
	t.Cleanup(closeStore)
	s = schemaBatchNativeConnection(ctx, t, runtime.Endpoint)
	return s, runtime.Endpoint, closeStore
}

func schemaBatchNativeConnection(ctx context.Context, t *testing.T, endpoint string) *Surreal {
	t.Helper()
	db, err := surrealdb.FromEndpointURLString(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal(err)
	}
	return &Surreal{db: db}
}

func schemaBatchNativeQuery(ctx context.Context, t *testing.T, s *Surreal, query string, variables map[string]any) {
	t.Helper()
	if _, err := surrealdb.Query[any](ctx, s.db, query, variables); err != nil {
		t.Fatal(err)
	}
}

func schemaBatchNativeMetadata(ctx context.Context, t *testing.T, s *Surreal) string {
	t.Helper()
	results, err := surrealdb.Query[any](ctx, s.db, `
INFO FOR TABLE repo;
INFO FOR TABLE evidence_pin;
INFO FOR TABLE caller_leaf_outcome;
INFO FOR TABLE caller_generation_publication;
INFO FOR TABLE investigation_watch_revision;`, nil)
	if err != nil || results == nil || len(*results) != 5 {
		t.Fatalf("schema metadata result: %v", err)
	}
	values := make([]any, 0, len(*results))
	for _, result := range *results {
		values = append(values, result.Result)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
