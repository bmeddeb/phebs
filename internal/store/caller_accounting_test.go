//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func callerAccountingVars(native *storeSDKTestConnection, request *connection.RPCRequest) (map[string]any, error) {
	if request.Method != "query" || len(request.Params) != 2 {
		return nil, errors.New("unexpected caller method")
	}
	if vars, ok := request.Params[1].(map[string]any); ok {
		return vars, nil
	}
	raw, ok := request.Params[1].(cbor.RawMessage)
	if !ok {
		return nil, errors.New("caller payload was not bound")
	}
	var vars map[string]any
	err := native.codec.Unmarshal(raw, &vars)
	return vars, err
}

func TestCallerAdmissionAccounting(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, mode := range []string{"create", "replay", "refused", "native_error", "lost_write"} {
			t.Run(map[bool]string{false: "ordinary/", true: "selected/"}[selected]+mode, func(t *testing.T) {
				ctx := t.Context()
				var owner *storeCallOwner
				var controller *storeaccounting.Controller
				if selected {
					ctx, owner, controller = storeAccountingFixture(t, 40, 2)
				}
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				admission, _, err := prepareCallerGenerationAdmission(CallerGenerationAdmission{
					Generation: internalCallerGeneration(), Disposition: CallerGenerationAdmitted,
				}, []CallerLeafPair{}, []CallerLeafOutcome{})
				if err != nil {
					t.Fatal(err)
				}
				job := Job{ID: "caller_leaf_job:owned", Kind: JobCallerLeaf, Target: admission.Generation.Repository,
					Status: StatusRunning, LeaseToken: "lease", ClaimedBy: "worker"}
				writes, reads := 0, 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					vars, err := callerAccountingVars(native, request)
					if err != nil {
						return nil, err
					}
					sql := request.Params[0].(string)
					switch {
					case strings.Contains(sql, "SELECT * FROM caller_leaf_outcome"):
						reads++
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: []callerLeafOutcomeRec{}}}, nil
					case sql == recordCallerGenerationAdmissionSQL:
						writes++
						if !reflect.DeepEqual(vars["admission_rid"], callerGenerationAdmissionID(admission.Generation)) ||
							vars["generation_digest"] != admission.Generation.Digest || vars["pair_set_digest"] != admission.PairSetDigest ||
							vars["writer_schema"] != CallerLeafWriterSchema {
							return nil, errors.New("admission fixed operand or body changed")
						}
						if selected {
							prefix, err := controller.Snapshot()
							if err != nil || prefix.Transactions != 1 || prefix.Rows != 1 || prefix.MaximumRows != 1 {
								return nil, errors.New("admission preceded exact parent ACK")
							}
						}
						if mode == "lost_write" {
							return nil, context.DeadlineExceeded
						}
						if mode == "native_error" {
							return nil, &surrealdb.QueryError{Message: "phebs-conflict: neutral caller refusal"}
						}
						rows := []callerGenerationAdmissionRec{}
						if mode != "refused" {
							persisted := admission
							persisted.RecordedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
							rows = append(rows, callerGenerationAdmissionRec{CallerGenerationAdmission: persisted,
								Repository: admission.Generation.Repository, GenerationDigest: admission.Generation.Digest})
						}
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
					default:
						return nil, errors.New("unexpected caller query")
					}
				}
				err = s.RecordCallerGenerationAdmission(ctx, job, admission, []CallerLeafPair{})
				if (err == nil) != (mode == "create" || mode == "replay") {
					t.Fatalf("admission error=%v", err)
				}
				if mode == "refused" && !errors.Is(err, ErrConflict) {
					t.Fatalf("authority refusal=%v", err)
				}
				if writes != 1 || reads != 1 {
					t.Fatalf("admission added queries: writes=%d reads=%d", writes, reads)
				}
				if selected {
					prefix, _ := controller.Snapshot()
					if prefix.Transactions != 1 || prefix.Rows != 1 || prefix.MaximumRows != 1 ||
						(prefix.Producers[0].Calls == 1) != (mode == "lost_write") {
						t.Fatalf("admission prefix=%+v", prefix)
					}
				}
			})
		}
	}
}

func TestCallerPublicationClearAccounting(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, mode := range []string{"absent", "present", "retry", "native_error", "lost_write"} {
			t.Run(map[bool]string{false: "ordinary/", true: "selected/"}[selected]+mode, func(t *testing.T) {
				ctx := t.Context()
				var owner *storeCallOwner
				var controller *storeaccounting.Controller
				if selected {
					ctx, owner, controller = storeAccountingFixture(t, 40, 2)
				}
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				repository := "example.com/neutral/caller"
				writes := 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					vars, err := callerAccountingVars(native, request)
					if err != nil || !reflect.DeepEqual(vars["rid"], callerGenerationPublicationID(repository)) ||
						!reflect.DeepEqual(vars["repo_rid"], repoID(repository)) {
						return nil, errors.New("clear fixed operands changed")
					}
					sql := request.Params[0].(string)
					if !strings.Contains(sql, "LET $current = DELETE $rid RETURN BEFORE;") ||
						!strings.Contains(sql, "IF array::len($current) = 1 {") ||
						!strings.Contains(sql, "UPDATE $repo_rid SET caller_publication_revision") {
						return nil, errors.New("clear native guards changed")
					}
					writes++
					if selected {
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != uint64(writes) || prefix.Rows != 2*uint64(writes) || prefix.MaximumRows != 2 {
							return nil, errors.New("clear preceded exact parent ACK")
						}
					}
					if mode == "retry" && writes == 1 {
						return nil, &surrealdb.QueryError{Message: "phebs-conflict: neutral retry"}
					}
					if mode == "native_error" {
						return nil, &surrealdb.QueryError{Message: "phebs-permanent: neutral writer refusal"}
					}
					if mode == "lost_write" {
						return nil, context.DeadlineExceeded
					}
					return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
				}
				err := s.ClearCallerGenerationPublication(ctx, repository)
				if (err == nil) != (mode == "absent" || mode == "present" || mode == "retry") {
					t.Fatalf("clear error=%v", err)
				}
				wantWrites := 1
				if mode == "retry" {
					wantWrites = 2
				}
				if writes != wantWrites || native.calls != writes {
					t.Fatalf("clear added query/retry: writes=%d calls=%d", writes, native.calls)
				}
				if selected {
					prefix, _ := controller.Snapshot()
					if prefix.Transactions != uint64(writes) || prefix.Rows != 2*uint64(writes) ||
						(prefix.Producers[0].Calls == 1) != (mode == "lost_write") {
						t.Fatalf("clear prefix=%+v", prefix)
					}
				}
			})
		}
	}
}

func callerAccountingSummary(t *testing.T) CallerGenerationPublicationSummary {
	t.Helper()
	generation, err := prepareCallerGeneration(internalCallerGeneration())
	if err != nil {
		t.Fatal(err)
	}
	digest := internalCallerDigest('a')
	return CallerGenerationPublicationSummary{
		Generation: generation, PairPayloadDigest: digest, PairSetDigest: digest,
		PeakOpenFiles: maxCallerPeakOpenFiles, ManifestDigest: digest,
		ManifestPath:        callerpublicationid.ManifestName(generation.Digest, digest),
		PublicationRevision: 1, PublicationIncarnation: digest,
		WriterSchema: CallerGenerationPublicationWriterSchema,
		PublishedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCallerSummaryExplicitTransactionAccounting(t *testing.T) {
	for _, operation := range []string{"summary", "authority", "joint"} {
		for _, mode := range []string{"current", "stale", "native_error", "lost_reply"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				summary := callerAccountingSummary(t)
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					if _, err := callerAccountingVars(native, request); err != nil {
						return nil, err
					}
					wantSQL := callerGenerationPublicationSummaryCurrentSQL
					if operation == "joint" {
						wantSQL = callerGenerationPublicationSummariesAuthorityCurrentSQL
					}
					if request.Params[0] != wantSQL {
						return nil, errors.New("summary source changed")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != 0 || prefix.MaximumRows != 0 {
						return nil, errors.New("explicit zero-row transaction preceded parent ACK")
					}
					if mode == "native_error" {
						return nil, &surrealdb.QueryError{Message: "neutral summary refusal"}
					}
					if mode == "lost_reply" {
						return nil, context.DeadlineExceeded
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []callerGenerationCurrentResult{{Current: mode == "current"}}}}, nil
				}
				var current bool
				var err error
				switch operation {
				case "summary":
					current, err = s.CallerGenerationPublicationSummaryCurrent(ctx, summary)
				case "authority":
					current, err = s.CallerGenerationPublicationSummaryAuthorityCurrent(ctx, summary)
				case "joint":
					current, err = s.CallerGenerationPublicationSummariesAuthorityCurrent(ctx, []CallerGenerationPublicationSummary{summary})
				}
				if (err == nil) != (mode == "current" || mode == "stale") || current != (mode == "current") {
					t.Fatalf("summary current=%t error=%v", current, err)
				}
				prefix, _ := controller.Snapshot()
				if native.calls != 1 || prefix.Transactions != 1 || prefix.Rows != 0 || prefix.MaximumRows != 0 ||
					(prefix.Producers[0].Calls == 1) != (mode == "lost_reply") {
					t.Fatalf("summary calls=%d prefix=%+v", native.calls, prefix)
				}
			})
		}
	}
}

func TestCallerPublicationAccounting(t *testing.T) {
	for _, mode := range []string{"publish", "replay", "refused", "native_error", "lost_write"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			summary := callerAccountingSummary(t)
			admission, _, err := prepareCallerGenerationAdmission(CallerGenerationAdmission{
				Generation: summary.Generation, Disposition: CallerGenerationAdmitted,
			}, []CallerLeafPair{}, []CallerLeafOutcome{})
			if err != nil {
				t.Fatal(err)
			}
			admission.RecordedAt = summary.PublishedAt
			publication, _, err := prepareCallerGenerationPublication(CallerGenerationPublication{
				Generation: summary.Generation, Pairs: []CallerGenerationPairPublication{},
				ManifestDigest: summary.ManifestDigest, ManifestPath: summary.ManifestPath,
			}, admission, []CallerLeafOutcome{})
			if err != nil {
				t.Fatal(err)
			}
			job := Job{ID: "caller_leaf_job:owned", Kind: JobCallerLeaf, Target: admission.Generation.Repository,
				Status: StatusRunning, LeaseToken: "lease", ClaimedBy: "worker"}
			writes := 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				vars, err := callerAccountingVars(native, request)
				if err != nil {
					return nil, err
				}
				switch sql := request.Params[0].(string); {
				case sql == "SELECT * FROM $rid":
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []callerGenerationAdmissionRec{{
						CallerGenerationAdmission: admission, Repository: summary.Generation.Repository, GenerationDigest: summary.Generation.Digest,
					}}}}, nil
				case strings.Contains(sql, "SELECT * FROM caller_leaf_outcome"):
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []callerLeafOutcomeRec{}}}, nil
				case sql == publishCallerGenerationSQL:
					writes++
					if !reflect.DeepEqual(vars["publication_rid"], callerGenerationPublicationID(summary.Generation.Repository)) ||
						!reflect.DeepEqual(vars["repo_rid"], repoID(summary.Generation.Repository)) || vars["manifest_digest"] != publication.ManifestDigest {
						return nil, errors.New("publication fixed operands changed")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != 2 || prefix.MaximumRows != 2 {
						return nil, errors.New("publication preceded exact parent ACK")
					}
					if mode == "lost_write" {
						return nil, context.DeadlineExceeded
					}
					if mode == "native_error" {
						return nil, &surrealdb.QueryError{Message: "phebs-permanent: neutral publication refusal"}
					}
					rows := []callerGenerationPublicationRec{}
					if mode != "refused" {
						persisted := publication
						persisted.PairPayloadDigest = summary.PairPayloadDigest
						persisted.PublicationRevision = 1
						persisted.PublicationIncarnation = summary.PublicationIncarnation
						persisted.PublishedAt = summary.PublishedAt
						rid := callerGenerationPublicationID(summary.Generation.Repository)
						rows = append(rows, callerGenerationPublicationRec{CallerGenerationPublication: persisted,
							Repository: summary.Generation.Repository, GenerationDigest: summary.Generation.Digest, RecID: &rid, PairPayloadValid: true})
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
				default:
					return nil, errors.New("unexpected publication source")
				}
			}
			err = s.PublishCallerGeneration(ctx, job, publication)
			if (err == nil) != (mode == "publish" || mode == "replay") {
				t.Fatalf("publication error=%v", err)
			}
			prefix, _ := controller.Snapshot()
			if native.calls != 3 || writes != 1 || prefix.Transactions != 1 || prefix.Rows != 2 ||
				(prefix.Producers[0].Calls == 1) != (mode == "lost_write") {
				t.Fatalf("publication calls=%d prefix=%+v", native.calls, prefix)
			}
		})
	}
}

func TestCallerLeafOutcomeClearAccounting(t *testing.T) {
	for _, mode := range []string{"absent", "present", "native_error", "lost_write"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			generation, err := prepareCallerGeneration(internalCallerGeneration())
			if err != nil {
				t.Fatal(err)
			}
			pair, err := prepareCallerLeafPair(generation, CallerLeafPair{
				Domain: "grpc-caller", ExtractorVersion: "1.0.0", LeafAdapterVersion: "direct-syntax-base-v1",
				LeafPrefix: "00", LeafPrefixBits: 2, CandidateMemberName: "candidate.ndjson",
				CandidateRecordCount: 1, CandidateDeclaredBytes: 1, CandidateContentBytes: 1,
				CandidateContentDigest: internalCallerDigest('8'),
			})
			if err != nil {
				t.Fatal(err)
			}
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				vars, err := callerAccountingVars(native, request)
				if err != nil || !reflect.DeepEqual(vars["outcome_rid"], callerLeafOutcomeID(generation, pair)) ||
					!reflect.DeepEqual(vars["admission_rid"], callerGenerationAdmissionID(generation)) ||
					!reflect.DeepEqual(vars["publication_rid"], callerGenerationPublicationID(generation.Repository)) ||
					!reflect.DeepEqual(vars["repo_rid"], repoID(generation.Repository)) {
					return nil, errors.New("leaf clear fixed operands changed")
				}
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != 1 || prefix.Rows != 4 || prefix.MaximumRows != 4 {
					return nil, errors.New("leaf clear preceded exact parent ACK")
				}
				if mode == "native_error" {
					return nil, &surrealdb.QueryError{Message: "phebs-permanent: neutral writer refusal"}
				}
				if mode == "lost_write" {
					return nil, context.DeadlineExceeded
				}
				return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
			}
			err = s.ClearCallerLeafOutcome(ctx, generation, pair)
			if (err == nil) != (mode == "absent" || mode == "present") {
				t.Fatalf("leaf clear error=%v", err)
			}
			prefix, _ := controller.Snapshot()
			if native.calls != 1 || prefix.Transactions != 1 || prefix.Rows != 4 ||
				(prefix.Producers[0].Calls == 1) != (mode == "lost_write") {
				t.Fatalf("leaf clear calls=%d prefix=%+v", native.calls, prefix)
			}
		})
	}
}

// The two table-wide caller-leaf clears have no actual target vectors yet.
// Ordinary mode keeps their original atomic statements; selected mode must
// refuse before any submission instead of counting a guessed deletion, and the
// refusal latches the owner exactly like the other explicit unsupported recipes.
func TestCallerLeafTableClearsUnsupported(t *testing.T) {
	for _, name := range []string{"generation", "all"} {
		t.Run(name, func(t *testing.T) {
			for _, selected := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
					ctx := t.Context()
					var owner *storeCallOwner
					var controller *storeaccounting.Controller
					if selected {
						ctx, owner, controller = storeAccountingFixture(t, 40, 2)
					}
					db, native := storeAccountingDB(t, ctx, owner)
					s := &Surreal{db: db, accounting: owner}
					generation := internalCallerGeneration()
					native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
						vars, err := callerAccountingVars(native, request)
						if err != nil || !reflect.DeepEqual(vars["repo_rid"], repoID(generation.Repository)) ||
							!reflect.DeepEqual(vars["publication_rid"], callerGenerationPublicationID(generation.Repository)) ||
							vars["clear_all"] != (name == "all") {
							return nil, errors.New("table-wide clear fixed inputs changed")
						}
						return []surrealdb.QueryResult[any]{{Status: "OK"}}, nil
					}
					var err error
					if name == "generation" {
						err = s.ClearCallerLeafGeneration(ctx, generation)
					} else {
						err = s.ClearAllCallerLeafState(ctx, generation.Repository)
					}
					if !selected {
						if err != nil || native.calls != 1 {
							t.Fatalf("ordinary table-wide clear changed: calls=%d error=%v", native.calls, err)
						}
						return
					}
					if !errors.Is(err, storeaccounting.ErrDescriptor) || native.calls != 0 {
						t.Fatalf("selected table-wide clear was not refused before submission: calls=%d error=%v", native.calls, err)
					}
					prefix, _ := controller.Snapshot()
					if prefix.Transactions != 0 || prefix.Rows != 0 || prefix.Complete {
						t.Fatalf("refusal invented a write prefix: %+v", prefix)
					}
					if _, err := s.GetAPIKey(ctx, "later"); err == nil || native.calls != 0 {
						t.Fatalf("unsupported table-wide clear did not latch owner failure: calls=%d error=%v", native.calls, err)
					}
				})
			}
		})
	}
}

func TestCallerLeafFanoutAccounting(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, mode := range []string{"written", "replay", "refused", "changed", "lost_write", "null_census"} {
			t.Run(map[bool]string{false: "create/", true: "coalesce/"}[pending]+mode, func(t *testing.T) {
				ctx, owner, controller := storeAccountingFixture(t, 40, 2)
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				generation, err := prepareCallerGeneration(internalCallerGeneration())
				if err != nil {
					t.Fatal(err)
				}
				pair, err := prepareCallerLeafPair(generation, CallerLeafPair{
					Domain: "grpc-caller", ExtractorVersion: "1.0.0", LeafAdapterVersion: "direct-syntax-base-v1",
					LeafPrefix: "00", LeafPrefixBits: 2, CandidateMemberName: "candidate.ndjson",
					CandidateRecordCount: 1, CandidateDeclaredBytes: 1, CandidateContentBytes: 1,
					CandidateContentDigest: internalCallerDigest('8'),
				})
				if err != nil {
					t.Fatal(err)
				}
				digest := internalCallerDigest('9')
				outcome, err := prepareCallerLeafOutcome(CallerLeafOutcome{Generation: generation, Pair: pair,
					Disposition: CallerLeafSucceeded, Receipt: &CallerLeafArtifactReceipt{
						ArtifactName: callerleafid.ArtifactName(pair.PairDigest, digest), ArtifactCount: 1,
						ContentDigest: digest, MetadataDigest: internalCallerDigest('a'),
					}})
				if err != nil {
					t.Fatal(err)
				}
				job := Job{ID: "caller_leaf_job:owned", Kind: JobCallerLeaf, Target: generation.Repository,
					Status: StatusRunning, LeaseToken: "lease", ClaimedBy: "worker"}
				pendingID := models.NewRecordID(string(JobCallerLeaf), "pending")
				writes := 0
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					vars, err := callerAccountingVars(native, request)
					if err != nil {
						return nil, err
					}
					if request.Params[0] == queuePendingSelection {
						if vars["table"] != string(JobCallerLeaf) || vars["target"] != generation.Repository {
							return nil, errors.New("pending census scope changed")
						}
						rows := []jobRec{}
						if pending {
							rows = append(rows, jobRec{RecID: &pendingID})
						}
						if mode == "null_census" {
							rows = nil
						}
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
					}
					writes++
					sql := request.Params[0].(string)
					wantSQL := recordCallerLeafOutcomeCreateSQL
					if pending {
						wantSQL = recordCallerLeafOutcomeUpdateSQL
					}
					if sql != wantSQL || strings.Contains(sql, "UPDATE $pending SET") != pending ||
						strings.Contains(sql, "CREATE caller_leaf_job CONTENT") == pending ||
						!strings.Contains(sql, "IF array::len($written) = 1 AND [$actual_pending] != [$pending_ids[0]]") ||
						!reflect.DeepEqual(vars["outcome_rid"], callerLeafOutcomeID(generation, pair)) || vars["repository"] != generation.Repository {
						return nil, errors.New("leaf fanout emitted an unselected write operand")
					}
					rawIDs, err := native.codec.Marshal(vars["pending_ids"])
					var ids []models.RecordID
					if err != nil || native.codec.Unmarshal(rawIDs, &ids) != nil || ids == nil ||
						len(ids) > 1 || (len(ids) == 1) != pending || pending && !reflect.DeepEqual(ids[0], pendingID) {
						return nil, errors.New("leaf fanout pending vector changed")
					}
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != 3 || prefix.MaximumRows != 3 {
						return nil, errors.New("leaf fanout preceded exact parent ACK")
					}
					if mode == "lost_write" {
						return nil, context.DeadlineExceeded
					}
					if mode == "changed" {
						return nil, &surrealdb.QueryError{Message: "phebs-conflict: caller leaf pending census changed"}
					}
					rows := []callerLeafOutcomeRec{}
					if mode != "refused" {
						persisted := outcome
						persisted.RecordedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
						rows = append(rows, callerLeafOutcomeRec{CallerLeafOutcome: persisted,
							Repository: generation.Repository, GenerationDigest: generation.Digest, Domain: pair.Domain, LeafPrefix: pair.LeafPrefix})
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
				}
				err = s.RecordCallerLeafOutcome(ctx, job, outcome)
				if (err == nil) != (mode == "written" || mode == "replay") {
					t.Fatalf("leaf fanout error=%v", err)
				}
				wantWrites := uint64(1)
				if mode == "null_census" {
					wantWrites = 0
				}
				prefix, _ := controller.Snapshot()
				if uint64(writes) != wantWrites || native.calls != writes+1 || prefix.Transactions != wantWrites || prefix.Rows != 3*wantWrites ||
					(prefix.Producers[0].Calls == 1) != (mode == "lost_write") {
					t.Fatalf("leaf fanout calls=%d prefix=%+v", native.calls, prefix)
				}
			})
		}
	}
}
