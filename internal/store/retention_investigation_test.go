package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func investigationRetentionTestComponents() []RetentionComponent {
	return []RetentionComponent{
		RetentionInvestigation,
		RetentionInvestigationRevision,
		RetentionInvestigationChangeBrief,
		RetentionWorkbenchMutation,
		RetentionWorkbenchDisposition,
		RetentionInvestigationRun,
		RetentionInvestigationRunEvent,
		RetentionInvestigationRunArtifact,
		RetentionInvestigationArtifactOwner,
		RetentionInvestigationArtifactOwnerRelease,
		RetentionInvestigationArtifactRetentionOverride,
		RetentionInvestigationDecision,
		RetentionInvestigationDisposition,
		RetentionInvestigationBaselineDesignation,
		RetentionInvestigationGrant,
		RetentionInvestigationCursor,
		RetentionInvestigationCreation,
		RetentionInvestigationConsumerSnapshot,
		RetentionInvestigationConsumerEdgeLedger,
		RetentionInvestigationReviewProjection,
		RetentionInvestigationReviewItem,
		RetentionInvestigationDossier,
		RetentionInvestigationWatch,
		RetentionInvestigationWatchRevision,
	}
}

func investigationRetentionTestRequests() []RetentionComponentRequest {
	components := investigationRetentionTestComponents()
	requests := make([]RetentionComponentRequest, len(components))
	for index, component := range components {
		reported := 77
		requests[index] = RetentionComponentRequest{
			Component:          component,
			ReportedIdentities: reported,
			ScanIdentities:     reported + 1,
		}
	}
	return requests
}

func seedInvestigationRetentionRow(
	t *testing.T,
	store *Surreal,
	component RetentionComponent,
	index int,
	content map[string]any,
) {
	t.Helper()
	plan, ok := investigationRetentionPlans[component]
	if !ok {
		t.Fatalf("seed unsupported Investigation retention component %q", component)
	}
	requireRetentionStatement(t, t.Context(), store, `
CREATE type::record($table, $id) CONTENT $content RETURN NONE`, map[string]any{
		"table":   plan.table,
		"id":      fmt.Sprintf("retention-%03d", index),
		"content": content,
	})
}

func TestInvestigationRetentionEmptyReadyStoreIsExactZero(t *testing.T) {
	store := newRetentionTestStore(t)
	requests := investigationRetentionTestRequests()
	results, err := store.CollectInvestigationRetention(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 24 || len(investigationRetentionPlans) != 24 {
		t.Fatalf(
			"results/plans = %d/%d, want exact 24/24",
			len(results),
			len(investigationRetentionPlans),
		)
	}

	reportedTotal, scanTotal := 0, 0
	for index, result := range results {
		request := requests[index]
		reportedTotal += request.ReportedIdentities
		scanTotal += request.ScanIdentities
		plan, exists := investigationRetentionPlans[request.Component]
		if !exists || plan.table != string(request.Component) ||
			plan.readiness != retentionNoReadiness {
			t.Fatalf(
				"component %d %q plan = %+v/%t",
				index,
				request.Component,
				plan,
				exists,
			)
		}
		if result.Component != request.Component || result.Err != nil ||
			result.Summary != (RetentionComponentSummary{}) {
			t.Fatalf("empty component %d = %+v", index, result)
		}
	}
	if reportedTotal != 1_848 || scanTotal != 1_872 {
		t.Fatalf("Investigation allocation = %d/%d, want 1848/1872", reportedTotal, scanTotal)
	}
	if _, included := investigationRetentionPlans[RetentionComponent(JobInvestigate)]; included {
		t.Fatal("investigation_run_job is present in the Investigation retention plans")
	}
}

func TestInvestigationRetentionCatalogProjectionIsFixed(t *testing.T) {
	store := newRetentionTestStore(t)
	tables, err := store.retentionCatalogTables(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != len(investigationRetentionPlans) || len(tables) != 24 {
		t.Fatalf(
			"projected catalog tables = %d, want exact 24 from a larger database catalog",
			len(tables),
		)
	}
	for component, plan := range investigationRetentionPlans {
		if _, present := tables[plan.table]; !present {
			t.Fatalf("projected catalog omitted %q for component %q", plan.table, component)
		}
	}
	if _, present := tables[string(JobInvestigate)]; present {
		t.Fatal("projected Investigation catalog includes investigation_run_job")
	}
}

func TestInvestigationRetentionCountsAllTwentyFourTablesAndStates(t *testing.T) {
	store := newRetentionTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	want := make(map[RetentionComponent]int64, 24)
	seed := func(component RetentionComponent, content map[string]any) {
		t.Helper()
		index := int(want[component])
		seedInvestigationRetentionRow(t, store, component, index, content)
		want[component]++
	}

	for index, lifecycle := range []string{"draft", "active", "concluded", "archived"} {
		seed(RetentionInvestigation, map[string]any{
			"investigation_id": fmt.Sprintf("investigation-%d", index),
			"lifecycle":        lifecycle,
		})
	}
	for index := range 2 {
		seed(RetentionInvestigationRevision, map[string]any{
			"investigation_id": "investigation-1",
			"seq":              index + 1,
		})
	}
	seed(RetentionInvestigationChangeBrief, map[string]any{
		"brief_id":         "brief-1",
		"revision_id":      "revision-1",
		"investigation_id": "investigation-1",
	})
	for index, operation := range []string{"create", "revise"} {
		seed(RetentionWorkbenchMutation, map[string]any{
			"mutation_key":     fmt.Sprintf("mutation-%d", index),
			"principal":        "user:owner",
			"idempotency_key":  fmt.Sprintf("mutation-request-%d", index),
			"operation":        operation,
			"investigation_id": "investigation-1",
			"revision_id":      fmt.Sprintf("revision-%d", index+1),
		})
	}
	for index, category := range []string{"accepted", "rejected", "completed", "reopened", "waived"} {
		seed(RetentionWorkbenchDisposition, map[string]any{
			"category":         category,
			"disposition_id":   fmt.Sprintf("workbench-disposition-%d", index),
			"principal":        "user:owner",
			"idempotency_key":  fmt.Sprintf("disposition-request-%d", index),
			"investigation_id": "investigation-1",
			"revision_id":      "revision-2",
			"created_at":       now.Add(time.Duration(index) * time.Second),
			"suggestion_id":    fmt.Sprintf("suggestion-%d", index),
			"sequence":         index + 1,
			"supersedes":       fmt.Sprintf("prior-disposition-%d", index),
		})
	}
	for index := range 2 {
		content := map[string]any{
			"revision_id":     "revision-2",
			"seq":             index + 1,
			"idempotency_key": fmt.Sprintf("run-request-%d", index),
		}
		if index == 1 {
			content["lease_owner"] = "worker:one"
			content["lease_until"] = now.Add(time.Minute)
		}
		seed(RetentionInvestigationRun, content)
	}
	for index, state := range []string{
		"queued", "enumerating", "analyzing", "publishing",
		"published", "failed", "canceled",
	} {
		seed(RetentionInvestigationRunEvent, map[string]any{
			"new_state": state,
			"event_id":  fmt.Sprintf("run-event-%d", index),
			"run_id":    "run-1",
			"sequence":  index + 1,
		})
	}
	for index, terminal := range []string{"published", "failed", "canceled"} {
		seed(RetentionInvestigationRunArtifact, map[string]any{
			"terminal_status": terminal,
			"artifact_id":     fmt.Sprintf("artifact-%d", index),
			"run_id":          fmt.Sprintf("run-%d", index),
			"created_at":      now.Add(time.Duration(index) * time.Second),
		})
	}
	for index, kind := range []string{"investigation", "baseline", "dossier"} {
		seed(RetentionInvestigationArtifactOwner, map[string]any{
			"owner_kind":  kind,
			"owner_key":   fmt.Sprintf("owner-%d", index),
			"artifact_id": fmt.Sprintf("artifact-%d", index),
		})
	}
	seed(RetentionInvestigationArtifactOwnerRelease, map[string]any{
		"owner_key": "owner-0",
	})
	for index, policy := range []string{
		"revocation", "mandatory_deletion", "legal_policy", "approved_retention",
	} {
		seed(RetentionInvestigationArtifactRetentionOverride, map[string]any{
			"policy":      policy,
			"override_id": fmt.Sprintf("override-%d", index),
			"artifact_id": fmt.Sprintf("artifact-%d", index),
		})
	}
	seed(RetentionInvestigationDecision, map[string]any{
		"decision_id":      "decision-1",
		"investigation_id": "investigation-1",
		"claim_scope":      "claim-1",
	})
	seed(RetentionInvestigationDecision, map[string]any{
		"decision_id":      "decision-2",
		"investigation_id": "investigation-1",
		"claim_scope":      "claim-2",
		"supersedes":       "decision-1",
	})
	seed(RetentionInvestigationDisposition, map[string]any{
		"disposition_id": "disposition-1",
		"subject_kind":   "consumer",
		"subject_id":     "consumer-1",
	})
	seed(RetentionInvestigationDisposition, map[string]any{
		"disposition_id": "disposition-2",
		"subject_kind":   "consumer",
		"subject_id":     "consumer-2",
		"supersedes":     "disposition-1",
	})
	seed(RetentionInvestigationBaselineDesignation, map[string]any{
		"designation_id":   "baseline-1",
		"investigation_id": "investigation-1",
		"claim_scope":      "claim-1",
		"workflow_scope":   "workflow-1",
	})
	seed(RetentionInvestigationBaselineDesignation, map[string]any{
		"designation_id":   "baseline-2",
		"investigation_id": "investigation-1",
		"claim_scope":      "claim-1",
		"workflow_scope":   "workflow-2",
		"supersedes":       "baseline-1",
	})
	seed(RetentionInvestigationGrant, map[string]any{
		"role":             "reader",
		"grant_key":        "grant-1",
		"investigation_id": "investigation-1",
		"principal":        "user:reader",
	})
	for index, principal := range []string{"user:owner", "user:reader"} {
		seed(RetentionInvestigationCursor, map[string]any{
			"cursor_key":               fmt.Sprintf("cursor-%d", index),
			"investigation_id":         "investigation-1",
			"principal":                principal,
			"cursor":                   fmt.Sprintf("cursor-payload-%d", index),
			"authz_revision":           index,
			"authorization_generation": fmt.Sprintf("generation-%d", index),
		})
	}
	seed(RetentionInvestigationCreation, map[string]any{
		"creation_key":    "creation-1",
		"principal":       "user:owner",
		"idempotency_key": "creation-request-1",
	})
	for index := range 2 {
		seed(RetentionInvestigationConsumerSnapshot, map[string]any{
			"snapshot_id":      fmt.Sprintf("snapshot-%d", index),
			"principal":        "user:owner",
			"artifact_id":      fmt.Sprintf("artifact-%d", index),
			"investigation_id": "investigation-1",
			"sequence":         index + 1,
		})
	}
	for index, active := range []bool{true, false} {
		seed(RetentionInvestigationConsumerEdgeLedger, map[string]any{
			"ledger_id":               fmt.Sprintf("ledger-%d", index),
			"investigation_id":        "investigation-1",
			"principal":               "user:owner",
			"continuity_namespace":    "continuity-1",
			"logical_relationship_id": fmt.Sprintf("edge-%d", index),
			"active":                  active,
		})
	}
	for index := range 2 {
		seed(RetentionInvestigationReviewProjection, map[string]any{
			"projection_record_id": fmt.Sprintf("projection-record-%d", index),
			"principal":            "user:owner",
			"source_snapshot_id":   fmt.Sprintf("snapshot-%d", index),
			"projection_id":        "review-rule",
			"projection_version":   "1.0",
			"investigation_id":     "investigation-1",
			"evaluated_at":         now.Add(time.Duration(index) * time.Minute),
		})
	}
	reviewStates := []map[string]any{
		{"expires_at": now.Add(time.Hour)},
		{"expires_at": now.Add(-time.Hour)},
		{
			"expires_at":    now.Add(time.Hour),
			"superseded_by": "projection-record-1",
			"superseded_at": now,
		},
	}
	for index, state := range reviewStates {
		content := map[string]any{
			"review_item_id":     fmt.Sprintf("review-item-%d", index),
			"investigation_id":   "investigation-1",
			"principal":          "user:owner",
			"projection_id":      "review-rule",
			"projection_version": "1.0",
			"evaluated_at":       now.Add(time.Duration(index) * time.Second),
			"projection_key":     fmt.Sprintf("review-key-%d", index),
		}
		for key, value := range state {
			content[key] = value
		}
		seed(RetentionInvestigationReviewItem, content)
	}
	seed(RetentionInvestigationDossier, map[string]any{
		"dossier_id":       "dossier-1",
		"investigation_id": "investigation-1",
		"principal":        "user:owner",
		"created_at":       now,
		"root_digest":      "dossier-root-1",
	})
	seed(RetentionInvestigationDossier, map[string]any{
		"dossier_id":       "dossier-2",
		"investigation_id": "investigation-1",
		"principal":        "user:owner",
		"created_at":       now.Add(time.Second),
		"root_digest":      "dossier-root-2",
		"predecessor_id":   "dossier-1",
	})
	watchStates := []map[string]any{
		{"enabled": true, "expires_at": now.Add(time.Hour)},
		{"enabled": false, "expires_at": now.Add(time.Hour)},
		{"enabled": true, "expires_at": now.Add(-time.Hour)},
	}
	for index, state := range watchStates {
		seed(RetentionInvestigationWatch, map[string]any{
			"watch_id":   fmt.Sprintf("watch-%d", index),
			"owner":      "user:owner",
			"enabled":    state["enabled"],
			"expires_at": state["expires_at"],
		})
	}
	for index := range 2 {
		seed(RetentionInvestigationWatchRevision, map[string]any{
			"watch_id": "watch-0",
			"seq":      index + 1,
		})
	}

	requests := investigationRetentionTestRequests()
	results, err := store.CollectInvestigationRetention(t.Context(), requests)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range results {
		component := requests[index].Component
		wantCount := want[component]
		if wantCount == 0 {
			t.Fatalf("component %q was not seeded", component)
		}
		if result.Component != component || result.Err != nil ||
			result.Summary.ScannedIdentities != int(wantCount) ||
			result.Summary.ReportedIdentities != wantCount ||
			result.Summary.Truncated || result.Summary.Bytes != nil {
			t.Fatalf(
				"component %d %q = %+v, want exact %d",
				index,
				component,
				result,
				wantCount,
			)
		}
	}
}

func seedInvestigationRetentionCapRows(
	t *testing.T,
	store *Surreal,
	component RetentionComponent,
	start, count int,
) {
	t.Helper()
	ids := make([]string, count)
	for index := range count {
		ids[index] = fmt.Sprintf("retention-cap-%04d", start+index)
	}
	plan := investigationRetentionPlans[component]
	var statement string
	switch component {
	case RetentionInvestigation:
		statement = `
FOR $id IN $ids {
	CREATE type::record($table, $id) CONTENT {
		investigation_id: $id,
		lifecycle: 'active'
	} RETURN NONE
}`
	case RetentionInvestigationWatch:
		statement = `
FOR $id IN $ids {
	CREATE type::record($table, $id) CONTENT {
		watch_id: $id,
		owner: 'user:retention'
	} RETURN NONE
}`
	default:
		t.Fatalf("unsupported cap fixture component %q", component)
	}
	requireRetentionStatement(t, t.Context(), store, statement, map[string]any{
		"table": plan.table,
		"ids":   ids,
	})
}

func TestInvestigationRetentionExactAtCapAndCapPlusOne(t *testing.T) {
	for _, test := range []struct {
		name      string
		component RetentionComponent
		reported  int
		scan      int
	}{
		{
			name: "79-slot component", component: RetentionInvestigation,
			reported: 79, scan: 80,
		},
		{
			name: "78-slot tail component", component: RetentionInvestigationWatch,
			reported: 78, scan: 79,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newRetentionTestStore(t)
			request := RetentionComponentRequest{
				Component:          test.component,
				ReportedIdentities: test.reported,
				ScanIdentities:     test.scan,
			}
			collect := func() RetentionComponentSummary {
				t.Helper()
				results, err := store.CollectInvestigationRetention(
					t.Context(),
					[]RetentionComponentRequest{request},
				)
				if err != nil || len(results) != 1 || results[0].Err != nil {
					t.Fatalf("collect = %+v, %v", results, err)
				}
				return results[0].Summary
			}

			seedInvestigationRetentionCapRows(
				t,
				store,
				test.component,
				0,
				test.reported,
			)
			atCap := collect()
			if atCap.ScannedIdentities != test.reported ||
				atCap.ReportedIdentities != int64(test.reported) ||
				atCap.Truncated || atCap.Bytes != nil {
				t.Fatalf("at-cap summary = %+v", atCap)
			}

			seedInvestigationRetentionCapRows(
				t,
				store,
				test.component,
				test.reported,
				1,
			)
			capPlusOne := collect()
			if capPlusOne.ScannedIdentities != test.scan ||
				capPlusOne.ReportedIdentities != int64(test.reported) ||
				!capPlusOne.Truncated || capPlusOne.Bytes != nil {
				t.Fatalf("cap-plus-one summary = %+v", capPlusOne)
			}
		})
	}
}

func TestInvestigationRetentionAllocationUsesSafetyBounds(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	for _, component := range []RetentionComponent{
		RetentionInvestigation,
		RetentionInvestigationWatchRevision,
	} {
		results, err := store.CollectInvestigationRetention(
			ctx,
			[]RetentionComponentRequest{{
				Component: component, ReportedIdentities: 1, ScanIdentities: 2,
			}},
		)
		if err != nil || len(results) != 1 || results[0].Err != nil ||
			results[0].Summary != (RetentionComponentSummary{}) {
			t.Fatalf("safe reduced allocation for %q = %+v, %v", component, results, err)
		}
	}

	for _, test := range []struct {
		name     string
		reported int
		scan     int
	}{
		{name: "zero", reported: 0, scan: 1},
		{name: "above component cap", reported: 80, scan: 81},
		{name: "missing sentinel", reported: 1, scan: 1},
		{name: "extra scan", reported: 1, scan: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.CollectInvestigationRetention(
				ctx,
				[]RetentionComponentRequest{{
					Component:          RetentionInvestigation,
					ReportedIdentities: test.reported,
					ScanIdentities:     test.scan,
				}},
			)
			if err == nil {
				t.Fatalf("allocation %d/%d was accepted", test.reported, test.scan)
			}
		})
	}

	requests := investigationRetentionTestRequests()
	if _, err := store.CollectInvestigationRetention(ctx, requests); err != nil {
		t.Fatalf("full 1848/1872 allocation: %v", err)
	}
	overAggregate := make([]RetentionComponentRequest, len(requests))
	for index, component := range investigationRetentionTestComponents() {
		overAggregate[index] = RetentionComponentRequest{
			Component: component, ReportedIdentities: 79, ScanIdentities: 80,
		}
	}
	if _, err := store.CollectInvestigationRetention(ctx, overAggregate); err == nil {
		t.Fatal("aggregate 1896/1920 allocation was accepted above the 1848/1872 ceiling")
	}
	if _, err := store.CollectInvestigationRetention(ctx, []RetentionComponentRequest{
		{Component: RetentionInvestigation, ReportedIdentities: 1, ScanIdentities: 2},
		{Component: RetentionInvestigation, ReportedIdentities: 1, ScanIdentities: 2},
	}); err == nil {
		t.Fatal("duplicate Investigation retention component was accepted")
	}
	if _, err := store.CollectInvestigationRetention(ctx, []RetentionComponentRequest{{
		Component: "unknown-investigation-table", ReportedIdentities: 1, ScanIdentities: 2,
	}}); err == nil {
		t.Fatal("unknown Investigation retention component was accepted")
	}
	tooMany := append(
		append([]RetentionComponentRequest(nil), requests...),
		requests[0],
	)
	if _, err := store.CollectInvestigationRetention(ctx, tooMany); err == nil {
		t.Fatal("25 Investigation retention requests were accepted")
	}
}

func TestInvestigationRetentionMissingTableIsLocalized(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	seedInvestigationRetentionRow(t, store, RetentionInvestigation, 0, map[string]any{
		"investigation_id": "retention-present",
		"lifecycle":        "active",
	})
	requireRetentionStatement(
		t,
		ctx,
		store,
		"REMOVE TABLE investigation_review_item",
		nil,
	)

	requests := investigationRetentionTestRequests()
	results, err := store.CollectInvestigationRetention(ctx, requests)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for index, result := range results {
		component := requests[index].Component
		switch component {
		case RetentionInvestigationReviewItem:
			failures++
			if !errors.Is(result.Err, ErrRetentionComponentUnavailable) ||
				result.Summary != (RetentionComponentSummary{}) {
				t.Fatalf("missing table result = %+v", result)
			}
		case RetentionInvestigation:
			if result.Err != nil || result.Summary.ScannedIdentities != 1 ||
				result.Summary.ReportedIdentities != 1 || result.Summary.Truncated {
				t.Fatalf("seeded peer result = %+v", result)
			}
		default:
			if result.Err != nil || result.Summary != (RetentionComponentSummary{}) {
				t.Fatalf("empty peer %q = %+v", component, result)
			}
		}
	}
	if failures != 1 {
		t.Fatalf("localized failures = %d, want 1", failures)
	}
}

func TestInvestigationRetentionPlanPushesPhysicalLimitWithoutSort(t *testing.T) {
	store := newRetentionTestStore(t)
	requireRetentionExplain(
		t,
		t.Context(),
		store,
		"SELECT id FROM type::table($table) ORDER BY id LIMIT $scan_limit EXPLAIN FULL",
		map[string]any{
			"table":      "investigation_review_item",
			"scan_limit": 3,
		},
		"TableScan",
		"investigation_review_item",
	)
}

func TestInvestigationRetentionExcludesRunJob(t *testing.T) {
	store := newRetentionTestStore(t)
	ctx := t.Context()
	seedRetentionRows(t, ctx, store, string(JobInvestigate), "retention-job", 1, map[string]any{
		"target":     "investigation:retention",
		"status":     string(StatusDone),
		"attempts":   1,
		"created_at": time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	})

	results, err := store.CollectInvestigationRetention(
		ctx,
		investigationRetentionTestRequests(),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Err != nil || result.Summary != (RetentionComponentSummary{}) {
			t.Fatalf("Investigation result includes run job: %+v", result)
		}
	}
	if _, err := store.CollectInvestigationRetention(ctx, []RetentionComponentRequest{{
		Component: RetentionComponent(JobInvestigate), ReportedIdentities: 1, ScanIdentities: 2,
	}}); err == nil {
		t.Fatal("Investigation collector accepted investigation_run_job")
	}

	coreResults, err := store.CollectCoreRetention(ctx, []RetentionComponentRequest{{
		Component: RetentionComponent(JobInvestigate), ReportedIdentities: 1, ScanIdentities: 2,
	}})
	if err != nil || len(coreResults) != 1 || coreResults[0].Err != nil ||
		coreResults[0].Summary.ScannedIdentities != 1 ||
		coreResults[0].Summary.ReportedIdentities != 1 ||
		coreResults[0].Summary.Truncated {
		t.Fatalf("core Investigation job result = %+v, %v", coreResults, err)
	}
}

func TestInvestigationRetentionSharedEnvelopeRejectsStatementError(t *testing.T) {
	results := []surrealdb.QueryResult[[]retentionRow]{{
		Error: &surrealdb.QueryError{Message: "retention query failed"},
	}}
	if err := retentionQueryResultsError(&results); err == nil {
		t.Fatal("errored single result envelope was accepted")
	}
}
