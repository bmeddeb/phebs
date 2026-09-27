//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestActiveJobMigrationAccounting(t *testing.T) {
	for _, mode := range []string{"empty", "marked", "lost_indexes", "marker_error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			calls, writes := 0, uint64(0)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				sql, ok := request.Params[0].(string)
				if !ok {
					return nil, errors.New("missing source SQL")
				}
				var result any = []any{}
				switch {
				case sql == "SELECT version FROM $marker LIMIT 1":
					if mode == "marked" || writes == 2 {
						result = []map[string]any{{"version": jobActiveMigrationVersion}}
					}
				case strings.HasPrefix(sql, "INFO FOR TABLE "):
					table := strings.TrimPrefix(sql, "INFO FOR TABLE ")
					result = map[string]any{"indexes": map[string]any{table + "_pending_key": "present"}}
				case strings.HasPrefix(sql, "SELECT id, target, status, attempts, created_at, lease_token"):
				case sql == "BEGIN;\n"+pendingJobIndexes+"\nCOMMIT;":
					writes++
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != 8 || prefix.MaximumRows != 8 {
						return nil, errors.New("eight actual definitions forwarded without their ACK")
					}
					if mode == "lost_indexes" {
						return nil, context.DeadlineExceeded
					}
				case strings.Contains(sql, "unsupported active-job migration generation"):
					writes++
					var vars map[string]any
					raw, ok := request.Params[1].(cbor.RawMessage)
					if !ok || native.codec.Unmarshal(raw, &vars) != nil || vars["marker"] != jobActiveMigrationID() {
						return nil, errors.New("missing actual marker operand")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 2 || prefix.Rows != 9 {
						return nil, errors.New("marker forwarded without its ACK")
					}
					if mode == "marker_error" {
						return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "neutral marker refusal"}}, nil
					}
				default:
					return nil, errors.New("unexpected source recipe or retry")
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
			}
			err := s.migrateLegacyJobs(ctx)
			wantCalls, wantTX, wantRows := 20, uint64(2), uint64(9)
			switch mode {
			case "marked":
				wantCalls, wantTX, wantRows = 1, 0, 0
			case "lost_indexes":
				wantCalls, wantTX, wantRows = 18, 1, 8
			case "marker_error":
				wantCalls = 19
			}
			prefix, _ := controller.Snapshot()
			if (err == nil) != (mode == "empty" || mode == "marked") || calls != wantCalls ||
				prefix.Transactions != wantTX || prefix.Rows != wantRows {
				t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
		})
	}
}

func TestStartupMigrationAccounting(t *testing.T) {
	for _, test := range []struct {
		name, table, version, statement string
		marker                          models.RecordID
		migrate                         func(*Surreal, context.Context) error
	}{
		{"candidate", "candidate_manifest_publication", candidateControlRevisionMigrationVersion, candidateControlEmptyMigration, candidateControlRevisionMigrationID(), (*Surreal).migrateCandidateControlRevisions},
		{"capability", "api_key", apiKeyCapabilityMigrationVersion, apiKeyCapabilityEmptyMigration, apiKeyCapabilityMigrationStateID(), (*Surreal).migrateAPIKeyCapabilities},
	} {
		for _, mode := range []string{"marked", "empty", "positive", "null_probe", "wrong_table", "lost_write", "changed_probe", "future_marker"} {
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				calls := 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					calls++
					var result any
					switch calls {
					case 1:
						result = []map[string]any{}
						switch mode {
						case "marked":
							result = []map[string]any{{"version": test.version}}
						case "future_marker":
							result = []map[string]any{{"version": "future"}}
						}
					case 2:
						result = []models.RecordID{}
						switch mode {
						case "positive":
							result = []models.RecordID{models.NewRecordID(test.table, uint64(7))}
						case "null_probe":
							result = nil
						case "wrong_table":
							result = []models.RecordID{models.NewRecordID("wrong", "id")}
						}
					case 3:
						if request.Method != "query" || len(request.Params) != 2 || request.Params[0] != test.statement {
							return nil, errors.New("empty migration changed its source recipe")
						}
						var vars map[string]any
						raw, ok := request.Params[1].(cbor.RawMessage)
						if !ok || native.codec.Unmarshal(raw, &vars) != nil || vars["marker"] != test.marker {
							return nil, errors.New("empty migration lost its exact marker operand")
						}
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != 1 || prefix.Rows != 1 || prefix.MaximumRows != 1 {
							return nil, errors.New("marker forwarded before parent submission ACK")
						}
						if mode == "lost_write" {
							return nil, context.DeadlineExceeded
						}
						if mode == "changed_probe" {
							return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "phebs-conflict: migration is no longer empty"}}, nil
						}
					case 4:
						result = []map[string]any{{"version": test.version}}
					default:
						return nil, errors.New("migration automatically retried")
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
				}
				err := test.migrate(s, ctx)
				wantCalls, wantWrites := 2, uint64(0)
				switch mode {
				case "marked", "future_marker":
					wantCalls = 1
				case "empty":
					wantCalls, wantWrites = 4, 1
				case "lost_write", "changed_probe":
					wantCalls, wantWrites = 3, 1
				}
				if (err == nil) != (mode == "marked" || mode == "empty") || calls != wantCalls {
					t.Fatalf("calls=%d error=%v; want calls=%d", calls, err, wantCalls)
				}
				if mode == "positive" && !errors.Is(err, storeaccounting.ErrDescriptor) {
					t.Fatalf("legacy targets were guessed: %v", err)
				}
				prefix, _ := controller.Snapshot()
				if prefix.Transactions != wantWrites || prefix.Rows != wantWrites || prefix.Complete {
					t.Fatalf("migration prefix=%+v", prefix)
				}
			})
		}
	}
}

func TestStartupEmptyMigrationNativeFence(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenLocalMemory(ctx, t.TempDir())
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
	for _, test := range []struct {
		name, table, predicate, prepare, insert, statement string
		marker                                             models.RecordID
		vars                                               map[string]any
		migrate                                            func(*Surreal, context.Context) error
	}{
		{"candidate", "candidate_manifest_publication", "control_revision = NONE OR control_revision < 1",
			"DEFINE FIELD OVERWRITE control_revision ON candidate_manifest_publication TYPE option<int>;",
			`CREATE candidate_manifest_publication:legacy SET repository = 'neutral', head_commit = 'neutral', unit_digest = '', policy_digest = 'neutral', manifest_digest = 'neutral', generation_digest = 'neutral', manifest_path = 'neutral', published_at = time::now();`,
			candidateControlEmptyMigration, candidateControlRevisionMigrationID(), map[string]any{"wanted": candidateControlRevisionMigrationVersion}, (*Surreal).migrateCandidateControlRevisions},
		{"capability", "api_key", "capabilities = NONE",
			"DEFINE FIELD OVERWRITE capabilities ON api_key TYPE option<array<string>>;",
			`CREATE api_key:legacy SET user_id = '', name = 'Neutral', prefix = 'neutral', hash = 'neutral', created_at = time::now();`,
			apiKeyCapabilityEmptyMigration, apiKeyCapabilityMigrationStateID(), map[string]any{"version": apiKeyCapabilityMigrationVersion}, (*Surreal).migrateAPIKeyCapabilities},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := func(sql string, vars map[string]any) {
				t.Helper()
				if _, err := surrealdb.Query[any](ctx, s.db, sql, vars); err != nil {
					t.Fatal(err)
				}
			}
			test.vars["marker"] = test.marker
			query("DELETE $marker;"+test.prepare, test.vars)
			if empty, err := s.migrationTableEmpty(ctx, test.table, test.predicate); err != nil || !empty {
				t.Fatalf("preflight empty=%t error=%v", empty, err)
			}
			query(test.insert, nil)
			if _, err := surrealdb.Query[any](ctx, s.db, test.statement, test.vars); err == nil || !strings.Contains(err.Error(), "no longer empty") {
				t.Fatalf("changed native empty probe did not refuse: %v", err)
			}
			marker, err := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM $marker", test.vars)
			if err != nil || marker == nil || len(*marker) != 1 || len((*marker)[0].Result) != 0 {
				t.Fatalf("refused transaction committed marker: %+v %v", marker, err)
			}
			// Ordinary legacy behavior is retained; selected mode refuses it.
			if err := test.migrate(s, ctx); err != nil {
				t.Fatal(err)
			}
			if empty, err := s.migrationTableEmpty(ctx, test.table, test.predicate); err != nil || !empty {
				t.Fatalf("legacy migration did not repair rows: empty=%t error=%v", empty, err)
			}
		})
	}
}
