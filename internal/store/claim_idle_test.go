package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	connectionhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

type claimIdleStep struct {
	sql    string
	rows   []jobRec
	err    error
	cancel bool
}

type claimIdleConnection struct {
	*failingOpenConnection
	codec  *surrealcbor.Codec
	steps  []claimIdleStep
	calls  int
	leases []string
	cancel context.CancelFunc
}

func (conn *claimIdleConnection) Send(_ context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	if method != "query" || len(params) != 2 || conn.calls >= len(conn.steps) {
		return nil, errors.New("unexpected claim test call")
	}
	step := conn.steps[conn.calls]
	conn.calls++
	query, ok := params[0].(string)
	vars, varsOK := params[1].(map[string]any)
	if !ok || !varsOK || query != step.sql {
		return nil, errors.New("unexpected claim test query")
	}
	if query == claimCandidateSQL {
		if len(vars) != 1 || vars["table"] != string(JobSync) {
			return nil, errors.New("selection received claim credentials")
		}
	} else {
		candidate, ok := vars["cand"].(models.RecordID)
		lease, leaseOK := vars["lease"].(string)
		decoded, err := hex.DecodeString(lease)
		if !ok || !leaseOK || len(vars) != 3 || vars["who"] != "claim-test" || err != nil || len(decoded) != 16 {
			return nil, errors.New("invalid selected claim bindings")
		}
		selected := conn.steps[conn.calls-2].rows
		if len(selected) != 1 || !reflect.DeepEqual(candidate, *selected[0].RecID) {
			return nil, errors.New("claim did not bind the actual selected record")
		}
		conn.leases = append(conn.leases, lease)
	}
	if step.cancel {
		conn.cancel()
	}
	if step.err != nil {
		return nil, step.err
	}
	body, err := conn.codec.Marshal([]surrealdb.QueryResult[[]jobRec]{{Status: "OK", Result: step.rows}})
	if err != nil {
		return nil, err
	}
	raw := cbor.RawMessage(body)
	return &connection.RPCResponse[cbor.RawMessage]{Result: &raw}, nil
}

func TestClaimJobReadOnlySelectionAndResolvedRaces(t *testing.T) {
	row := func(key string) []jobRec {
		rid := models.NewRecordID(string(JobSync), key)
		return []jobRec{{RecID: &rid, Job: Job{Status: StatusClaimed, ClaimedBy: "claim-test"}}}
	}
	first, second := row("first"), row("second")
	conflict := errors.New("neutral transaction conflict")
	failure := errors.New("neutral permanent transport failure")
	for _, test := range []struct {
		name      string
		steps     []claimIdleStep
		wantError error
		wantID    string
		writes    int
	}{
		{"empty", []claimIdleStep{{sql: claimCandidateSQL}}, ErrNotFound, "", 0},
		{"positive", []claimIdleStep{{sql: claimCandidateSQL, rows: first}, {sql: claimSelectedJobSQL, rows: first}}, nil, "first", 1},
		{"race to next job", []claimIdleStep{{sql: claimCandidateSQL, rows: first}, {sql: claimSelectedJobSQL}, {sql: claimCandidateSQL, rows: second}, {sql: claimSelectedJobSQL, rows: second}}, nil, "second", 2},
		{"race drains queue", []claimIdleStep{{sql: claimCandidateSQL, rows: first}, {sql: claimSelectedJobSQL}, {sql: claimCandidateSQL}}, ErrNotFound, "", 1},
		{"selection retry", []claimIdleStep{{sql: claimCandidateSQL, err: conflict}, {sql: claimCandidateSQL}}, ErrNotFound, "", 0},
		{"write retry", []claimIdleStep{{sql: claimCandidateSQL, rows: first}, {sql: claimSelectedJobSQL, err: conflict}, {sql: claimCandidateSQL, rows: second}, {sql: claimSelectedJobSQL, rows: second}}, nil, "second", 2},
		{"selection failure", []claimIdleStep{{sql: claimCandidateSQL, err: failure}}, failure, "", 0},
		{"write failure", []claimIdleStep{{sql: claimCandidateSQL, rows: first}, {sql: claimSelectedJobSQL, err: failure}}, failure, "", 1},
		{"cancel after selection", []claimIdleStep{{sql: claimCandidateSQL, rows: first, cancel: true}}, context.Canceled, "", 0},
		{"canceled retry", []claimIdleStep{{sql: claimCandidateSQL, err: conflict, cancel: true}}, context.Canceled, "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, conn := claimIdleTestStore(t, test.steps, cancel)
			job, err := s.ClaimJob(ctx, JobSync, "claim-test")
			wantRecord := models.NewRecordID(string(JobSync), test.wantID)
			if !errors.Is(err, test.wantError) || conn.calls != len(test.steps) || len(conn.leases) != test.writes {
				t.Fatalf("claim error=%v calls=%d writes=%d", err, conn.calls, len(conn.leases))
			}
			if test.wantID == "" {
				if job != nil {
					t.Fatal("failed claim returned a job")
				}
			} else if job == nil || job.ID != wantRecord.String() || job.Status != StatusClaimed {
				t.Fatalf("claimed = %+v", job)
			}
			if len(conn.leases) == 2 && conn.leases[0] == conn.leases[1] {
				t.Fatal("retry reused its previous lease")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var absent *Surreal
	if _, err := absent.ClaimJob(ctx, JobSync, "claim-test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled claim = %v", err)
	}
}

func TestClaimJobMalformedCandidateRefusesBeforeWrite(t *testing.T) {
	valid := models.NewRecordID(string(JobSync), "valid")
	wrongTable := models.NewRecordID(string(JobIndex), "valid")
	numeric := models.NewRecordID(string(JobSync), 7)
	invalid := models.NewRecordID(string(JobSync), "bad/key")
	for _, test := range []struct {
		name string
		rows []jobRec
	}{
		{"missing ID", []jobRec{{}}},
		{"wrong table", []jobRec{{RecID: &wrongTable}}},
		{"numeric ID", []jobRec{{RecID: &numeric}}},
		{"invalid ID", []jobRec{{RecID: &invalid}}},
		{"multiple", []jobRec{{RecID: &valid}, {RecID: &valid}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, conn := claimIdleTestStore(t, []claimIdleStep{{sql: claimCandidateSQL, rows: test.rows}}, nil)
			job, err := s.ClaimJob(t.Context(), JobSync, "claim-test")
			if err == nil || errors.Is(err, ErrNotFound) || job != nil || conn.calls != 1 || len(conn.leases) != 0 {
				t.Fatalf("malformed candidate: job=%+v error=%v calls=%d", job, err, conn.calls)
			}
		})
	}
}

func claimIdleTestStore(t *testing.T, steps []claimIdleStep, cancel context.CancelFunc) (*Surreal, *claimIdleConnection) {
	t.Helper()
	codec := surrealcbor.New()
	conn := &claimIdleConnection{codec: codec, steps: steps, cancel: cancel,
		failingOpenConnection: &failingOpenConnection{Connection: connectionhttp.New(&connection.Config{Unmarshaler: codec})}}
	db, err := surrealdb.FromConnection(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return &Surreal{db: db}, conn
}

func TestClaimJobNativeEligibilityAndConcurrency(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-30*time.Second).Before(deadline) {
		deadline = outer.Add(-30 * time.Second)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	s := newServiceCatalogV3InternalStoreContext(ctx, t)
	for _, kind := range durableJobKinds {
		t.Run(string(kind), func(t *testing.T) {
			if job, err := s.ClaimJob(ctx, kind, "empty-worker"); !errors.Is(err, ErrNotFound) || job != nil {
				t.Fatalf("empty = %+v, %v", job, err)
			}
			future := claimIdleNativeCreate(ctx, t, s, kind, "future")
			claimIdleNativeQuery(ctx, t, s,
				"UPDATE type::record($id) SET created_at = time::now() - 3h, not_before = time::now() + 1h", future.ID)
			if job, err := s.ClaimJob(ctx, kind, "future-worker"); !errors.Is(err, ErrNotFound) || job != nil {
				t.Fatalf("future-only = %+v, %v", job, err)
			}
			first := claimIdleNativeCreate(ctx, t, s, kind, "first")
			claimIdleNativeQuery(ctx, t, s,
				"UPDATE type::record($id) SET created_at = time::now() - 2h, attempts = 2, force = true", first.ID)
			second := claimIdleNativeCreate(ctx, t, s, kind, "second")
			for _, want := range []*Job{first, second} {
				got, err := s.ClaimJob(ctx, kind, "due-worker")
				if err != nil || got == nil || got.ID != want.ID || got.Status != StatusClaimed || got.ClaimedBy != "due-worker" ||
					got.ClaimedAt == nil || got.HeartbeatAt == nil || len(got.LeaseToken) != 32 {
					t.Fatalf("due claim = %+v, %v", got, err)
				}
				if want == first && (got.Attempts != 2 || !got.Force) {
					t.Fatalf("claim changed durable attempt/force: %+v", got)
				}
			}
			if pending, err := s.ListJobs(ctx, kind, StatusPending); err != nil || len(pending) != 1 || pending[0].ID != future.ID || pending[0].NotBefore == nil {
				t.Fatalf("future row changed: %+v, %v", pending, err)
			}
		})
	}
	t.Run("actual selected race and eligibility snapshot", func(t *testing.T) {
		for _, changed := range []bool{false, true} {
			job := claimIdleNativeCreate(ctx, t, s, JobSync, fmt.Sprintf("selected-%t", changed))
			selected, err := surrealdb.Query[[]jobRec](ctx, s.db, claimCandidateSQL, map[string]any{"table": string(JobSync)})
			if err != nil {
				t.Fatal(err)
			}
			rows := firstNonEmpty(selected)
			if len(rows) != 1 || rows[0].toJob(JobSync).ID != job.ID {
				t.Fatalf("actual selection = %+v, %v", rows, err)
			}
			if changed {
				claimIdleNativeQuery(ctx, t, s, "UPDATE type::record($id) SET not_before = time::now() + 1h", job.ID)
			} else if _, err := s.ClaimJob(ctx, JobSync, "winning-worker"); err != nil {
				t.Fatal(err)
			}
			result, err := surrealdb.Query[[]jobRec](ctx, s.db, claimSelectedJobSQL,
				map[string]any{"cand": *rows[0].RecID, "who": "selected-worker", "lease": strings.Repeat("a", 32)})
			if err != nil || (len(firstNonEmpty(result)) == 1) != changed {
				t.Fatalf("selected conditional result=%+v error=%v", result, err)
			}
		}
	})
	t.Run("concurrent pollers", func(t *testing.T) {
		const jobs, workers = 24, 4
		for index := range jobs {
			claimIdleNativeCreate(ctx, t, s, JobSync, fmt.Sprintf("concurrent-%02d", index))
		}
		var mu sync.Mutex
		claimed := make([]string, 0, jobs)
		var failures []error
		var joins sync.WaitGroup
		for index := range workers {
			joins.Go(func() {
				for {
					job, err := s.ClaimJob(ctx, JobSync, fmt.Sprintf("concurrent-worker-%d", index))
					if errors.Is(err, ErrNotFound) {
						return
					}
					mu.Lock()
					if err != nil {
						failures = append(failures, err)
					} else {
						claimed = append(claimed, job.ID)
					}
					mu.Unlock()
					if err != nil {
						return
					}
				}
			})
		}
		joins.Wait()
		slices.Sort(claimed)
		if len(failures) != 0 || len(claimed) != jobs || len(slices.Compact(claimed)) != jobs {
			t.Fatalf("concurrent claims=%d failures=%v", len(claimed), failures)
		}
	})
}

func claimIdleNativeCreate(ctx context.Context, t *testing.T, s *Surreal, kind JobKind, target string) *Job {
	t.Helper()
	job, err := s.CreateJob(ctx, kind, target)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func claimIdleNativeQuery(ctx context.Context, t *testing.T, s *Surreal, query, id string) {
	t.Helper()
	if _, err := surrealdb.Query[any](ctx, s.db, query, map[string]any{"id": id}); err != nil {
		t.Fatal(err)
	}
}
