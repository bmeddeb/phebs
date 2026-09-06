//go:build darwin || linux

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func deleteRepoTestObservation() deleteRepoObservation {
	return deleteRepoObservation{
		PublishedRuns: []models.RecordID{}, StagedRuns: []models.RecordID{},
		Attempts: []models.RecordID{}, Outcomes: []models.RecordID{},
		ExtractionJobs: []models.RecordID{}, CandidateJobs: []models.RecordID{},
		ResolverJobs: []models.RecordID{}, CallerJobs: []models.RecordID{},
		Schedules: []models.RecordID{}, Plans: []models.RecordID{}, Currents: []models.RecordID{},
		Candidates: []models.RecordID{}, Resolvers: []models.RecordID{}, Callers: []models.RecordID{},
		CallerOutcomes: []models.RecordID{}, CallerAdmissions: []models.RecordID{},
		Permissions: []models.RecordID{}, Connections: []models.RecordID{},
	}
}

func deleteRepoTestFields(observed *deleteRepoObservation) []struct {
	ids   *[]models.RecordID
	table string
} {
	return []struct {
		ids   *[]models.RecordID
		table string
	}{
		{&observed.PublishedRuns, "extraction_run"}, {&observed.StagedRuns, "extraction_run"},
		{&observed.Attempts, "extraction_attempt"}, {&observed.Outcomes, "extraction_domain_outcome"},
		{&observed.ExtractionJobs, "extraction_job"}, {&observed.CandidateJobs, "candidate_manifest_job"},
		{&observed.ResolverJobs, "resolver_catalog_job"}, {&observed.CallerJobs, "caller_leaf_job"},
		{&observed.Schedules, "generation_schedule"}, {&observed.Plans, "service_state_v3_plan"},
		{&observed.Currents, "generation_schedule_current"}, {&observed.Candidates, "candidate_manifest_publication"},
		{&observed.Resolvers, "resolver_catalog_publication"}, {&observed.Callers, "caller_generation_publication"},
		{&observed.CallerOutcomes, "caller_leaf_outcome"}, {&observed.CallerAdmissions, "caller_generation_admission"},
		{&observed.Permissions, "repo_permission"}, {&observed.Connections, "repo_connection"},
	}
}

func TestDeleteRepoAccounting(t *testing.T) {
	for _, mode := range []string{"empty", "all_vectors", "limit", "overflow", "sentinel", "too_many", "null", "wrong_table", "bad_sentinel", "canceled", "changed", "read_conflict", "retry_limit", "lost"} {
		t.Run(mode, func(t *testing.T) {
			base, owner, controller := storeAccountingFixture(t, 40, 2)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			db, native := storeAccountingDB(t, base, owner)
			observed := deleteRepoTestObservation()
			wantRows, wantTX, wantCalls := uint64(1), uint64(1), 2
			wantOK := true
			switch mode {
			case "all_vectors":
				for index, field := range deleteRepoTestFields(&observed) {
					*field.ids = []models.RecordID{models.NewRecordID(field.table, uint64(index))}
				}
				wantRows = 19
			case "limit", "overflow", "sentinel", "too_many", "bad_sentinel":
				count := map[string]int{"limit": 511, "overflow": 512, "sentinel": 513, "too_many": 514, "bad_sentinel": 513}[mode]
				for index := range count {
					observed.Connections = append(observed.Connections, models.NewRecordID("repo_connection", uint64(index)))
				}
				wantRows = uint64(count + 1)
				if mode != "limit" {
					wantOK, wantCalls, wantRows, wantTX = false, 1, 0, 0
				}
				if mode == "bad_sentinel" {
					observed.Connections[512] = models.NewRecordID("repo", "wrong")
				}
			case "null", "wrong_table", "canceled":
				wantOK, wantCalls, wantRows, wantTX = false, 1, 0, 0
				switch mode {
				case "null":
					observed.Permissions = nil
				case "wrong_table":
					observed.Permissions = []models.RecordID{models.NewRecordID("repo", "wrong")}
				}
			case "changed":
				wantCalls, wantTX, wantRows = 4, 2, 3
			case "read_conflict":
				wantCalls = 3
			case "retry_limit":
				wantOK, wantCalls, wantTX, wantRows = false, 2*maxQueueRetries, maxQueueRetries, maxQueueRetries
			case "lost":
				wantOK = false
			}
			writes := uint64(0)
			acceptedRows := uint64(0)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				query := request.Params[0].(string)
				if query == deleteRepoCensusSQL+"RETURN [$delete_repo_census];" {
					if mode == "read_conflict" && native.calls == 1 {
						return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "transaction conflict"}}, nil
					}
					if mode == "canceled" {
						cancel()
					}
					if mode == "changed" && writes == 1 {
						observed.Connections = []models.RecordID{models.NewRecordID("repo_connection", "new")}
					}
					return generationAccountingCensusReply(1, []deleteRepoObservation{observed}), nil
				}
				if query != deleteRepoSelectedSQL {
					return nil, errors.New("deletion escaped the supplied-operand transaction")
				}
				var actual struct {
					Deletion deleteRepoObservation `cbor:"deletion"`
					RID      models.RecordID       `cbor:"rid"`
				}
				if err := native.codec.Unmarshal(request.Params[1].(cbor.RawMessage), &actual); err != nil {
					return nil, err
				}
				got, err := native.codec.Marshal(actual.Deletion)
				if err != nil {
					return nil, err
				}
				want, err := native.codec.Marshal(observed)
				if err != nil || !bytes.Equal(got, want) || actual.RID != repoID("neutral") {
					return nil, errors.New("deletion changed its actual census or fixed repo operand")
				}
				rows := uint64(1)
				for _, field := range deleteRepoTestFields(&observed) {
					rows += uint64(len(*field.ids))
				}
				writes++
				acceptedRows += rows
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != writes || prefix.Rows != acceptedRows {
					return nil, errors.New("deletion submitted before its exact target-vector ACK")
				}
				if mode == "retry_limit" || mode == "changed" && writes == 1 {
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "phebs-conflict: repository deletion census changed"}}, nil
				}
				if mode == "lost" {
					return nil, context.DeadlineExceeded
				}
				return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
			}
			err := (&Surreal{db: db, accounting: owner}).DeleteRepo(ctx, "neutral")
			prefix, _ := controller.Snapshot()
			if (err == nil) != wantOK || native.calls != wantCalls || prefix.Transactions != wantTX || prefix.Rows != wantRows {
				t.Fatalf("calls=%d prefix=%+v error=%v", native.calls, prefix, err)
			}
			if (mode == "overflow" || mode == "sentinel") && !errors.Is(err, storeaccounting.ErrDescriptor) {
				t.Fatalf("overflow classification=%v", err)
			}
		})
	}
}

func TestDeleteRepoAccountingOrdinaryOverflow(t *testing.T) {
	ctx, _, _ := storeAccountingFixture(t, 40, 2)
	db, native := storeAccountingDB(t, ctx, nil)
	observed := deleteRepoTestObservation()
	for index := range 512 {
		observed.Connections = append(observed.Connections, models.NewRecordID("repo_connection", uint64(index)))
	}
	native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
		if native.calls == 1 {
			return generationAccountingCensusReply(1, []deleteRepoObservation{observed}), nil
		}
		if native.calls != 2 || request.Params[0] != deleteRepoLegacySQL {
			return nil, errors.New("ordinary overflow split or replaced the original atomic writer")
		}
		return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
	}
	if err := (&Surreal{db: db}).DeleteRepo(ctx, "neutral"); err != nil || native.calls != 2 {
		t.Fatalf("ordinary overflow calls=%d error=%v", native.calls, err)
	}
}

func TestDeleteRepoAccountingSourcePreservesWriter(t *testing.T) {
	// Only concrete target expressions differ; every original statement,
	// predicate, assignment and ordering must otherwise remain byte-identical.
	body := strings.TrimPrefix(deleteRepoSelectedSQL, "BEGIN;\n"+deleteRepoCensusSQL+`
IF $delete_repo_census != $deletion {
 THROW 'phebs-conflict: repository deletion census changed';
};
`)
	pairs := []string{
		"$deletion.published_runs", "extraction_run", "$deletion.staged_runs", "extraction_run",
		"$deletion.attempts", "extraction_attempt", "$deletion.outcomes", "extraction_domain_outcome",
		"$deletion.extraction_jobs", "extraction_job", "$deletion.candidate_jobs", "candidate_manifest_job",
		"$deletion.resolver_jobs", "resolver_catalog_job", "$deletion.caller_jobs", "caller_leaf_job",
		"$deletion.schedules", "generation_schedule", "$deletion.plans", "service_state_v3_plan",
		"$deletion.currents", "generation_schedule_current", "$deletion.candidates", "candidate_manifest_publication",
		"$deletion.resolvers", "resolver_catalog_publication", "$deletion.callers", "caller_generation_publication",
		"$deletion.caller_outcomes", "caller_leaf_outcome", "$deletion.caller_admissions", "caller_generation_admission",
		"$deletion.permissions", "repo_permission", "$deletion.connections", "repo_connection",
	}
	if "BEGIN;\n"+strings.NewReplacer(pairs...).Replace(body) != deleteRepoLegacySQL || len(pairs) != 36 {
		t.Fatal("bounded cleanup changed one of its nineteen original mutation statements")
	}
	file, err := parser.ParseFile(token.NewFileSet(), "surreal.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "DeleteRepo" && fn.Name.Name != "deleteRepoCensus" {
			continue
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
			if selector, ok := target.(*ast.SelectorExpr); ok && (selector.Sel.Name == "Query" || selector.Sel.Name == "Begin" || selector.Sel.Name == "Commit" || selector.Sel.Name == "Cancel") {
				t.Fatal("repository cleanup bypassed its captured SDK owner")
			}
			if name, ok := target.(*ast.Ident); ok && name.Name == "storeQuery" {
				calls++
				owner, ok := call.Args[1].(*ast.SelectorExpr)
				if !ok || owner.Sel.Name != "accounting" || fmt.Sprint(owner.X) != "s" {
					t.Fatal("repository cleanup lost its captured owner")
				}
			}
			return true
		})
	}
	if calls != 2 {
		t.Fatalf("source query sites=%d, want2", calls)
	}
}

func TestDeleteRepoNativeCensusAndAtomicity(t *testing.T) {
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
	const repository, other = "example.com/neutral/deleting", "example.com/neutral/retained"
	for _, name := range []string{repository, other} {
		if err := s.UpsertRepo(ctx, Repo{Name: name}); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []JobKind{JobExtract, JobCandidate, JobResolverCatalog, JobCallerLeaf} {
			if _, err := s.CreateJob(ctx, kind, name); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := surrealdb.Query[any](ctx, s.db, `
CREATE repo_connection CONTENT {repo: $name, connection: 'neutral'};
CREATE repo_permission CONTENT {repo: $name, group: 'neutral'};`, map[string]any{"name": name}); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]any{
		"rid": repoID(repository), "name": repository,
		"state_reconcile_stage": ServiceStateV3ReconcileStage,
		"state_activate_stage":  ServiceStateV3ActivateStage,
		"relationship_v3_stage": ServiceRelationshipV3ScheduleStage,
	}
	observed, rows, err := s.deleteRepoCensus(ctx, vars)
	if err != nil || rows != 7 {
		t.Fatalf("native initial census rows=%d error=%v", rows, err)
	}
	// A real intervening member makes the whole writer refuse before mutation.
	if _, err := surrealdb.Query[any](ctx, s.db, `CREATE repo_connection CONTENT {repo: $name, connection: 'late'}`, vars); err != nil {
		t.Fatal(err)
	}
	vars["deletion"] = observed
	if _, err := surrealdb.Query[any](ctx, s.db, deleteRepoSelectedSQL, vars); err == nil || !strings.Contains(err.Error(), "census changed") {
		t.Fatalf("stale census did not refuse: %v", err)
	}
	beforeFailure, rows, err := s.deleteRepoCensus(ctx, vars)
	if err != nil || rows != 8 {
		t.Fatalf("stale refusal mutated earlier targets: rows=%d error=%v", rows, err)
	}
	// The real last-statement event fails after all eighteen preceding writer
	// statements. Their effects must roll back with the final repo deletion.
	if _, err := surrealdb.Query[any](ctx, s.db, `DEFINE EVENT delete_repo_test ON repo WHEN $event = 'DELETE' THEN {
 THROW 'phebs-permanent: delete repo final statement test';
};`, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepo(ctx, repository); err == nil || !strings.Contains(err.Error(), "final statement test") {
		t.Fatalf("late native error was not preserved: %v", err)
	}
	afterFailure, rows, err := s.deleteRepoCensus(ctx, vars)
	if err != nil || rows != 8 {
		t.Fatalf("late failure changed census rows=%d error=%v", rows, err)
	}
	beforeBytes, err := cbor.Marshal(beforeFailure)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := cbor.Marshal(afterFailure)
	if err != nil || !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("late failure changed selected native IDs: %v", err)
	}
	if _, err := s.GetRepo(ctx, repository); err != nil {
		t.Fatalf("late failure removed repo: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db, "REMOVE EVENT delete_repo_test ON repo;", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepo(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRepo(ctx, repository); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted repo remains: %v", err)
	}
	if _, rows, err := s.deleteRepoCensus(ctx, vars); err != nil || rows != 1 {
		t.Fatalf("deleted repo retains eligible targets: rows=%d error=%v", rows, err)
	}
	vars["name"], vars["rid"] = other, repoID(other)
	if _, rows, err := s.deleteRepoCensus(ctx, vars); err != nil || rows != 7 {
		t.Fatalf("cleanup changed another repository: rows=%d error=%v", rows, err)
	}
	if _, err := s.GetRepo(ctx, other); err != nil {
		t.Fatal(err)
	}
}
