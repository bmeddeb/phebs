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
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestGenerationAccountingPreparationPriorReadRefusesBeforeSDK(t *testing.T) {
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{StoreWriteAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The existing W gate permits this attempt, but the added S gate must
	// refuse before even an empty prior census touches this absent SDK handle.
	_, enqueueErr := (&Surreal{}).EnqueueGenerationSchedule(ctx,
		generationSpec("example.com/acme/gated-prior", "sha256:"+strings.Repeat("d", 64)))
	counts, finishErr := ledger.Finish()
	if enqueueErr == nil || finishErr == nil || counts != (readaccounting.Counts{StoreReadAttempts: 1, StoreWriteAttempts: 1}) {
		t.Fatalf("prior gate counts=%+v enqueue=%v finish=%v", counts, enqueueErr, finishErr)
	}
}

func generationAccountingCensusReply(controls int, rows any) []surrealdb.QueryResult[any] {
	results := make([]surrealdb.QueryResult[any], controls+1)
	for index := range results {
		results[index].Status = "OK"
	}
	results[controls].Result = rows
	return results
}

func TestGenerationAccountingRetrySubmittedOperands(t *testing.T) {
	for _, test := range []struct {
		name    string
		attempt int
		current bool
		wantErr error
	}{
		{"successor", 0, true, nil},
		{"exhausted", 2, true, ErrGenerationExhausted},
		{"stale", 2, false, ErrGenerationStale},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			digest := "sha256:" + strings.Repeat("a", 64)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if native.calls == 1 {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []GenerationSchedule{{Digest: digest, MaxAttempts: 3}}}}, nil
				}
				if native.calls == 3 && test.wantErr == nil {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{map[string]any{
						"id": models.NewRecordID("generation_schedule_chunk", "successor"), "attempt": 1,
					}}}}, nil
				}
				sql, ok := request.Params[0].(string)
				if !ok || strings.Count(sql, "UPDATE ") != 3 || strings.Count(sql, "CREATE generation_schedule_chunk CONTENT") != 1 {
					return nil, errors.New("retry submitted mutation bodies changed")
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != 1 || prefix.Rows != 4 || prefix.MaximumRows != 4 {
					t.Errorf("four supplied retry operands lack exact ACK: %+v, %v", prefix, err)
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{map[string]any{
					"owned": true, "current": test.current, "exhausted": test.attempt == 2,
				}}}}, nil
			}
			_, err := (&Surreal{db: db, accounting: owner}).RetryGenerationChunk(ctx, GenerationChunk{
				ID: "actual", ScheduleDigest: digest, Repository: "example.com/acme/retry", Stage: "source-observation",
				Status: GenerationChunkRunning, LeaseToken: "lease", ClaimedBy: "worker", Attempt: test.attempt,
			}, "neutral failure", time.Now())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("retry error=%v; want %v", err, test.wantErr)
			}
		})
	}
}

func TestGenerationAccountingCensusShapeAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name     string
		results  []surrealdb.QueryResult[[]int]
		canceled bool
		wantErr  bool
	}{
		{"empty", []surrealdb.QueryResult[[]int]{{Status: "OK"}, {Status: "OK", Result: []int{}}}, false, false},
		{"short", []surrealdb.QueryResult[[]int]{{Status: "OK", Result: []int{}}}, false, true},
		{"extra", []surrealdb.QueryResult[[]int]{{Status: "OK"}, {Status: "OK"}, {Status: "OK", Result: []int{}}}, false, true},
		{"control_data", []surrealdb.QueryResult[[]int]{{Status: "OK", Result: []int{1}}, {Status: "OK", Result: []int{}}}, false, true},
		{"control_error", []surrealdb.QueryResult[[]int]{{Status: "ERR"}, {Status: "OK", Result: []int{}}}, false, true},
		{"final_null", []surrealdb.QueryResult[[]int]{{Status: "OK"}, {Status: "OK"}}, false, true},
		{"canceled_empty", []surrealdb.QueryResult[[]int]{{Status: "OK"}, {Status: "OK", Result: []int{}}}, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			_, err := generationCensusRows(ctx, &test.results, 1)
			if (err != nil) != test.wantErr || test.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("census shape/cancel error=%v", err)
			}
		})
	}
}

func TestGenerationAccountingSourceCoverage(t *testing.T) {
	unsupported := map[string]bool{}
	for path, want := range map[string]int{
		"generation_schedule.go": 26, "generation_lifecycle.go": 3,
		"job_lifecycle.go": 3, "partitioned_evidence.go": 10, "partitioned_assertions.go": 3,
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
					switch selector.Sel.Name {
					case "Query", "Begin", "Commit", "Cancel":
						t.Fatalf("%s bypass at %s", fn.Name.Name, set.Position(call.Pos()))
					}
				}
				name, ok := target.(*ast.Ident)
				if !ok || name.Name != "storeQuery" && name.Name != "queryGenerationSchedule" {
					return true
				}
				wantArgs := 6
				if name.Name == "queryGenerationSchedule" {
					wantArgs = 7
				}
				if len(call.Args) != wantArgs {
					t.Fatal("missing source recipe")
				}
				var actual bytes.Buffer
				if err := format.Node(&actual, set, call.Args[1]); err != nil {
					t.Fatal(err)
				}
				wantOwner := "s.accounting"
				if fn.Name.Name == "queryGenerationSchedule" {
					wantOwner = "owner"
				} else if fn.Recv != nil && strings.HasPrefix(fn.Recv.List[0].Names[0].Name, "reader") {
					wantOwner = "reader.store.accounting"
				}
				if actual.String() != wantOwner {
					t.Fatalf("%s owner=%s", fn.Name.Name, &actual)
				}
				actual.Reset()
				if err := format.Node(&actual, set, call.Args[len(call.Args)-1]); err != nil {
					t.Fatal(err)
				}
				if actual.String() == "storeUnsupported()" {
					unsupported[fn.Name.Name] = true
				}
				if fn.Name.Name == "ReadGenerationStaleLeaseTransition" && actual.String() != "storeWrite(0)" {
					t.Fatal("explicit read transaction lost its zero-row transaction attempt")
				}
				count++
				return true
			})
		}
		if count != want {
			t.Fatalf("%s calls=%d want%d", path, count, want)
		}
	}
	if len(unsupported) != 2 || !unsupported["FenceCurrentGenerationScheduleForDiagnostic"] || !unsupported["deletePartitionedPins"] {
		t.Fatalf("unclosed source boundaries=%v", unsupported)
	}
}

func TestGenerationAccountingPinOverflowPreservesOrdinaryAtomicity(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(fmt.Sprintf("selected_%t", selected), func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			if !selected {
				owner = nil
			}
			db, native := storeAccountingDB(t, ctx, owner)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if native.calls == 1 {
					ids := make([]models.RecordID, 513)
					for index := range ids {
						ids[index] = models.NewRecordID("evidence_pin", fmt.Sprintf("pin-%d", index))
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: ids}}, nil
				}
				if selected || request.Params[0] != "DELETE evidence_pin WHERE kind = $owner RETURN BEFORE" {
					return nil, errors.New("overflow changed ordinary atomic query or escaped selected refusal")
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}}, nil
			}
			err := (&Surreal{db: db, accounting: owner}).UnpinPartitionedExtractionOwner(ctx, "neutral")
			if (err != nil) != selected || selected && native.calls != 1 || !selected && native.calls != 2 {
				t.Fatalf("overflow calls=%d err=%v", native.calls, err)
			}
			prefix, prefixErr := controller.Snapshot()
			if !selected && prefixErr != nil || selected && prefix.Complete || prefix.Transactions != 0 || prefix.Rows != 0 {
				t.Fatalf("unknown overflow invented prefix=%+v/%v", prefix, prefixErr)
			}
		})
	}
}

func TestGenerationAccountingCensusRefusalAndLostReply(t *testing.T) {
	for _, test := range []struct {
		name string
		ids  []models.RecordID
		lost bool
	}{
		{"missing_array", nil, false},
		{"wrong_table", []models.RecordID{models.NewRecordID("repo", "wrong")}, false},
		{"lost_reply", []models.RecordID{models.NewRecordID(string(JobSync), "actual")}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			native.call = func(_ context.Context, _ *connection.RPCRequest) (any, error) {
				if native.calls == 1 {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: test.ids}}, nil
				}
				return nil, errors.New("neutral lost native reply")
			}
			s := &Surreal{db: db, accounting: owner}
			if _, err := s.DeleteOldestTerminalJobs(ctx, JobSync, nil, 16); err == nil {
				t.Fatal("invalid census or unknown completion accepted")
			}
			prefix, err := controller.Snapshot()
			want := uint64(0)
			if test.lost {
				want = 1
				if _, retryErr := s.DeleteOldestTerminalJobs(ctx, JobSync, nil, 16); retryErr == nil {
					t.Fatal("lost reply allowed another source submission")
				}
			}
			if !test.lost && err != nil || test.lost && prefix.Complete ||
				prefix.Transactions != want || prefix.Rows != want || native.calls != 1+int(want) {
				t.Fatalf("refusal/lost prefix=%+v/%v calls%d", prefix, err, native.calls)
			}
		})
	}
}

func TestGenerationAccountingEnqueueAndClaimOperands(t *testing.T) {
	if !strings.Contains(enqueueGenerationSchedulePriorSQL, "LET $prior_ids = SELECT VALUE id") ||
		strings.Contains(enqueueGenerationSchedulePriorSQL, "LET $prior_ids = IF") {
		t.Fatal("prior identity census is not the native array-valued selection")
	}
	spec := generationSpec("example.com/acme/queue", "sha256:"+strings.Repeat("b", 64))
	digest := generationScheduleDigest(spec)
	now := time.Now().UTC()
	schedule := GenerationSchedule{
		Schema: GenerationScheduleSchema, Digest: digest, Repository: spec.Repository,
		Stage: spec.Stage, Generation: spec.Generation, ResourceClass: spec.ResourceClass,
		TotalItems: spec.TotalItems, ChunkItems: spec.ChunkItems, TotalChunks: 65,
		MaxAttempts: spec.MaxAttempts, RepositoryTokens: spec.RepositoryTokens,
		Status: GenerationScheduleActive, CreatedAt: now, UpdatedAt: now,
	}
	for _, priorPresent := range []bool{false, true} {
		t.Run(fmt.Sprintf("enqueue_prior_%t", priorPresent), func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			priorIDs := []models.RecordID{}
			var oldDigest any = models.None
			if priorPresent {
				oldDigest = "sha256:" + strings.Repeat("c", 64)
				priorIDs = append(priorIDs, models.NewRecordID("generation_schedule", []any{"actual-native-id", int64(7)}))
			}
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if native.calls == 1 {
					return generationAccountingCensusReply(2, []any{map[string]any{
						"old_digest": oldDigest, "ids": priorIDs,
					}}), nil
				}
				raw, ok := request.Params[1].(cbor.RawMessage)
				if !ok {
					return nil, errors.New("enqueue has no owned payload")
				}
				var fields map[string]cbor.RawMessage
				if err := native.codec.Unmarshal(raw, &fields); err != nil {
					return nil, err
				}
				if priorPresent {
					actual, err := native.codec.Marshal(priorIDs[0])
					if err != nil || !bytes.Equal(actual, fields["prior_schedule"]) {
						return nil, errors.New("enqueue replaced actual prior identity")
					}
				}
				prefix, err := controller.Snapshot()
				want := uint64(3 + len(priorIDs))
				if err != nil || prefix.Transactions != 1 || prefix.Rows != want || prefix.MaximumRows != want {
					return nil, errors.New("enqueue supplied vector not ACKed")
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []GenerationSchedule{schedule}}}, nil
			}
			if _, err := (&Surreal{db: db, accounting: owner}).EnqueueGenerationSchedule(ctx, spec); err != nil || native.calls != 2 {
				t.Fatalf("enqueue calls%d err%v", native.calls, err)
			}
		})
	}
	for _, choice := range []string{"none", "one", "conflict"} {
		t.Run("claim_"+choice, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			selected, writes := 0, 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if native.calls == 1 {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []GenerationSchedule{schedule}}}, nil
				}
				sql, _ := request.Params[0].(string)
				if !strings.HasPrefix(sql, "BEGIN;") {
					selected++
					ids := []models.RecordID{}
					if choice != "none" {
						ids = append(ids, models.NewRecordID("generation_schedule_chunk", fmt.Sprintf("actual-choice-%d", selected)))
					}
					return generationAccountingCensusReply(5, ids), nil
				}
				writes++
				var payload struct {
					Chunk models.RecordID `json:"chunk"`
				}
				raw, ok := request.Params[1].(cbor.RawMessage)
				if !ok || native.codec.Unmarshal(raw, &payload) != nil || payload.Chunk.ID != fmt.Sprintf("actual-choice-%d", selected) {
					return nil, errors.New("claim did not submit actual current choice")
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != uint64(writes) || prefix.Rows != uint64(writes*3) || prefix.MaximumRows != 3 {
					return nil, errors.New("claim exact prefix missing")
				}
				if choice == "conflict" && writes == 1 {
					return nil, &surrealdb.QueryError{Message: "generation claim candidate conflict"}
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}}, nil
			}
			_, err := (&Surreal{db: db, accounting: owner}).ClaimGenerationChunk(ctx, spec.ResourceClass, "neutral-worker")
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("claim refusal=%v", err)
			}
			wantWrites := 1
			switch choice {
			case "none":
				wantWrites = 0
			case "conflict":
				wantWrites = 2
			}
			if writes != wantWrites || selected != max(1, wantWrites) {
				t.Fatalf("claim selected%d writes%d", selected, writes)
			}
		})
	}
}

// These real SDK/SA01 boundary tests inspect the owned native payload before
// its scripted response. Native scheduler semantics remain engine-test work.
func TestGenerationAccountingLeaseTargets(t *testing.T) {
	now := time.Now().UTC()
	chunk := GenerationChunk{
		ID: "actual-chunk", ScheduleDigest: "sha256:" + strings.Repeat("a", 64),
		Repository: "example.com/acme/generation", Stage: "observation",
		LeaseToken: "actual-lease", ClaimedBy: "actual-worker",
		Status: GenerationChunkRunning, HeartbeatAt: &now,
	}
	for _, test := range []struct {
		name   string
		fields []string
		invoke func(context.Context, *Surreal) error
	}{
		{"heartbeat", []string{"chunk"}, func(ctx context.Context, s *Surreal) error {
			return s.HeartbeatGenerationChunk(ctx, chunk)
		}},
		{"fail", []string{"chunk", "schedule", "repository_state"}, func(ctx context.Context, s *Surreal) error {
			return s.FailGenerationChunk(ctx, chunk, "neutral")
		}},
		{"defer", []string{"chunk", "schedule", "repository_state"}, func(ctx context.Context, s *Surreal) error {
			return s.DeferGenerationChunk(ctx, chunk, "neutral", time.Second)
		}},
		{"release", []string{"chunk", "schedule", "repository_state"}, func(ctx context.Context, s *Surreal) error {
			return s.ReleaseGenerationChunk(ctx, chunk, "neutral")
		}},
		{"reap", []string{"chunk", "schedule", "repository_state"}, func(ctx context.Context, s *Surreal) error {
			return s.releaseStaleGenerationChunk(ctx, chunk, now.Add(time.Second))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Method != "query" || len(request.Params) != 2 {
					return nil, errors.New("unexpected generation native call")
				}
				raw, ok := request.Params[1].(cbor.RawMessage)
				if !ok {
					return nil, errors.New("generation payload is not owned")
				}
				var payload map[string]cbor.RawMessage
				if err := native.codec.Unmarshal(raw, &payload); err != nil {
					return nil, err
				}
				for _, field := range test.fields {
					var id models.RecordID
					if err := native.codec.Unmarshal(payload[field], &id); err != nil || id.Table == "" || id.ID == nil {
						return nil, errors.New("actual supplied generation target is absent")
					}
				}
				prefix, err := controller.Snapshot()
				want := uint64(len(test.fields))
				if err != nil || prefix.Transactions != 1 || prefix.Rows != want || prefix.MaximumRows != want {
					return nil, errors.New("generation target prefix absent at native send")
				}
				// Guard refusal changes no native row, but it cannot erase the
				// actual fixed record operands submitted in this attempt.
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}}, nil
			}
			if err := test.invoke(ctx, &Surreal{db: db, accounting: owner}); !errors.Is(err, ErrGenerationLeaseLost) {
				t.Fatalf("guard refusal=%v", err)
			}
			if native.calls != 1 {
				t.Fatalf("native calls=%d", native.calls)
			}
		})
	}
}

func TestGenerationAccountingResourceClassMigration(t *testing.T) {
	for _, test := range []struct {
		name, version string
		wantCalls     int
		wantWrite     bool
		wantError     bool
	}{
		{"cold", "", 3, true, false},
		{"warm", generationResourceClassMigrationVersion, 1, false, false},
		{"future", "future-schema", 1, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			writes := uint64(0)
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Method != "query" || len(request.Params) != 2 {
					return nil, errors.New("unexpected migration native call")
				}
				sql, ok := request.Params[0].(string)
				if !ok {
					return nil, errors.New("missing migration statement")
				}
				result := []generationResourceClassMigrationState{}
				if strings.Contains(sql, "DEFINE FIELD OVERWRITE") {
					if !test.wantWrite || writes != 0 || !strings.HasPrefix(sql, "\nBEGIN;") ||
						!strings.HasSuffix(sql, "COMMIT;") || strings.Count(sql, "DEFINE FIELD OVERWRITE") != 2 ||
						strings.Index(sql, "THROW 'phebs-permanent:") > strings.Index(sql, "DEFINE FIELD OVERWRITE") {
						return nil, errors.New("migration lost its guarded three-target atomic group")
					}
					writes++
				} else if test.version != "" {
					result = append(result, generationResourceClassMigrationState{Version: test.version})
				} else if writes != 0 {
					result = append(result, generationResourceClassMigrationState{Version: generationResourceClassMigrationVersion})
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != writes || prefix.Rows != writes*3 || prefix.MaximumRows != writes*3 {
					return nil, errors.New("migration source prefix is not exact")
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
			}
			err := (&Surreal{db: db, accounting: owner}).migrateGenerationResourceClasses(ctx)
			if (err != nil) != test.wantError || native.calls != test.wantCalls {
				t.Fatalf("migration calls=%d err=%v", native.calls, err)
			}
		})
	}
}

func TestGenerationAccountingNativeSelectedTargets(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	for _, test := range []struct {
		name, table string
		count       int
		controls    int
		eligible    bool
		generation  bool
		invoke      func(context.Context, *Surreal) error
	}{
		{"jobs_empty", string(JobSync), 0, 0, true, false, func(ctx context.Context, s *Surreal) error {
			_, err := s.DeleteOldestTerminalJobs(ctx, JobSync, nil, 16)
			return err
		}},
		{"jobs_sixteen", string(JobSync), 16, 0, true, false, func(ctx context.Context, s *Surreal) error {
			_, err := s.DeleteOldestTerminalJobs(ctx, JobSync, nil, 16)
			return err
		}},
		{"generation_ineligible", "generation_schedule_chunk", 0, 1, false, true, func(ctx context.Context, s *Surreal) error {
			_, err := s.collectGenerationSchedule(ctx, generationLifecycleCandidate{Digest: digest, Generation: digest, Repository: "neutral", Stage: "observation"}, 16, 2)
			return err
		}},
		{"generation_unexpanded", "generation_schedule_chunk", 0, 1, true, true, func(ctx context.Context, s *Surreal) error {
			_, err := s.collectGenerationSchedule(ctx, generationLifecycleCandidate{Digest: digest, Generation: digest, Repository: "neutral", Stage: "observation"}, 16, 2)
			return err
		}},
		{"generation_v3", "generation_schedule_chunk", 14, 2, true, true, func(ctx context.Context, s *Surreal) error {
			_, err := s.collectGenerationSchedule(ctx, generationLifecycleCandidate{Digest: digest, Generation: digest, Repository: "neutral", Stage: ServiceStateV3ReconcileStage}, 16, 2)
			return err
		}},
		{"pins_empty", "evidence_pin", 0, 0, true, false, func(ctx context.Context, s *Surreal) error {
			return s.UnpinPartitionedExtractionOwner(ctx, "neutral")
		}},
		{"pins_full", "evidence_pin", 512, 0, true, false, func(ctx context.Context, s *Surreal) error {
			return s.ReconcilePartitionedExtractionOwners(ctx, nil)
		}},
		{"unrooted_empty", "extraction_run", 0, 0, true, false, func(ctx context.Context, s *Surreal) error {
			_, err := s.ReleaseOneUnrootedPartitionRun(ctx)
			return err
		}},
		{"unrooted_one", "extraction_run", 1, 0, true, false, func(ctx context.Context, s *Surreal) error {
			_, err := s.ReleaseOneUnrootedPartitionRun(ctx)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			ids := make([]models.RecordID, test.count)
			for index := range ids {
				ids[index] = models.NewRecordID(test.table, []any{"native-composite", int64(index)})
			}
			writes := 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Method != "query" || len(request.Params) != 2 {
					return nil, errors.New("unexpected native census call")
				}
				var result any
				controls := 0
				if native.calls == 1 {
					result = ids
					if test.generation {
						controls = 9
						result = []any{}
						if test.eligible {
							result = []any{map[string]any{"ids": ids}}
						}
					} else if test.table == "extraction_run" {
						controls = 1
					}
				} else {
					writes++
					raw, ok := request.Params[1].(cbor.RawMessage)
					if !ok {
						return nil, errors.New("census targets not owned")
					}
					var fields map[string]cbor.RawMessage
					if err := native.codec.Unmarshal(raw, &fields); err != nil {
						return nil, err
					}
					if test.table != "extraction_run" {
						actual, err := native.codec.Marshal(ids)
						if err != nil || !bytes.Equal(actual, fields["ids"]) {
							return nil, errors.New("actual native census IDs changed")
						}
					}
					result = []any{map[string]any{"deleted": 0}}
				}
				prefix, err := controller.Snapshot()
				want := uint64(writes * (test.count + test.controls))
				if err != nil || prefix.Transactions != uint64(writes) || prefix.Rows != want || prefix.MaximumRows != want {
					return nil, fmt.Errorf("census prefix=%+v want%d", prefix, want)
				}
				return generationAccountingCensusReply(controls, result), nil
			}
			if err := test.invoke(ctx, &Surreal{db: db, accounting: owner}); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if test.eligible && test.count+test.controls != 0 {
				wantCalls++
			}
			if native.calls != wantCalls {
				t.Fatalf("native calls=%d want%d", native.calls, wantCalls)
			}
		})
	}
}
