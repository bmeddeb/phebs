//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// Real SA01 and SDK/CBOR exercise pre-forward operands and failure ownership.
// Scripted replies are not evidence of native query/transaction semantics.
func TestQueueAccountingJobOperands(t *testing.T) {
	for _, kind := range durableJobKinds {
		for _, pending := range []bool{false, true} {
			for _, operation := range []string{"create", "enqueue", "ensure"} {
				if operation == "create" && pending {
					continue
				}
				t.Run(fmt.Sprintf("%s/%t/%s", kind, pending, operation), func(t *testing.T) {
					ctx, owner, controller := storeAccountingFixture(t, 40, 2)
					db, native := storeAccountingDB(t, ctx, owner)
					s := &Surreal{db: db, accounting: owner}
					id := models.NewRecordID(string(kind), []any{"neutral", uint64(7)})
					ids := []models.RecordID{}
					if pending {
						ids = append(ids, id)
					}
					wantRows := uint64(1)
					switch kind {
					case JobExtract, JobCallerLeaf, JobResolverCatalog:
						wantRows++
					case JobIndex:
						if !pending {
							wantRows++
						}
					}
					calls := 0
					native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
						calls++
						sql, payload, err := queueAccountingPayload(native, request)
						if err != nil {
							return nil, err
						}
						if sql == queuePendingSelection {
							result := []jobRec{}
							if pending {
								// Generic projections still use the supplied target.
								result = append(result, jobRec{RecID: &id, Job: Job{Target: "different legacy target"}})
							}
							return queueAccountingOK(result), nil
						}
						if payload.Table != string(kind) || payload.Target != "supplied" ||
							operation != "create" && !reflect.DeepEqual(payload.Pending, ids) {
							return nil, errors.New("queue mutation lost supplied identities")
						}
						if strings.Count(sql, "UPDATE type::record('repo', $target)") != int(wantRows-1) ||
							strings.Count(sql, "CREATE type::table($table) CONTENT") != btoi(!pending) ||
							strings.Count(sql, "UPDATE $pending SET") != btoi(pending) {
							return nil, errors.New("queue mutation submitted inactive write operands")
						}
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != 1 || prefix.Rows != wantRows || prefix.MaximumRows != wantRows {
							return nil, errors.New("queue mutation forwarded before exact operand ACK")
						}
						return queueAccountingOK([]jobRec{{RecID: &id, Job: Job{Status: StatusPending}}}), nil
					}
					var err error
					switch operation {
					case "create":
						_, err = s.CreateJob(ctx, kind, "supplied")
					case "enqueue":
						_, err = s.EnqueuePending(ctx, kind, "supplied", true)
					case "ensure":
						_, err = s.EnsureJobSuccessor(ctx, Job{ID: string(kind) + ":active", Kind: kind, Target: "supplied", LeaseToken: "lease", ClaimedBy: "worker"}, true)
					}
					wantCalls := 2
					if operation == "create" {
						wantCalls = 1
					}
					if err != nil || calls != wantCalls {
						t.Fatalf("calls=%d error=%v", calls, err)
					}
				})
			}
		}
	}
}

type queueAccountingVars struct {
	Table   string            `json:"table"`
	Target  string            `json:"target"`
	ID      string            `json:"id"`
	Lease   string            `json:"lease"`
	Pending []models.RecordID `json:"pending_ids"`
	IDs     []models.RecordID `json:"ids"`
}

// Retained former generic SQL arms are test-only parity fixtures. The runtime
// submits only its one source-known applicable projection.
const projectLatestIndexJobSQL = `
IF $table = 'indexing_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_index_job = $created_job.id,
		latest_index_job_created_at = $created_job.created_at,
		latest_index_job_projection_version = 't30.6n-indexing-job-latest-v1'
	WHERE latest_index_job_created_at = NONE
		OR latest_index_job_created_at < $created_job.created_at
		OR (latest_index_job_created_at = $created_job.created_at
			AND latest_index_job < $created_job.id)
	RETURN NONE;
};
IF $table = 'extraction_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_extraction_job = $created_job.id,
		latest_extraction_job_created_at = $created_job.created_at,
		latest_extraction_job_projection_version = 't40r1-extraction-job-latest-v1'
	WHERE latest_extraction_job_created_at = NONE
		OR latest_extraction_job_created_at < $created_job.created_at
		OR (latest_extraction_job_created_at = $created_job.created_at
			AND latest_extraction_job < $created_job.id)
	RETURN NONE;
};
IF $table = 'caller_leaf_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_caller_job = $created_job.id,
		latest_caller_job_created_at = $created_job.created_at,
		latest_caller_job_projection_version = 't40r1-caller-job-latest-v1'
	WHERE latest_caller_job_created_at = NONE
		OR latest_caller_job_created_at < $created_job.created_at
		OR (latest_caller_job_created_at = $created_job.created_at
			AND latest_caller_job < $created_job.id)
	RETURN NONE;
};
IF $table = 'resolver_catalog_job' AND $created_job != NONE {
	UPDATE type::record('repo', $target)
	SET latest_resolver_job = $created_job.id,
		latest_resolver_job_created_at = $created_job.created_at,
		latest_resolver_job_projection_version = 't40r1-resolver-job-latest-v1'
	WHERE latest_resolver_job_created_at = NONE
		OR latest_resolver_job_created_at < $created_job.created_at
		OR (latest_resolver_job_created_at = $created_job.created_at
			AND latest_resolver_job < $created_job.id)
	RETURN NONE;
};`

const projectExistingJobProjectionSQL = `
IF $table = 'extraction_job' AND $created_job = NONE AND array::len($job) = 1 {
	UPDATE type::record('repo', $target)
	SET latest_extraction_job = $job[0].id,
		latest_extraction_job_created_at = $job[0].created_at,
		latest_extraction_job_projection_version = 't40r1-extraction-job-latest-v1'
	WHERE latest_extraction_job_created_at = NONE
		OR latest_extraction_job_created_at < $job[0].created_at
		OR (latest_extraction_job_created_at = $job[0].created_at
			AND latest_extraction_job < $job[0].id)
	RETURN NONE;
};
IF $table = 'caller_leaf_job' AND $created_job = NONE AND array::len($job) = 1 {
	UPDATE type::record('repo', $target)
	SET latest_caller_job = $job[0].id,
		latest_caller_job_created_at = $job[0].created_at,
		latest_caller_job_projection_version = 't40r1-caller-job-latest-v1'
	WHERE latest_caller_job_created_at = NONE
		OR latest_caller_job_created_at < $job[0].created_at
		OR (latest_caller_job_created_at = $job[0].created_at
			AND latest_caller_job < $job[0].id)
	RETURN NONE;
};
IF $table = 'resolver_catalog_job' AND $created_job = NONE AND array::len($job) = 1 {
	UPDATE type::record('repo', $target)
	SET latest_resolver_job = $job[0].id,
		latest_resolver_job_created_at = $job[0].created_at,
		latest_resolver_job_projection_version = 't40r1-resolver-job-latest-v1'
	WHERE latest_resolver_job_created_at = NONE
		OR latest_resolver_job_created_at < $job[0].created_at
		OR (latest_resolver_job_created_at = $job[0].created_at
			AND latest_resolver_job < $job[0].id)
	RETURN NONE;
};`

func TestQueueAccountingProjectionRecipes(t *testing.T) {
	for _, kind := range durableJobKinds {
		for _, pending := range []bool{false, true} {
			legacy := projectLatestIndexJobSQL
			if pending {
				legacy = projectExistingJobProjectionSQL
			}
			want := ""
			for _, arm := range strings.Split(legacy, "\n};") {
				if strings.HasPrefix(strings.TrimSpace(arm), "IF $table = '"+string(kind)+"' ") {
					want = arm + "\n};"
				}
			}
			got := queueProjectionSQL(kind, pending)
			if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(want), " ") {
				t.Fatalf("%s/pending=%t projection differs from its original source arm", kind, pending)
			}
		}
	}
}

func queueAccountingPayload(native *storeSDKTestConnection, request *connection.RPCRequest) (string, queueAccountingVars, error) {
	var payload queueAccountingVars
	if request.Method != "query" || len(request.Params) != 2 {
		return "", payload, errors.New("unexpected queue RPC")
	}
	sql, ok := request.Params[0].(string)
	raw, rawOK := request.Params[1].(cbor.RawMessage)
	if !ok || !rawOK {
		return "", payload, errors.New("queue payload was not immutable CBOR")
	}
	err := native.codec.Unmarshal(raw, &payload)
	return sql, payload, err
}

func queueAccountingOK(rows any) any {
	return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}
}

func TestQueueAccountingSuccessorTransitions(t *testing.T) {
	for _, operation := range []string{"fail", "defer", "release", "requeue"} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", operation, pending), func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				active := models.NewRecordID(string(JobCallerLeaf), "active")
				successor := models.NewRecordID(string(JobCallerLeaf), "pending")
				job := Job{ID: active.String(), Kind: JobCallerLeaf, Target: "neutral", LeaseToken: "lease", ClaimedBy: "worker"}
				ids := []models.RecordID{}
				if pending {
					ids = append(ids, successor)
				}
				calls := 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					calls++
					sql, payload, err := queueAccountingPayload(native, request)
					if err != nil {
						return nil, err
					}
					if calls == 1 {
						wantSQL := queuePendingSelection
						if operation == "fail" {
							wantSQL = queueRecoverySelection
							if payload.Lease != job.LeaseToken {
								return nil, errors.New("recovery census lost lease provenance")
							}
						}
						if sql != wantSQL {
							return nil, errors.New("successor census changed")
						}
						rows := []jobRec{}
						if pending {
							rows = append(rows, jobRec{RecID: &successor})
						}
						return queueAccountingOK(rows), nil
					}
					if calls != 2 || payload.ID != job.ID || !reflect.DeepEqual(payload.Pending, ids) || !strings.Contains(sql, "IF $owned != NONE AND $actual_successor != $pending_ids[0]") {
						return nil, errors.New("successor mutation lost exact census or lease guard")
					}
					if strings.Count(sql, "UPDATE type::record($id)") != 1 ||
						strings.Count(sql, "UPDATE $successor SET") != btoi(pending) {
						return nil, errors.New("successor mutation submitted inactive alternative operands")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != 1+uint64(len(ids)) {
						return nil, errors.New("successor mutation forwarded without exact ACK")
					}
					next := time.Now().UTC().Add(time.Minute)
					status := StatusPending
					if pending {
						status = StatusCanceled
					}
					return queueAccountingOK([]jobRec{{RecID: &active, Job: Job{Status: status, NotBefore: &next}}}), nil
				}
				var err error
				switch operation {
				case "fail":
					err = s.FailJobWithSuccessor(ctx, job, "neutral")
				case "defer":
					_, err = s.DeferJob(ctx, job, "neutral", time.Minute)
				case "release":
					err = s.ReleaseJob(ctx, job, "neutral")
				case "requeue":
					err = s.RequeueJob(ctx, job, "neutral", time.Now().UTC())
				}
				if err != nil || calls != 2 {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
			})
		}
	}
}

func TestQueueAccountingRetryAndUncertainty(t *testing.T) {
	for _, mode := range []string{"changed", "cap", "lost", "lost_lease"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			id := models.NewRecordID(string(JobIndex), "pending")
			calls, writes := 0, 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				sql, _, err := queueAccountingPayload(native, request)
				if err != nil {
					return nil, err
				}
				if sql == queuePendingSelection {
					rows := []jobRec{}
					if writes != 0 {
						rows = append(rows, jobRec{RecID: &id})
					}
					return queueAccountingOK(rows), nil
				}
				writes++
				if mode == "lost" {
					return nil, context.DeadlineExceeded
				}
				if mode == "lost_lease" {
					return queueAccountingOK([]jobRec{}), nil
				}
				if writes == 1 || mode == "cap" {
					return nil, &surrealdb.QueryError{Message: "phebs-conflict: pending job census changed"}
				}
				return queueAccountingOK([]jobRec{{RecID: &id, Job: Job{Status: StatusPending}}}), nil
			}
			_, err := s.EnsureJobSuccessor(ctx, Job{ID: string(JobIndex) + ":active", Kind: JobIndex, Target: "neutral", LeaseToken: "lease", ClaimedBy: "worker"}, false)
			wantCalls, wantTX, wantRows := 2, uint64(1), uint64(2)
			if mode == "changed" {
				wantCalls, wantTX, wantRows = 4, 2, 3
			}
			if mode == "cap" {
				wantCalls, wantTX, wantRows = 2*maxQueueRetries, maxQueueRetries, maxQueueRetries+1
			}
			prefix, _ := controller.Snapshot()
			if (err == nil) != (mode == "changed") || calls != wantCalls || prefix.Transactions != wantTX || prefix.Rows != wantRows {
				t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
			if mode != "changed" && !IsSuccessorRetry(err) || mode == "lost_lease" && !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("successor failure classification lost: %v", err)
			}
			if mode == "lost" {
				if _, err := s.EnqueuePending(ctx, JobIndex, "neutral", false); err == nil || calls != wantCalls {
					t.Fatal("uncertain native reply permitted a later submission")
				}
			}
		})
	}
}

func TestQueueAccountingNativeCensusFences(t *testing.T) {
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
	queued, err := s.EnqueuePending(ctx, JobIndex, "neutral", false)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.queuePendingIDs(ctx, JobIndex, "neutral", "")
	if err != nil || len(ids) != 1 || ids[0].String() != queued.ID {
		t.Fatalf("actual native census=%v error=%v", ids, err)
	}
	claimed, err := s.ClaimJob(ctx, JobIndex, "worker")
	if err != nil {
		t.Fatal(err)
	}
	// The selected pending row was claimed after the external census. The
	// transaction must refuse before changing that row or creating a successor.
	_, err = surrealdb.Query[[]jobRec](ctx, s.db, enqueuePendingSQL(JobIndex, true),
		map[string]any{"table": string(JobIndex), "target": "neutral", "force": true, "pending_ids": ids})
	if err == nil || !isRetryableEnqueue(err) || !strings.Contains(err.Error(), "census changed") {
		t.Fatalf("stale enqueue census did not refuse: %v", err)
	}
	if ids, err := s.queuePendingIDs(ctx, JobIndex, "neutral", ""); err != nil || len(ids) != 0 {
		t.Fatalf("refused transaction minted pending work: %v %v", ids, err)
	}
	if err := s.SetJobStatus(ctx, *claimed, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	successor, err := s.EnsureJobSuccessor(ctx, *claimed, false)
	if err != nil {
		t.Fatal(err)
	}
	ids, err = s.queuePendingIDs(ctx, JobIndex, "neutral", claimed.LeaseToken)
	if err != nil || len(ids) != 1 || ids[0].String() != successor.ID {
		t.Fatalf("native recovery census=%v error=%v", ids, err)
	}
	if n, err := s.CancelPendingJobs(ctx, JobIndex, "neutral"); err != nil || n != 1 {
		t.Fatalf("actual cancellation=%d error=%v", n, err)
	}
	_, err = surrealdb.Query[[]jobRec](ctx, s.db, cancelPendingJobsSelectedSQL,
		map[string]any{"table": string(JobIndex), "target": "neutral", "limit": restoreClearRows + 1, "ids": ids})
	if err == nil || !strings.Contains(err.Error(), "census changed") {
		t.Fatalf("stale cancellation census did not refuse: %v", err)
	}
	if n, err := s.CancelPendingJobs(ctx, JobIndex, "neutral"); err != nil || n != 0 {
		t.Fatalf("native empty cancellation=%d error=%v", n, err)
	}
}

func TestQueueAccountingCensusRefusals(t *testing.T) {
	id := models.NewRecordID(string(JobIndex), "pending")
	wrong := models.NewRecordID("wrong_table", "pending")
	for _, test := range []struct {
		name   string
		rows   any
		cancel bool
	}{
		{"null", nil, false},
		{"no_id", []jobRec{{}}, false},
		{"wrong_table", []jobRec{{RecID: &wrong}}, false},
		{"too_many", []jobRec{{RecID: &id}, {RecID: &id}}, false},
		{"canceled_after_read", []jobRec{{RecID: &id}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, owner, controller := storeAccountingFixture(t, 40, 2)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			db, native := storeAccountingDB(t, base, owner)
			s := &Surreal{db: db, accounting: owner}
			calls := 0
			native.call = func(context.Context, *connection.RPCRequest) (any, error) {
				calls++
				if test.cancel {
					cancel()
				}
				return queueAccountingOK(test.rows), nil
			}
			if _, err := s.EnqueuePending(ctx, JobIndex, "neutral", false); err == nil {
				t.Fatal("invalid census permitted a mutation")
			}
			prefix, _ := controller.Snapshot()
			if calls != 1 || prefix.Transactions != 0 || prefix.Rows != 0 {
				t.Fatalf("calls=%d prefix=%+v", calls, prefix)
			}
		})
	}
}

func TestQueueAccountingLeaseAndIdle(t *testing.T) {
	for _, operation := range []string{"empty_claim", "claim", "heartbeat", "running", "done", "exhausted_reap"} {
		t.Run(operation, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			id := models.NewRecordID(string(JobIndex), "active")
			beat := time.Now().UTC().Add(-time.Hour)
			job := Job{ID: id.String(), Kind: JobIndex, Target: "neutral", LeaseToken: "lease", ClaimedBy: "worker", HeartbeatAt: &beat}
			calls := 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				sql, _, err := queueAccountingPayload(native, request)
				if err != nil {
					return nil, err
				}
				if sql == claimCandidateSQL {
					rows := []jobRec{}
					if operation == "claim" {
						rows = append(rows, jobRec{RecID: &id})
					}
					return queueAccountingOK(rows), nil
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != 1 || prefix.Rows != 1 {
					return nil, errors.New("lease target forwarded without one-row ACK")
				}
				return queueAccountingOK([]jobRec{{RecID: &id, Job: job}}), nil
			}
			var err error
			switch operation {
			case "empty_claim", "claim":
				_, err = s.ClaimJob(ctx, JobIndex, "worker")
			case "heartbeat":
				err = s.HeartbeatJob(ctx, job)
			case "running":
				err = s.SetJobStatus(ctx, job, StatusRunning, "")
			case "done":
				err = s.SetJobStatus(ctx, job, StatusDone, "")
			case "exhausted_reap":
				err = s.reapOne(ctx, job, time.Now().UTC(), 1)
			}
			prefix, _ := controller.Snapshot()
			if operation == "empty_claim" {
				if !errors.Is(err, ErrNotFound) || prefix.Transactions != 0 || calls != 1 {
					t.Fatalf("idle calls=%d prefix=%+v error=%v", calls, prefix, err)
				}
			} else if err != nil || prefix.Transactions != 1 || prefix.Rows != 1 {
				t.Fatalf("lease calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
		})
	}
}

func TestQueueAccountingCancellationBound(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, count := range []int{0, 1, 512, 513} {
			t.Run(fmt.Sprintf("selected_%t/rows_%d", selected, count), func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				if !selected {
					owner = nil
				}
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				ids := make([]models.RecordID, count)
				for i := range ids {
					ids[i] = models.NewRecordID(string(JobIndex), fmt.Sprintf("neutral-%04d", i))
				}
				calls := 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					calls++
					if calls == 1 {
						return queueAccountingOK(ids), nil
					}
					if selected {
						sql, payload, err := queueAccountingPayload(native, request)
						if err != nil || sql != cancelPendingJobsSelectedSQL || !reflect.DeepEqual(payload.IDs, ids) {
							return nil, errors.New("cancellation lost its actual operand vector")
						}
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != 1 || prefix.Rows != uint64(count) {
							return nil, errors.New("cancellation forwarded without exact ACK")
						}
					} else if count == 513 && !strings.HasPrefix(request.Params[0].(string), "UPDATE type::table($table)") {
						return nil, errors.New("ordinary overflow silently truncated")
					}
					return queueAccountingOK(make([]jobRec, count)), nil
				}
				n, err := s.CancelPendingJobs(ctx, JobIndex, "neutral")
				wantCalls := 2
				if count == 0 || selected && count == 513 {
					wantCalls = 1
				}
				if selected && count == 513 {
					if !errors.Is(err, storeaccounting.ErrDescriptor) || n != 0 {
						t.Fatalf("overflow result=%d error=%v", n, err)
					}
				} else if err != nil || n != count {
					t.Fatalf("result=%d error=%v", n, err)
				}
				if calls != wantCalls {
					t.Fatalf("calls=%d want%d", calls, wantCalls)
				}
			})
		}
	}
}

func TestQueueAccountingSourceCoverage(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "surreal.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	started, queries := false, 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		started = started || function.Name.Name == "CreateJob"
		if !started {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := call.Fun
			if generic, ok := callee.(*ast.IndexExpr); ok {
				callee = generic.X
			}
			if selector, ok := callee.(*ast.SelectorExpr); ok && selector.Sel.Name == "Query" {
				t.Errorf("raw SDK call remains in %s", function.Name)
			}
			if name, ok := callee.(*ast.Ident); ok && name.Name == "storeQuery" {
				queries++
				owner, ok := call.Args[1].(*ast.SelectorExpr)
				if !ok || owner.Sel.Name != "accounting" {
					t.Errorf("uncaptured owner in %s", function.Name)
				}
			}
			return true
		})
	}
	if !started || queries != 15 {
		t.Fatalf("queue source queries=%d want15", queries)
	}
}
