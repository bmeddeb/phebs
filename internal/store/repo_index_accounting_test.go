//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func repoIndexTestObservation(clearing bool) repoIndexObservation {
	retire, repair, revision, outcomes := clearing, false, false, clearing
	return repoIndexObservation{
		Before:   []cbor.RawMessage{{0xa0}},
		Branches: repoIndexBranches{Retire: &retire, Repair: &repair, Revision: &revision, Outcomes: &outcomes},
		Catalogs: []models.RecordID{}, Callers: []models.RecordID{},
		Published: []models.RecordID{}, Staged: []models.RecordID{},
		Attempts: []models.RecordID{}, Outcomes: []models.RecordID{}, Pending: []repoIndexPending{},
	}
}

func TestRepoIndexCensusRetry(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, clearing := range []bool{false, true} {
			for _, mode := range []string{"retry", "exhausted", "lost", "canceled", "other_query", "untyped_conflict"} {
				t.Run(fmt.Sprintf("selected_%t/clear_%t/%s", selected, clearing, mode), func(t *testing.T) {
					base, owner, controller := storeAccountingFixture(t, 40, 2)
					ctx, cancel := context.WithCancel(base)
					defer cancel()
					if !selected {
						owner = nil
					}
					db, native := storeAccountingDB(t, base, owner)
					calls, attempts := 0, 0
					census := repoIndexTestObservation(clearing)
					*census.Branches.Retire, *census.Branches.Revision = true, true
					native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
						calls++
						if calls%2 == 1 {
							// A new identity each time proves the writer reobserves its
							// inputs rather than replaying the stale transaction.
							census.Callers = []models.RecordID{models.NewRecordID("caller_generation_publication", fmt.Sprint(calls))}
							return generationAccountingCensusReply(7, []repoIndexObservation{census}), nil
						}
						attempts++
						raw, err := native.codec.Marshal(request.Params[1])
						if err != nil {
							return nil, err
						}
						var payload struct {
							Census repoIndexObservation `json:"index_census"`
						}
						if err := native.codec.Unmarshal(raw, &payload); err != nil {
							return nil, err
						}
						if !reflect.DeepEqual(payload.Census, census) {
							t.Fatal("retry reused stale census")
						}
						if selected {
							prefix, err := controller.Snapshot()
							if err != nil || prefix.Transactions != uint64(attempts) || prefix.Rows != uint64(5*attempts) {
								t.Fatalf("retry lacks exact ACK: %+v %v", prefix, err)
							}
						}
						switch mode {
						case "lost":
							return nil, context.DeadlineExceeded
						case "canceled":
							cancel()
						case "other_query":
							return nil, &surrealdb.QueryError{Message: "other conflict"}
						case "untyped_conflict":
							return nil, errors.New("phebs-conflict: repository index census changed")
						}
						if mode != "retry" || attempts == 1 {
							return []surrealdb.QueryResult[any]{
								{Status: "ERR", Result: "The query was not executed due to a failed transaction"},
								{Status: "ERR", Result: "An error occurred: phebs-conflict: repository index census changed"},
								{Status: "ERR", Result: "Cannot COMMIT: the transaction was aborted due to a prior error"},
							}, nil
						}
						return queueAccountingOK([]Repo{{Name: "neutral"}}), nil
					}
					s := &Surreal{db: db, accounting: owner}
					var err error
					if clearing {
						err = s.ClearRepoIndexState(ctx, "neutral")
					} else {
						err = s.SetRepoIndexedState(ctx, "neutral", "commit", nil, nil, time.Now())
					}
					wantAttempts := 1
					if mode == "retry" {
						wantAttempts = 2
					}
					if mode == "exhausted" {
						wantAttempts = maxQueueRetries
					}
					if (err == nil) != (mode == "retry") || attempts != wantAttempts || calls != 2*wantAttempts {
						t.Fatalf("calls=%d attempts=%d want=%d err=%v", calls, attempts, wantAttempts, err)
					}
				})
			}
		}
	}
}

func TestRepoIndexCensusConflictRequiresKnownReply(t *testing.T) {
	conflict := &surrealdb.QueryError{Message: "An error occurred: phebs-conflict: repository index census changed"}
	aborted := &surrealdb.QueryError{Message: "The query was not executed due to a failed transaction"}
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"native_join", errors.Join(aborted, errors.Join(aborted, conflict), aborted), true},
		{"unknown_before", errors.Join(context.DeadlineExceeded, conflict), false},
		{"unknown_after", errors.Join(conflict, context.DeadlineExceeded), false},
		{"nested_unknown", errors.Join(aborted, errors.Join(conflict, context.Canceled)), false},
		{"untyped_marker", errors.New(conflict.Message), false},
		{"different_conflict", &surrealdb.QueryError{Message: "other conflict"}, false},
		{"nil", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			matched, known := repoIndexCensusConflict(test.err)
			if got := matched && known; got != test.want {
				t.Fatalf("retry=%t want=%t", got, test.want)
			}
		})
	}
}

// These single-attempt SA01/SDK/CBOR fixtures prove pre-forward accounting and
// failure custody, not native SQL evaluation. TestRepoIndexCensusRetry exercises
// the public methods' refresh/retry policy separately.
func TestRepoIndexAccountingOperands(t *testing.T) {
	for _, clearing := range []bool{false, true} {
		for _, mode := range []string{"noop", "changed", "caller", "fanout", "coalesce", "repair", "missing", "changed_census", "lost", "canceled"} {
			if clearing && mode == "repair" {
				continue
			}
			t.Run(fmt.Sprintf("%t/%s", clearing, mode), func(t *testing.T) {
				base, owner, controller := storeAccountingFixture(t, 40, 2)
				ctx, cancel := context.WithCancel(base)
				defer cancel()
				db, native := storeAccountingDB(t, base, owner)
				census := repoIndexTestObservation(clearing)
				wantRows := uint64(1)
				if clearing {
					wantRows++
				}
				if mode == "changed" || mode == "caller" || mode == "fanout" || mode == "coalesce" || mode == "repair" {
					*census.Branches.Retire, *census.Branches.Revision = true, true
					wantRows = 3
				}
				switch mode {
				case "caller":
					census.Callers = []models.RecordID{models.NewRecordID("caller_generation_publication", "neutral")}
					wantRows += 2
				case "fanout", "coalesce":
					census.Catalogs = []models.RecordID{models.NewRecordID("resolver_catalog_publication", "neutral")}
					wantRows += 3
					if mode == "coalesce" {
						census.Pending = []repoIndexPending{{
							ID:         models.NewRecordID(string(JobResolverCatalog), []any{"pending", uint64(7)}),
							Projection: models.NewRecordID("repo", "different-legacy-target"),
						}}
					}
				case "repair":
					*census.Branches.Repair, *census.Branches.Outcomes = true, true
					census.Published = []models.RecordID{models.NewRecordID("extraction_run", "published")}
					census.Staged = []models.RecordID{models.NewRecordID("extraction_run", "staged")}
					census.Attempts = []models.RecordID{models.NewRecordID("extraction_attempt", "attempt")}
					census.Outcomes = []models.RecordID{models.NewRecordID("extraction_domain_outcome", "outcome")}
					wantRows += 4
				case "missing":
					census = repoIndexTestObservation(false)
					census.Before = []cbor.RawMessage{}
					wantRows = 1
				}
				calls := 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					calls++
					if calls == 1 {
						if mode == "canceled" {
							cancel()
						}
						return generationAccountingCensusReply(7, []repoIndexObservation{census}), nil
					}
					sql := request.Params[0].(string)
					var payload struct {
						Census     repoIndexObservation `json:"index_census"`
						Projection models.RecordID      `json:"index_projection"`
					}
					if err := native.codec.Unmarshal(request.Params[1].(cbor.RawMessage), &payload); err != nil {
						return nil, err
					}
					projection := repoID("neutral")
					if len(census.Pending) == 1 {
						projection = census.Pending[0].Projection
					}
					if !reflect.DeepEqual(payload.Census, census) || !reflect.DeepEqual(payload.Projection, projection) ||
						!strings.Contains(sql, "DELETE $index_census.catalogs") ||
						!strings.Contains(sql, "DELETE $index_census.callers") ||
						strings.Contains(sql, "$index_census.pending[0].id") != (len(census.Catalogs) == 1) ||
						strings.Contains(sql, "UPDATE $index_projection") != (len(census.Catalogs) == 1) ||
						strings.Contains(sql, "DELETE $publication_rid") != *census.Branches.Retire ||
						strings.Contains(sql, "SET evidence_revision =") != *census.Branches.Revision ||
						strings.Contains(sql, "SET caller_publication_revision =") != (len(census.Callers) == 1) ||
						strings.Contains(sql, "CREATE resolver_catalog_job CONTENT") != (len(census.Catalogs) == 1 && len(census.Pending) == 0) ||
						strings.Contains(sql, "UPDATE $pending_catalog SET") != (len(census.Catalogs) == 1 && len(census.Pending) == 1) ||
						strings.Index(sql, "caller-generation publication writer is not active") > strings.Index(sql, "repository index census changed") ||
						strings.Index(sql, "repository index census changed") > strings.Index(sql, "LET $updated = UPDATE") {
						return nil, errors.New("index mutation lost actual operand/authority fence")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != wantRows || prefix.MaximumRows != wantRows {
						return nil, errors.New("index mutation forwarded before exact operand ACK")
					}
					switch mode {
					case "changed_census":
						return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "phebs-conflict: repository index census changed"}}, nil
					case "lost":
						return nil, context.DeadlineExceeded
					case "missing":
						return queueAccountingOK([]Repo{}), nil
					}
					return queueAccountingOK([]Repo{{Name: "neutral"}}), nil
				}
				s := &Surreal{db: db, accounting: owner}
				var err error
				if clearing {
					err = s.clearRepoIndexStateOnce(ctx, "neutral")
				} else {
					err = s.setRepoIndexedStateOnce(ctx, "neutral", "commit", nil, nil, time.Now())
				}
				wantCalls, wantTX := 2, uint64(1)
				if mode == "canceled" {
					wantCalls, wantTX, wantRows = 1, 0, 0
				}
				prefix, _ := controller.Snapshot()
				wantOK := mode != "missing" && mode != "changed_census" && mode != "lost" && mode != "canceled"
				if (err == nil) != wantOK || calls != wantCalls || prefix.Transactions != wantTX || prefix.Rows != wantRows {
					t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
				}
				if mode == "missing" && !errors.Is(err, ErrNotFound) {
					t.Fatalf("failure custody changed: prefix=%+v error=%v", prefix, err)
				}
			})
		}
	}
}

func TestRepoIndexAccountingBoundAndRefusals(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, count := range []int{509, 510, 512, 513} {
			t.Run(fmt.Sprintf("%t/%d", selected, count), func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				if !selected {
					owner = nil
				}
				db, native := storeAccountingDB(t, ctx, owner)
				census := repoIndexTestObservation(true)
				*census.Branches.Revision = true
				for index := range count {
					census.Outcomes = append(census.Outcomes, models.NewRecordID("extraction_domain_outcome", uint64(index)))
				}
				calls := 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					calls++
					if calls == 1 {
						return generationAccountingCensusReply(7, []repoIndexObservation{census}), nil
					}
					bounded := strings.Contains(request.Params[0].(string), "repository index census changed")
					if bounded != (count == 509) {
						return nil, errors.New("ordinary overflow did not keep atomic original predicate")
					}
					return queueAccountingOK([]Repo{{Name: "neutral"}}), nil
				}
				err := (&Surreal{db: db, accounting: owner}).ClearRepoIndexState(ctx, "neutral")
				prefix, _ := controller.Snapshot()
				if selected && count > 509 {
					if !errors.Is(err, storeaccounting.ErrDescriptor) || calls != 1 || prefix.Transactions != 0 || prefix.Rows != 0 {
						t.Fatalf("oversize submitted: calls=%d prefix=%+v error=%v", calls, prefix, err)
					}
				} else if err != nil || calls != 2 || selected && (prefix.Rows != 512 || prefix.MaximumRows != 512) {
					t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
				}
			})
		}
	}
	for _, mode := range []string{"null", "flag", "before", "wrong_table", "duplicate", "branch", "pending", "unit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			census := repoIndexTestObservation(false)
			var unit *analysisunit.State
			switch mode {
			case "null":
				census.Callers = nil
			case "flag":
				census.Branches.Retire = nil
			case "before":
				census.Before[0] = cbor.RawMessage{0xf6}
			case "wrong_table", "duplicate":
				*census.Branches.Retire, *census.Branches.Revision = true, true
				id := models.NewRecordID("caller_generation_publication", "one")
				if mode == "wrong_table" {
					id = repoID("wrong")
				}
				census.Callers = []models.RecordID{id}
				if mode == "duplicate" {
					census.Callers = append(census.Callers, id)
				}
			case "branch":
				census.Outcomes = []models.RecordID{models.NewRecordID("extraction_domain_outcome", "inactive")}
			case "pending":
				census.Pending = []repoIndexPending{{ID: models.NewRecordID(string(JobResolverCatalog), "orphan"), Projection: repoID("neutral")}}
			case "unit":
				unit = &analysisunit.State{Digest: "invalid"}
			}
			calls := 0
			native.call = func(_ context.Context, _ *connection.RPCRequest) (any, error) {
				calls++
				if mode == "duplicate" && calls == 2 {
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "phebs-conflict: repository index census changed"}}, nil
				}
				return generationAccountingCensusReply(7, []repoIndexObservation{census}), nil
			}
			err := (&Surreal{db: db, accounting: owner}).setRepoIndexedStateOnce(ctx, "neutral", "commit", nil, unit, time.Now())
			prefix, _ := controller.Snapshot()
			wantCalls, wantRows, wantTX := 1, uint64(0), uint64(0)
			if mode == "unit" {
				wantCalls = 0
			}
			if mode == "duplicate" {
				// Native refusal retains the attempted supplied-operand prefix.
				wantCalls, wantRows, wantTX = 2, 5, 1
			}
			if err == nil || calls != wantCalls || prefix.Rows != wantRows || prefix.Transactions != wantTX {
				t.Fatalf("invalid census submitted: calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
		})
	}
}

func TestRepoIndexAccountingProjectionRecipe(t *testing.T) {
	if repoIndexResolverProjectionSQL != strings.Replace(projectResolverJobSQL,
		"type::record('repo', $resolver_projected.target)", "$index_projection", 1) {
		t.Fatal("bounded projection changed more than its supplied native target")
	}
}

func TestRepoIndexAccountingNativeCensus(t *testing.T) {
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
	const repository, projected = "example.com/neutral/index-census", "example.com/neutral/legacy-projection"
	for _, name := range []string{repository, projected} {
		if err := s.UpsertRepo(ctx, Repo{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	focused, err := (analysisunit.Scope{Repository: repository, Name: "service", Primary: []string{"service"}}).State()
	if err != nil {
		t.Fatal(err)
	}
	whole := analysisunit.CloneState(focused)
	whole.SearchIndexPosture = analysisunit.SearchIndexWholeRepository
	if err := s.SetRepoIndexedState(ctx, repository, "commit", nil, whole, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"published", "staged"} {
		requireCandidateRawQuery(t, ctx, s, `
CREATE $rid CONTENT {
 repo: $name, commit: 'commit', unit_digest: $unit_digest, run_id: $run_id,
 status: $status, store_schema_version: $schema, evidence_format_version: $format,
 evidence_migration_version: $migration, retention_quarantined: false
};`, map[string]any{
			"rid": extractionRunID(status), "name": repository, "unit_digest": focused.Digest,
			"run_id": status, "status": status, "schema": evidenceStoreSchemaVersion,
			"format": evidenceFormatVersion, "migration": evidenceMigrationVersion,
		})
	}
	attemptID := models.NewRecordID("extraction_attempt", []any{"neutral", uint64(1)})
	requireCandidateRawQuery(t, ctx, s, `
CREATE $rid CONTENT {
 repo: $name, commit: 'commit', unit_digest: $unit_digest, status: 'staged',
 store_schema_version: $schema, evidence_format_version: $format,
 evidence_migration_version: $migration
};`, map[string]any{
		"rid": attemptID, "name": repository, "unit_digest": focused.Digest,
		"schema": evidenceStoreSchemaVersion, "format": evidenceFormatVersion, "migration": evidenceMigrationVersion,
	})
	// Raw schema-valid publication intentionally exercises the retiring writer,
	// not the public authority validator or a fabricated successful catalog.
	publication := ResolverCatalogPublication{
		Repository: repository, Declarations: []ResolverCatalogDeclarationPublication{},
		ResolverPacks: []ResolverCatalogPack{}, ControlRevision: 1,
		WriterSchema: resolverCatalogWriterSchema, PublishedAt: time.Now(),
	}
	requireCandidateRawQuery(t, ctx, s, "CREATE $rid CONTENT $body;", map[string]any{
		"rid": resolverCatalogPublicationID(repository), "body": publication,
	})
	pending := models.NewRecordID(string(JobResolverCatalog), "legacy")
	requireCandidateRawQuery(t, ctx, s, `CREATE $rid CONTENT {
 target: $projected, status: 'pending', attempts: 0, created_at: time::now(),
 pending_key: $name, force: false
};`, map[string]any{"rid": pending, "projected": projected, "name": repository})
	vars := map[string]any{
		"rid": repoID(repository), "name": repository, "hash": "commit", "unit": focused, "unit_digest": focused.Digest,
		"evidence_store_schema": evidenceStoreSchemaVersion, "evidence_format": evidenceFormatVersion,
		"evidence_migration": evidenceMigrationVersion, "max_evidence_identity_bytes": maxEvidenceIdentityBytes,
	}
	census, sql, err := s.repoIndexCensus(ctx, vars, false)
	if err != nil || len(census.Catalogs) != 1 || len(census.Published) != 1 || len(census.Staged) != 1 ||
		len(census.Attempts) != 1 || len(census.Pending) != 1 || census.writeRows() != 9 ||
		!reflect.DeepEqual(census.Pending[0].Projection, repoID(projected)) {
		t.Fatalf("native positive census=%+v error=%v", census, err)
	}
	vars["index_census"] = census
	requireCandidateRawQuery(t, ctx, s, "UPDATE $rid SET target = $name;", map[string]any{"rid": pending, "name": repository})
	_, err = surrealdb.Query[any](ctx, s.db, "BEGIN;"+sql+repoIndexCensusFenceSQL+`
UPDATE $rid SET indexed_commit_hash = 'must-not-commit';
COMMIT;`, vars)
	if conflict, known := repoIndexCensusConflict(err); !conflict || !known {
		t.Fatalf("changed actual projection was not fenced: %v", err)
	}
	repo, err := s.GetRepo(ctx, repository)
	if err != nil || repo.IndexedCommitHash != "commit" {
		t.Fatalf("refused census changed repo: %+v %v", repo, err)
	}
	requireCandidateRawQuery(t, ctx, s, "UPDATE $rid SET target = $name;", map[string]any{"rid": pending, "name": projected})
	if err := s.SetRepoIndexedState(ctx, repository, "commit", nil, focused, time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err := surrealdb.Query[[]struct {
		Status string `json:"status"`
	}](ctx, s.db,
		"SELECT id, status FROM $ids ORDER BY id;", map[string]any{"ids": []models.RecordID{extractionRunID("published"), extractionRunID("staged")}})
	if err != nil || result == nil || len(*result) != 1 || len((*result)[0].Result) != 2 ||
		(*result)[0].Result[0].Status != "superseded" || (*result)[0].Result[1].Status != "aborted" {
		t.Fatalf("actual evidence targets were not updated: %+v %v", result, err)
	}
	remaining, err := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM $ids;",
		map[string]any{"ids": []models.RecordID{attemptID, resolverCatalogPublicationID(repository)}})
	if err != nil || remaining == nil || len(*remaining) != 1 || len((*remaining)[0].Result) != 0 {
		t.Fatalf("actual retired targets remain: %+v %v", remaining, err)
	}
	projection, err := surrealdb.Query[[]struct {
		ID models.RecordID `json:"latest_resolver_job"`
	}](ctx, s.db,
		"SELECT latest_resolver_job FROM $rid;", map[string]any{"rid": repoID(projected)})
	if err != nil || projection == nil || len((*projection)[0].Result) != 1 ||
		!reflect.DeepEqual((*projection)[0].Result[0].ID, pending) {
		t.Fatalf("legacy target projection lost: %+v %v", projection, err)
	}
	for range 2 {
		if err := s.ClearRepoIndexState(ctx, repository); err != nil {
			t.Fatal(err)
		}
	}
}
