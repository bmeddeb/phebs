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
)

func TestEvidenceSweepFixedAccounting(t *testing.T) {
	for _, name := range []string{"mark", "finalize", "known_error", "lost", "dynamic"} {
		t.Run(name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			rid := extractionRunID("neutral-run")
			candidate := evidenceSweepCandidateRec{RecID: &rid, RunID: "neutral-run", Repo: "example.invalid/neutral", Status: "aborted"}
			if name == "finalize" || name == "dynamic" {
				candidate.Status, candidate.Phase = "deleting", "finalize"
				if name == "dynamic" {
					candidate.Phase = "associations"
				}
			}
			calls := 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				switch calls {
				case 1:
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []evidenceMigrationStateRec{{Version: evidenceMigrationVersion}}}}, nil
				case 2:
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []evidenceSweepCandidateRec{candidate}}}, nil
				case 3:
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != 1 || prefix.Rows != 2 {
						return nil, errors.New("sweep mutation preceded fixed operand admission")
					}
					sql := request.Params[0].(string)
					if !strings.Contains(sql, "UPDATE $rid") || name == "finalize" && !strings.Contains(sql, "DELETE $rid") ||
						name != "finalize" && !strings.Contains(sql, "UPDATE $attempt_rid") {
						return nil, errors.New("sweep fixed operands changed")
					}
					if name == "lost" {
						return nil, context.DeadlineExceeded
					}
					if name == "known_error" {
						return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "known refusal"}}, nil
					}
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []EvidenceSweepProgress{{RunsMarkedDeleting: 1}}}}, nil
				default:
					return nil, errors.New("unexpected sweep query")
				}
			}
			_, err := (&Surreal{db: db, accounting: owner}).SweepEvidence(ctx, time.Now(), time.Hour)
			wantCalls, wantTX, wantRows := 3, uint64(1), uint64(2)
			if name == "dynamic" {
				wantCalls, wantTX, wantRows = 2, 0, 0
			}
			prefix, _ := controller.Snapshot()
			if (err == nil) != (name == "mark" || name == "finalize") || calls != wantCalls || prefix.Transactions != wantTX || prefix.Rows != wantRows {
				t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
		})
	}
}

func TestLifecycleAndRetentionAccounting(t *testing.T) {
	for _, name := range []string{"cursor_read", "cursor_write", "cursor_conflict", "cursor_error", "cursor_lost", "readiness", "index", "catalog", "rows", "pin_ranges", "legacy_catalog_scan", "legacy_catalog_delete",
		"derived_catalog", "derived_readiness", "derived_candidate", "derived_focused", "derived_resolver", "derived_caller", "derived_fence", "evidence_candidates"} {
		t.Run(name, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			state := &Surreal{db: db, accounting: owner}
			write := strings.HasPrefix(name, "cursor_") && name != "cursor_read"
			want := uint64(0)
			if write {
				want = 1 // The supplied cursor RID still counts on a false CAS guard.
			}
			calls := 0
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				calls++
				prefix, err := controller.Snapshot()
				if err != nil || prefix.Transactions != want || prefix.Rows != want {
					return nil, errors.New("lifecycle query preceded its exact accounting prefix")
				}
				if write {
					if !strings.Contains(request.Params[0].(string), "UPSERT $rid") {
						return nil, errors.New("cursor fixed operand is missing")
					}
					switch name {
					case "cursor_lost":
						return nil, context.DeadlineExceeded
					case "cursor_error":
						return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "known refusal"}}, nil
					default:
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: []bool{name != "cursor_conflict"}}}, nil
					}
				}
				if name == "derived_fence" {
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: []callerGenerationCurrentResult{{Current: true}}}}, nil
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{}}}, nil
			}
			var err error
			collection := emptyDerivedRetentionCollection(1)
			request := RetentionComponentRequest{ReportedIdentities: 1, ScanIdentities: 2}
			switch name {
			case "cursor_read":
				_, _, err = state.GetLifecycleCursor(ctx, "neutral")
			case "cursor_write", "cursor_conflict", "cursor_error", "cursor_lost":
				err = state.CompareAndSwapLifecycleCursor(ctx, "neutral", 0, "next")
			case "readiness":
				_, err = state.retentionReadiness(ctx, retentionEvidenceReady)
			case "index":
				err = state.requireRetentionIndex(ctx, "evidence_pin_kind", "evidence_pin")
			case "catalog":
				_, err = state.retentionCatalogTables(ctx)
			case "rows":
				_, err = state.retentionRows(ctx, coreRetentionPlans[RetentionExtractionRun], 2)
			case "pin_ranges":
				_, err = state.retentionPinRows(ctx, retentionOtherPins, 2)
			case "legacy_catalog_scan":
				_, err = state.SweepCatalogLifecycle(ctx, "", 1, 1, 1)
			case "legacy_catalog_delete":
				_, err = state.collectCatalogGeneration(ctx, catalogLifecycleCandidate{}, 1)
			case "derived_catalog":
				_, err = state.derivedRetentionCatalogTables(ctx)
			case "derived_readiness":
				_, err = state.derivedRetentionReadiness(ctx, derivedRetentionCandidate)
			case "derived_candidate":
				err = state.collectDerivedCandidate(ctx, request, &collection)
			case "derived_focused":
				err = state.collectDerivedFocused(ctx, request, &collection)
			case "derived_resolver":
				err = state.collectDerivedResolver(ctx, request, &collection)
			case "derived_caller":
				err = state.collectDerivedCaller(ctx, request, &collection)
			case "derived_fence":
				_, err = state.derivedRetentionCallerAuthoritiesCurrent(ctx, []CallerGenerationPublicationSummary{{}})
			case "evidence_candidates":
				_, err = state.nextEvidenceSweepCandidate(ctx, time.Now())
			}
			wantCalls := 1
			if name == "pin_ranges" {
				wantCalls = 3
			}
			if name == "legacy_catalog_delete" {
				wantCalls = 0
			}
			if name == "evidence_candidates" {
				wantCalls = 2
			}
			failed := name == "cursor_conflict" || name == "cursor_error" || name == "cursor_lost" || name == "legacy_catalog_delete"
			prefix, _ := controller.Snapshot()
			if (err != nil) != failed || calls != wantCalls || prefix.Transactions != want || prefix.Rows != want {
				t.Fatalf("calls=%d prefix=%+v error=%v", calls, prefix, err)
			}
			if name == "cursor_conflict" && !errors.Is(err, ErrLifecycleCursorConflict) {
				t.Fatalf("CAS conflict classification changed: %v", err)
			}
		})
	}
}
