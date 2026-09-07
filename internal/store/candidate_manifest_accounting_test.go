//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func candidateAccountingPublication(repository string) CandidateManifestPublication {
	return CandidateManifestPublication{
		Repository: repository, HeadCommit: strings.Repeat("a", 40),
		PolicyDigest: internalCallerDigest('b'), ManifestDigest: internalCallerDigest('c'),
		GenerationDigest: internalCallerDigest('d'), ManifestPath: candidateid.ManifestName(repository),
		ControlRevision: 1, PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func candidateAccountingObservation(active bool) candidateManifestObservation {
	revision := active
	return candidateManifestObservation{
		Before: []cbor.RawMessage{{0xa0}, {0xa0}}, Active: &active, Revision: &revision,
		Catalogs: []models.RecordID{}, Callers: []models.RecordID{}, Published: []models.RecordID{},
		Staged: []models.RecordID{}, Attempts: []models.RecordID{}, Outcomes: []models.RecordID{},
		Extraction: []repoIndexPending{}, Resolver: []repoIndexPending{},
	}
}

func candidateAccountingOperation(ctx context.Context, s *Surreal, publication CandidateManifestPublication, clear bool) error {
	if clear {
		return s.ClearCandidateManifestPublication(ctx, publication.Repository)
	}
	return s.PublishCandidateManifest(ctx, publication)
}

func candidateAccountingCensusSQL(clear bool) (string, int) {
	if clear {
		return candidateClearCensusSQL + "RETURN [$candidate_census];", candidateClearCensusControls
	}
	return candidatePublishCensusSQL + "RETURN [$candidate_census];", candidatePublishCensusControls
}

// Scripted replies traverse the actual SDK connection and CBOR marshaler.
// They establish supplied operands and SA01 ACK ordering, not SQL evaluation.
func TestCandidateManifestAccountingOperands(t *testing.T) {
	for _, clear := range []bool{false, true} {
		for _, mode := range []string{"inactive", "new", "coalesced", "retire_all", "exact", "known_failure", "unknown_failure", "refused"} {
			t.Run(fmt.Sprintf("clear_%t/%s", clear, mode), func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				db, native := storeAccountingDB(t, ctx, owner)
				publication := candidateAccountingPublication("example.com/neutral/candidate")
				census := candidateAccountingObservation(mode != "inactive")
				wantRows := uint64(6)
				if clear {
					wantRows = 2
				}
				if mode == "inactive" {
					wantRows = 1
					if clear {
						wantRows = 0
					}
				}
				if mode == "exact" {
					*census.Revision = false
					if !clear {
						wantRows--
					} else {
						// A native clear of a missing pointer still retires callers.
						*census.Active = false
						census.Callers = []models.RecordID{models.NewRecordID("caller_generation_publication", "orphan")}
					}
				}
				if mode == "coalesced" || mode == "retire_all" {
					census.Catalogs = []models.RecordID{models.NewRecordID("resolver_catalog_publication", []any{"neutral", uint64(7)})}
					census.Callers = []models.RecordID{models.NewRecordID("caller_generation_publication", "caller")}
					census.Outcomes = []models.RecordID{models.NewRecordID("extraction_domain_outcome", "outcome")}
					wantRows += 4 // Three selected rows and the single-caller revision.
					if clear {
						wantRows += 2 // Resolver successor and its projected repo.
					} else {
						census.Published = []models.RecordID{models.NewRecordID("extraction_run", "published")}
						census.Staged = []models.RecordID{models.NewRecordID("extraction_run", "staged")}
						census.Attempts = []models.RecordID{models.NewRecordID("extraction_attempt", "attempt")}
						wantRows += 3
					}
				}
				if mode == "coalesced" {
					census.Resolver = []repoIndexPending{{ID: models.NewRecordID(string(JobResolverCatalog), "pending"), Projection: repoID("different-legacy-target")}}
					if !clear {
						census.Extraction = []repoIndexPending{{ID: models.NewRecordID(string(JobExtract), []any{"pending", uint64(1)}), Projection: repoID("other-legacy-target")}}
					}
				}
				if got := census.writeRows(clear); got != wantRows {
					t.Fatalf("fixture operands=%d want=%d", got, wantRows)
				}
				censusSQL, controls := candidateAccountingCensusSQL(clear)
				reads, writes := 0, 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					sql := request.Params[0].(string)
					if sql == censusSQL {
						reads++
						return generationAccountingCensusReply(controls, []candidateManifestObservation{census}), nil
					}
					writes++
					var payload struct {
						Census     candidateManifestObservation `json:"candidate_expected"`
						Extraction models.RecordID              `json:"candidate_extraction_projection"`
						Resolver   models.RecordID              `json:"candidate_resolver_projection"`
					}
					if err := native.codec.Unmarshal(request.Params[1].(cbor.RawMessage), &payload); err != nil {
						return nil, err
					}
					if !reflect.DeepEqual(payload.Census, census) || !strings.Contains(sql, candidateManifestCensusFenceSQL) ||
						strings.Index(sql, candidateManifestCensusFenceSQL) > strings.Index(sql, "LET $caller_writer_ok") {
						return nil, errors.New("candidate lost exact CBOR preimage or pre-mutation fence")
					}
					if strings.Contains(sql, "SET caller_publication_revision") != (len(census.Callers) == 1) ||
						strings.Contains(sql, "SET evidence_revision") != *census.Revision {
						return nil, errors.New("candidate emitted inactive revision operand")
					}
					resolverFanout := *census.Active && (!clear || len(census.Catalogs) == 1)
					if strings.Contains(sql, "CREATE resolver_catalog_job CONTENT") != (resolverFanout && len(census.Resolver) == 0) ||
						strings.Contains(sql, "UPDATE $pending_catalog SET") != (resolverFanout && len(census.Resolver) == 1) ||
						strings.Contains(sql, "UPDATE $candidate_resolver_projection") != resolverFanout {
						return nil, errors.New("candidate resolver branch/projection operands differ")
					}
					if resolverFanout {
						projection := repoID(publication.Repository)
						if len(census.Resolver) == 1 {
							projection = census.Resolver[0].Projection
						}
						if !reflect.DeepEqual(payload.Resolver, projection) {
							return nil, errors.New("candidate resolver target changed")
						}
					}
					if !clear {
						if !strings.Contains(sql, "UPSERT $publication_rid") ||
							strings.Contains(sql, "CREATE extraction_job CONTENT") != (*census.Active && len(census.Extraction) == 0) ||
							strings.Contains(sql, "UPDATE $pending SET") != (*census.Active && len(census.Extraction) == 1) ||
							strings.Contains(sql, "UPDATE $candidate_extraction_projection") != *census.Active {
							return nil, errors.New("candidate extraction or supplied UPSERT operand differs")
						}
						projection := repoID(publication.Repository)
						if len(census.Extraction) == 1 {
							projection = census.Extraction[0].Projection
						}
						if !reflect.DeepEqual(payload.Extraction, projection) {
							return nil, errors.New("candidate extraction target changed")
						}
					} else if strings.Contains(sql, "DELETE $rid RETURN NONE") != *census.Active {
						return nil, errors.New("candidate clear emitted inactive pointer DELETE")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != wantRows || prefix.MaximumRows != wantRows {
						return nil, fmt.Errorf("candidate native write preceded exact ACK: %+v %v", prefix, err)
					}
					if mode == "known_failure" {
						return nil, &surrealdb.QueryError{Message: "phebs-permanent: neutral native refusal"}
					}
					if mode == "unknown_failure" {
						return nil, context.DeadlineExceeded
					}
					rows := []candidateManifestPublicationRec{}
					if mode != "inactive" && mode != "refused" {
						rows = append(rows, candidateManifestPublicationRec{CandidateManifestPublication: publication})
					}
					return queueAccountingOK(rows), nil
				}
				err := candidateAccountingOperation(ctx, &Surreal{db: db, accounting: owner}, publication, clear)
				wantOK := mode != "known_failure" && mode != "unknown_failure" && (clear || mode != "inactive" && mode != "refused")
				prefix, _ := controller.Snapshot()
				if (err == nil) != wantOK || reads != 1 || writes != 1 || prefix.Transactions != 1 || prefix.Rows != wantRows ||
					(prefix.Producers[0].Calls != 0) != (mode == "unknown_failure") {
					t.Fatalf("read/write=%d/%d prefix=%+v err=%v wantOK=%t", reads, writes, prefix, err, wantOK)
				}
			})
		}
	}
}

func TestCandidateManifestAccountingBoundsAndMalformed(t *testing.T) {
	for _, clear := range []bool{false, true} {
		for _, selected := range []bool{false, true} {
			for _, count := range []int{506, 507, 510, 511, 513} {
				t.Run(fmt.Sprintf("clear_%t/selected_%t/%d", clear, selected, count), func(t *testing.T) {
					ctx, owner, controller := storeAccountingFixture(t, 40, 2)
					if !selected {
						owner = nil
					}
					db, native := storeAccountingDB(t, ctx, owner)
					census := candidateAccountingObservation(true)
					for i := range count {
						census.Outcomes = append(census.Outcomes, models.NewRecordID("extraction_domain_outcome", uint64(i)))
					}
					rows := uint64(count + 6)
					if clear {
						rows = uint64(count + 2)
					}
					calls := 0
					_, controls := candidateAccountingCensusSQL(clear)
					native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
						calls++
						if calls == 1 {
							return generationAccountingCensusReply(controls, []candidateManifestObservation{census}), nil
						}
						wantSQL := candidatePublishSQL(nil)
						if clear {
							wantSQL = candidateClearSQL(nil)
						}
						sql := request.Params[0].(string)
						if rows > restoreClearRows && sql != wantSQL || rows <= restoreClearRows && !strings.Contains(sql, candidateManifestCensusFenceSQL) {
							return nil, errors.New("candidate overflow narrowed ordinary predicate or lost bounded fence")
						}
						return queueAccountingOK([]candidateManifestPublicationRec{{CandidateManifestPublication: candidateAccountingPublication("example.com/neutral/bounds")}}), nil
					}
					err := candidateAccountingOperation(ctx, &Surreal{db: db, accounting: owner}, candidateAccountingPublication("example.com/neutral/bounds"), clear)
					prefix, _ := controller.Snapshot()
					if selected && rows > restoreClearRows {
						if !errors.Is(err, storeaccounting.ErrDescriptor) || calls != 1 || prefix.Transactions != 0 || prefix.Rows != 0 {
							t.Fatalf("overflow reached native: calls=%d prefix=%+v error=%v", calls, prefix, err)
						}
					} else if err != nil || calls != 2 || selected && (prefix.Transactions != 1 || prefix.Rows != rows || prefix.MaximumRows != rows) {
						t.Fatalf("bounds calls=%d prefix=%+v error=%v", calls, prefix, err)
					}
				})
			}
		}
		for _, mode := range []string{"null_vector", "active", "revision", "before", "wrong_table", "oversize_vector", "null_pending", "many_pending", "wrong_pending", "wrong_projection", "arity", "control_data", "empty_result", "canceled"} {
			t.Run(fmt.Sprintf("clear_%t/%s", clear, mode), func(t *testing.T) {
				base, owner, controller := storeAccountingFixture(t, 40, 2)
				ctx, cancel := context.WithCancel(base)
				defer cancel()
				db, native := storeAccountingDB(t, base, owner)
				census := candidateAccountingObservation(true)
				switch mode {
				case "null_vector":
					census.Outcomes = nil
				case "active":
					census.Active = nil
				case "revision":
					census.Revision = nil
				case "before":
					census.Before = census.Before[:1]
				case "wrong_table":
					census.Callers = []models.RecordID{repoID("wrong")}
				case "oversize_vector":
					for i := range restoreClearRows + 2 {
						census.Outcomes = append(census.Outcomes, models.NewRecordID("extraction_domain_outcome", uint64(i)))
					}
				case "null_pending":
					census.Resolver = nil
				case "many_pending", "wrong_pending", "wrong_projection":
					census.Resolver = []repoIndexPending{{ID: models.NewRecordID(string(JobResolverCatalog), "pending"), Projection: repoID("neutral")}}
					if mode == "many_pending" {
						census.Resolver = append(census.Resolver, census.Resolver[0])
					}
					if mode == "wrong_pending" {
						census.Resolver[0].ID = repoID("wrong")
					}
					if mode == "wrong_projection" {
						census.Resolver[0].Projection.Table = string(JobExtract)
					}
				}
				_, controls := candidateAccountingCensusSQL(clear)
				calls := 0
				native.call = func(_ context.Context, _ *connection.RPCRequest) (any, error) {
					calls++
					result := generationAccountingCensusReply(controls, []candidateManifestObservation{census})
					switch mode {
					case "arity":
						result = result[1:]
					case "control_data":
						result[0].Result = []candidateManifestObservation{census}
					case "empty_result":
						result[controls].Result = []candidateManifestObservation{}
					case "canceled":
						cancel()
					}
					return result, nil
				}
				err := candidateAccountingOperation(ctx, &Surreal{db: db, accounting: owner}, candidateAccountingPublication("example.com/neutral/invalid"), clear)
				prefix, _ := controller.Snapshot()
				if err == nil || calls != 1 || prefix.Transactions != 0 || prefix.Rows != 0 {
					t.Fatalf("malformed census forwarded: calls=%d prefix=%+v error=%v", calls, prefix, err)
				}
			})
		}
	}
}

func TestCandidateManifestAccountingRetry(t *testing.T) {
	for _, mode := range []string{"write_retry", "read_retry", "exhausted", "unknown_conflict"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			publication := candidateAccountingPublication("example.com/neutral/retry")
			census := candidateAccountingObservation(true)
			censusSQL, controls := candidateAccountingCensusSQL(false)
			reads, writes := 0, 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Params[0].(string) == censusSQL {
					reads++
					if mode == "read_retry" && reads == 1 {
						return nil, &surrealdb.QueryError{Message: "phebs-conflict: neutral read retry"}
					}
					census.Outcomes = []models.RecordID{models.NewRecordID("extraction_domain_outcome", fmt.Sprint(reads))}
					return generationAccountingCensusReply(controls, []candidateManifestObservation{census}), nil
				}
				writes++
				var payload struct {
					Census candidateManifestObservation `json:"candidate_expected"`
				}
				if err := native.codec.Unmarshal(request.Params[1].(cbor.RawMessage), &payload); err != nil {
					return nil, err
				}
				if !reflect.DeepEqual(payload.Census, census) {
					return nil, errors.New("retry reused stale candidate preimage")
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != uint64(writes) || prefix.Rows != uint64(7*writes) {
					return nil, errors.New("candidate retry lacks cumulative ACK")
				}
				if mode == "unknown_conflict" {
					return nil, errors.New("transport lost while conflict reply was unknown")
				}
				if mode == "exhausted" || mode == "write_retry" && writes == 1 {
					return nil, &surrealdb.QueryError{Message: "phebs-conflict: candidate mutation census changed"}
				}
				return queueAccountingOK([]candidateManifestPublicationRec{{CandidateManifestPublication: publication}}), nil
			}
			err := (&Surreal{db: db, accounting: owner}).PublishCandidateManifest(ctx, publication)
			wantReads, wantWrites := 2, 2
			if mode == "read_retry" {
				wantWrites = 1
			}
			if mode == "exhausted" {
				wantReads, wantWrites = maxQueueRetries, maxQueueRetries
			}
			if mode == "unknown_conflict" {
				wantReads, wantWrites = 1, 1
			}
			if (err == nil) != (mode == "write_retry" || mode == "read_retry") || reads != wantReads || writes != wantWrites {
				t.Fatalf("retry reads/writes=%d/%d want=%d/%d error=%v", reads, writes, wantReads, wantWrites, err)
			}
		})
	}
}

func TestCandidateManifestAccountingReads(t *testing.T) {
	for _, list := range []bool{false, true} {
		for _, mode := range []string{"valid", "empty", "malformed", "known_failure", "unknown_failure"} {
			t.Run(fmt.Sprintf("list_%t/%s", list, mode), func(t *testing.T) {
				base, owner, controller := storeAccountingFixture(t, 40, 2)
				ctx, ledger, err := readaccounting.Start(base, readaccounting.Counts{StoreReadAttempts: 1})
				if err != nil {
					t.Fatal(err)
				}
				db, native := storeAccountingDB(t, ctx, owner)
				publication := candidateAccountingPublication("example.com/neutral/read")
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					want := "SELECT * FROM $rid"
					if list {
						want = "SELECT * FROM candidate_manifest_publication ORDER BY repository"
					}
					if request.Params[0] != want {
						return nil, errors.New("candidate reader SQL changed")
					}
					if mode == "known_failure" {
						return nil, &surrealdb.QueryError{Message: "neutral unavailable"}
					}
					if mode == "unknown_failure" {
						return nil, context.DeadlineExceeded
					}
					rows := []candidateManifestPublicationRec{}
					if mode != "empty" {
						if mode == "malformed" {
							publication.ControlRevision = 0
						}
						rows = append(rows, candidateManifestPublicationRec{CandidateManifestPublication: publication})
					}
					return queueAccountingOK(rows), nil
				}
				s := &Surreal{db: db, accounting: owner}
				if list {
					_, err = s.ListCandidateManifestPublications(ctx)
				} else {
					_, err = s.GetCandidateManifestPublication(ctx, publication.Repository)
				}
				wantOK := mode == "valid" || list && mode == "empty"
				counts, finishErr := ledger.Finish()
				wantReads := uint64(1)
				if list {
					wantReads = 0
				}
				prefix, _ := controller.Snapshot()
				if (err == nil) != wantOK || native.calls != 1 || finishErr != nil || counts.StoreReadAttempts != wantReads || prefix.Transactions != 0 || prefix.Rows != 0 ||
					prefix.Producers[0].Calls != 0 {
					t.Fatalf("read counts=%+v prefix=%+v error=%v finish=%v", counts, prefix, err, finishErr)
				}
			})
		}
	}
}

// Native vars mirror the public method's input bindings; no test response or
// caller-supplied census is admitted as production accounting authority.
func candidateAccountingNativeVars(publication CandidateManifestPublication, clear bool) map[string]any {
	vars := map[string]any{
		"repo_rid": repoID(publication.Repository), "repository": publication.Repository,
		"caller_migration_rid": callerGenerationPublicationMigrationID(), "caller_migration_version": callerGenerationPublicationMigrationVersion,
		"candidate_limit": restoreClearRows + 1,
	}
	if clear {
		vars["rid"] = candidateManifestPublicationID(publication.Repository)
		return vars
	}
	vars["publication_rid"], vars["head_commit"], vars["unit_digest"] = candidateManifestPublicationID(publication.Repository), publication.HeadCommit, publication.UnitDigest
	vars["policy_digest"], vars["manifest_digest"], vars["generation_digest"] = publication.PolicyDigest, publication.ManifestDigest, publication.GenerationDigest
	vars["manifest_path"], vars["requested_control_revision"], vars["published_at"] = publication.ManifestPath, publication.ControlRevision, storeTimestamp(time.Now())
	vars["evidence_store_schema"], vars["evidence_format"], vars["evidence_migration"] = evidenceStoreSchemaVersion, evidenceFormatVersion, evidenceMigrationVersion
	vars["max_evidence_identity_bytes"] = maxEvidenceIdentityBytes
	return vars
}

// One engine for this top-level suite. Each attempted write uses the genuine
// census and generated SQL; the hook changes native state after census, before
// BEGIN. Public publish retries are tested separately above, so a successful
// refreshed attempt cannot hide the first transaction's atomic refusal here.
func TestCandidateManifestAccountingNativeFences(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	directory := t.TempDir()
	s, err := OpenLocalMemory(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := s.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	runtime, err := ReadLocalRuntime(directory)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := url.ParseRequestURI(runtime.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	conn := &resolverRetirementNativeConnection{Connection: gorillaws.New(connection.NewConfig(endpoint))}
	db, err := surrealdb.FromConnection(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := db.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Use(ctx, "phebs", "phebs"); err != nil {
		t.Fatal(err)
	}
	observed := &Surreal{db: db}
	requireCandidateRawQuery(t, ctx, s, "REMOVE TABLE caller_generation_publication; DEFINE TABLE caller_generation_publication SCHEMALESS;", nil)
	for _, clear := range []bool{false, true} {
		for _, mode := range []string{"unchanged", "changed_ids", "changed_authority", "changed_pending"} {
			t.Run(fmt.Sprintf("clear_%t/%s", clear, mode), func(t *testing.T) {
				repository := fmt.Sprintf("example.com/neutral/candidate-fence/%t/%s", clear, strings.ReplaceAll(mode, "_", "-"))
				publication := candidateAccountingPublication(repository)
				if err := s.UpsertRepo(ctx, Repo{Name: repository}); err != nil {
					t.Fatal(err)
				}
				if err := s.SetRepoIndexed(ctx, repository, publication.HeadCommit, time.Now()); err != nil {
					t.Fatal(err)
				}
				requireCandidateRawQuery(t, ctx, s, "UPSERT $rid CONTENT $body;", map[string]any{"rid": candidateManifestPublicationID(repository), "body": publication})
				catalog := resolverAccountingPublication(t)
				catalog.Repository, catalog.ManifestPath = repository, resolvercatalogid.ManifestName(repository)
				requireCandidateRawQuery(t, ctx, s, "UPSERT $rid CONTENT $body;", map[string]any{"rid": resolverCatalogPublicationID(repository), "body": catalog})
				rid := models.NewRecordID("caller_generation_publication", []any{repository, "one"})
				requireCandidateRawQuery(t, ctx, s, "CREATE $rid SET repository = $repository;", map[string]any{"rid": rid, "repository": repository})
				pending := models.NewRecordID(string(JobResolverCatalog), repository)
				requireCandidateRawQuery(t, ctx, s, "CREATE $rid CONTENT {target: $repository, pending_key: $repository, status: 'pending', attempts: 0, created_at: time::now(), force: false};", map[string]any{"rid": pending, "repository": repository})
				next := publication
				next.PolicyDigest, next.ManifestDigest, next.GenerationDigest, next.ControlRevision = internalCallerDigest('e'), internalCallerDigest('f'), internalCallerDigest('9'), 0
				vars := candidateAccountingNativeVars(next, clear)
				censusSQL, controls := candidateAccountingCensusSQL(clear)
				census, err := observed.candidateManifestCensus(ctx, strings.TrimSuffix(censusSQL, "RETURN [$candidate_census];"), controls, vars)
				if err != nil || !*census.Active || len(census.Callers) != 1 || len(census.Catalogs) != 1 || len(census.Resolver) != 1 {
					t.Fatalf("native census=%+v error=%v", census, err)
				}
				vars["candidate_expected"], vars["candidate_extraction_projection"], vars["candidate_resolver_projection"] = census, repoID(repository), census.Resolver[0].Projection
				conn.before = nil
				if mode != "unchanged" {
					conn.before = func(callCtx context.Context) error {
						statement := "CREATE $rid SET repository = $repository;"
						mutation := map[string]any{"rid": models.NewRecordID("caller_generation_publication", []any{repository, "two"}), "repository": repository}
						switch mode {
						case "changed_authority":
							statement, mutation = "UPDATE $rid SET version = 'neutral-invalid-writer';", map[string]any{"rid": callerGenerationPublicationMigrationID()}
						case "changed_pending":
							statement, mutation = "UPDATE $rid SET target = 'example.com/neutral/changed';", map[string]any{"rid": pending}
						}
						_, err := surrealdb.Query[any](callCtx, s.db, statement, mutation)
						return err
					}
				}
				if mode == "changed_authority" {
					t.Cleanup(func() {
						requireCandidateRawQuery(t, ctx, s, "UPDATE $rid SET version = $version;", map[string]any{"rid": callerGenerationPublicationMigrationID(), "version": callerGenerationPublicationMigrationVersion})
					})
				}
				sql := candidatePublishSQL(&census)
				if clear {
					sql = candidateClearSQL(&census)
				}
				before := conn.writes
				_, err = surrealdb.Query[any](ctx, db, sql, vars)
				if (err == nil) != (mode == "unchanged") || conn.writes != before+1 {
					t.Fatalf("native write error=%v attempts=%d", err, conn.writes-before)
				}
				if mode != "unchanged" && !strings.Contains(err.Error(), "candidate mutation census changed") {
					t.Fatalf("unexpected native refusal: %v", err)
				}
				actual, getErr := s.GetCandidateManifestPublication(ctx, repository)
				if clear && mode == "unchanged" {
					if !errors.Is(getErr, ErrNotFound) {
						t.Fatalf("candidate was not cleared: %+v %v", actual, getErr)
					}
				} else {
					want := publication.ManifestDigest
					if mode == "unchanged" {
						want = next.ManifestDigest
					}
					if getErr != nil || actual.ManifestDigest != want {
						t.Fatalf("candidate authority changed: %+v %v", actual, getErr)
					}
				}
				pointers, err := surrealdb.Query[[]models.RecordID](ctx, s.db, "SELECT VALUE id FROM caller_generation_publication WHERE repository = $repository;", map[string]any{"repository": repository})
				wantPointers := 1
				if mode == "unchanged" {
					wantPointers = 0
				}
				if mode == "changed_ids" {
					wantPointers = 2
				}
				if err != nil || len(firstDomainRows(pointers)) != wantPointers {
					t.Fatalf("partial caller retirement: %+v %v", pointers, err)
				}
				repo, err := s.GetRepo(ctx, repository)
				wantRevision := int64(0)
				if mode == "unchanged" {
					wantRevision = 1
				}
				if err != nil || repo.CallerPublicationRevision != wantRevision {
					t.Fatalf("partial repository revision: %+v %v", repo, err)
				}
			})
		}
	}
}
