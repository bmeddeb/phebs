//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

// The actual public writer and strict reader run through the SDK and real SA01.
// The small reply model retains emitted records; it is not an engine or proof
// of native transaction atomicity. Existing native catalog tests own that gate.
func TestServiceCatalogV3AccountingCandidateColdReuseAndLostReply(t *testing.T) {
	for _, test := range []struct {
		services int
		lost     bool
		refuse   bool
	}{{1, false, false}, {513, false, false}, {1, true, false}, {1, false, true}} {
		t.Run(fmt.Sprintf("services_%d_lost_%t_fence_%t", test.services, test.lost, test.refuse), func(t *testing.T) {
			const repository = "example.com/acme/catalog-accounting"
			commit := strings.Repeat("7", 40)
			services := make([]servicecatalog.Service, test.services)
			for index := range services {
				services[index] = servicecatalog.Service{
					Key: fmt.Sprintf("service-%04d", index), DisplayName: "Neutral service",
					Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
				}
			}
			generation := serviceStateV3Generation(t, repository, commit, "v1", services)
			members := len(generation.Members)
			if members == 0 || members > servicecatalogv3.MaxMembers {
				t.Fatal("fixture did not use the validated member bound")
			}
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			records := make(map[string]map[string]any)
			var transactions, rows, activeRows, maximum uint64
			var uuid models.UUID
			active, commits, cancels := false, 0, 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (result any, callErr error) {
				defer func() {
					if callErr != nil && !test.lost {
						t.Logf("native %s refused: %v", request.Method, callErr)
					}
				}()
				switch request.Method {
				case "begin":
					if active {
						return nil, errors.New("catalog began a second transaction")
					}
					transactions++
					uuid.UUID[0]++
					active, activeRows, result = true, 0, uuid
				case "commit", "cancel":
					if len(request.Params) != 1 {
						return nil, errors.New("catalog terminal lost its UUID parameter")
					}
					id, ok := request.Params[0].(*models.UUID)
					if !active || !ok || id == nil || *id != uuid {
						return nil, errors.New("catalog terminal lost its native UUID")
					}
					if request.Method == "commit" {
						commits++
					} else {
						cancels++
					}
					active = false
				case "query":
					if active {
						id, ok := request.Txn.(*models.UUID)
						if !ok || id == nil || *id != uuid {
							return nil, errors.New("catalog query escaped its native transaction")
						}
					} else if request.Txn != nil {
						return nil, errors.New("strict reader borrowed a completed transaction")
					}
					if len(request.Params) != 2 {
						return nil, errors.New("catalog query lost payload")
					}
					sql, sqlOK := request.Params[0].(string)
					raw, rawOK := request.Params[1].(cbor.RawMessage)
					var vars map[string]any
					if !sqlOK || !rawOK || native.codec.Unmarshal(raw, &vars) != nil {
						return nil, errors.New("catalog payload is not the selected CBOR snapshot")
					}
					rid, ok := vars["rid"].(models.RecordID)
					if !ok {
						return nil, errors.New("catalog query lost its actual record operand")
					}
					result = []map[string]any{}
					switch {
					case strings.HasPrefix(sql, "SELECT "):
						if rid.Table == "repo" {
							result = []map[string]any{{"indexed_commit_hash": commit, "deleting": test.refuse}}
						} else if row, present := records[rid.String()]; present {
							result = []map[string]any{row}
						}
					case strings.HasPrefix(sql, "CREATE $rid CONTENT "), strings.HasPrefix(sql, "UPSERT $rid CONTENT "):
						if !active {
							return nil, errors.New("catalog mutation was not inside the original Begin")
						}
						rows++
						activeRows++
						maximum = max(maximum, activeRows)
						// One supplied RID is checked before the native marshaler
						// returns. No SQL maximum is treated as actual work.
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != transactions || prefix.Rows != rows || prefix.MaximumRows != maximum {
							return nil, errors.New("catalog operand forwarded before its exact Append ACK")
						}
						if test.lost {
							return nil, context.DeadlineExceeded
						}
						delete(vars, "rid")
						vars["id"] = rid
						records[rid.String()] = vars
						result = []map[string]any{vars}
					default:
						return nil, errors.New("unreviewed catalog fixture statement")
					}
					result = []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}
				default:
					return nil, errors.New("unexpected catalog SDK control")
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != transactions || prefix.Rows != rows || prefix.MaximumRows != maximum {
					return nil, errors.New("catalog control/read changed its genuine prefix")
				}
				return result, nil
			}
			err := s.PublishServiceCatalogV3Candidate(ctx, generation)
			if test.refuse {
				if !errors.Is(err, ErrConflict) || transactions != maxServiceCatalogV3PublishAttempts ||
					rows != 0 || commits != 0 || cancels != maxServiceCatalogV3PublishAttempts {
					t.Fatalf("repository fence did not retain and cancel each actual attempt: %d/%d/%d, %v", transactions, rows, cancels, err)
				}
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
				if err := owner.checkpoint(ctx); err != nil {
					t.Fatal(err)
				}
				return
			}
			if test.lost {
				prefix, _ := controller.Snapshot()
				if err == nil || transactions != 1 || rows != 1 || commits != 0 || cancels != 0 ||
					prefix.Transactions != 1 || prefix.Rows != 1 || prefix.Complete {
					t.Fatalf("lost reply was replayed or lost its prefix: %+v, %v", prefix, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Cold: M members + root + authority + lifecycle + M edges +
			// candidate. The absent prior root emits no three-target advance.
			wantRows := uint64(2*members + 4)
			if transactions != 1 || rows != wantRows || maximum != wantRows || commits != 1 || cancels != 0 {
				t.Fatalf("cold actual prefix=%d/%d/%d, commits=%d cancels=%d", transactions, rows, maximum, commits, cancels)
			}
			if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
				t.Fatal(err)
			}
			if transactions != 2 || rows != wantRows || maximum != wantRows || commits != 2 || cancels != 0 {
				t.Fatal("exact reuse charged absent mutations or lost its real read-only Begin")
			}
			if err := controller.Fence(); err != nil {
				t.Fatal(err)
			}
			if err := owner.checkpoint(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
