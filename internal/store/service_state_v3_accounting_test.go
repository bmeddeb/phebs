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
	"reflect"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// Scripted replies establish the real SA01/native-send boundary, not native
// state validity. Existing engine compatibility tests own the latter claim.
type stateAccountingStep struct {
	restoreClearStep
	vector string
}

func stateAccountingScript(t *testing.T, steps []stateAccountingStep) (context.Context, *Surreal, *storeaccounting.Controller) {
	t.Helper()
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	index := 0
	var transactions, rows, maximum uint64
	native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
		if request.Method != "query" || len(request.Params) != 2 || index >= len(steps) {
			return nil, errors.New("unexpected state accounting submission")
		}
		step := steps[index]
		sql, ok := request.Params[0].(string)
		if !ok || !strings.Contains(sql, step.contains) {
			return nil, errors.New("state accounting source query changed")
		}
		raw, ok := request.Params[1].(cbor.RawMessage)
		if !ok {
			return nil, errors.New("state accounting payload is not owned CBOR")
		}
		var payload struct {
			PriorDigest string              `json:"prior_digest"`
			IDs         []models.RecordID   `json:"ids"`
			Repos       []restoreCallerRepo `json:"repos"`
			FutureIDs   []models.RecordID   `json:"future_ids"`
			SummaryIDs  []models.RecordID   `json:"summary_preimage_ids"`
			Updates     []struct {
				RID            models.RecordID `json:"rid"`
				PreimageRID    models.RecordID `json:"preimage_rid"`
				CreatePreimage bool            `json:"create_preimage"`
			} `json:"updates"`
			PreimageIDs   []models.RecordID `json:"preimage_ids"`
			WriteSummary  bool              `json:"write_summary"`
			CreateSummary bool              `json:"create_summary_preimage"`
		}
		if err := native.codec.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		actual := uint64(0)
		switch step.vector {
		case "plan":
			if strings.Contains(sql, "UPDATE $prior_rid") != (payload.PriorDigest != "") {
				t.Error("inactive prior-plan mutation was submitted")
				return nil, errors.New("inactive prior-plan mutation was submitted")
			}
			actual = 1
			if payload.PriorDigest != "" {
				actual++
			}
		case "chunk":
			actual = uint64(1 + len(payload.Updates) + len(payload.PreimageIDs))
			preimageIDs := []models.RecordID{}
			for _, update := range payload.Updates {
				if update.CreatePreimage {
					preimageIDs = append(preimageIDs, update.RID)
				}
			}
			if !reflect.DeepEqual(preimageIDs, payload.PreimageIDs) ||
				!strings.Contains(sql, "FOR $rid IN $preimage_ids") ||
				strings.Count(sql, "CREATE service_state_v3_preimage CONTENT") != 1 {
				t.Error("preimage CREATE must consume only the supplied selected vector")
				return nil, errors.New("preimage CREATE must consume only the supplied selected vector")
			}
			writesSummary := strings.Contains(sql, "UPSERT $summary_rid CONTENT")
			createsSummary := strings.Contains(sql, "CREATE service_state_v3_repository_preimage CONTENT")
			if writesSummary != payload.WriteSummary || createsSummary != payload.CreateSummary {
				t.Error("inactive fixed summary mutation was submitted")
				return nil, errors.New("inactive fixed summary mutation was submitted")
			}
			if writesSummary {
				actual++
			}
			if createsSummary {
				actual++
			}
		case "ids":
			actual = uint64(len(payload.IDs))
		case "caller":
			actual = uint64(len(payload.IDs) + len(payload.Repos))
		case "future":
			actual = uint64(len(payload.FutureIDs))
		case "pairs":
			actual = uint64(2 * len(payload.Updates))
		case "summary":
			actual = uint64(1 + len(payload.SummaryIDs))
		case "fixed":
			actual = 1
		case "":
		default:
			return nil, errors.New("unknown test payload vector")
		}
		if step.vector != "" {
			transactions++
			rows += actual
			maximum = max(maximum, actual)
		}
		prefix, err := controller.Snapshot()
		if err != nil || prefix.Transactions != transactions || prefix.Rows != rows || prefix.MaximumRows != maximum {
			return nil, fmt.Errorf("native payload vector lacks exact ACK prefix: %+v, expected %d/%d/%d", prefix, transactions, rows, maximum)
		}
		if step.ids != nil && !reflect.DeepEqual(step.ids, payload.IDs) {
			return nil, errors.New("native record identities changed")
		}
		index++
		if step.err != nil {
			return nil, step.err
		}
		return []surrealdb.QueryResult[any]{{Status: "OK", Result: step.rows}}, nil
	}
	t.Cleanup(func() {
		if index != len(steps) {
			t.Errorf("state submissions=%d; want %d", index, len(steps))
		}
	})
	return ctx, &Surreal{db: db, accounting: owner}, controller
}

func TestServiceStateV3AccountingPlanAndPrefixes(t *testing.T) {
	for _, priorPresent := range []bool{false, true} {
		t.Run(fmt.Sprintf("plan_prior_%t", priorPresent), func(t *testing.T) {
			plan, _, _ := serviceStateV3HeadroomFixture(t, serviceStateV3Reconcile, 1)
			var prior *ServiceStateV3Plan
			if priorPresent {
				copy := plan
				prior = &copy
			}
			ctx, s, _ := stateAccountingScript(t, []stateAccountingStep{{restoreClearStep: restoreClearStep{
				contains: "CREATE $plan_rid", rows: []any{serviceStateV3PlanContent(plan)},
			}, vector: "plan"}})
			if err := s.createServiceStateV3Plan(ctx, plan, prior, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, test := range []struct {
		name, phase     string
		count           int
		preimages       int
		summaryPreimage bool
	}{
		{"unchanged_summary", serviceStateV3Activate, 0, 0, false},
		{"member9_single_change", serviceStateV3Activate, 1, 0, false},
		{"activation_512", serviceStateV3Activate, 512, 0, false},
		{"reconcile_511", serviceStateV3Reconcile, 511, 0, false},
		{"reconcile_512", serviceStateV3Reconcile, 512, 0, false},
		{"reconcile_noop", serviceStateV3Reconcile, 0, 0, false},
		{"activation_preimages_255", serviceStateV3Activate, 255, 255, true},
		{"reconcile_preimages_256", serviceStateV3Reconcile, 256, 256, true},
		{"mixed_preimages", serviceStateV3Reconcile, 511, 1, true},
		{"existing_summary_preimage", serviceStateV3Activate, 511, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, changes, summary := serviceStateV3HeadroomFixture(t, test.phase, test.count)
			preimages := make([]bool, len(changes))
			for index := range preimages {
				preimages[index] = index < test.preimages
			}
			writes, count, err := serviceStateV3ChunkWrites(t.Context(), plan, changes, 512, "", summary, preimages, test.summaryPreimage)
			if err != nil {
				t.Fatal(err)
			}
			var steps []stateAccountingStep
			for _, write := range writes[:count] {
				steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{
					contains: "UPDATE $plan_rid CONTENT", rows: []any{serviceStateV3PlanContent(write.plan)},
				}, vector: "chunk"})
			}
			ctx, s, _ := stateAccountingScript(t, steps)
			for _, write := range writes[:count] {
				err := s.commitServiceStateV3TargetChunk(ctx, GenerationChunk{}, plan, write.plan, write.updates, summary, write.summary,
					false, serviceStateV3WriteTargets{}, write.preimages, write.summaryPreimage, write.payloadRecords)
				if err != nil {
					t.Fatal(err)
				}
				plan = write.plan
				if write.summary != nil {
					summary = write.summary
				}
			}
		})
	}
}

func TestServiceStateV3AccountingRestoreVectors(t *testing.T) {
	for _, count := range []int{0, 1, 256, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			selector, summary, reads := restoreStateV3PureFixture(t, count)
			var steps []stateAccountingStep
			for _, read := range reads {
				steps = append(steps, stateAccountingStep{restoreClearStep: read})
			}
			if count == 0 {
				// The fixture contains one summary preimage even with no service
				// rows; retain its genuine final two-target write.
				steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{contains: restoreStateV3SummarySQL}, vector: "summary"})
			} else {
				steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{contains: restoreStateV3DeleteFutureSQL}, vector: "future"})
				for offset := 0; offset < count; offset += 256 {
					steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{contains: restoreStateV3PairsSQL}, vector: "pairs"})
				}
				steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{contains: restoreStateV3SummarySQL}, vector: "summary"})
			}
			ctx, s, _ := stateAccountingScript(t, steps)
			if err := s.restoreSelectedServiceStateV3Snapshot(ctx, selector, summary); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceStateV3AccountingClearAndMigrationVectors(t *testing.T) {
	for _, name := range []string{"delete", "repo_reset", "caller_pair", "migration"} {
		t.Run(name, func(t *testing.T) {
			table := "generation_schedule"
			if name == "repo_reset" {
				table = "repo"
			}
			if name == "caller_pair" {
				table = "caller_generation_publication"
			}
			if name == "migration" {
				table = "service_state_v3_current"
			}
			ids := []models.RecordID{models.NewRecordID(table, uint64(17)), models.NewRecordID(table, []any{"native", uint64(1)})}
			steps := []stateAccountingStep{{restoreClearStep: restoreClearStep{contains: "SELECT VALUE id", rows: ids}}}
			vector := "ids"
			if name == "caller_pair" {
				vector = "caller"
				steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{contains: "FROM repo WHERE name IN $keys", rows: []restoreCallerRepo{
					{ID: models.NewRecordID("repo", "actual"), Name: restoreClearRaw(t, uint64(17)), Revision: restoreClearRaw(t, uint64(1))},
				}}})
			}
			steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{contains: "FOR $rid IN $ids", ids: ids}, vector: vector},
				stateAccountingStep{restoreClearStep: restoreClearStep{contains: "SELECT VALUE id", rows: []models.RecordID{}}})
			if name == "repo_reset" {
				steps = append(steps, stateAccountingStep{restoreClearStep: restoreClearStep{contains: "latest_extraction_job != NONE", rows: []models.RecordID{}}})
			}
			ctx, s, _ := stateAccountingScript(t, steps)
			var err error
			switch name {
			case "delete":
				err = s.clearRestoreTable(ctx, table, "", "", map[string]any{})
			case "repo_reset":
				err = s.resetRestoreRepoProjections(ctx)
			case "caller_pair":
				err = s.clearRestoreCallerPointers(ctx, "", map[string]any{})
			case "migration":
				err = s.backfillServiceStateV3VisibleFrom(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceStateV3AccountingCandidateKeepsNativeTransaction(t *testing.T) {
	plan, _, _ := serviceStateV3HeadroomFixture(t, serviceStateV3Activate, 1)
	spec := generationSpec(plan.Repository, plan.Digest)
	spec.Stage, spec.TotalItems, spec.ChunkItems = ServiceStateV3ActivateStage, int64(plan.TotalChunks), 1
	digest, err := GenerationScheduleDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	plan.ScheduleDigest = digest
	schedule := GenerationSchedule{
		Schema: GenerationScheduleSchema, Digest: digest, Repository: spec.Repository, Stage: spec.Stage,
		Generation: spec.Generation, ResourceClass: spec.ResourceClass, TotalItems: spec.TotalItems,
		ChunkItems: 1, TotalChunks: plan.TotalChunks, MaxAttempts: spec.MaxAttempts,
		RepositoryTokens: spec.RepositoryTokens, Status: GenerationScheduleActive,
		CreatedAt: plan.CreatedAt, UpdatedAt: plan.UpdatedAt,
	}
	if err := ValidateGenerationSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	db, native := storeAccountingDB(t, ctx, owner)
	s := &Surreal{db: db, accounting: owner}
	uuid := models.UUID{}
	uuid.UUID[0] = 37
	replies := []any{[]any{}, []serviceStateV3SchedulePointerRec{{ScheduleDigest: digest}},
		[]GenerationSchedule{schedule}, []any{serviceStateV3PlanContent(plan)}, nil}
	queries := 0
	native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
		switch request.Method {
		case "begin":
			return uuid, nil
		case "commit":
			return nil, nil
		case "query":
			nativeUUID, ok := request.Txn.(*models.UUID)
			if !ok || nativeUUID == nil || *nativeUUID != uuid || queries >= len(replies) {
				return nil, errors.New("candidate fence lost the actual parent UUID")
			}
			if queries == 4 {
				raw, ok := request.Params[1].(cbor.RawMessage)
				if !ok {
					return nil, errors.New("candidate fence payload not retained")
				}
				// Only record-ID fields are decoded here; the request also
				// contains its unchanged string digest controls.
				var fields map[string]cbor.RawMessage
				if err := native.codec.Unmarshal(raw, &fields); err != nil {
					return nil, err
				}
				for _, target := range []struct{ key, table string }{
					{"plan_rid", "service_state_v3_plan"}, {"schedule_rid", "generation_schedule"}, {"current_rid", "generation_schedule_current"},
				} {
					var rid models.RecordID
					if err := native.codec.Unmarshal(fields[target.key], &rid); err != nil {
						return nil, err
					}
					if rid.Table != target.table || rid.ID == nil {
						return nil, errors.New("candidate fence changed target vector")
					}
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != 1 || prefix.Rows != 3 || prefix.MaximumRows != 3 {
					return nil, errors.New("candidate fence did not append three targets to one Begin")
				}
			}
			result := replies[queries]
			queries++
			return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
		default:
			return nil, errors.New("unexpected candidate transaction operation")
		}
	}
	tx, err := storeBegin(ctx, owner, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.fenceServiceStateV3CandidateAdvance(ctx, tx, plan.Repository, "prior", "next"); err != nil {
		t.Fatal(err)
	}
	if err := storeCommit(ctx, owner, tx); err != nil {
		t.Fatal(err)
	}
	if queries != 5 {
		t.Fatalf("candidate queries=%d", queries)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := owner.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStateV3AccountingSourceCoverage(t *testing.T) {
	writes := map[string][]string{
		"createServiceStateV3Plan":                          {"storeWrite(submittedRows)"},
		"commitServiceStateV3TargetChunk":                   {"storeWrite(uint64(actualRecords))"},
		"migrateServiceStateV3Schema":                       {"storeRead()", "storeRead()", "storeWrite(1)"},
		"migrateServiceStateV3SnapshotSchemaWithDefinition": {"storeRead()", "storeWrite(1)", "storeWrite(1)"},
		"backfillServiceStateV3VisibleFrom":                 {"storeRead()", "storeWrite(uint64(len(ids)))"},
		"migrateServiceSourceGenerationCompatibility":       {"storeWrite(1)"},
		"fenceServiceStateV3CandidateAdvance":               {"storeWrite(3)"},
		"restoreClearWrite":                                 {"storeWrite(rows)"},
	}
	seen := map[string]bool{}
	helperRows := map[string][]string{
		"clearRestoreTable":                     {"uint64(len(ids))"},
		"resetRestoreRepoProjections":           {"uint64(len(ids))"},
		"clearRestoreCallerPointers":            {"uint64(len(ids) + len(rows))"},
		"restoreSelectedServiceStateV3Snapshot": {"uint64(len(ids))", "uint64(2 * len(pairs))", "uint64(1 + len(summaryIDs))"},
	}
	helperCalls := 0
	for path, want := range map[string]int{"service_state_v3.go": 34, "service_state_v3_read.go": 9, "service_state_v3_restore.go": 3, "restore_clear.go": 3} {
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		owned := map[ast.Expr]bool{}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			index := 0
			helperIndex := 0
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if helper, ok := call.Fun.(*ast.SelectorExpr); ok && helper.Sel.Name == "restoreClearWrite" {
					if len(call.Args) != 4 || helperIndex >= len(helperRows[fn.Name.Name]) {
						t.Fatal("unreviewed restore helper submission")
					}
					var expression bytes.Buffer
					if err := format.Node(&expression, set, call.Args[3]); err != nil {
						t.Fatal(err)
					}
					if expression.String() != helperRows[fn.Name.Name][helperIndex] {
						t.Fatal("restore helper detached count from actual payload")
					}
					helperIndex++
					helperCalls++
				}
				target := call.Fun
				if generic, ok := target.(*ast.IndexExpr); ok {
					target = generic.X
				}
				name, ok := target.(*ast.Ident)
				if !ok || name.Name != "storeQuery" {
					return true
				}
				if len(call.Args) != 6 {
					t.Fatal("state query lost closed argument shape")
				}
				var owner, connection, recipe bytes.Buffer
				for _, value := range []struct {
					out  *bytes.Buffer
					expr ast.Expr
				}{{&owner, call.Args[1]}, {&connection, call.Args[2]}, {&recipe, call.Args[5]}} {
					if err := format.Node(value.out, set, value.expr); err != nil {
						t.Fatal(err)
					}
				}
				wantRecipe := "storeRead()"
				if exact, ok := writes[fn.Name.Name]; ok {
					if index >= len(exact) {
						t.Fatal("state gained an unreviewed recipe")
					}
					wantRecipe = exact[index]
					seen[fn.Name.Name] = true
				}
				if owner.String() != "s.accounting" || recipe.String() != wantRecipe || connection.String() != "s.db" && connection.String() != "tx" {
					t.Fatalf("%s query escaped its same owner/payload recipe: %s/%s/%s", fn.Name.Name, &owner, &connection, &recipe)
				}
				owned[call.Args[2]] = true
				count++
				index++
				return true
			})
			if exact, ok := writes[fn.Name.Name]; ok && index != len(exact) {
				t.Fatalf("%s recipe count changed", fn.Name.Name)
			}
			if helperIndex != len(helperRows[fn.Name.Name]) {
				t.Fatal("restore helper call disappeared")
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				if selector.Sel.Name == "db" && !owned[selector] {
					t.Error("state borrowed native connection outside annotated query")
				}
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "surrealdb" && selector.Sel.Name != "Transaction" {
					t.Error("state retained a raw SDK escape")
				}
			}
			return true
		})
		if count != want {
			t.Fatalf("%s query census=%d; want %d", path, count, want)
		}
	}
	if len(seen) != len(writes) {
		t.Fatal("state write recipe disappeared")
	}
	if helperCalls != 6 {
		t.Fatalf("restore helper calls=%d", helperCalls)
	}
}
