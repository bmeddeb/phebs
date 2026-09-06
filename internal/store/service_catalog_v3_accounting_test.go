//go:build darwin || linux

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestServiceCatalogV3AccountingSourceCoverage(t *testing.T) {
	writes := map[string][]string{
		"publishServiceCatalogV3CandidateOnce":               {"storeWrite(1)", "storeWrite(1)", "storeWrite(1)", "storeWrite(1)"},
		"migrateServiceCatalogV3Schema":                      {"storeWrite(1)"},
		"ensureServiceCatalogV3LifecycleMetadata":            {"storeWrite(1)", "storeWrite(1)", "storeWrite(1)"},
		"migrateServiceCatalogV3LifecycleSchema":             {"storeWrite(1)"},
		"RepairServiceCatalogV3Startup":                      {"storeWrite(1)"},
		"deleteServiceCatalogV3Orphan":                       {"storeWrite(1)"},
		"drainServiceStateV3Preimages":                       {"storeWrite(uint64(len(rowIDs)))", "storeWrite(1)"},
		"retireServiceCatalogV3Generation":                   {"storeWrite(1)"},
		"drainServiceCatalogV3Generation":                    {"storeWrite(3)"},
		"finalizeServiceCatalogV3Generation":                 {"storeWrite(3)"},
		"RetireServiceRuntimeSelectorForRepositoryDeletion":  {"storeWrite(2)"},
		"selectServiceRuntime":                               {"storeWrite(1)", "storeWrite(1)"},
		"updateServiceRuntimeCurrentCatalogReference":        {"storeWrite(1)", "storeWrite(1)"},
		"migrateServiceRuntimeSelectorSchema":                {"storeWrite(1)", "storeWrite(1)"},
		"migrateServiceCatalogV3RelationshipReferenceSchema": {"storeWrite(1)", "storeWrite(1)"},
		"writeServiceCatalogV3RelationshipReference":         {"storeWrite(1)"},
		"deleteStaleServiceCatalogV3RelationshipReferences":  {"storeWrite(uint64(len(ids)))"},
		"UnpinServiceCatalogV3RelationshipReference":         {"storeWrite(1)"},
	}
	seen := make(map[string][]string)
	terminals := map[string]int{}
	for path, want := range map[string]int{
		"service_catalog_v3.go":                        19,
		"service_catalog_v3_lifecycle.go":              42,
		"service_runtime_selector.go":                  27,
		"service_catalog_v3_relationship_reference.go": 10,
	} {
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			owner := "s.accounting"
			if fn.Recv == nil {
				owner = "owner"
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				target := call.Fun
				if generic, ok := target.(*ast.IndexExpr); ok {
					target = generic.X
				}
				if selector, ok := target.(*ast.SelectorExpr); ok {
					if selector.Sel.Name == "Query" || selector.Sel.Name == "Begin" || selector.Sel.Name == "Commit" || selector.Sel.Name == "Cancel" {
						t.Fatalf("%s bypasses accounting at %s", fn.Name.Name, set.Position(call.Pos()))
					}
					return true
				}
				name, ok := target.(*ast.Ident)
				if !ok {
					return true
				}
				if name.Name != "storeQuery" && name.Name != "storeBegin" && name.Name != "storeCommit" && name.Name != "storeCancel" {
					return true
				}
				var actual bytes.Buffer
				if len(call.Args) < 3 {
					t.Fatal("missing captured owner")
				}
				if err := format.Node(&actual, set, call.Args[1]); err != nil {
					t.Fatal(err)
				}
				if actual.String() != owner {
					t.Fatalf("%s owner=%s", fn.Name.Name, &actual)
				}
				if name.Name != "storeQuery" {
					terminals[name.Name]++
					return true
				}
				if len(call.Args) != 6 {
					t.Fatal("query lost source descriptor")
				}
				actual.Reset()
				if err := format.Node(&actual, set, call.Args[5]); err != nil {
					t.Fatal(err)
				}
				if actual.String() != "storeRead()" {
					seen[fn.Name.Name] = append(seen[fn.Name.Name], actual.String())
				}
				count++
				return true
			})
		}
		if count != want {
			t.Fatalf("%s source calls=%d want%d", path, count, want)
		}
	}
	if !reflect.DeepEqual(seen, writes) {
		t.Fatalf("source target recipes changed: %v", seen)
	}
	if !reflect.DeepEqual(terminals, map[string]int{"storeBegin": 5, "storeCommit": 4, "storeCancel": 5}) {
		t.Fatalf("transaction coverage=%v", terminals)
	}
	if count, err := schemaBatchDefinitionCount(serviceCatalogV3RelationshipReferenceSchema); err != nil || count != 12 {
		t.Fatalf("reference definition group=%d/%v", count, err)
	}
}

// These scripted SDK replies check actual source operands against the parent
// SA01 prefix before native submission. They do not prove native row validity.
func TestServiceCatalogV3AccountingFixedTargets(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	candidate := serviceCatalogV3LifecycleRec{RootDigest: digest, Repository: "example.com/acme/catalog", AuthorityVersion: "neutral-authority"}
	for _, test := range []struct {
		name   string
		fields []string
		result any
		invoke func(context.Context, *Surreal) error
	}{
		{"retire_guard_false", []string{"rid"}, []serviceCatalogV3RetireResult{{Transitioned: 0}}, func(ctx context.Context, s *Surreal) error {
			_, err := s.retireServiceCatalogV3Generation(ctx, candidate, 3)
			return err
		}},
		{"finalize", []string{"root_rid", "lifecycle_rid", "authority_rid"}, []serviceCatalogV3FinalizeResult{{DeletedRoot: 1}}, func(ctx context.Context, s *Surreal) error {
			_, err := s.finalizeServiceCatalogV3Generation(ctx, candidate)
			return err
		}},
		{"selector_delete_guard_false", []string{"selector_rid", "reference_rid"}, []bool{false}, func(ctx context.Context, s *Surreal) error {
			err := s.RetireServiceRuntimeSelectorForRepositoryDeletion(ctx, candidate.Repository)
			if !errors.Is(err, ErrConflict) {
				return fmt.Errorf("missing ordinary refusal: %v", err)
			}
			return nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Method != "query" || len(request.Params) != 2 {
					return nil, errors.New("unexpected native call")
				}
				raw, ok := request.Params[1].(cbor.RawMessage)
				if !ok {
					return nil, errors.New("native payload is not owned")
				}
				var fields map[string]cbor.RawMessage
				if err := native.codec.Unmarshal(raw, &fields); err != nil {
					return nil, err
				}
				for _, name := range test.fields {
					var rid models.RecordID
					if err := native.codec.Unmarshal(fields[name], &rid); err != nil {
						return nil, err
					}
					if rid.Table == "" || rid.ID == nil {
						return nil, errors.New("missing actual target")
					}
				}
				prefix, err := controller.Snapshot()
				want := uint64(len(test.fields))
				if err != nil || prefix.Transactions != 1 || prefix.Rows != want || prefix.MaximumRows != want {
					return nil, errors.New("target prefix missing before native call")
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: test.result}}, nil
			}
			if err := test.invoke(ctx, &Surreal{db: db, accounting: owner}); err != nil {
				t.Fatal(err)
			}
			if native.calls != 1 {
				t.Fatalf("native calls=%d", native.calls)
			}
		})
	}
}

func TestServiceCatalogV3AccountingCurrentReferenceKeepsTransaction(t *testing.T) {
	for _, backend := range []string{ServiceRuntimeV2, ServiceRuntimeV3} {
		t.Run(backend, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			uuid := models.UUID{}
			uuid.UUID[0] = 31
			queries := 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				switch request.Method {
				case "begin":
					return uuid, nil
				case "commit":
					return nil, nil
				case "query":
					actual, ok := request.Txn.(*models.UUID)
					if !ok || actual == nil || *actual != uuid {
						return nil, errors.New("lost same native UUID")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != uint64(queries) {
						return nil, errors.New("wrong cumulative UUID prefix")
					}
					queries++
					if queries == 1 {
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}}, nil
					}
					return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
				default:
					return nil, errors.New("unexpected native terminal")
				}
			}
			tx, err := storeBegin(ctx, owner, db)
			if err != nil {
				t.Fatal(err)
			}
			selector := ServiceRuntimeSelector{Backend: backend, Repository: "example.com/acme/catalog", CatalogRootDigest: "sha256:" + strings.Repeat("a", 64), ChangedAt: time.Now().UTC()}
			if err := updateServiceRuntimeCurrentCatalogReference(ctx, owner, tx, selector); err != nil {
				t.Fatal(err)
			}
			if err := storeCommit(ctx, owner, tx); err != nil {
				t.Fatal(err)
			}
			_ = storeCancel(ctx, owner, tx) // Existing deferred terminal no-op owns no second submission.
			if queries != 2 || native.calls != 4 {
				t.Fatalf("queries/calls=%d/%d", queries, native.calls)
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

func TestServiceCatalogV3AccountingRelationshipCleanup(t *testing.T) {
	const table = "service_catalog_v3_relationship_reference"
	ids := make([]models.RecordID, 513)
	for i := range ids {
		ids[i] = models.NewRecordID(table, fmt.Sprintf("neutral-%04d", i))
	}
	steps := []stateAccountingStep{
		{restoreClearStep: restoreClearStep{contains: "SELECT VALUE id", rows: ids[:512]}},
		{restoreClearStep: restoreClearStep{contains: "FOR $rid IN $ids", ids: ids[:512]}, vector: "ids"},
		{restoreClearStep: restoreClearStep{contains: "SELECT VALUE id", rows: ids[512:]}},
		{restoreClearStep: restoreClearStep{contains: "FOR $rid IN $ids", ids: ids[512:]}, vector: "ids"},
		{restoreClearStep: restoreClearStep{contains: "SELECT VALUE id", rows: []models.RecordID{}}},
	}
	ctx, s, controller := stateAccountingScript(t, steps)
	if err := s.deleteStaleServiceCatalogV3RelationshipReferences(ctx, nil); err != nil {
		t.Fatal(err)
	}
	prefix, err := controller.Snapshot()
	if err != nil || prefix.Transactions != 2 || prefix.Rows != 513 || prefix.MaximumRows != 512 {
		t.Fatalf("prefix=%+v/%v", prefix, err)
	}
}

func TestServiceCatalogV3AccountingRelationshipCleanupRefusals(t *testing.T) {
	t.Run("canceled_empty_is_not_success", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		s, conn := restoreClearScript(t, []restoreClearStep{{contains: "SELECT VALUE id", rows: []models.RecordID{}, cancel: true}}, cancel)
		if err := s.deleteStaleServiceCatalogV3RelationshipReferences(ctx, nil); !errors.Is(err, context.Canceled) || conn.calls != 1 {
			t.Fatalf("canceled empty calls=%d/%v", conn.calls, err)
		}
	})
	for _, test := range []struct {
		name string
		rows any
	}{
		{"null", nil}, {"overflow", make([]models.RecordID, 513)}, {"wrong_table", []models.RecordID{models.NewRecordID("repo", "neutral")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, s, controller := stateAccountingScript(t, []stateAccountingStep{{restoreClearStep: restoreClearStep{contains: "SELECT VALUE id", rows: test.rows}}})
			if err := s.deleteStaleServiceCatalogV3RelationshipReferences(ctx, nil); err == nil {
				t.Fatal("invalid census succeeded")
			}
			prefix, _ := controller.Snapshot()
			if prefix.Transactions != 0 || prefix.Rows != 0 {
				t.Fatalf("invented write: %+v", prefix)
			}
		})
	}
	t.Run("fixed_page_limit", func(t *testing.T) {
		id := []models.RecordID{models.NewRecordID("service_catalog_v3_relationship_reference", "neutral")}
		steps := make([]restoreClearStep, 0, 65)
		for range 32 {
			steps = append(steps, restoreClearStep{contains: "SELECT VALUE id", rows: id}, restoreClearStep{contains: "FOR $rid IN $ids", ids: id})
		}
		steps = append(steps, restoreClearStep{contains: "SELECT VALUE id", rows: id})
		s, conn := restoreClearScript(t, steps, nil)
		if err := s.deleteStaleServiceCatalogV3RelationshipReferences(t.Context(), nil); err == nil || conn.calls != 65 {
			t.Fatalf("bounded calls=%d/%v", conn.calls, err)
		}
	})
}

func TestServiceCatalogV3AccountingNativeRelationshipCleanup(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
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
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	const table = "service_catalog_v3_relationship_reference"
	keep, futureKeep := "sha256:"+strings.Repeat("e", 64), "sha256:"+strings.Repeat("f", 64)
	rows := make([]map[string]any, 515)
	for index := range rows {
		generation := fmt.Sprintf("sha256:%064x", index)
		if index == 514 {
			generation = keep
		}
		rows[index] = map[string]any{
			"id":         models.NewRecordID(table, fmt.Sprintf("neutral-%04d", index)),
			"repository": "example.com/acme/cleanup", "relationship_generation_digest": generation,
			"relationship_root_digest": "sha256:" + strings.Repeat("a", 64), "catalog_root_digest": "sha256:" + strings.Repeat("b", 64),
			"catalog_control_revision": 1, "state_control_revision": 1, "state_summary_digest": "sha256:" + strings.Repeat("c", 64), "recorded_at": time.Now().UTC(),
		}
	}
	for start := 0; start < len(rows); start += 512 {
		if _, err := surrealdb.Query[any](ctx, s.db, "INSERT $rows;", map[string]any{"rows": rows[start:min(start+512, len(rows))]}); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]any{"generations": []string{keep, futureKeep}, "limit": 512}
	selected, err := surrealdb.Query[[]models.RecordID](ctx, s.db, serviceCatalogV3RelationshipCleanupSelection, vars)
	if err != nil {
		t.Fatal(err)
	}
	ids := firstDomainRows(selected)
	if len(ids) != 512 {
		t.Fatalf("native selected page=%d", len(ids))
	}
	vars["ids"] = ids
	if _, err := surrealdb.Query[any](ctx, s.db, "UPDATE $rid SET relationship_generation_digest=$generation;", map[string]any{"rid": ids[0], "generation": futureKeep}); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db, serviceCatalogV3RelationshipCleanupWrite, vars); err == nil {
		t.Fatal("changed native page was deleted")
	}
	count := func() int {
		t.Helper()
		result, err := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM service_catalog_v3_relationship_reference;", nil)
		if err != nil {
			t.Fatal(err)
		}
		return len(firstDomainRows(result))
	}
	if got := count(); got != 515 {
		t.Fatalf("stale page refusal changed %d records", got)
	}
	if _, err := surrealdb.Query[any](ctx, s.db, `DEFINE EVENT t422_cleanup_stop ON TABLE service_catalog_v3_relationship_reference
WHEN $event = 'DELETE' AND $before.relationship_generation_digest = 'sha256:0000000000000000000000000000000000000000000000000000000000000201'
THEN { THROW 'phebs-permanent: neutral cleanup interruption' };`, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteStaleServiceCatalogV3RelationshipReferences(ctx, []string{keep, futureKeep}); err == nil {
		t.Fatal("interrupted cleanup succeeded")
	}
	if got := count(); got != 3 {
		t.Fatalf("actual committed prefix retained %d records, want3", got)
	}
	if _, err := surrealdb.Query[any](ctx, s.db, "REMOVE EVENT t422_cleanup_stop ON TABLE service_catalog_v3_relationship_reference;", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteStaleServiceCatalogV3RelationshipReferences(ctx, []string{keep, futureKeep}); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 2 {
		t.Fatalf("recovery retained %d records, want2", got)
	}
	if err := s.deleteStaleServiceCatalogV3RelationshipReferences(ctx, []string{keep, futureKeep}); err != nil {
		t.Fatal(err)
	}
}
