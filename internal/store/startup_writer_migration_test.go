//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

type startupWriterCase struct {
	name, version, prior, statement string
	marker                          models.RecordID
	migrate                         func(*Surreal, context.Context) error
}

func startupWriterCases() []startupWriterCase {
	return []startupWriterCase{
		{"resolver", resolverCatalogWriterMigrationVersion, resolverCatalogPriorMigration, resolverCatalogEmptyMigration, resolverCatalogMigrationID(), (*Surreal).migrateResolverCatalogWriter},
		{"leaf", callerLeafWriterMigrationVersion, "", callerLeafEmptyMigration, callerLeafMigrationID(), (*Surreal).migrateCallerLeafWriter},
		{"publication", callerGenerationPublicationMigrationVersion, callerGenerationPublicationPriorMigration, callerPublicationEmptyMigration, callerGenerationPublicationMigrationID(), (*Surreal).migrateCallerGenerationPublications},
	}
}

func startupWriterTestMarker(marker models.RecordID, version string) []map[string]any {
	if version == "" {
		return []map[string]any{}
	}
	return []map[string]any{{"id": marker, "version": version, "missing": false}}
}

func TestStartupWriterMigrationAccounting(t *testing.T) {
	for _, test := range startupWriterCases() {
		for _, mode := range []string{"current", "empty", "prior_empty", "positive", "null_probe", "wrong_table", "future", "lost_write", "changed_preimage", "missing_completion"} {
			if mode == "prior_empty" && test.prior == "" {
				continue
			}
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				calls, probes, writes := 0, 0, 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					calls++
					if request.Method != "query" || len(request.Params) != 2 {
						return nil, errors.New("unexpected migration method")
					}
					sql, ok := request.Params[0].(string)
					if !ok {
						return nil, errors.New("missing migration source")
					}
					var result any
					switch {
					case sql == startupWriterMarkerSQL:
						version := ""
						if mode == "current" || writes == 1 && mode != "missing_completion" {
							version = test.version
						} else if mode == "future" {
							version = "future"
						} else if mode == "prior_empty" {
							version = test.prior
						}
						result = startupWriterTestMarker(test.marker, version)
					case strings.HasPrefix(sql, "SELECT VALUE id FROM "):
						probes++
						result = []models.RecordID{}
						if probes == 1 {
							switch mode {
							case "positive":
								result = []models.RecordID{models.NewRecordID(strings.Fields(sql)[4], uint64(7))}
							case "null_probe":
								result = nil
							case "wrong_table":
								result = []models.RecordID{models.NewRecordID("wrong", "id")}
							}
						}
					case sql == test.statement:
						writes++
						var payload struct {
							Marker models.RecordID `json:"marker"`
							Wanted string          `json:"wanted"`
						}
						raw, ok := request.Params[1].(cbor.RawMessage)
						if !ok || native.codec.Unmarshal(raw, &payload) != nil || payload.Marker != test.marker || payload.Wanted != test.version {
							return nil, errors.New("marker operand was not retained")
						}
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != 1 || prefix.Rows != 1 || prefix.MaximumRows != 1 {
							return nil, errors.New("marker write preceded parent attempt ACK")
						}
						if mode == "lost_write" {
							return nil, context.DeadlineExceeded
						}
						if mode == "changed_preimage" {
							return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "phebs-conflict: changed migration preimage"}}, nil
						}
					default:
						return nil, errors.New("unadmitted legacy migration forwarded")
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
				}
				err := test.migrate(s, ctx)
				ok := mode == "current" || mode == "empty" || mode == "prior_empty"
				if (err == nil) != ok {
					t.Fatalf("migration error=%v", err)
				}
				if mode == "positive" && !errors.Is(err, storeaccounting.ErrDescriptor) {
					t.Fatalf("positive legacy target set was guessed: %v", err)
				}
				wantWrites := uint64(0)
				if mode == "empty" || mode == "prior_empty" || mode == "lost_write" || mode == "changed_preimage" || mode == "missing_completion" {
					wantWrites = 1
				}
				prefix, _ := controller.Snapshot()
				if uint64(writes) != wantWrites || prefix.Transactions != wantWrites || prefix.Rows != wantWrites || prefix.MaximumRows != wantWrites {
					t.Fatalf("calls=%d writes=%d prefix=%+v", calls, writes, prefix)
				}
				if mode == "current" {
					wantCalls := 1
					if test.name == "resolver" {
						wantCalls = 3
					}
					if calls != wantCalls {
						t.Fatalf("current migration calls=%d; want %d", calls, wantCalls)
					}
				}
				if (prefix.Producers[0].Calls == 1) != (mode == "lost_write") {
					t.Fatalf("unknown submission custody=%+v", prefix)
				}
			})
		}
	}
}

func TestStartupWriterMarkerStrictNativeShape(t *testing.T) {
	marker := callerLeafMigrationID()
	for _, mode := range []string{"absent", "none", "current", "null_rows", "missing_witness", "null_version", "false_none", "true_text", "empty_version", "wrong_id", "many", "missing_envelope"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, _ := storeAccountingFixture(t, 1, 1)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Params[0] != startupWriterMarkerSQL {
					return nil, errors.New("marker query source changed")
				}
				row := map[string]any{"id": marker, "version": callerLeafWriterMigrationVersion, "missing": false}
				var result any
				switch mode {
				case "absent":
					result = []map[string]any{}
				case "null_rows":
				case "missing_envelope":
					return []surrealdb.QueryResult[any]{}, nil
				default:
					switch mode {
					case "none", "false_none":
						row["version"] = cbor.RawMessage{0xc6, 0xf6}
						row["missing"] = mode == "none"
					case "missing_witness":
						delete(row, "missing")
					case "null_version":
						row["version"] = nil
					case "true_text":
						row["missing"] = true
					case "empty_version":
						row["version"] = ""
					case "wrong_id":
						row["id"] = models.NewRecordID("store_migration", "other")
					}
					rows := []map[string]any{row}
					if mode == "many" {
						rows = append(rows, row)
					}
					result = rows
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
			}
			version, err := s.startupWriterMarker(ctx, marker)
			ok := mode == "absent" || mode == "none" || mode == "current"
			if (err == nil) != ok || ok && (version == callerLeafWriterMigrationVersion) != (mode == "current") {
				t.Fatalf("marker version=%q error=%v", version, err)
			}
		})
	}
}
