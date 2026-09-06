package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	connectionhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

type restoreClearStep struct {
	contains string
	rows     any
	err      error
	cancel   bool
	ids      []models.RecordID
	repos    []restoreCallerRepo
}

type restoreClearConnection struct {
	*failingOpenConnection
	codec  *surrealcbor.Codec
	steps  []restoreClearStep
	calls  int
	cancel context.CancelFunc
}

func (conn *restoreClearConnection) Send(_ context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	if method != "query" || len(params) != 2 || conn.calls >= len(conn.steps) {
		return nil, errors.New("unexpected restore clear test call")
	}
	step := conn.steps[conn.calls]
	conn.calls++
	statement, ok := params[0].(string)
	vars, varsOK := params[1].(map[string]any)
	if !ok || !varsOK || !strings.Contains(statement, step.contains) {
		return nil, fmt.Errorf("unexpected restore clear test query at %d", conn.calls)
	}
	if step.ids != nil && !reflect.DeepEqual(vars["ids"], step.ids) {
		return nil, errors.New("restore clear did not bind actual selected native IDs")
	}
	if step.repos != nil && !reflect.DeepEqual(vars["repos"], step.repos) {
		return nil, errors.New("restore clear did not bind actual repository preimages")
	}
	if step.cancel {
		conn.cancel()
	}
	if step.err != nil {
		return nil, step.err
	}
	body, err := conn.codec.Marshal([]surrealdb.QueryResult[any]{{Status: "OK", Result: step.rows}})
	if err != nil {
		return nil, err
	}
	raw := cbor.RawMessage(body)
	return &connection.RPCResponse[cbor.RawMessage]{Result: &raw}, nil
}

func restoreClearScript(t *testing.T, steps []restoreClearStep, cancel context.CancelFunc) (*Surreal, *restoreClearConnection) {
	t.Helper()
	codec := surrealcbor.New()
	conn := &restoreClearConnection{
		failingOpenConnection: &failingOpenConnection{Connection: connectionhttp.New(&connection.Config{Unmarshaler: codec})},
		codec:                 codec, steps: steps, cancel: cancel,
	}
	db, err := surrealdb.FromConnection(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return &Surreal{db: db}, conn
}

func TestRestoreClearNativeIDValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		ids  []models.RecordID
		bad  bool
	}{
		{"string", []models.RecordID{models.NewRecordID("repo", "a:b / c")}, false},
		{"number", []models.RecordID{models.NewRecordID("repo", int64(42))}, false},
		{"uuid", []models.RecordID{models.NewRecordID("repo", cbor.Tag{Number: 37, Content: make([]byte, 16)})}, false},
		{"composite", []models.RecordID{models.NewRecordID("repo", []any{int64(1), map[string]any{"k": "v"}})}, false},
		{"object", []models.RecordID{models.NewRecordID("repo", map[string]any{"k": []any{1, "v"}})}, false},
		{"empty", nil, false},
		{"missing", []models.RecordID{models.NewRecordID("repo", nil)}, true},
		{"wrong table", []models.RecordID{models.NewRecordID("other", "x")}, true},
		{"unsupported", []models.RecordID{models.NewRecordID("repo", make(chan int))}, true},
		{"overflow", make([]models.RecordID, restoreClearRows+1), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateRestoreClearIDs(test.ids, "repo", restoreClearRows); (err != nil) != test.bad {
				t.Fatalf("validation error = %v; want bad %t", err, test.bad)
			}
		})
	}
}

func TestRestoreClearPagePrefixAndCancellation(t *testing.T) {
	ids := []models.RecordID{models.NewRecordID("generation_schedule", uint64(7)), models.NewRecordID("generation_schedule", []any{"x", uint64(1)})}
	failed := errors.New("lost clear response")
	for _, test := range []struct {
		name  string
		steps []restoreClearStep
		want  error
	}{
		{"empty", []restoreClearStep{{contains: "SELECT VALUE id", rows: []models.RecordID{}}}, nil},
		{"canceled empty is not success", []restoreClearStep{{contains: "SELECT VALUE id", rows: []models.RecordID{}, cancel: true}}, context.Canceled},
		{"actual vector and final empty", []restoreClearStep{
			{contains: "SELECT VALUE id", rows: ids},
			{contains: "FOR $rid IN $ids", ids: ids},
			{contains: "SELECT VALUE id", rows: []models.RecordID{}},
		}, nil},
		{"cancel selected no write", []restoreClearStep{{contains: "SELECT VALUE id", rows: ids, cancel: true}}, context.Canceled},
		{"lost response stops no retry", []restoreClearStep{
			{contains: "SELECT VALUE id", rows: ids},
			{contains: "FOR $rid IN $ids", ids: ids, err: failed},
		}, failed},
		{"committed prefix then cancel", []restoreClearStep{
			{contains: "SELECT VALUE id", rows: ids},
			{contains: "FOR $rid IN $ids", ids: ids, cancel: true},
		}, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, conn := restoreClearScript(t, test.steps, cancel)
			err := s.clearRestoreTable(ctx, "generation_schedule", "", "", map[string]any{})
			if !errors.Is(err, test.want) || conn.calls != len(test.steps) {
				t.Fatalf("clear = %v, calls %d; want %v, %d", err, conn.calls, test.want, len(test.steps))
			}
		})
	}
}

func TestRestoreClearClosedWriterGuards(t *testing.T) {
	for _, test := range []struct {
		family restoreClearFamily
		marker models.RecordID
	}{
		{restoreClearCaller, callerLeafMigrationID()},
		{restoreClearCandidate, evidenceMigrationStateID()},
		{restoreClearResolver, resolverCatalogMigrationID()},
	} {
		guard, vars, err := restoreClearGuard(test.family)
		if err != nil || !reflect.DeepEqual(vars["migration_rid"], test.marker) ||
			!reflect.DeepEqual(vars["caller_migration_rid"], callerGenerationPublicationMigrationID()) ||
			strings.Count(guard, "SELECT id FROM") != 2 {
			t.Fatalf("family %d guard = %q, %+v, %v", test.family, guard, vars, err)
		}
	}
	if guard, vars, err := restoreClearGuard(restoreClearSchedules); err != nil || guard != "" || len(vars) != 0 {
		t.Fatalf("generation clear gained writer guards: %q, %v, %v", guard, vars, err)
	}
	if _, _, err := restoreClearGuard(0); err == nil {
		t.Fatal("unknown family accepted")
	}
}

func TestRestoreClearCallerExactPairing(t *testing.T) {
	ids := []models.RecordID{models.NewRecordID("caller_generation_publication", "actual"), models.NewRecordID("caller_generation_publication", uint64(2))}
	for index, repos := range [][]restoreCallerRepo{
		{},
		{{ID: models.NewRecordID("repo", "native-id"), Name: restoreClearRaw(t, "actual"), Revision: restoreClearRaw(t, int64(7))}},
		{{ID: models.NewRecordID("repo", "native-id"), Name: restoreClearRaw(t, uint64(2)), Revision: restoreClearRaw(t, 1.5)}},
		{{ID: models.NewRecordID("repo", "native-id"), Name: restoreClearRaw(t, []any{1, "x"}), Revision: restoreClearRaw(t, models.DecimalString("2.25"))}},
		{{ID: models.NewRecordID("repo", "native-id"), Name: restoreClearRaw(t, map[string]any{"x": 1}), Revision: restoreClearRaw(t, models.DecimalString("2.25"))}},
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			steps := []restoreClearStep{
				{contains: "SELECT VALUE id FROM caller_generation_publication", rows: ids},
				{contains: "FROM repo WHERE name IN $keys", rows: repos},
				{contains: "UPDATE $repository.id", ids: ids, repos: repos},
				{contains: "SELECT VALUE id FROM caller_generation_publication", rows: []models.RecordID{}},
			}
			s, conn := restoreClearScript(t, steps, nil)
			guard, vars, err := restoreClearGuard(restoreClearCaller)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.clearRestoreCallerPointers(t.Context(), guard, vars); err != nil || conn.calls != len(steps) {
				t.Fatalf("paired clear = %v; calls %d", err, conn.calls)
			}
		})
	}
}

func restoreClearRaw(t *testing.T, value any) cbor.RawMessage {
	t.Helper()
	raw, err := surrealcbor.New().Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type restoreClearFixtureRepo struct {
	ID       models.RecordID `json:"id"`
	Name     string          `json:"name"`
	Revision int64           `json:"revision"`
}

// A test-only wrapper observes the actual fixed write vectors and can lose a
// reply only after the native SDK call returns. The production clear has no
// callback, injected connection, counter or alternate mutation path.
type restoreClearNativeConnection struct {
	connection.Connection
	writes []int
	before func(context.Context, map[string]any) error
	after  func() error
}

func (conn *restoreClearNativeConnection) Send(ctx context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	write := false
	if method == "query" && len(params) == 2 {
		statement, statementOK := params[0].(string)
		vars, varsOK := params[1].(map[string]any)
		if statementOK && varsOK && strings.HasPrefix(statement, "BEGIN;") {
			ids, ok := vars["ids"].([]models.RecordID)
			if !ok || len(ids) == 0 {
				return nil, errors.New("native clear write lacks exact IDs")
			}
			rows := len(ids)
			if repos, ok := vars["repos"].([]restoreCallerRepo); ok {
				rows += len(repos)
			}
			if rows > restoreClearRows {
				return nil, errors.New("native clear submitted more than512 records")
			}
			conn.writes = append(conn.writes, rows)
			write = true
			if conn.before != nil {
				before := conn.before
				conn.before = nil
				if err := before(ctx, vars); err != nil {
					return nil, err
				}
			}
		}
	}
	response, err := conn.Connection.Send(ctx, method, params...)
	if err == nil && write && conn.after != nil {
		after := conn.after
		conn.after = nil
		if err := after(); err != nil {
			return nil, err
		}
	}
	return response, err
}

func restoreClearNativeObserved(ctx context.Context, t *testing.T, dataDir string) (*Surreal, *restoreClearNativeConnection) {
	t.Helper()
	runtime, err := ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.ParseRequestURI(runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn := &restoreClearNativeConnection{Connection: gorillaws.New(connection.NewConfig(endpoint))}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal(err)
	}
	return &Surreal{db: db}, conn
}

func TestRestoreClearNativePagesAndCallerPairing(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-30*time.Second).Before(deadline) {
		deadline = outer.Add(-30 * time.Second)
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
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	observed, conn := restoreClearNativeObserved(ctx, t, dataDir)
	query := func(statement string, vars map[string]any) {
		t.Helper()
		if _, queryErr := surrealdb.Query[any](ctx, s.db, statement, vars); queryErr != nil {
			t.Fatal(queryErr)
		}
	}
	// Neutral raw fixture tables intentionally omit row schemas: the clear
	// must depend on native IDs, not decode these malformed imported payloads.
	for _, table := range []string{
		"repo", "generation_schedule_current", "generation_schedule_repository",
		"generation_schedule_chunk", "generation_schedule", "service_state_v3_plan",
		"extraction_domain_root", "caller_generation_publication", "caller_leaf_outcome",
		"caller_generation_admission", "candidate_manifest_publication",
		"resolver_catalog_publication", "extraction_domain_outcome",
	} {
		query("REMOVE TABLE "+table+"; DEFINE TABLE "+table+" SCHEMALESS;", nil)
	}
	numbers := make([]int, 520)
	for index := range numbers {
		numbers[index] = index
	}
	for _, family := range []restoreClearFamily{restoreClearSchedules, restoreClearCaller, restoreClearCandidate, restoreClearResolver} {
		if !t.Run(fmt.Sprint(family), func(t *testing.T) {
			query(`FOR $n IN $numbers {
 CREATE type::record('repo', <string>$n) SET name = <string>$n, caller_publication_revision = 0,
 latest_extraction_job = 'stale', latest_resolver_job_created_at = time::now(), latest_caller_job_projection_version = 1;
};`, map[string]any{"numbers": numbers})
			query(`CREATE repo:42 SET name = 'special-number', caller_publication_revision = 0, latest_extraction_job = 'stale';
CREATE repo:[1, 'x'] SET name = 'special-array', caller_publication_revision = 0, latest_extraction_job = 'stale';
CREATE repo:{x: 1} SET name = 'special-object', caller_publication_revision = 0, latest_extraction_job = 'stale';
CREATE repo:u'01901949-f3c3-7000-8000-000000000001' SET name = 'special-uuid', caller_publication_revision = 0, latest_extraction_job = 'stale';`, nil)
			for _, table := range []string{
				"generation_schedule_current", "generation_schedule_repository", "generation_schedule_chunk",
				"generation_schedule", "service_state_v3_plan", "extraction_domain_root",
				"caller_generation_publication", "caller_leaf_outcome", "caller_generation_admission",
				"candidate_manifest_publication", "resolver_catalog_publication",
			} {
				query("FOR $n IN $numbers { CREATE type::record($table, <string>$n) SET malformed = true, repository = 'wrong'; };", map[string]any{"table": table, "numbers": numbers})
			}
			query(`CREATE extraction_domain_outcome:remove SET candidate_control_failure = true,
 store_schema_version = $schema, evidence_migration_version = $version;
CREATE extraction_domain_outcome:keep SET candidate_control_failure = false;
CREATE caller_generation_publication:42 SET malformed = true;
CREATE caller_generation_publication:[1, 'x'] SET malformed = true;
CREATE caller_generation_publication:{x: 1} SET malformed = true;
CREATE caller_generation_publication:u'01901949-f3c3-7000-8000-000000000001' SET malformed = true;`,
				map[string]any{"schema": evidenceStoreSchemaVersion, "version": evidenceMigrationVersion})
			if family == restoreClearCaller {
				// Simulate loss after one real committed pointer/revision page.
				lost := errors.New("lost native committed clear reply")
				conn.after = func() error { return lost }
				if err := observed.ClearAllCallerPublicationStateForRestore(ctx); !errors.Is(err, lost) {
					t.Fatalf("lost reply = %v", err)
				}
			}
			if family == restoreClearResolver {
				// Change one actual repository counter after the native page
				// selection. Its paired transaction must refuse before deleting
				// any selected pointer or incrementing any other counter.
				conn.before = func(callCtx context.Context, vars map[string]any) error {
					repos, ok := vars["repos"].([]restoreCallerRepo)
					if !ok || len(repos) == 0 {
						return errors.New("resolver clear did not select repository witnesses")
					}
					_, err := surrealdb.Query[any](callCtx, s.db,
						"UPDATE $rid SET caller_publication_revision = 7", map[string]any{"rid": repos[0].ID})
					return err
				}
				if err := observed.clearRestoreState(ctx, family); err == nil || !strings.Contains(err.Error(), "restore caller page changed") {
					t.Fatalf("changed repository witness = %v", err)
				}
				rows, queryErr := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM caller_generation_publication", nil)
				if queryErr != nil || len(firstDomainRows(rows)) != 524 {
					t.Fatalf("failed paired transaction deleted pointers: %v", queryErr)
				}
				query("UPDATE repo SET caller_publication_revision = 0", nil)
			}
			if family == restoreClearSchedules {
				// The selected repository page really commits before its owner
				// is canceled; a later explicit call can finish the prefix.
				writeCtx, stop := context.WithCancel(ctx)
				conn.after = func() error { stop(); return nil }
				err := observed.clearRestoreState(writeCtx, family)
				stop()
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("post-commit cancellation = %v", err)
				}
			}
			if err := observed.clearRestoreState(ctx, family); err != nil {
				t.Fatal(err)
			}
			if family != restoreClearSchedules {
				rows, queryErr := surrealdb.Query[[]restoreClearFixtureRepo](ctx, s.db,
					"SELECT id, name, caller_publication_revision AS revision FROM repo ORDER BY id", nil)
				if queryErr != nil || len(firstDomainRows(rows)) != 524 {
					t.Fatalf("caller repo census: %v", queryErr)
				}
				for _, row := range firstDomainRows(rows) {
					want := int64(1)
					if strings.HasPrefix(row.Name, "special-") {
						want = 0
					}
					if row.Revision != want {
						t.Fatalf("caller revision = %d, want %d", row.Revision, want)
					}
				}
				// Re-entry after a complete committed prefix cannot bump again.
				if err := observed.clearRestoreState(ctx, family); err != nil {
					t.Fatal(err)
				}
				after, queryErr := surrealdb.Query[[]restoreClearFixtureRepo](ctx, s.db,
					"SELECT id, name, caller_publication_revision AS revision FROM repo ORDER BY id", nil)
				if queryErr != nil || !reflect.DeepEqual(firstDomainRows(rows), firstDomainRows(after)) {
					t.Fatalf("repeated clear changed repository counters: %v", queryErr)
				}
				_, guardVars, guardErr := restoreClearGuard(family)
				if guardErr != nil {
					t.Fatal(guardErr)
				}
				query("UPDATE $rid SET version = 'future-writer'", map[string]any{"rid": guardVars["migration_rid"]})
				beforeWrites := len(conn.writes)
				if err := observed.clearRestoreState(ctx, family); err == nil || !strings.Contains(err.Error(), "writer generation is not active") || len(conn.writes) != beforeWrites {
					t.Fatalf("empty clear failed to preserve writer fence: %v", err)
				}
				query("UPDATE $rid SET version = $version", map[string]any{"rid": guardVars["migration_rid"], "version": guardVars["migration_version"]})
			}
			empty := map[restoreClearFamily][]string{
				restoreClearSchedules: {"generation_schedule_current", "generation_schedule_repository", "generation_schedule_chunk", "generation_schedule", "service_state_v3_plan", "extraction_domain_root"},
				restoreClearCaller:    {"caller_generation_publication", "caller_leaf_outcome", "caller_generation_admission"},
				restoreClearCandidate: {"caller_generation_publication", "candidate_manifest_publication", "resolver_catalog_publication"},
				restoreClearResolver:  {"caller_generation_publication", "resolver_catalog_publication"},
			}
			for _, table := range empty[family] {
				rows, err := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM "+table+" LIMIT 1", nil)
				if err != nil || len(firstDomainRows(rows)) != 0 {
					t.Fatalf("%s not empty: %v", table, err)
				}
			}
			if family == restoreClearCandidate {
				rows, err := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM extraction_domain_outcome", nil)
				if err != nil || len(firstDomainRows(rows)) != 1 || firstDomainRows(rows)[0].ID != "keep" {
					t.Fatalf("candidate clear changed precious outcome: %v", err)
				}
			}
			for _, table := range []string{
				"repo", "generation_schedule_current", "generation_schedule_repository", "generation_schedule_chunk",
				"generation_schedule", "service_state_v3_plan", "extraction_domain_root", "caller_generation_publication",
				"caller_leaf_outcome", "caller_generation_admission", "candidate_manifest_publication",
				"resolver_catalog_publication", "extraction_domain_outcome",
			} {
				query("DELETE "+table+";", nil)
			}
		}) {
			return
		}
	}
	if len(conn.writes) == 0 {
		t.Fatal("no actual clear transaction was observed")
	}
	t.Run("raw native name and revision witnesses", func(t *testing.T) {
		const seed = `
CREATE repo:number SET name = 42, caller_publication_revision = 1.5f;
CREATE repo:array SET name = [1, 'x'], caller_publication_revision = 2.25dec;
CREATE repo:object SET name = {x: 1}, caller_publication_revision = 3.5f;
CREATE repo:text SET name = 'text', caller_publication_revision = 4.75dec;
CREATE caller_generation_publication:42 SET repository = 'wrong';
CREATE caller_generation_publication:[1, 'x'] SET repository = 'wrong';
CREATE caller_generation_publication:{x: 1} SET repository = 'wrong';
CREATE caller_generation_publication:text SET repository = 'wrong';`
		readRepos := func() []restoreCallerRepo {
			t.Helper()
			rows, err := surrealdb.Query[[]restoreCallerRepo](ctx, s.db,
				"SELECT id, name, caller_publication_revision AS revision FROM repo ORDER BY id", nil)
			if err != nil || len(firstDomainRows(rows)) != 4 {
				t.Fatalf("raw repository census: %v", err)
			}
			return firstDomainRows(rows)
		}
		query(seed, nil)
		before := readRepos()
		// This is the former raw native arithmetic/matching recipe, used as
		// the compatibility oracle only on these four neutral fixture rows.
		query(`BEGIN;
LET $repositories = SELECT VALUE record::id(id) FROM caller_generation_publication;
UPDATE repo SET caller_publication_revision = (caller_publication_revision ?? 0) + 1
 WHERE name IN $repositories RETURN NONE;
DELETE caller_generation_publication RETURN NONE;
COMMIT;`, nil)
		want := readRepos()
		if reflect.DeepEqual(before, want) {
			t.Fatal("old native clear made no arithmetic changes")
		}
		query("DELETE repo;", nil)
		query(seed, nil)
		if err := observed.ClearAllCallerPublicationStateForRestore(ctx); err != nil {
			t.Fatalf("raw witness clear: %v", err)
		}
		if got := readRepos(); !reflect.DeepEqual(got, want) {
			t.Fatal("raw witness clear differs from native prior arithmetic")
		}
		query("UPDATE repo:text SET caller_publication_revision = {bad: true}; CREATE caller_generation_publication:text;", nil)
		beforeInvalid := readRepos()
		if _, err := surrealdb.Query[any](ctx, s.db, `BEGIN;
LET $repositories = SELECT VALUE record::id(id) FROM caller_generation_publication;
UPDATE repo SET caller_publication_revision = (caller_publication_revision ?? 0) + 1
 WHERE name IN $repositories RETURN NONE;
DELETE caller_generation_publication RETURN NONE;
COMMIT;`, nil); err == nil {
			t.Fatal("invalid arithmetic did not refuse the former native recipe")
		}
		beforeWrites := len(conn.writes)
		if err := observed.ClearAllCallerPublicationStateForRestore(ctx); err == nil {
			t.Fatal("invalid native arithmetic accepted")
		}
		if len(conn.writes) != beforeWrites+1 {
			t.Fatal("invalid arithmetic did not reach the bounded native transaction")
		}
		if got := readRepos(); !reflect.DeepEqual(got, beforeInvalid) {
			t.Fatal("invalid arithmetic changed a repository witness")
		}
		pointers, err := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM caller_generation_publication", nil)
		if err != nil || len(firstDomainRows(pointers)) != 1 || firstDomainRows(pointers)[0].ID != "text" {
			t.Fatalf("invalid arithmetic deleted its pointer: %v", err)
		}
	})
}
