package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

const replacementLineage = "provisional_repo_path_v1_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestCallerComparisonExactUnionPagesAndUnitAmbiguity(t *testing.T) {
	st := callerComparisonStore(t)
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
	}
	handler := api.New(callerMapOptions(st, "user:member", &visible))

	var first api.CallerComparisonPage
	code, body := catalogHTTP(
		t, handler, callerComparisonTarget("level=occurrence&page_size=2"), &first,
	)
	if code != http.StatusOK || first.SchemaVersion != "caller-comparison-v1" ||
		first.Query.Old.Lineage != callerMapLineage ||
		first.Query.Replacement.Lineage != replacementLineage ||
		first.TotalRows != 5 || len(first.Rows) != 2 ||
		first.Pagination.Complete || first.Pagination.NextCursor == "" ||
		first.Old.CoverageDigest == "" ||
		first.Old.CoverageDigest != first.Replacement.CoverageDigest ||
		first.Old.AttributionDigest == "" ||
		first.Replacement.AttributionDigest == "" {
		t.Fatalf("first comparison page = %d %s / %+v", code, body, first)
	}
	classifications := make(map[string]int)
	for _, row := range first.Rows {
		classifications[row.Classification]++
	}
	var second api.CallerComparisonPage
	code, body = catalogHTTP(
		t, handler,
		callerComparisonTarget("level=occurrence&page_size=2")+
			"&cursor="+url.QueryEscape(first.Pagination.NextCursor),
		&second,
	)
	if code != http.StatusOK || second.Pagination.NextCursor == "" {
		t.Fatalf("second comparison page = %d %s / %+v", code, body, second)
	}
	for _, row := range second.Rows {
		classifications[row.Classification]++
	}
	var third api.CallerComparisonPage
	code, body = catalogHTTP(
		t, handler,
		callerComparisonTarget("level=occurrence&page_size=2")+
			"&cursor="+url.QueryEscape(second.Pagination.NextCursor),
		&third,
	)
	if code != http.StatusOK || !third.Pagination.Complete || len(third.Rows) != 1 {
		t.Fatalf("third comparison page = %d %s / %+v", code, body, third)
	}
	for _, row := range third.Rows {
		classifications[row.Classification]++
	}
	wantOccurrence := map[string]int{
		"old_only_evidence": 1,
		"both_evidence":     2,
		"new_only_evidence": 1,
		"unresolved":        1,
	}
	if !equalStringIntMap(classifications, wantOccurrence) {
		t.Fatalf("occurrence classifications = %v, want %v",
			classifications, wantOccurrence)
	}

	var units api.CallerComparisonPage
	code, body = catalogHTTP(
		t, handler, callerComparisonTarget("level=unit&page_size=100"), &units,
	)
	if code != http.StatusOK || units.TotalRows != 5 || len(units.Rows) != 5 {
		t.Fatalf("unit comparison = %d %s / %+v", code, body, units)
	}
	unitClasses := make(map[string]int)
	var ambiguous *api.CallerComparisonRow
	for index := range units.Rows {
		row := &units.Rows[index]
		unitClasses[row.Classification]++
		if strings.Contains(row.Key, "src/b.go:30-40") {
			ambiguous = row
		}
		for _, side := range []api.CallerComparisonSide{row.Old, row.Replacement} {
			if len(side.Rows) > 4 {
				t.Fatalf("unbounded citation sample: %+v", side)
			}
		}
	}
	wantUnits := map[string]int{
		"old_only_evidence": 1,
		"both_evidence":     1,
		"new_only_evidence": 1,
		"unresolved":        2,
	}
	if !equalStringIntMap(unitClasses, wantUnits) {
		t.Fatalf("unit classifications = %v, want %v", unitClasses, wantUnits)
	}
	if ambiguous == nil || ambiguous.Classification != "unresolved" ||
		ambiguous.Unit != nil || ambiguous.Old.OccurrenceCount != 1 ||
		ambiguous.Replacement.OccurrenceCount != 1 {
		t.Fatalf("ambiguous attribution crossed unit identity: %+v", ambiguous)
	}
	for index := range 6 {
		putCallerMapAssertionFor(
			t, st, callerMapSourceRepo,
			fmt.Sprintf("citation-sample-%d", index),
			fmt.Sprintf("src/sample_%d.go", index), 200+index*10, 208+index*10,
			"CALLS_OPERATION", callerMapOperation, callerMapLineage,
			store.TierDerived, "scip", "resolved", "unit-sample", "team-sample",
		)
	}
	var bounded api.CallerComparisonPage
	code, body = catalogHTTP(
		t, handler,
		callerComparisonTarget("level=unit&unit=unit-sample&page_size=100"),
		&bounded,
	)
	if code != http.StatusOK || len(bounded.Rows) != 1 ||
		bounded.Rows[0].Old.OccurrenceCount != 6 ||
		len(bounded.Rows[0].Old.Rows) != 4 ||
		!bounded.Rows[0].Old.RowsTruncated {
		t.Fatalf("bounded citation sample = %d %s / %+v", code, body, bounded)
	}
	for _, call := range st.calls {
		if strings.HasPrefix(call, "put:") {
			t.Fatalf("comparison read persisted state: %v", st.calls)
		}
	}
}

func TestCallerComparisonDeterminismHiddenIsolationAndCursorBinding(t *testing.T) {
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
		callerMapHiddenRepo:   false,
	}
	firstStore := callerComparisonStore(t)
	firstHandler := api.New(callerMapOptions(firstStore, "user:member", &visible))
	var first api.CallerComparisonPage
	_, firstBody := catalogHTTP(
		t, firstHandler, callerComparisonTarget("level=unit&page_size=2"), &first,
	)
	if first.Pagination.NextCursor == "" {
		t.Fatalf("comparison cursor absent: %+v", first)
	}
	if strings.Contains(firstBody, callerMapHiddenRepo) {
		t.Fatalf("hidden repository leaked into comparison: %s", firstBody)
	}
	for _, call := range firstStore.calls {
		if strings.Contains(call, callerMapHiddenRepo) {
			t.Fatalf("comparison queried hidden repository: %v", firstStore.calls)
		}
	}

	secondStore := callerComparisonStore(t)
	hiddenRun := secondStore.runs[proofScope(callerMapHiddenRepo, "grpc-caller")]
	hiddenRun.Coverage.AssertionCount = 9_999
	hiddenRun.Coverage.Protocols = []string{
		"attribution-" + strings.Repeat("f", 64), "grpc",
	}
	secondStore.runs[proofScope(callerMapHiddenRepo, "grpc-caller")] = hiddenRun
	secondStore.assertions[callerMapHiddenRepo] = nil
	_, secondBody := catalogHTTP(
		t, api.New(callerMapOptions(secondStore, "user:member", &visible)),
		callerComparisonTarget("level=unit&page_size=2"), nil,
	)
	if firstBody != secondBody {
		t.Fatalf("hidden mutation changed comparison bytes:\n%s\n%s",
			firstBody, secondBody)
	}
	if !slices.Equal(firstStore.calls, secondStore.calls) {
		t.Fatalf("hidden mutation changed comparison work:\n%v\n%v",
			firstStore.calls, secondStore.calls)
	}

	scope := proofScope(callerMapSourceRepo, "grpc-caller")
	originalRun := firstStore.runs[scope]
	t.Run("coverage digest", func(t *testing.T) {
		run := originalRun
		run.Coverage.ReadBytes++
		firstStore.runs[scope] = run
		assertComparisonCursorInvalid(t, firstHandler, first.Pagination.NextCursor)
	})
	t.Run("attribution digest", func(t *testing.T) {
		run := originalRun
		run.Coverage.Protocols = []string{
			"attribution-" + strings.Repeat("c", 64), "grpc",
		}
		firstStore.runs[scope] = run
		assertComparisonCursorInvalid(t, firstHandler, first.Pagination.NextCursor)
	})
	firstStore.runs[scope] = originalRun
}

func assertComparisonCursorInvalid(
	t *testing.T,
	handler http.Handler,
	cursor string,
) {
	t.Helper()
	code, body := catalogHTTP(
		t, handler,
		callerComparisonTarget("level=unit&page_size=2")+
			"&cursor="+url.QueryEscape(cursor),
		nil,
	)
	if code != http.StatusConflict ||
		!strings.Contains(body, "cursor is no longer valid") {
		t.Fatalf("changed comparison snapshot = %d %s", code, body)
	}
}

func TestCallerComparisonFiltersRefusalsAndCapability(t *testing.T) {
	st := callerComparisonStore(t)
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
	}
	handler := api.New(callerMapOptions(st, "user:member", &visible))
	var page api.CallerComparisonPage
	code, body := catalogHTTP(
		t, handler,
		callerComparisonTarget(
			"level=unit&classification=old_only_evidence&owner=team-old&page_size=100",
		),
		&page,
	)
	if code != http.StatusOK || len(page.Rows) != 1 ||
		page.Rows[0].Classification != "old_only_evidence" {
		t.Fatalf("filtered comparison = %d %s / %+v", code, body, page)
	}
	var empty api.CallerComparisonPage
	code, body = catalogHTTP(
		t, handler,
		callerComparisonTarget(
			"level=unit&classification=old_only_evidence&owner=missing&page_size=100",
		),
		&empty,
	)
	if code != http.StatusOK || empty.TotalRows != 0 || len(empty.Rows) != 0 ||
		!strings.Contains(empty.Caveat, "not migration completion") ||
		!strings.Contains(empty.Caveat, "decommissioning safety") {
		t.Fatalf("empty old-only comparison = %d %s / %+v", code, body, empty)
	}
	_, version := catalogHTTP(t, handler, "/api/version", nil)
	if !strings.Contains(version, `"contract-caller-comparison"`) {
		t.Fatalf("comparison capability absent: %s", version)
	}

	for _, testCase := range []struct {
		name  string
		extra string
		code  int
	}{
		{name: "bad level", extra: "level=diagram", code: http.StatusBadRequest},
		{name: "bad classification", extra: "classification=migrated", code: http.StatusBadRequest},
		{name: "oversized page", extra: "page_size=101", code: http.StatusUnprocessableEntity},
		{name: "wrong replacement", extra: "replacement_lineage=missing", code: http.StatusNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			code, body := catalogHTTP(
				t, handler, callerComparisonTarget(testCase.extra), nil,
			)
			if code != testCase.code {
				t.Fatalf("%s = %d %s", testCase.name, code, body)
			}
		})
	}
}

func callerComparisonStore(t *testing.T) *proofAPIStore {
	t.Helper()
	st := callerMapStore(t)
	rewriteCallerUnit(t, st, callerMapSourceRepo, "caller-a", "unit-shared")
	replacementRun := st.runs[proofScope(callerMapContractRepo, "proto-contract")]
	declaration, resolution := catalogAssertion(
		callerMapContractRepo, replacementRun.ID, "decl-replacement",
		"DECLARES_OPERATION", "idl/orders_v2.proto",
		strings.TrimPrefix(callerMapOperation, "/"), replacementLineage,
		`{"schema":"proto-operation-detail-v1","request":{"raw":"GetRequestV2","resolution":"unresolved"},"response":{"raw":"GetResponseV2","resolution":"unresolved"},"client_streaming":false,"server_streaming":false}`,
	)
	putCatalogAssertion(st, declaration, resolution)

	putCallerMapAssertionFor(
		t, st, callerMapSourceRepo, "old-only", "src/old.go", 110, 120,
		"CALLS_OPERATION", callerMapOperation, callerMapLineage,
		store.TierDerived, "scip", "resolved", "unit-old", "team-old",
	)
	putCallerMapAssertionFor(
		t, st, callerMapSourceRepo, "replacement-both", "src/a.go", 10, 20,
		"CALLS_OPERATION", callerMapOperation, replacementLineage,
		store.TierDerived, "scip", "resolved", "unit-shared", "team-checkout",
	)
	putCallerMapAssertionFor(
		t, st, callerMapSourceRepo, "replacement-ambiguous", "src/b.go", 30, 40,
		"CALLS_OPERATION", callerMapOperation, replacementLineage,
		store.TierHeuristic, "syntax", "ambiguous", "unit-ambiguous", "team-platform",
	)
	putCallerMapAssertionFor(
		t, st, callerMapSourceRepo, "replacement-new", "src/new.go", 90, 100,
		"CALLS_OPERATION", callerMapOperation, replacementLineage,
		store.TierDerived, "scip", "resolved", "unit-new", "team-new",
	)
	return st
}

func rewriteCallerUnit(
	t *testing.T,
	st *proofAPIStore,
	repo, assertionID, unitID string,
) {
	t.Helper()
	for index := range st.assertions[repo] {
		assertion := &st.assertions[repo][index]
		if assertion.ID != assertionID {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(assertion.Detail), &detail); err != nil {
			t.Fatal(err)
		}
		candidates := detail["unit_candidates"].([]any)
		candidates[0].(map[string]any)["id"] = unitID
		encoded, err := json.Marshal(detail)
		if err != nil {
			t.Fatal(err)
		}
		assertion.Detail = string(encoded)
		return
	}
	t.Fatalf("assertion %s not found", assertionID)
}

func callerComparisonTarget(extra string) string {
	values := url.Values{}
	values.Set("old_protocol", "protobuf")
	values.Set("old_repository", callerMapContractRepo)
	values.Set("old_lineage", callerMapLineage)
	values.Set("old_operation", callerMapOperation)
	values.Set("replacement_protocol", "protobuf")
	values.Set("replacement_repository", callerMapContractRepo)
	values.Set("replacement_lineage", replacementLineage)
	values.Set("replacement_operation", callerMapOperation)
	if extra != "" {
		for key, options := range mustParseQuery(extra) {
			values[key] = options
		}
	}
	return "/api/compare_operation_callers?" + values.Encode()
}

func mustParseQuery(raw string) url.Values {
	values, err := url.ParseQuery(raw)
	if err != nil {
		panic(err)
	}
	return values
}

func equalStringIntMap(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
