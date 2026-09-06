//go:build darwin || linux

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

	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func resolverAccountingPublication(t *testing.T) ResolverCatalogPublication {
	t.Helper()
	declarations := []ResolverCatalogDeclarationPublication{}
	packs := []ResolverCatalogPack{}
	declarationDigest, err := resolverCatalogDeclarationSetDigest(declarations)
	if err != nil {
		t.Fatal(err)
	}
	packDigest, err := resolverCatalogPackSetDigest(packs)
	if err != nil {
		t.Fatal(err)
	}
	repository := "example.com/neutral/resolver"
	digest := "sha256:" + strings.Repeat("a", 64)
	return ResolverCatalogPublication{
		Repository: repository, HeadCommit: strings.Repeat("b", 40),
		Declarations: declarations, DeclarationSetDigest: declarationDigest,
		CandidateManifestDigest: digest, SourceLanePolicy: resolverCatalogSourceLanePolicy,
		ResolverPacks: packs, ResolverPackSetDigest: packDigest, CatalogPolicyDigest: digest,
		GenerationDigest: digest, ManifestDigest: digest, AuthorityDigest: digest,
		ManifestPath:    resolvercatalogid.ManifestName(repository),
		ControlRevision: 1, WriterSchema: resolverCatalogWriterSchema,
		PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// One-shot test observation immediately before a real native Send. It neither
// manufactures replies nor supplies accounting authority to production.
type resolverRetirementNativeConnection struct {
	connection.Connection
	before func(context.Context) error
	writes int
}

func (conn *resolverRetirementNativeConnection) Send(ctx context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	if method == "query" && len(params) == 2 {
		if sql, ok := params[0].(string); ok && strings.HasPrefix(strings.TrimSpace(sql), "BEGIN;") {
			conn.writes++
			if conn.before != nil {
				before := conn.before
				conn.before = nil
				if err := before(ctx); err != nil {
					return nil, err
				}
			}
		}
	}
	return conn.Connection.Send(ctx, method, params...)
}

func TestResolverRetirementNativeFences(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	s, err := OpenLocalMemory(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := s.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	runtime, err := ReadLocalRuntime(directory)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.ParseRequestURI(runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn := &resolverRetirementNativeConnection{Connection: gorillaws.New(connection.NewConfig(endpoint))}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
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
	observed := &Surreal{db: db}
	// Preserve raw imported-pointer compatibility independently of its normal
	// unique index/row schema. All resolver/repository/job schemas stay real.
	requireCandidateRawQuery(t, ctx, s, "REMOVE TABLE caller_generation_publication; DEFINE TABLE caller_generation_publication SCHEMALESS;", nil)
	for _, operation := range []string{"publish", "clear"} {
		for _, mode := range []string{"one", "changed_ids", "changed_authority", "changed_pending"} {
			if operation == "clear" && (mode == "changed_authority" || mode == "changed_pending") {
				continue
			}
			t.Run(operation+"/"+mode, func(t *testing.T) {
				catalog := resolverAccountingPublication(t)
				catalog.Repository += "/" + operation + "/" + strings.ReplaceAll(mode, "_", "-")
				catalog.ManifestPath = resolvercatalogid.ManifestName(catalog.Repository)
				catalog.ControlRevision = 0
				if err := s.UpsertRepo(ctx, Repo{Name: catalog.Repository, CloneURL: "https://" + catalog.Repository + ".git"}); err != nil {
					t.Fatal(err)
				}
				if err := s.SetRepoIndexed(ctx, catalog.Repository, catalog.HeadCommit, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				candidate := CandidateManifestPublication{
					Repository: catalog.Repository, HeadCommit: catalog.HeadCommit,
					PolicyDigest: internalCallerDigest('c'), ManifestDigest: catalog.CandidateManifestDigest,
					GenerationDigest: internalCallerDigest('d'), ManifestPath: candidateid.ManifestName(catalog.Repository),
					ControlRevision: 1, PublishedAt: time.Now().UTC(),
				}
				requireCandidateRawQuery(t, ctx, s, "UPSERT $rid CONTENT $value;", map[string]any{"rid": candidateManifestPublicationID(catalog.Repository), "value": candidate})
				if err := s.PublishResolverCatalog(ctx, catalog); err != nil {
					t.Fatal(err)
				}
				rid := models.NewRecordID("caller_generation_publication", []any{catalog.Repository, "one"})
				requireCandidateRawQuery(t, ctx, s, "CREATE $rid SET repository = $repository, neutral_raw = true;", map[string]any{"rid": rid, "repository": catalog.Repository})
				next := catalog
				next.GenerationDigest, next.ManifestDigest = internalCallerDigest('e'), internalCallerDigest('f')
				conn.before = nil
				if mode != "one" {
					conn.before = func(callCtx context.Context) error {
						statement := "CREATE $rid SET repository = $repository, neutral_raw = true;"
						vars := map[string]any{"rid": models.NewRecordID("caller_generation_publication", []any{catalog.Repository, "two"}), "repository": catalog.Repository}
						switch mode {
						case "changed_authority":
							statement = "UPDATE $rid SET manifest_digest = $digest;"
							vars = map[string]any{"rid": candidateManifestPublicationID(catalog.Repository), "digest": internalCallerDigest('9')}
						case "changed_pending":
							statement = "DELETE caller_leaf_job WHERE pending_key = $repository;"
						}
						_, err := surrealdb.Query[any](callCtx, s.db, statement, vars)
						return err
					}
				}
				before := conn.writes
				var result error
				if operation == "publish" {
					result = observed.PublishResolverCatalog(ctx, next)
				} else {
					result = observed.ClearResolverCatalogPublication(ctx, catalog.Repository)
				}
				if (result == nil) != (mode == "one") || conn.writes != before+1 {
					t.Fatalf("native result=%v write_delta=%d", result, conn.writes-before)
				}
				if mode != "one" && !strings.Contains(result.Error(), "census changed") {
					t.Fatalf("unexpected native refusal: %v", result)
				}
				rows, err := surrealdb.Query[[]models.RecordID](ctx, s.db, clearResolverCatalogCensusSQL, map[string]any{"repository": catalog.Repository})
				wantPointers := 1
				switch mode {
				case "one":
					wantPointers = 0
				case "changed_ids":
					wantPointers = 2
				}
				if err != nil || len(firstDomainRows(rows)) != wantPointers {
					t.Fatalf("retained pointers=%v error=%v", rows, err)
				}
				repo, err := s.GetRepo(ctx, catalog.Repository)
				wantRevision := int64(0)
				if mode == "one" {
					wantRevision = 1
				}
				if err != nil || repo.CallerPublicationRevision != wantRevision {
					t.Fatalf("retirement repo=%+v error=%v", repo, err)
				}
				actual, err := s.GetResolverCatalogPublication(ctx, catalog.Repository)
				if operation == "clear" && mode == "one" {
					if !errors.Is(err, ErrNotFound) {
						t.Fatalf("cleared resolver=%+v error=%v", actual, err)
					}
				} else {
					wantManifest := catalog.ManifestDigest
					if mode == "one" {
						wantManifest = next.ManifestDigest
					}
					if err != nil || actual.ManifestDigest != wantManifest {
						t.Fatalf("resolver authority=%+v error=%v", actual, err)
					}
				}
			})
		}
	}
}

// The script tests concrete SA01 prefixes and emitted operands. Native
// compatibility tests independently exercise the same source SQL and guards.
func TestResolverRetirementAccounting(t *testing.T) {
	for _, operation := range []string{"publish", "clear"} {
		for _, tc := range []struct {
			name     string
			count    int
			pending  bool
			ordinary bool
		}{
			{name: "inactive"}, {name: "empty"}, {name: "one", count: 1},
			{name: "many", count: 2, pending: true}, {name: "at_publish_cap", count: 509},
			{name: "above_publish_cap", count: 510}, {name: "at_clear_cap", count: 511},
			{name: "above_clear_cap", count: 512}, {name: "overflow", count: 513},
			{name: "ordinary_overflow", count: 513, ordinary: true},
			{name: "native_error", count: 1}, {name: "lost_write", count: 1},
			{name: "refused", count: 1}, {name: "null_ids"},
			{name: "missing_branch"}, {name: "bad_arity"}, {name: "wrong_table", count: 1},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				ctx := t.Context()
				var owner *storeCallOwner
				var controller *storeaccounting.Controller
				if !tc.ordinary {
					ctx, owner, controller = storeAccountingFixture(t, 40, 2)
				}
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				publication := resolverAccountingPublication(t)
				ids := make([]models.RecordID, tc.count)
				for i := range ids {
					ids[i] = models.NewRecordID("caller_generation_publication", fmt.Sprintf("neutral-%03d", i))
				}
				if tc.name == "null_ids" {
					ids = nil
				}
				if tc.name == "wrong_table" {
					ids[0].Table = "repo"
				}
				retire := tc.name != "inactive"
				branch := &retire
				if tc.name == "missing_branch" {
					branch = nil
				}
				wantRows := uint64(tc.count + 1)
				if operation == "publish" {
					wantRows += 2
				}
				if tc.count == 1 {
					wantRows++
				}
				malformed := tc.name == "null_ids" || tc.name == "wrong_table" || tc.name == "bad_arity" ||
					(operation == "publish" && tc.name == "missing_branch")
				admitted := !malformed && (wantRows <= 512 || tc.ordinary)
				reads, writes := 0, 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					vars, err := callerAccountingVars(native, request)
					if err != nil {
						return nil, err
					}
					sql := request.Params[0].(string)
					switch sql {
					case resolverCatalogPublicationCensusSQL:
						reads++
						results := make([]surrealdb.QueryResult[any], 23)
						for i := range results {
							results[i].Status = "OK"
						}
						results[22].Result = []resolverCatalogRetirementRec{{Retire: branch, IDs: ids}}
						if tc.name == "bad_arity" {
							results = results[1:]
						}
						return results, nil
					case clearResolverCatalogCensusSQL:
						reads++
						results := []surrealdb.QueryResult[any]{{Status: "OK", Result: ids}}
						if tc.name == "bad_arity" {
							results = append(results, results[0])
						}
						return results, nil
					case queuePendingSelection:
						reads++
						rows := []jobRec{}
						if tc.pending {
							id := models.NewRecordID("caller_leaf_job", "pending")
							rows = append(rows, jobRec{RecID: &id})
						}
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
					}
					writes++
					if !admitted {
						return nil, errors.New("refused census reached native write")
					}
					if tc.ordinary {
						wantSQL := publishResolverCatalogSQL
						if operation == "clear" {
							wantSQL = clearResolverCatalogSQL
						}
						if sql != wantSQL {
							return nil, errors.New("ordinary overflow was truncated")
						}
					} else {
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != 1 || prefix.Rows != wantRows || prefix.MaximumRows != wantRows {
							return nil, errors.New("resolver native write preceded exact parent ACK")
						}
						encoded, err := native.codec.Marshal(vars["retired_ids"])
						if err != nil {
							return nil, err
						}
						var submitted []models.RecordID
						if err := native.codec.Unmarshal(encoded, &submitted); err != nil {
							return nil, err
						}
						if !reflect.DeepEqual(submitted, ids) || !strings.Contains(sql, "!= $retired_ids") {
							return nil, errors.New("resolver actual retirement vector/echo changed")
						}
						if strings.Contains(sql, "DELETE caller_generation_publication") != (tc.count > 0) ||
							strings.Contains(sql, "SET caller_publication_revision") != (tc.count == 1) {
							return nil, errors.New("resolver emitted inactive retirement operand")
						}
						if operation == "publish" {
							if strings.Contains(sql, resolverCallerFanoutUpdateSQL) != tc.pending ||
								strings.Contains(sql, resolverCallerFanoutCreateSQL) == tc.pending ||
								!strings.Contains(sql, "[$actual_pending_caller] != [$pending_ids[0]]") ||
								!strings.Contains(sql, resolverCatalogPublicationPreimageSQL) {
								return nil, errors.New("resolver pending branch/authority guards changed")
							}
						}
					}
					if tc.name == "lost_write" {
						return nil, context.DeadlineExceeded
					}
					if tc.name == "native_error" {
						return nil, &surrealdb.QueryError{Message: "phebs-conflict: neutral census changed"}
					}
					rows := []resolverCatalogPublicationRec{}
					if tc.name != "refused" {
						rows = append(rows, resolverCatalogPublicationRec{ResolverCatalogPublication: publication})
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
				}
				var err error
				if operation == "publish" {
					err = s.PublishResolverCatalog(ctx, publication)
				} else {
					err = s.ClearResolverCatalogPublication(ctx, publication.Repository)
				}
				wantOK := admitted && tc.name != "lost_write" && tc.name != "native_error" && (operation != "publish" || tc.name != "refused")
				if (err == nil) != wantOK {
					t.Fatalf("error=%v wantOK=%v", err, wantOK)
				}
				wantWrites := 0
				if admitted {
					wantWrites = 1
				}
				wantReads := 1
				if operation == "publish" && !malformed && wantRows <= 512 {
					wantReads++
				}
				if reads != wantReads || writes != wantWrites {
					t.Fatalf("reads/writes=%d/%d want=%d/%d", reads, writes, wantReads, wantWrites)
				}
				if controller != nil {
					prefix, _ := controller.Snapshot()
					if prefix.Transactions != uint64(wantWrites) || prefix.Rows != uint64(wantWrites)*wantRows ||
						(prefix.Producers[0].Calls != 0) != (tc.name == "lost_write") {
						t.Fatalf("resolver prefix=%+v", prefix)
					}
				}
			})
		}
	}
}
