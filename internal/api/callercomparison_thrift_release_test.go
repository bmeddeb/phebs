package api_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
)

// TestExactCallerComparisonReleasedPairMatrix pins the PHEBS-44 fence: with no
// released claim every pair the exact engine accepts is compared; otherwise a
// pair is compared only when both endpoints share a protocol whose own claim
// is released. Mixed protobuf/Thrift pairs are never released. A refused pair
// answers 503 before any caller publication is read.
func TestExactCallerComparisonReleasedPairMatrix(t *testing.T) {
	pairs := [][2]string{
		{"protobuf", "protobuf"}, {"thrift", "thrift"},
		{"protobuf", "thrift"}, {"thrift", "protobuf"},
	}
	for _, test := range []struct {
		name                     string
		protobuf, thrift         bool
		admitted                 []string // "old/replacement"
		wantRefusalNamesProtocol string
	}{
		{name: "protobuf claim only", protobuf: true, admitted: []string{"protobuf/protobuf"}, wantRefusalNamesProtocol: "protobuf endpoint pairs only"},
		{name: "Thrift claim only", thrift: true, admitted: []string{"thrift/thrift"}, wantRefusalNamesProtocol: "thrift endpoint pairs only"},
		{name: "both claims", protobuf: true, thrift: true, admitted: []string{"protobuf/protobuf", "thrift/thrift"}, wantRefusalNamesProtocol: "protobuf or thrift endpoint pairs only"},
	} {
		for _, lane := range []struct {
			name string
			lane exactCallerProtocol
		}{{"protobuf fixture", protobufExactCaller}, {"thrift fixture", thriftExactCaller}} {
			fixture, _ := newExactCallerComparisonFixtureFor(t, lane.lane)
			opts := fixture.serviceOptions()
			opts.CallerReader = fixture.reader
			opts.CallerMap = fixture.service
			opts.CallerComparisonReleased = test.protobuf
			opts.ThriftCallerComparisonReleased = test.thrift
			service := api.NewCallerComparisonService(opts)
			base := exactComparisonQuery(fixture)
			for _, pair := range pairs {
				key := pair[0] + "/" + pair[1]
				t.Run(test.name+"/"+lane.name+"/"+key, func(t *testing.T) {
					query := base
					query.Old.Protocol, query.Replacement.Protocol = pair[0], pair[1]
					reads := fixture.store.publicationReads
					page, err := service.Compare(t.Context(), query, 1, "")
					if !slices.Contains(test.admitted, key) {
						requireCatalogStatus(t, err, http.StatusServiceUnavailable)
						if !strings.Contains(err.Error(), test.wantRefusalNamesProtocol) {
							t.Fatalf("refusal %q does not name %q", err, test.wantRefusalNamesProtocol)
						}
						if page != nil || fixture.store.publicationReads != reads {
							t.Fatal("unreleased endpoint pair reached caller publications")
						}
						return
					}
					// An admitted pair passes the fence. Only the fixture's own
					// protocol has a published generation, so the other lane is
					// answered by the engine, not by the release fence.
					if pair[0] == fixture.query.Endpoint.Protocol {
						if err != nil || page == nil || len(page.Rows) != 1 {
							t.Fatalf("released %s comparison = %+v, %v", key, page, err)
						}
					} else if err != nil && strings.Contains(err.Error(), "endpoint pairs only") {
						t.Fatalf("released %s pair refused by the release fence: %v", key, err)
					}
				})
			}
		}
	}
}

// TestExactThriftCallerComparisonReleasedEndToEnd is the PHEBS-44 end-to-end
// check: under the Thrift comparison claim alone, a published thrift-caller
// generation with two declaration lineages is compared over HTTP with the same
// neutral classification, per-side declarations and readable citations as the
// protobuf lane, and the capability is advertised.
func TestExactThriftCallerComparisonReleasedEndToEnd(t *testing.T) {
	fixture, _ := newExactCallerComparisonFixtureFor(t, thriftExactCaller)
	options := fixture.serviceOptions()
	options.CallerReader = fixture.reader
	options.CallerMap = fixture.service
	options.ThriftCallerComparisonReleased = true
	handler := api.New(options)

	query := exactComparisonQuery(fixture)
	if query.Old.Protocol != "thrift" || query.Replacement.Protocol != "thrift" {
		t.Fatalf("fixture query is not a Thrift pair: %+v", query)
	}
	values := url.Values{
		"old_protocol": {"thrift"}, "old_repository": {query.Old.Repository},
		"old_lineage": {query.Old.Lineage}, "old_operation": {query.Old.Operation},
		"replacement_protocol": {"thrift"}, "replacement_repository": {query.Replacement.Repository},
		"replacement_lineage": {query.Replacement.Lineage}, "replacement_operation": {query.Replacement.Operation},
		"freshness": {"any"}, "resolution": {"any"}, "ordering": {"source"},
		"level": {"occurrence"}, "page_size": {"100"},
	}
	var page api.CallerComparisonPage
	code, body := catalogHTTP(t, handler, "/api/compare_operation_callers?"+values.Encode(), &page)
	if code != http.StatusOK {
		t.Fatalf("HTTP Thrift comparison = %d %s", code, body)
	}
	if page.MatchingRowsState != "exact" || page.TotalRows == nil || *page.TotalRows != 4 ||
		page.Old.Declaration == nil || page.Replacement.Declaration == nil ||
		page.Old.Generation == nil || page.Replacement.Generation == nil {
		t.Fatalf("Thrift comparison page = %+v", page)
	}
	classes := map[string]int{}
	var citation string
	for _, row := range page.Rows {
		classes[row.Classification]++
		for _, side := range []api.CallerComparisonSide{row.Old, row.Replacement} {
			for _, sample := range side.Rows {
				if sample.Protocol != "thrift" {
					t.Fatalf("Thrift comparison sampled a %s row: %+v", sample.Protocol, sample)
				}
				if citation == "" {
					citation = sample.Source.Citation
				}
			}
		}
	}
	want := map[string]int{
		"old_only_evidence": 1, "both_evidence": 1,
		"new_only_evidence": 1, "unresolved": 1,
	}
	if !equalStringIntMap(classes, want) {
		t.Fatalf("Thrift occurrence classes = %v, want %v", classes, want)
	}
	var cited api.CallerMapCitation
	code, body = catalogHTTP(t, handler, "/api/contract_callers/citation?citation="+url.QueryEscape(citation), &cited)
	if code != http.StatusOK || cited.Content == "" {
		t.Fatalf("Thrift comparison citation = %d %s", code, body)
	}

	// Mixed and protobuf pairs stay refused under the Thrift claim alone.
	for _, pair := range [][2]string{{"protobuf", "thrift"}, {"thrift", "protobuf"}, {"protobuf", "protobuf"}} {
		values.Set("old_protocol", pair[0])
		values.Set("replacement_protocol", pair[1])
		if code, body := catalogHTTP(t, handler, "/api/compare_operation_callers?"+values.Encode(), nil); code != http.StatusServiceUnavailable {
			t.Fatalf("%s/%s pair under the Thrift claim = %d %s", pair[0], pair[1], code, body)
		}
	}
}
