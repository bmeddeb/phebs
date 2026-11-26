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

// TestThriftOperationImpactExactHTTP is the PHEBS-45 end-to-end check: under
// the Thrift claim alone, a published thrift-caller generation is reported
// through the same operation report route with the same conclusion states and
// shared citations, the capability is advertised, and a protobuf endpoint is
// refused before any caller read.
func TestThriftOperationImpactExactHTTP(t *testing.T) {
	fixture := newExactCallerAPIFixtureFor(t, 2, exactCallerRepository, t.TempDir(), nil, nil, thriftExactCaller)
	opts := fixture.serviceOptions()
	opts.CallerMap = fixture.service
	opts.ThriftOperationImpactEnabled = true
	opts.OperationImpact = api.NewOperationImpactService(opts)
	if opts.OperationImpact == nil {
		t.Fatal("Thrift operation Impact service not constructed")
	}
	handler := api.New(opts)
	values := url.Values{"shape": {"operation_callers"}, "protocol": {"thrift"}, "repository": {fixture.repository}, "lineage": {exactCallerLineage}, "operation": {thriftExactCaller.operation}, "page_size": {"2"}}
	var report api.OperationImpactReport
	code, body := catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), &report)
	if code != http.StatusOK || report.SchemaVersion != api.OperationImpactSchemaVersion || report.Conclusion.State != "evidence_present" || len(report.CallerPage.Rows) != 2 || report.Conclusion.Query.Endpoint.Protocol != "thrift" {
		t.Fatalf("Thrift report status=%d body=%s report=%+v", code, body, report)
	}
	for _, row := range report.CallerPage.Rows {
		if row.Protocol != "thrift" || row.Classification != "resolved_caller" || row.Operation != thriftExactCaller.operation {
			t.Fatalf("Thrift report row = %+v", row)
		}
	}
	if report.UnavailableSections.WireVerdict != "not_in_supported_shape" || report.UnavailableSections.FieldReferences != "not_in_supported_shape" {
		t.Fatalf("Thrift report enabled an unsupported section: %+v", report.UnavailableSections)
	}
	citation, err := fixture.service.ReadCitation(t.Context(), report.CallerPage.Rows[0].Source.Citation)
	if err != nil || !strings.HasPrefix(citation.Content, "call0000") {
		t.Fatalf("shared Thrift citation=%+v err=%v", citation, err)
	}
	values.Set("owner", "no-owner")
	code, body = catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), &report)
	if code != http.StatusOK || report.Conclusion.State != "exact_empty" {
		t.Fatalf("Thrift empty=%d %s", code, body)
	}
	values.Del("owner")
	var version struct {
		Capabilities []string `json:"capabilities"`
	}
	if code, body := catalogHTTP(t, handler, "/api/version", &version); code != http.StatusOK || !strings.Contains(strings.Join(version.Capabilities, ","), "contract-impact-operation") {
		t.Fatalf("capability under the Thrift claim = %d %s", code, body)
	}
	reads := fixture.store.publicationReads
	values.Set("protocol", "protobuf")
	values.Set("operation", exactCallerOperation)
	code, body = catalogHTTP(t, handler, "/api/operation_impact_report?"+values.Encode(), nil)
	if code != http.StatusBadRequest || !strings.Contains(body, "exact thrift endpoint") || fixture.store.publicationReads != reads {
		t.Fatalf("protobuf endpoint under the Thrift claim = %d %s (reads %d -> %d)", code, body, reads, fixture.store.publicationReads)
	}
}
