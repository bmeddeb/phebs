//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// These fixtures exercise the real SDK/SA01 submission boundary, not native
// writer guards or engine atomicity. The supplied run and attempt each count
// even when the unchanged SQL's writer/status guard matches no row.
func TestEvidenceRuntimeAccountingBegin(t *testing.T) {
	for _, mode := range []string{"ordinary", "partitioned", "nil_owner", "guard_false", "known_error", "lost", "decode", "canceled", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			base, owner, controller := storeAccountingFixture(t, 40, 2)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			if mode == "nil_owner" {
				owner = nil
			}
			db, native := storeAccountingDB(t, base, owner)
			scope := validOutcome().Scope
			calls := 0
			var supplied extractionRunRec
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				prefix, err := controller.Snapshot()
				if err != nil || owner != nil && (prefix.Transactions != 1 || prefix.Rows != 2) {
					return nil, errors.New("run creation preceded two-operand admission")
				}
				var vars struct {
					RID             models.RecordID `json:"rid"`
					Attempt         models.RecordID `json:"attempt_rid"`
					RunID           string          `json:"run_id"`
					Repo            string          `json:"repo"`
					Commit          string          `json:"commit"`
					UnitDigest      string          `json:"unit_digest"`
					Domain          string          `json:"domain"`
					PartitionActive bool            `json:"partition_active"`
				}
				encoded, err := native.codec.Marshal(request.Params[1])
				if err != nil {
					return nil, err
				}
				if err := native.codec.Unmarshal(encoded, &vars); err != nil {
					return nil, err
				}
				if vars.RunID == "" || !reflect.DeepEqual(vars.RID, extractionRunID(vars.RunID)) ||
					!reflect.DeepEqual(vars.Attempt, extractionAttemptID(scope)) ||
					vars.Repo != scope.Repository || vars.Commit != scope.Commit || vars.UnitDigest != scope.UnitDigest || vars.Domain != scope.Domain {
					return nil, errors.New("run/attempt operand identity changed")
				}
				sql := request.Params[0].(string)
				if !strings.Contains(sql, "CREATE $rid SET") || !strings.Contains(sql, "UPSERT $attempt_rid SET") {
					return nil, errors.New("fixed run operands missing")
				}
				supplied = extractionRunRec{RunID: vars.RunID, Repo: vars.Repo, Commit: vars.Commit,
					UnitDigest: vars.UnitDigest, Domain: vars.Domain, PartitionActive: vars.PartitionActive, Status: "staged"}
				switch mode {
				case "lost":
					return nil, context.DeadlineExceeded
				case "known_error":
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "writer refusal"}}, nil
				case "decode":
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: "not run rows"}}, nil
				case "guard_false":
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []extractionRunRec{}}}, nil
				default:
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []extractionRunRec{supplied}}}, nil
				}
			}
			if mode == "canceled" {
				cancel()
			}
			if mode == "invalid" {
				scope.Repository = ""
			}
			state := &Surreal{db: db, accounting: owner}
			var run *ExtractionRun
			var err error
			if mode == "partitioned" {
				run, err = state.BeginPartitionedExtractionRun(ctx, scope, "neutral-v1",
					"sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), "",
					PartitionedExtractionRunLimits{Facts: 1, Rows: 2, References: 1})
			} else {
				run, err = state.BeginExtractionRun(ctx, scope, "neutral-v1")
			}
			wantSuccess := mode == "ordinary" || mode == "partitioned" || mode == "nil_owner"
			if (err == nil) != wantSuccess || wantSuccess && (run == nil || run.ID != supplied.RunID || run.PartitionActive != (mode == "partitioned")) {
				t.Fatalf("run=%+v error=%v", run, err)
			}
			wantCalls, wantTX := 1, uint64(1)
			if mode == "canceled" || mode == "invalid" {
				wantCalls, wantTX = 0, 0
			}
			if mode == "nil_owner" {
				wantTX = 0
			}
			prefix, _ := controller.Snapshot()
			if calls != wantCalls || prefix.Transactions != wantTX || prefix.Rows != 2*wantTX {
				t.Fatalf("calls=%d prefix=%+v", calls, prefix)
			}
		})
	}
}

func TestEvidenceRuntimeAccountingAbort(t *testing.T) {
	for _, mode := range []string{"success", "guard_false", "missing", "retry", "retry_exhausted", "known_error", "lost", "cancel_after_read"} {
		t.Run(mode, func(t *testing.T) {
			base, owner, controller := storeAccountingFixture(t, 40, 2)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			db, native := storeAccountingDB(t, base, owner)
			scope := validOutcome().Scope
			run := extractionRunRec{RunID: "neutral-run", Repo: scope.Repository, Commit: scope.Commit,
				UnitDigest: scope.UnitDigest, Domain: scope.Domain, Status: "staged"}
			reads, writes := 0, 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				sql := request.Params[0].(string)
				if !strings.HasPrefix(sql, "BEGIN;") {
					reads++
					if mode == "cancel_after_read" {
						cancel()
					}
					if mode == "missing" {
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: []extractionRunRec{}}}, nil
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []extractionRunRec{run}}}, nil
				}
				writes++
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != uint64(writes) || prefix.Rows != 2*uint64(writes) {
					return nil, errors.New("abort preceded two-operand admission")
				}
				var vars struct {
					RID     models.RecordID `json:"rid"`
					Attempt models.RecordID `json:"attempt_rid"`
				}
				encoded, err := native.codec.Marshal(request.Params[1])
				if err != nil {
					return nil, err
				}
				if err := native.codec.Unmarshal(encoded, &vars); err != nil {
					return nil, err
				}
				if !reflect.DeepEqual(vars.RID, extractionRunID(run.RunID)) || !reflect.DeepEqual(vars.Attempt, extractionAttemptID(scope)) ||
					!strings.Contains(sql, "UPDATE $rid SET") || !strings.Contains(sql, "UPDATE $attempt_rid SET") {
					return nil, errors.New("abort changed its run/attempt operands")
				}
				if mode == "lost" {
					return nil, context.DeadlineExceeded
				}
				if mode == "retry_exhausted" || mode == "retry" && writes == 1 {
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "native retry conflict"}}, nil
				}
				if mode == "known_error" {
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "writer refusal"}}, nil
				}
				if mode == "guard_false" {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []extractionRunRec{}}}, nil
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []extractionRunRec{run}}}, nil
			}
			err := (&Surreal{db: db, accounting: owner}).AbortExtractionRun(ctx, run.RunID)
			wantReads, wantWrites := 1, 1
			switch mode {
			case "missing", "cancel_after_read":
				wantWrites = 0
			case "guard_false":
				wantReads = 2
			case "retry":
				wantWrites = 2
			case "retry_exhausted":
				wantWrites = maxQueueRetries
			}
			prefix, _ := controller.Snapshot()
			if (err == nil) != (mode == "success" || mode == "retry") || reads != wantReads || writes != wantWrites ||
				prefix.Transactions != uint64(wantWrites) || prefix.Rows != 2*uint64(wantWrites) {
				t.Fatalf("reads=%d writes=%d prefix=%+v error=%v", reads, writes, prefix, err)
			}
		})
	}
}

func TestEvidenceRuntimeAccountingReads(t *testing.T) {
	for _, mode := range []string{"run", "outcome", "repair", "repair_empty", "invalid_outcome", "missing_run", "invalid_repair"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			outcome := validOutcome()
			outcome.Generation.Digest = ComputeExtractionGenerationDigest(outcome.Generation)
			scope := outcome.Scope
			calls := 0
			native.call = func(context.Context, *connection.RPCRequest) (any, error) {
				calls++
				var rows any
				switch mode {
				case "run":
					rows = []extractionRunRec{{RunID: "neutral-run"}}
				case "outcome", "invalid_outcome":
					if mode == "invalid_outcome" {
						outcome.Generation.Digest = "sha256:" + strings.Repeat("0", 64)
					}
					rows = []extractionDomainOutcomeRec{{Repo: scope.Repository, Commit: scope.Commit, UnitDigest: scope.UnitDigest, Domain: scope.Domain,
						Disposition: outcome.Disposition, Generation: outcome.Generation, RunID: outcome.RunID,
						ReceiptSchema: outcome.ReceiptSchema, Receipt: outcome.Receipt, RecordedAt: time.Now().UTC()}}
				case "repair":
					rows = []map[string]int{{"count": 1}}
				default:
					rows = []any{}
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
			}
			state := &Surreal{db: db, accounting: owner}
			var err error
			switch mode {
			case "run", "missing_run":
				_, err = state.getRun(ctx, "neutral-run")
			case "outcome", "invalid_outcome":
				_, err = state.LatestExtractionDomainOutcome(ctx, scope)
			default:
				publication := t305CandidatePublication(scope.Repository, scope.Commit, scope.UnitDigest,
					outcome.Generation.CandidatePolicyDigest, outcome.Generation.CandidateManifestDigest, "sha256:"+strings.Repeat("f", 64))
				publication.ControlRevision, publication.PublishedAt = 1, time.Now().UTC()
				if mode == "invalid_repair" {
					publication.ControlRevision = 0
				}
				var needed bool
				needed, err = state.CandidateControlRepairNeeded(ctx, publication)
				if needed != (mode == "repair") {
					t.Fatalf("repair=%v error=%v", needed, err)
				}
			}
			wantCalls := 1
			if mode == "invalid_repair" {
				wantCalls = 0
			}
			wantErr := mode == "invalid_outcome" || mode == "missing_run" || mode == "invalid_repair"
			prefix, _ := controller.Snapshot()
			if (err != nil) != wantErr || calls != wantCalls || prefix.Transactions != 0 || prefix.Rows != 0 {
				t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
		})
	}
}

func TestEvidenceRuntimeAccountingSourceCoverage(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "evidence.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"beginExtractionRun": "storeWrite", "getRun": "storeRead",
		"AbortExtractionRun": "storeWrite", "LatestExtractionDomainOutcome": "storeRead", "CandidateControlRepairNeeded": "storeRead"}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || want[function.Name.Name] == "" {
			continue
		}
		queries := 0
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			generic, ok := call.Fun.(*ast.IndexExpr)
			if !ok {
				return true
			}
			callee, ok := generic.X.(*ast.Ident)
			if !ok || callee.Name != "storeQuery" {
				t.Errorf("unaccounted generic call in %s", function.Name)
				return true
			}
			queries++
			owner, ok := call.Args[1].(*ast.SelectorExpr)
			if !ok || owner.Sel.Name != "accounting" {
				t.Errorf("uncaptured owner in %s", function.Name)
			}
			recipe, ok := call.Args[len(call.Args)-1].(*ast.CallExpr)
			if !ok {
				t.Errorf("missing source recipe in %s", function.Name)
				return true
			}
			name, ok := recipe.Fun.(*ast.Ident)
			if !ok || name.Name != want[function.Name.Name] {
				t.Errorf("wrong recipe in %s", function.Name)
			}
			if want[function.Name.Name] == "storeWrite" {
				if len(recipe.Args) != 1 {
					t.Errorf("wrong fixed target arity in %s", function.Name)
				} else if count, ok := recipe.Args[0].(*ast.BasicLit); !ok || count.Value != "2" {
					t.Errorf("wrong fixed target count in %s", function.Name)
				}
			}
			return true
		})
		if queries != 1 {
			t.Errorf("%s has %d source queries, want 1", function.Name, queries)
		}
		delete(want, function.Name.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing source functions: %v", want)
	}
}
