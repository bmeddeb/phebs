package api_test

import (
	"net/http"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
)

func TestExactCallerComparisonReleasedProtocolFence(t *testing.T) {
	fixture, _ := newExactCallerComparisonFixture(t)
	opts := fixture.serviceOptions()
	opts.CallerReader = fixture.reader
	opts.CallerMap = fixture.service
	opts.CallerComparisonReleased = true
	service := api.NewCallerComparisonService(opts)
	query := exactComparisonQuery(fixture)
	if page, err := service.Compare(t.Context(), query, 1, ""); err != nil || page == nil || len(page.Rows) != 1 {
		t.Fatalf("supported released comparison = %+v, %v", page, err)
	}
	for _, protocols := range [][2]string{{"protobuf", "thrift"}, {"thrift", "protobuf"}, {"thrift", "thrift"}} {
		t.Run(protocols[0]+"/"+protocols[1], func(t *testing.T) {
			unsupported := query
			unsupported.Old.Protocol, unsupported.Replacement.Protocol = protocols[0], protocols[1]
			reads := fixture.store.publicationReads
			page, err := service.Compare(t.Context(), unsupported, 1, "")
			requireCatalogStatus(t, err, http.StatusServiceUnavailable)
			if page != nil || fixture.store.publicationReads != reads {
				t.Fatal("unsupported released endpoint pair reached caller publications")
			}
		})
	}
}
