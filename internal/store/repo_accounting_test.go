//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestRepoDeletingAccounting(t *testing.T) {
	for _, mode := range []string{"active", "retire", "reactivate", "coalesce", "missing", "null", "canceled", "changed", "lost", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			base, owner, controller := storeAccountingFixture(t, 40, 2)
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			db, native := storeAccountingDB(t, base, owner)
			before := mode == "reactivate" || mode == "coalesce"
			deleting := mode == "retire" || mode == "overflow"
			observation := repoDeletingObservation{
				Before: []*bool{&before}, Callers: []models.RecordID{}, Pending: []models.RecordID{},
			}
			wantRows := uint64(1)
			switch mode {
			case "retire":
				observation.Callers = []models.RecordID{models.NewRecordID("caller_generation_publication", "neutral")}
				wantRows = 3
			case "reactivate", "coalesce":
				wantRows = 3
				if mode == "coalesce" {
					observation.Pending = []models.RecordID{models.NewRecordID(string(JobCallerLeaf), "pending")}
				}
			case "missing":
				observation.Before = []*bool{}
			case "null":
				observation.Before = []*bool{nil}
			case "overflow":
				observation.Callers = make([]models.RecordID, 512)
				for index := range observation.Callers {
					observation.Callers[index] = models.NewRecordID("caller_generation_publication", uint64(index))
				}
			}
			calls := 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				if calls == 1 {
					if mode == "canceled" {
						cancel()
					}
					return generationAccountingCensusReply(2, []repoDeletingObservation{observation}), nil
				}
				if calls != 2 || !strings.Contains(request.Params[0].(string), "IF $current_deleting_census != $deleting_census") ||
					!strings.Contains(request.Params[0].(string), "DELETE $retiring_callers") ||
					!strings.Contains(request.Params[0].(string), "$current_deleting_census.pending[0]") {
					return nil, errors.New("deletion mutation is not bound to its native witness")
				}
				sql := request.Params[0].(string)
				if strings.Contains(sql, "(caller_publication_revision ?? 0) + 1") != (mode == "retire") ||
					strings.Contains(sql, "CREATE caller_leaf_job CONTENT") != (mode == "reactivate") ||
					strings.Contains(sql, "UPDATE $pending_caller SET") != (mode == "coalesce") ||
					strings.Contains(sql, "SET latest_caller_job =") != (mode == "reactivate" || mode == "coalesce") {
					return nil, errors.New("deletion submitted an uncounted inactive write operand")
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != 1 || prefix.Rows != wantRows {
					return nil, errors.New("repository mutation preceded exact operand ACK")
				}
				switch mode {
				case "changed":
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "phebs-conflict: repository deletion census changed"}}, nil
				case "lost":
					return nil, context.DeadlineExceeded
				case "missing":
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []Repo{}}}, nil
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []Repo{{Name: "neutral"}}}}, nil
			}
			err := (&Surreal{db: db, accounting: owner}).SetRepoDeleting(ctx, "neutral", deleting)
			wantCalls, wantTX := 2, uint64(1)
			switch mode {
			case "null", "canceled", "overflow":
				wantCalls, wantTX, wantRows = 1, 0, 0
			}
			prefix, _ := controller.Snapshot()
			wantOK := mode == "active" || mode == "retire" || mode == "reactivate" || mode == "coalesce"
			if (err == nil) != wantOK || calls != wantCalls || prefix.Transactions != wantTX || prefix.Rows != wantRows {
				t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
			if mode == "missing" && !errors.Is(err, ErrNotFound) || mode == "overflow" && !errors.Is(err, storeaccounting.ErrDescriptor) {
				t.Fatalf("repository refusal classification lost: %v", err)
			}
		})
	}
}

func TestRepoDeletingNativeCensusFence(t *testing.T) {
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
	const repository = "example.com/neutral/repository"
	if err := s.UpsertRepo(ctx, Repo{Name: repository}); err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"rid": repoID(repository), "repository": repository, "deleting": false}
	observed, err := s.repoDeletingCensus(ctx, vars)
	if err != nil || len(observed.Before) != 1 || *observed.Before[0] || len(observed.Pending) != 0 || len(observed.Callers) != 0 {
		t.Fatalf("native active observation=%+v error=%v", observed, err)
	}
	if err := s.SetRepoDeleting(ctx, repository, true); err != nil {
		t.Fatal(err)
	}
	vars["deleting_census"] = observed
	_, err = surrealdb.Query[any](ctx, s.db, "BEGIN;"+repoDeletingCensusSQL+`
IF $current_deleting_census != $deleting_census {
 THROW 'phebs-conflict: repository deletion census changed';
};
UPDATE $rid SET deleting = false;
COMMIT;`, vars)
	if err == nil || !strings.Contains(err.Error(), "census changed") {
		t.Fatalf("stale deletion observation did not refuse: %v", err)
	}
	current, err := s.GetRepo(ctx, repository)
	if err != nil || !current.Deleting {
		t.Fatalf("refused transaction changed deleting state: %+v %v", current, err)
	}
	for range 2 {
		if err := s.SetRepoDeleting(ctx, repository, false); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.queuePendingIDs(ctx, JobCallerLeaf, repository, "")
	if err != nil || len(pending) != 1 {
		t.Fatalf("reactivation/refresh successor=%v error=%v", pending, err)
	}
}

func TestRepoConnectionAccounting(t *testing.T) {
	for _, mode := range []string{"replace", "prune", "empty", "null", "overflow", "replacement_overflow", "changed", "lost"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			ids := []models.RecordID{models.NewRecordID("repo_connection", "one"), models.NewRecordID("repo_connection", uint64(2))}
			repos := []string{"neutral/a"}
			switch mode {
			case "empty":
				ids, repos = []models.RecordID{}, nil
			case "null":
				ids = nil
			case "overflow":
				ids = make([]models.RecordID, 513)
				for index := range ids {
					ids[index] = models.NewRecordID("repo_connection", uint64(index))
				}
			case "replacement_overflow":
				repos = make([]string, 511)
			}
			calls, rows := 0, uint64(len(ids)+len(repos))
			if mode == "prune" {
				rows = uint64(len(ids))
			}
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				if calls == 1 {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: ids}}, nil
				}
				if calls != 2 || !strings.Contains(request.Params[0].(string), "IF $actual != $ids") {
					return nil, errors.New("missing exact native echo or automatic retry")
				}
				var vars map[string]any
				if err := native.codec.Unmarshal(request.Params[1].(cbor.RawMessage), &vars); err != nil {
					return nil, err
				}
				actual, ok := vars["ids"].([]any)
				if !ok || len(actual) != len(ids) {
					return nil, errors.New("actual deletion operands missing")
				}
				for index := range ids {
					if actual[index] != ids[index] {
						return nil, errors.New("deletion operand identity changed")
					}
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != 1 || prefix.Rows != rows || prefix.MaximumRows != rows {
					return nil, errors.New("mutation preceded accepted source operands")
				}
				switch mode {
				case "changed":
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "phebs-conflict: connection census changed"}}, nil
				case "lost":
					return nil, context.DeadlineExceeded
				}
				return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
			}
			s := &Surreal{db: db, accounting: owner}
			var err error
			if mode == "prune" {
				err = s.PruneConnections(ctx, []string{"retained"})
			} else {
				err = s.SetRepoConnections(ctx, "neutral", repos)
			}
			wantCalls, wantTX := 2, uint64(1)
			switch mode {
			case "empty", "null", "overflow", "replacement_overflow":
				wantCalls, wantTX, rows = 1, 0, 0
			}
			prefix, _ := controller.Snapshot()
			if (err == nil) != (mode == "replace" || mode == "prune" || mode == "empty") ||
				calls != wantCalls || prefix.Transactions != wantTX || prefix.Rows != rows {
				t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
			if (mode == "overflow" || mode == "replacement_overflow") && !errors.Is(err, storeaccounting.ErrDescriptor) {
				t.Fatalf("unsupported positive targets not refused: %v", err)
			}
		})
	}
}
