package api

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type operationImpactCallerStub struct {
	page   *CallerMapPage
	err    error
	calls  int
	query  CallerMapQuery
	size   int
	cursor string
}

func (stub *operationImpactCallerStub) List(_ context.Context, query CallerMapQuery, size int, cursor string) (*CallerMapPage, error) {
	stub.calls++
	stub.query, stub.size, stub.cursor = query, size, cursor
	return stub.page, stub.err
}

func operationImpactTestPage() *CallerMapPage {
	query := normalizeCallerMapQuery(CallerMapQuery{Endpoint: CallerMapEndpoint{Protocol: "protobuf", Repository: "example.com/repo", Lineage: "decl", Operation: "/demo.Service/Get"}})
	return &CallerMapPage{SchemaVersion: exactCallerMapSchemaVersion, Query: query,
		Rows:              []CallerMapRow{{Classification: "extractor_abstention", Resolution: "unresolved", Tier: "unresolved", Unit: CallerMapUnitAttribution{State: "unavailable", Reason: "no_snapshot"}}},
		Generation:        &CallerMapGeneration{State: "current", Plane: exactCallerMapPlane, Repository: query.Endpoint.Repository, Commit: strings.Repeat("a", 40), GenerationDigest: "sha256:" + strings.Repeat("b", 64)},
		Scope:             &AnalysisScopeProjection{Repository: query.Endpoint.Repository, Commit: strings.Repeat("a", 40), ScopePosture: "whole-repository"},
		MatchingRowsState: "exact", TotalMatchingRows: callerMapTotal(1), Pagination: CallerMapPagination{Complete: true}, Caveat: "Static evidence; no absence claim.",
	}
}

func TestOperationImpactUsesOneUnmodifiedCallerPage(t *testing.T) {
	page := operationImpactTestPage()
	stub := &operationImpactCallerStub{page: page}
	service := &OperationImpactService{callers: stub}
	report, err := service.Build(t.Context(), operationImpactShape, page.Query, 50, "opaque")
	if err != nil || stub.calls != 1 || stub.query != page.Query || stub.size != 50 || stub.cursor != "opaque" || report.CallerPage != page || &report.CallerPage.Rows[0] != &page.Rows[0] || report.Conclusion.State != "evidence_present" || report.Conclusion.Query != page.Query || report.Conclusion.GenerationDigest != page.Generation.GenerationDigest || report.UnavailableSections.SavedReport != "not_in_supported_shape" {
		t.Fatalf("report=%+v stub=%+v err=%v", report, stub, err)
	}
	if report.CallerPage.Rows[0].Unit.State != "unavailable" || report.CallerPage.Rows[0].Classification != "extractor_abstention" || !strings.Contains(report.Conclusion.Qualification, "per-row resolution") {
		t.Fatal("uncertainty reinterpreted")
	}
}

func TestOperationImpactEmptyUnavailableAndInvalidPages(t *testing.T) {
	for _, test := range []struct {
		name, want string
		change     func(*CallerMapPage)
		refuse     bool
	}{
		{"exact empty", "exact_empty", func(p *CallerMapPage) { p.Rows = []CallerMapRow{}; p.TotalMatchingRows = callerMapTotal(0) }, false},
		{"unavailable", "unavailable", func(p *CallerMapPage) {
			p.Rows = []CallerMapRow{}
			p.TotalMatchingRows = nil
			p.MatchingRowsState = "unavailable"
			p.Generation.State = "stale"
		}, false},
		{"crossed query", "", func(p *CallerMapPage) { p.Query.Endpoint.Repository = "secret.example/repo" }, true},
		{"crossed source", "", func(p *CallerMapPage) { p.Generation.Repository = "secret.example/repo" }, true},
		{"oversized page", "", func(p *CallerMapPage) { p.Rows = make([]CallerMapRow, 101); p.TotalMatchingRows = callerMapTotal(101) }, true},
		{"unavailable zero", "", func(p *CallerMapPage) {
			p.Generation.State = "stale"
			p.MatchingRowsState = "unavailable"
			p.Rows = []CallerMapRow{}
			p.TotalMatchingRows = callerMapTotal(0)
		}, true},
		{"legacy bytes", "", func(p *CallerMapPage) { p.SchemaVersion = "caller-map-v1" }, true},
		{"invented certificate", "", func(p *CallerMapPage) { p.CoverageDigest = "synthetic" }, true},
		{"unknown state", "", func(p *CallerMapPage) { p.Generation.State = "other" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			page := operationImpactTestPage()
			query := page.Query
			test.change(page)
			stub := &operationImpactCallerStub{page: page}
			report, err := (&OperationImpactService{callers: stub}).Build(t.Context(), operationImpactShape, query, 100, "")
			if stub.calls != 1 || (err != nil) != test.refuse {
				t.Fatalf("calls=%d report=%+v err=%v", stub.calls, report, err)
			}
			if !test.refuse && report.Conclusion.State != test.want {
				t.Fatalf("conclusion=%+v", report.Conclusion)
			}
		})
	}
}

func TestOperationImpactUnsupportedAndErrors(t *testing.T) {
	stub := &operationImpactCallerStub{page: operationImpactTestPage()}
	service := &OperationImpactService{callers: stub}
	for _, shape := range []string{"", "unknown", "field_references", "proposed_change", "implementations", "wire_verdict", "matching_syntactic_calls", "saved_report"} {
		if _, err := service.Build(t.Context(), shape, stub.page.Query, 50, ""); err == nil || stub.calls != 0 {
			t.Fatalf("shape=%s calls=%d err=%v", shape, stub.calls, err)
		}
	}
	query := stub.page.Query
	query.Endpoint.Protocol = "thrift"
	if _, err := service.Build(t.Context(), operationImpactShape, query, 50, ""); err == nil || stub.calls != 0 {
		t.Fatal("foreign protocol admitted")
	}
	stub.err = errors.New("source refused")
	if _, err := service.Build(t.Context(), operationImpactShape, stub.page.Query, 50, ""); !errors.Is(err, stub.err) || stub.calls != 1 {
		t.Fatal("source refusal changed")
	}
	if NewOperationImpactService(Options{OperationImpactEnabled: true, CallerMap: &CallerMapService{}}) != nil || NewOperationImpactService(Options{CallerMap: &CallerMapService{exact: &exactCallerMapService{}}}) != nil {
		t.Fatal("unadmitted or legacy engine accepted")
	}
}
