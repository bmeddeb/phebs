package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	callerMapContractRepo = "github.com/acme/contracts"
	callerMapSourceRepo   = "github.com/acme/service"
	callerMapHiddenRepo   = "github.com/acme/hidden"
	callerMapLineage      = "provisional_repo_path_v1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	callerMapOperation    = "/acme.orders.v1.Orders/Get"
	callerMapAttr         = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestCallerMapPagesExactIdentityAndHidesInvisibleRepositories(t *testing.T) {
	st := callerMapStore(t)
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
		callerMapHiddenRepo:   false,
	}
	handler := api.New(callerMapOptions(st, "user:member", &visible))
	target := callerMapTarget("ordering=unit&page_size=2")
	var first api.CallerMapPage
	code, body := catalogHTTP(t, handler, target, &first)
	if code != http.StatusOK {
		t.Fatalf("first page = %d %s", code, body)
	}
	if first.SchemaVersion != "caller-map-v1" ||
		first.Query.Endpoint.Lineage != callerMapLineage ||
		first.TotalMatchingRows != 3 || first.Pagination.Complete ||
		first.Pagination.NextCursor == "" || len(first.Rows) != 2 ||
		first.AttributionDigest == "" || len(first.Groups) == 0 {
		t.Fatalf("first caller page = %+v", first)
	}
	seen := map[string]bool{}
	for _, row := range first.Rows {
		if row.Source.Repository == callerMapHiddenRepo ||
			row.Source.Commit != catalogCommit ||
			row.Source.Path == "" || row.Source.StartLine < 1 ||
			row.Source.EndByte <= row.Source.StartByte {
			t.Fatalf("caller row = %+v", row)
		}
		seen[row.Source.AssertionID] = true
	}
	var second api.CallerMapPage
	code, body = catalogHTTP(
		t, handler, target+"&cursor="+url.QueryEscape(first.Pagination.NextCursor),
		&second,
	)
	if code != http.StatusOK || !second.Pagination.Complete ||
		len(second.Rows) != 1 || seen[second.Rows[0].Source.AssertionID] {
		t.Fatalf("second page = %d %s / %+v", code, body, second)
	}
	if second.Rows[0].Classification != "extractor_abstention" ||
		second.Rows[0].Unit.State != "unavailable" {
		t.Fatalf("unattributed abstention was lost: %+v", second.Rows[0])
	}
	for _, call := range st.calls {
		if strings.Contains(call, callerMapHiddenRepo) {
			t.Fatalf("hidden repository affected caller-map work: %v", st.calls)
		}
		if strings.HasPrefix(call, "put:") {
			t.Fatalf("caller-map read persisted a proof bundle: %v", st.calls)
		}
	}
	if strings.Contains(body, callerMapHiddenRepo) {
		t.Fatalf("hidden repository leaked: %s", body)
	}
}

func TestCallerMapFiltersAndCursorBindings(t *testing.T) {
	st := callerMapStore(t)
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
	}
	handler := api.New(callerMapOptions(st, "user:member", &visible))
	var filtered api.CallerMapPage
	target := callerMapTarget(
		"owner=team-checkout&resolution=scip&freshness=fresh&page_size=1",
	)
	code, body := catalogHTTP(t, handler, target, &filtered)
	if code != http.StatusOK || len(filtered.Rows) != 1 ||
		filtered.Rows[0].Classification != "resolved_caller" ||
		filtered.Rows[0].Unit.Candidates[0].Owners[0] != "team-checkout" {
		t.Fatalf("filtered caller map = %d %s / %+v", code, body, filtered)
	}

	var first api.CallerMapPage
	code, body = catalogHTTP(
		t, handler, callerMapTarget("page_size=1"), &first,
	)
	if code != http.StatusOK || first.Pagination.NextCursor == "" {
		t.Fatalf("cursor page = %d %s / %+v", code, body, first)
	}
	run := st.runs[proofScope(callerMapSourceRepo, "grpc-caller")]
	run.Coverage.Protocols = []string{
		"attribution-" + strings.Repeat("b", 64), "grpc",
	}
	st.runs[proofScope(callerMapSourceRepo, "grpc-caller")] = run
	code, body = catalogHTTP(
		t, handler,
		callerMapTarget("page_size=1")+
			"&cursor="+url.QueryEscape(first.Pagination.NextCursor),
		nil,
	)
	if code != http.StatusConflict {
		t.Fatalf("changed attribution cursor = %d %s", code, body)
	}
}

func TestCallerMapRejectsDigestBearingLegacyRunAsConflict(t *testing.T) {
	st := callerMapStore(t)
	run := st.runs[proofScope(callerMapSourceRepo, "grpc-caller")]
	run.Extractor = "1.1.0"
	st.runs[proofScope(callerMapSourceRepo, "grpc-caller")] = run
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
	}
	code, body := catalogHTTP(
		t,
		api.New(callerMapOptions(st, "user:member", &visible)),
		callerMapTarget("page_size=1"),
		nil,
	)
	if code != http.StatusConflict ||
		!strings.Contains(body, "republished by the current extractor") {
		t.Fatalf("legacy caller run = %d %s", code, body)
	}
}

func TestCallerMapEveryFilterAndOrderingIsPinned(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		query      string
		want       int
		firstID    string
		firstGroup string
	}{
		{name: "unit id", query: "unit=unit-caller-a", want: 1, firstID: "caller-a"},
		{name: "logical service", query: "unit=orders", want: 2},
		{name: "owner", query: "owner=team-platform", want: 1, firstID: "caller-b"},
		{name: "path", query: "path_prefix=src%2Fb.go", want: 1, firstID: "caller-b"},
		{name: "code role", query: "code_role=production", want: 3},
		{name: "tier", query: "tier=derived", want: 1, firstID: "caller-a"},
		{name: "freshness", query: "freshness=fresh", want: 3},
		{name: "typed resolution", query: "resolution=scip", want: 1, firstID: "caller-a"},
		{name: "syntax resolution", query: "resolution=syntax", want: 1, firstID: "caller-b"},
		{name: "unresolved", query: "resolution=unresolved", want: 1, firstID: "caller-gap"},
		{
			name: "unit ordering", query: "ordering=unit", want: 3,
			firstID: "caller-b", firstGroup: "ambiguous:unit-caller-b",
		},
		{name: "source ordering", query: "ordering=source", want: 3, firstID: "caller-a"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			st := callerMapStore(t)
			visible := map[string]bool{
				callerMapContractRepo: true,
				callerMapSourceRepo:   true,
			}
			var page api.CallerMapPage
			code, body := catalogHTTP(
				t, api.New(callerMapOptions(st, "user:member", &visible)),
				callerMapTarget(testCase.query+"&page_size=100"), &page,
			)
			if code != http.StatusOK || len(page.Rows) != testCase.want ||
				page.TotalMatchingRows != testCase.want {
				t.Fatalf("filtered page = %d %s / %+v", code, body, page)
			}
			if testCase.firstID != "" &&
				page.Rows[0].Source.AssertionID != testCase.firstID {
				t.Fatalf("first row = %+v, want %s", page.Rows[0], testCase.firstID)
			}
			if testCase.firstGroup != "" &&
				page.Rows[0].UnitGroup != testCase.firstGroup {
				t.Fatalf("first group = %q, want %q",
					page.Rows[0].UnitGroup, testCase.firstGroup)
			}
		})
	}
}

func TestCallerMapHiddenMutationCannotAffectBytesOrWorkShape(t *testing.T) {
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
		callerMapHiddenRepo:   false,
	}
	firstStore := callerMapStore(t)
	_, firstBody := catalogHTTP(
		t, api.New(callerMapOptions(firstStore, "user:member", &visible)),
		callerMapTarget("page_size=100"), nil,
	)

	secondStore := callerMapStore(t)
	hiddenRun := secondStore.runs[proofScope(callerMapHiddenRepo, "grpc-caller")]
	hiddenRun.ID = "run-hidden-mutated"
	hiddenRun.Coverage.AssertionCount = 999
	hiddenRun.Coverage.Protocols = []string{
		"attribution-" + strings.Repeat("f", 64), "grpc",
	}
	secondStore.runs[proofScope(callerMapHiddenRepo, "grpc-caller")] = hiddenRun
	secondStore.assertions[callerMapHiddenRepo] = nil
	_, secondBody := catalogHTTP(
		t, api.New(callerMapOptions(secondStore, "user:member", &visible)),
		callerMapTarget("page_size=100"), nil,
	)

	if firstBody != secondBody {
		t.Fatalf("hidden mutation changed serialized caller map:\nfirst %s\nsecond %s",
			firstBody, secondBody)
	}
	if !slices.Equal(firstStore.calls, secondStore.calls) {
		t.Fatalf("hidden mutation changed work shape:\nfirst %v\nsecond %v",
			firstStore.calls, secondStore.calls)
	}
}

func TestCallerMapCursorInvalidatesEverySnapshotDimension(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*proofAPIStore, map[string]bool)
	}{
		{
			name: "publication",
			mutate: func(st *proofAPIStore, _ map[string]bool) {
				run := st.runs[proofScope(callerMapSourceRepo, "grpc-caller")]
				run.ID = "replacement-run"
				st.runs[proofScope(callerMapSourceRepo, "grpc-caller")] = run
			},
		},
		{
			name: "coverage",
			mutate: func(st *proofAPIStore, _ map[string]bool) {
				run := st.runs[proofScope(callerMapSourceRepo, "grpc-caller")]
				run.Coverage.ReadBytes++
				st.runs[proofScope(callerMapSourceRepo, "grpc-caller")] = run
			},
		},
		{
			name: "attribution",
			mutate: func(st *proofAPIStore, _ map[string]bool) {
				run := st.runs[proofScope(callerMapSourceRepo, "grpc-caller")]
				run.Coverage.Protocols = []string{
					"attribution-" + strings.Repeat("b", 64), "grpc",
				}
				st.runs[proofScope(callerMapSourceRepo, "grpc-caller")] = run
			},
		},
		{
			name: "permission",
			mutate: func(_ *proofAPIStore, visible map[string]bool) {
				visible[callerMapSourceRepo] = false
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			st := callerMapStore(t)
			visible := map[string]bool{
				callerMapContractRepo: true,
				callerMapSourceRepo:   true,
			}
			handler := api.New(callerMapOptions(st, "user:member", &visible))
			var first api.CallerMapPage
			code, body := catalogHTTP(
				t, handler, callerMapTarget("page_size=1"), &first,
			)
			if code != http.StatusOK || first.Pagination.NextCursor == "" {
				t.Fatalf("first page = %d %s / %+v", code, body, first)
			}
			testCase.mutate(st, visible)
			code, body = catalogHTTP(
				t, handler,
				callerMapTarget("page_size=1")+
					"&cursor="+url.QueryEscape(first.Pagination.NextCursor),
				nil,
			)
			if code != http.StatusConflict {
				t.Fatalf("stale %s cursor = %d %s", testCase.name, code, body)
			}
		})
	}
}

func TestCallerMapCursorBindsEndpointFiltersOrderingAndPageSize(t *testing.T) {
	st := callerMapStore(t)
	visible := map[string]bool{
		callerMapContractRepo: true,
		callerMapSourceRepo:   true,
	}
	handler := api.New(callerMapOptions(st, "user:member", &visible))
	var first api.CallerMapPage
	code, body := catalogHTTP(
		t, handler, callerMapTarget("page_size=1"), &first,
	)
	if code != http.StatusOK || first.Pagination.NextCursor == "" {
		t.Fatalf("first page = %d %s / %+v", code, body, first)
	}
	for _, testCase := range []struct {
		name  string
		extra string
	}{
		{name: "filter", extra: "owner=team-checkout&page_size=1"},
		{name: "ordering", extra: "ordering=unit&page_size=1"},
		{name: "page size", extra: "page_size=2"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			code, body := catalogHTTP(
				t, handler,
				callerMapTarget(testCase.extra)+
					"&cursor="+url.QueryEscape(first.Pagination.NextCursor),
				nil,
			)
			if code != http.StatusBadRequest {
				t.Fatalf("changed %s cursor = %d %s", testCase.name, code, body)
			}
		})
	}
	changedEndpoint := strings.Replace(
		callerMapTarget("page_size=1"),
		callerMapLineage,
		"other-declaration-lineage",
		1,
	)
	code, body = catalogHTTP(
		t, handler,
		changedEndpoint+"&cursor="+url.QueryEscape(first.Pagination.NextCursor),
		nil,
	)
	if code != http.StatusBadRequest {
		t.Fatalf("changed endpoint cursor = %d %s", code, body)
	}
}

func TestCallerMapDarkAndDeclarationIdentityRefusals(t *testing.T) {
	st := callerMapStore(t)
	dark := api.New(api.Options{
		Version: "test", Store: st, Evidence: st,
		Principal: func(context.Context) string { return "user:member" },
	})
	code, body := catalogHTTP(t, dark, callerMapTarget(""), nil)
	if code != http.StatusNotFound || strings.Contains(body, "caller") {
		t.Fatalf("dark caller map = %d %s", code, body)
	}

	anonymous := api.New(callerMapOptions(st, "", nil))
	code, body = catalogHTTP(t, anonymous, callerMapTarget(""), nil)
	if code != http.StatusNotFound || strings.Contains(body, "coverage") {
		t.Fatalf("anonymous caller map = %d %s", code, body)
	}
	_, version := catalogHTTP(t, anonymous, "/api/version", nil)
	if strings.Contains(version, "contract-caller-map") {
		t.Fatalf("anonymous capability leak: %s", version)
	}

	handler := api.New(callerMapOptions(st, "user:member", nil))
	wrong := url.Values{}
	wrong.Set("protocol", "protobuf")
	wrong.Set("repository", callerMapContractRepo)
	wrong.Set("lineage", "wrong-lineage")
	wrong.Set("operation", callerMapOperation)
	code, body = catalogHTTP(
		t, handler, "/api/contract_callers?"+wrong.Encode(), nil,
	)
	if code != http.StatusNotFound {
		t.Fatalf("wrong declaration identity = %d %s", code, body)
	}
	_, version = catalogHTTP(t, handler, "/api/version", nil)
	if !strings.Contains(version, `"contract-caller-map"`) {
		t.Fatalf("caller-map capability absent: %s", version)
	}
}

func callerMapTarget(extra string) string {
	values := url.Values{}
	values.Set("protocol", "protobuf")
	values.Set("repository", callerMapContractRepo)
	values.Set("lineage", callerMapLineage)
	values.Set("operation", callerMapOperation)
	target := "/api/contract_callers?" + values.Encode()
	if extra != "" {
		target += "&" + extra
	}
	return target
}

func callerMapOptions(
	st *proofAPIStore,
	principal string,
	visible *map[string]bool,
) api.Options {
	options := catalogOptions(st, principal, visible)
	options.CallerMapEnabled = true
	return options
}

func callerMapStore(t *testing.T) *proofAPIStore {
	t.Helper()
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: callerMapContractRepo, IndexedCommitHash: catalogCommit},
			{Name: callerMapHiddenRepo, IndexedCommitHash: catalogCommit},
			{Name: callerMapSourceRepo, IndexedCommitHash: catalogCommit},
		},
		runs:        make(map[string]store.ExtractionRun),
		attempts:    make(map[string]store.ExtractionAttempt),
		assertions:  make(map[string][]store.Assertion),
		resolutions: make(map[string]store.EvidenceResolution),
	}
	declarationRun := catalogRun(
		callerMapContractRepo, "proto-contract", "run-declaration", 1,
	)
	st.runs[proofScope(callerMapContractRepo, "proto-contract")] = declarationRun
	declaration, declarationResolution := catalogAssertion(
		callerMapContractRepo, declarationRun.ID, "decl-operation",
		"DECLARES_OPERATION", "idl/orders.proto",
		strings.TrimPrefix(callerMapOperation, "/"), callerMapLineage,
		`{"schema":"proto-operation-detail-v1","request":{"raw":"GetRequest","resolution":"unresolved"},"response":{"raw":"GetResponse","resolution":"unresolved"},"client_streaming":false,"server_streaming":false}`,
	)
	putCatalogAssertion(st, declaration, declarationResolution)

	for _, repo := range []string{callerMapSourceRepo, callerMapHiddenRepo} {
		run := catalogRun(repo, "grpc-caller", "run-"+strings.TrimPrefix(repo, "github.com/acme/"), 3)
		run.Extractor = "1.3.0"
		run.Coverage.Protocols = []string{
			"attribution-" + callerMapAttr, "grpc", "resolution-scip-v1",
		}
		st.runs[proofScope(repo, "grpc-caller")] = run
	}
	putCallerMapAssertion(
		t, st, callerMapSourceRepo, "caller-a", "src/a.go", 10, 20,
		"CALLS_OPERATION", callerMapLineage, store.TierDerived, "scip",
		"resolved", "team-checkout",
	)
	putCallerMapAssertion(
		t, st, callerMapSourceRepo, "caller-b", "src/b.go", 30, 40,
		"CALLS_OPERATION", callerMapLineage, store.TierHeuristic, "syntax",
		"ambiguous", "team-platform",
	)
	putCallerMapAssertion(
		t, st, callerMapSourceRepo, "caller-gap", "src/c.go", 50, 60,
		"UNRESOLVED_CALLER", "", store.TierUnresolved, "syntax",
		"unavailable", "",
	)
	// Equal operation spelling under another declaration identity is never
	// part of the exact Caller Map.
	putCallerMapAssertion(
		t, st, callerMapSourceRepo, "caller-other-lineage", "src/d.go", 70, 80,
		"CALLS_OPERATION", "provisional_repo_path_v1_"+strings.Repeat("d", 64),
		store.TierDerived, "scip", "resolved", "team-checkout",
	)
	putCallerMapAssertion(
		t, st, callerMapHiddenRepo, "caller-hidden", "src/secret.go", 10, 20,
		"CALLS_OPERATION", callerMapLineage, store.TierDerived, "scip",
		"resolved", "team-secret",
	)
	return st
}

func putCallerMapAssertion(
	t *testing.T,
	st *proofAPIStore,
	repo, id, path string,
	start, end int,
	predicate, lineage, tier, resolution, unitState, owner string,
) {
	putCallerMapAssertionFor(
		t, st, repo, id, path, start, end, predicate, callerMapOperation,
		lineage, tier, resolution, unitState, "unit-"+id, owner,
	)
}

func putCallerMapAssertionFor(
	t *testing.T,
	st *proofAPIStore,
	repo, id, path string,
	start, end int,
	predicate, operation, lineage, tier, resolution, unitState, unitID, owner string,
) {
	t.Helper()
	runID := st.runs[proofScope(repo, "grpc-caller")].ID
	atomID := "ea_" + id
	detail := map[string]any{
		"schema": "go-caller-detail-v1", "resolution": resolution,
		"protocol": "grpc", "attribution_digest": "sha256:" + callerMapAttr,
		"unit_state": unitState,
	}
	if predicate == "UNRESOLVED_CALLER" {
		detail["unresolved_reason"] = "unsupported_receiver_flow"
	}
	if owner != "" {
		detail["unit_candidates"] = []map[string]any{{
			"id": unitID, "logical_services": []string{"orders"},
			"owners": []string{owner},
		}}
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	assertion := store.Assertion{
		ID: id, Predicate: predicate,
		Subject: path + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end),
		Object:  operation, Lineage: lineage, Tier: tier,
		CodeRole: "production", Repo: repo, RunID: runID,
		Supporting: []string{atomID}, Detail: string(encoded),
	}
	st.assertions[repo] = append(st.assertions[repo], assertion)
	st.resolutions[proofEvidenceScope(repo, runID, atomID)] = store.EvidenceResolution{
		Atom: store.EvidenceAtom{
			ID: atomID, SchemaVersion: "t20-test",
			BlobDigest: "sha256:" + strings.Repeat("e", 64),
			StartByte:  start, EndByte: end,
		},
		Occurrences: []store.SnapshotEvidence{{
			ID: "occ_" + id, AtomID: atomID, Repo: repo, Commit: catalogCommit,
			Path: path, StartLine: start/10 + 1, EndLine: start/10 + 1,
			VisibilityScope: "repo:" + repo, RunID: runID,
		}},
	}
}

// T20.11-13 review: the one-open-list expansion bound is enforced, not a
// fixture coincidence — one ambiguous row cannot serialize more than 64
// candidates, and truncation is explicit via the pre-truncation total.
func TestCallerMapUnitCandidateBound(t *testing.T) {
	st := callerMapStore(t)
	for index := range st.assertions[callerMapSourceRepo] {
		assertion := &st.assertions[callerMapSourceRepo][index]
		if assertion.ID != "caller-b" {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(assertion.Detail), &detail); err != nil {
			t.Fatal(err)
		}
		candidates := make([]map[string]any, 0, 70)
		for candidate := 0; candidate < 70; candidate++ {
			candidates = append(candidates, map[string]any{
				"id":     fmt.Sprintf("unit-%03d", candidate),
				"owners": []string{"team-platform"},
			})
		}
		detail["unit_candidates"] = candidates
		encoded, err := json.Marshal(detail)
		if err != nil {
			t.Fatal(err)
		}
		assertion.Detail = string(encoded)
	}
	handler := api.New(callerMapOptions(st, "user:member", nil))
	var page api.CallerMapPage
	code, body := catalogHTTP(t, handler, callerMapTarget(""), &page)
	if code != http.StatusOK {
		t.Fatalf("caller map = %d %s", code, body)
	}
	found := false
	for _, row := range page.Rows {
		if row.Source.AssertionID != "caller-b" {
			continue
		}
		found = true
		if len(row.Unit.Candidates) != 64 || row.Unit.CandidateTotal != 70 {
			t.Fatalf("candidate bound not enforced: %d shown of %d",
				len(row.Unit.Candidates), row.Unit.CandidateTotal)
		}
	}
	if !found {
		t.Fatalf("ambiguous row missing: %+v", page.Rows)
	}
}
