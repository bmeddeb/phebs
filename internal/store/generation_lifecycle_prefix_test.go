//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Scripted native SDK replies distinguish confirmed earlier deletions from
// submitted operands. This is not an actual database rollback/recovery test.
func TestGenerationLifecycleFailureKeepsEarlierConfirmedPrefix(t *testing.T) {
	for _, test := range []struct {
		name         string
		failCall     int
		malformed    bool
		wantCalls    int
		wantDeleted  int
		transactions uint64
	}{
		{"malformed_second_candidate", 0, true, 3, 3, 1},
		{"second_census_refused", 4, false, 4, 3, 1},
		{"second_mutation_refused", 5, false, 5, 3, 2},
		{"first_mutation_refused", 3, false, 3, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			candidates := []generationLifecycleCandidate{
				{Digest: "sha256:" + strings.Repeat("a", 64), Repository: "example.com/acme/prefix", Stage: "source-observation",
					Generation: "sha256:" + strings.Repeat("c", 64), UpdatedAt: time.Now().UTC()},
				{Digest: "sha256:" + strings.Repeat("b", 64), Repository: "example.com/acme/prefix", Stage: "source-observation",
					Generation: "sha256:" + strings.Repeat("d", 64), UpdatedAt: time.Now().UTC()},
			}
			if test.malformed {
				candidates[1].Generation = "invalid"
			}
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if native.calls == test.failCall {
					return nil, &surrealdb.QueryError{Message: "phebs-permanent: controlled lifecycle refusal"}
				}
				if native.calls == 1 {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: candidates}}, nil
				}
				sql, ok := request.Params[0].(string)
				if !ok {
					return nil, errors.New("lifecycle query missing")
				}
				if strings.HasPrefix(sql, "BEGIN;") {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []generationLifecycleDelete{{Deleted: 3}}}}, nil
				}
				ids := []models.RecordID{models.NewRecordID("generation_schedule_chunk", "one"), models.NewRecordID("generation_schedule_chunk", "two")}
				return generationAccountingCensusReply(9, []any{map[string]any{"ids": ids}}), nil
			}
			sweep, err := (&Surreal{db: db, accounting: owner}).SweepGenerationScheduleLifecycle(ctx, "", 5, 16, 2)
			if err == nil || sweep.Scanned != 2 || sweep.Deleted != test.wantDeleted || native.calls != test.wantCalls {
				t.Fatalf("failed sweep lost/invented confirmed prefix: %+v / %v, calls=%d", sweep, err, native.calls)
			}
			prefix, err := controller.Snapshot()
			if err != nil || prefix.Transactions != test.transactions || prefix.Rows != 3*test.transactions {
				t.Fatalf("submitted operands differ from confirmed deletions: %+v / %v", prefix, err)
			}
		})
	}
}
