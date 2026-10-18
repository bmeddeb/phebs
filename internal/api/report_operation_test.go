package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
)

func TestOperationImpactExactHTTPAndSharedCitation(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 2)
	opts := fixture.serviceOptions()
	opts.CallerMap = fixture.service
	opts.OperationImpactEnabled = true
	opts.OperationImpact = api.NewOperationImpactService(opts)
	handler := api.New(opts)
	values := url.Values{"shape": {"operation_callers"}, "protocol": {"protobuf"}, "repository": {fixture.repository}, "lineage": {exactCallerLineage}, "operation": {exactCallerOperation}, "page_size": {"1"}}
	var report api.OperationImpactReport
	code, body := catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), &report)
	if code != http.StatusOK || report.SchemaVersion != api.OperationImpactSchemaVersion || report.Conclusion.State != "evidence_present" || len(report.CallerPage.Rows) != 1 || report.CallerPage.Pagination.Complete || report.CallerPage.Coverage != nil || report.CallerPage.CoverageDigest != "" {
		t.Fatalf("status=%d body=%s report=%+v", code, body, report)
	}
	var transport struct {
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal([]byte(body), &transport); err != nil || transport.Schema == "" {
		t.Fatalf("missing Huma HTTP schema metadata: body=%s err=%v", body, err)
	}
	citation, err := fixture.service.ReadCitation(t.Context(), report.CallerPage.Rows[0].Source.Citation)
	if err != nil || citation.Content != "call00000()" {
		t.Fatalf("shared citation=%+v err=%v", citation, err)
	}
	values.Set("cursor", report.CallerPage.Pagination.NextCursor)
	code, body = catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), &report)
	if code != http.StatusOK || !report.CallerPage.Pagination.Complete || len(report.CallerPage.Rows) != 1 {
		t.Fatalf("continuation=%d %s", code, body)
	}
	values.Del("cursor")
	values.Set("owner", "no-owner")
	code, body = catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), &report)
	if code != http.StatusOK || report.Conclusion.State != "exact_empty" || report.CallerPage.TotalMatchingRows == nil || *report.CallerPage.TotalMatchingRows != 0 {
		t.Fatalf("empty=%d %s", code, body)
	}
	values.Del("owner")
	for _, extra := range []string{"&field_number=0", "&bundle=retained", "&shape=operation_callers", "&unknown=value"} {
		code, body = catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode()+extra, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("mixed input=%d %s", code, body)
		}
	}
	fixture.store.currentRevision++
	report = api.OperationImpactReport{}
	code, body = catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), &report)
	if code != http.StatusOK || report.Conclusion.State != "unavailable" || report.CallerPage.TotalMatchingRows != nil {
		t.Fatalf("gap=%d %s", code, body)
	}
	fixture.visible[fixture.repository] = false
	code, body = catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), nil)
	if code != http.StatusNotFound {
		t.Fatalf("denied=%d %s", code, body)
	}
}

func TestOperationImpactUnavailableSourceTransitionsHTTP(t *testing.T) {
	for _, test := range []struct {
		name, generationState string
		configure             func(*exactCallerAPIFixture)
	}{
		{"indexed before resolver", "missing", func(f *exactCallerAPIFixture) {
			f.store.candidate, f.store.resolver, f.store.publication = nil, nil, nil
		}},
		{"not indexed", "missing", func(f *exactCallerAPIFixture) {
			f.store.repos[0].IndexedCommitHash = ""
			f.store.publication = nil
		}},
		{"indexed replacement with retained caller", "stale", func(f *exactCallerAPIFixture) {
			f.store.repos[0].IndexedCommitHash = strings.Repeat("f", 40)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExactCallerAPIFixture(t, 1)
			test.configure(fixture)
			page, err := fixture.service.List(t.Context(), fixture.query, 1, "")
			if err != nil || page.Generation.State != test.generationState || page.MatchingRowsState != "unavailable" {
				t.Fatalf("shared Caller Map gap=%+v err=%v", page, err)
			}
			opts := fixture.serviceOptions()
			opts.CallerMap, opts.OperationImpactEnabled = fixture.service, true
			opts.OperationImpact = api.NewOperationImpactService(opts)
			values := url.Values{"shape": {"operation_callers"}, "protocol": {"protobuf"}, "repository": {fixture.repository}, "lineage": {exactCallerLineage}, "operation": {exactCallerOperation}, "page_size": {"1"}}
			var report api.OperationImpactReport
			code, body := catalogHTTP(t, api.New(opts), "/api/operation_impact_report?"+values.Encode(), &report)
			if code != http.StatusOK || report.CallerPage == nil {
				t.Fatalf("gap HTTP=%d %s", code, body)
			}
			actual := report.CallerPage
			if report.Conclusion.State != "unavailable" || actual.Generation.State != test.generationState || actual.Scope.Commit != page.Scope.Commit || actual.Generation.Commit != page.Generation.Commit || actual.Declaration != nil || actual.TotalMatchingRows != nil || len(actual.Rows) != 0 || !actual.Pagination.Complete {
				t.Fatalf("gap was promoted or rewritten: %s", body)
			}
		})
	}
}
