//go:build darwin || linux

package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Scripted SDK/CBOR replies plus genuine SA01 acknowledgements prove bounded
// submission and error behavior, not native database contention or phase fit.
func TestBoundedJobClaimIterations(t *testing.T) {
	for _, test := range []struct {
		name, failure           string
		selected, bounded       bool
		failures, reads, writes int
		want                    string
	}{
		{"first", "lost", true, true, 0, 1, 1, "success"},
		{"last_lost", "lost", true, true, 63, 64, 64, "success"},
		{"last_read", "read", true, true, 63, 64, 1, "success"},
		{"last_write", "write", true, true, 63, 64, 64, "success"},
		{"exhaust_lost", "lost", true, true, 64, 64, 64, "conflict"},
		{"exhaust_read", "read", true, true, 64, 64, 0, "query"},
		{"exhaust_write", "write", true, true, 64, 64, 64, "query"},
		{"mixed_iterations", "mixed", true, true, 64, 64, 32, "query"},
		{"empty", "empty", true, true, 1, 1, 0, "empty"},
		{"ordinary_over_64", "lost", false, false, 65, 66, 66, "success"},
		{"old_selected_over_64", "lost", true, false, 65, 66, 66, "success"},
		{"read_nonretryable", "read_permanent", true, true, 1, 1, 0, "query"},
		{"write_nonretryable", "write_permanent", true, true, 1, 1, 1, "query"},
		{"duplicate_candidate", "duplicate", true, true, 1, 1, 0, "malformed"},
		{"wrong_table", "wrong_table", true, true, 1, 1, 0, "malformed"},
		{"missing_id", "missing_id", true, true, 1, 1, 0, "malformed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			if !test.selected {
				owner = nil
			}
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner, boundedJobClaims: test.bounded}
			reads, writes := 0, 0
			pending := false
			var candidate models.RecordID
			leases := map[string]bool{}
			lastQueryError := ""
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Method != "query" || len(request.Params) != 2 {
					return nil, errors.New("unexpected bounded claim RPC")
				}
				sql, ok := request.Params[0].(string)
				if !ok {
					return nil, errors.New("missing bounded claim SQL")
				}
				raw, encoded := request.Params[1].(cbor.RawMessage)
				if !encoded {
					if test.selected {
						return nil, errors.New("selected variables bypassed immutable CBOR")
					}
					var err error
					raw, err = native.codec.Marshal(request.Params[1])
					if err != nil {
						return nil, err
					}
				}
				var vars struct {
					Table, Who, Lease string
					Cand              models.RecordID
				}
				if err := native.codec.Unmarshal(raw, &vars); err != nil {
					return nil, err
				}
				failQuery := func() (any, error) {
					lastQueryError = fmt.Sprintf("conflict at native selection %d", reads)
					if strings.HasSuffix(test.failure, "permanent") {
						lastQueryError = "phebs-permanent: conflict is not retryable"
					}
					return nil, &surrealdb.QueryError{Message: lastQueryError}
				}
				switch sql {
				case claimCandidateSQL:
					if pending || vars.Table != string(JobIndex) {
						return nil, errors.New("claim did not recensus its exact kind")
					}
					reads++
					if reads <= test.failures && (strings.HasPrefix(test.failure, "read") || test.failure == "mixed" && reads%2 == 1) {
						return failQuery()
					}
					candidate = models.NewRecordID(string(JobIndex), fmt.Sprintf("candidate-%d", reads))
					rows := []jobRec{{RecID: &candidate}}
					switch test.failure {
					case "empty":
						rows = nil
					case "duplicate":
						rows = append(rows, rows[0])
					case "wrong_table":
						candidate = models.NewRecordID(string(JobExtract), "wrong-table")
					case "missing_id":
						rows[0].RecID = nil
					}
					pending = test.want != "empty" && test.want != "malformed"
					return queueAccountingOK(rows), nil
				case claimSelectedJobSQL:
					if !pending || vars.Cand.String() != candidate.String() || vars.Who != "worker" || leases[vars.Lease] {
						return nil, errors.New("claim reused stale census, worker or lease")
					}
					lease, err := hex.DecodeString(vars.Lease)
					if err != nil || len(lease) != 16 {
						return nil, errors.New("invalid fresh lease")
					}
					leases[vars.Lease], pending = true, false
					writes++
					if test.selected {
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != uint64(writes) || prefix.Rows != uint64(writes) || prefix.MaximumRows != 1 {
							return nil, errors.New("claim forwarded without exact one-row ACK")
						}
					}
					if reads <= test.failures {
						if test.failure == "lost" {
							return queueAccountingOK([]jobRec{}), nil
						}
						return failQuery()
					}
					return queueAccountingOK([]jobRec{{RecID: &candidate, Job: Job{Status: StatusClaimed, ClaimedBy: vars.Who, LeaseToken: vars.Lease}}}), nil
				default:
					return nil, errors.New("claim changed supplied SQL")
				}
			}
			job, err := s.ClaimJob(ctx, JobIndex, "worker")
			switch test.want {
			case "success":
				if err != nil || job == nil || job.ID != candidate.String() || job.Status != StatusClaimed || !leases[job.LeaseToken] {
					t.Fatalf("claim=%+v error=%v", job, err)
				}
			case "conflict":
				if job != nil || !errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
					t.Fatalf("exhaustion invented a job or empty queue: %+v %v", job, err)
				}
			case "query":
				var query *surrealdb.QueryError
				if job != nil || !errors.As(err, &query) || query.Message != lastQueryError {
					t.Fatalf("final native error was not preserved: %v", err)
				}
			case "empty":
				if job != nil || !errors.Is(err, ErrNotFound) {
					t.Fatalf("empty claim=%+v error=%v", job, err)
				}
			case "malformed":
				if job != nil || err == nil || errors.Is(err, ErrNotFound) {
					t.Fatalf("malformed claim=%+v error=%v", job, err)
				}
			}
			if reads != test.reads || writes != test.writes || native.calls != reads+writes {
				t.Fatalf("reads/writes=%d/%d want%d/%d calls=%d", reads, writes, test.reads, test.writes, native.calls)
			}
			prefix, err := controller.Snapshot()
			wantWrites := uint64(0)
			if test.selected {
				wantWrites = uint64(writes)
			}
			if err != nil || prefix.Transactions != wantWrites || prefix.Rows != wantWrites || prefix.Producers[0].Calls != 0 {
				t.Fatalf("accepted prefix=%+v error=%v", prefix, err)
			}
			if test.selected {
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
				if err := owner.checkpoint(ctx); err != nil {
					t.Fatalf("known reply or bounded exhaustion poisoned owner: %v", err)
				}
			}
		})
	}
}

func TestBoundedJobClaimCancellation(t *testing.T) {
	for _, afterRead := range []bool{false, true} {
		t.Run(fmt.Sprintf("after_read_%t", afterRead), func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner, boundedJobClaims: true}
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if !afterRead {
				cancel()
			}
			native.call = func(context.Context, *connection.RPCRequest) (any, error) {
				cancel()
				id := models.NewRecordID(string(JobIndex), "canceled")
				return queueAccountingOK([]jobRec{{RecID: &id}}), nil
			}
			job, err := s.ClaimJob(callCtx, JobIndex, "worker")
			prefix, _ := controller.Snapshot()
			if job != nil || err == nil || callCtx.Err() == nil || native.calls != btoi(afterRead) || prefix.Transactions != 0 {
				t.Fatalf("canceled claim=%+v error=%v calls=%d prefix=%+v", job, err, native.calls, prefix)
			}
		})
	}
}

func TestBoundedJobClaimConstructorRequiresSelectedOwner(t *testing.T) {
	for _, digest := range []string{"bad", "sha256:" + strings.Repeat("a", 64)} {
		t.Run(digest, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "not-created")
			s, err := OpenLocalWithConfigAndBoundedJobClaims(t.Context(), dataDir, digest)
			if s != nil || err == nil || digest != "bad" && !errors.Is(err, storeaccounting.ErrConfig) {
				t.Fatalf("unselected constructor=%v error=%v", s, err)
			}
			if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused constructor touched its target: %v", err)
			}
		})
	}
}
