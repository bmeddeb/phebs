package api

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
)

func TestCollectProofEvidenceBindsEveryFilterToAnExplicitCoverageDomain(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		domains []string
		filter  assertionFilter
	}{
		{name: "missing filter domain", domains: []string{"one"}, filter: assertionFilter{Predicate: "CALLS_OPERATION"}},
		{name: "domain absent from coverage", domains: []string{"one", "two"}, filter: assertionFilter{Domain: "three", Predicate: "CALLS_OPERATION"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			certificate := &extract.CoverageCertificate{Domains: test.domains}
			_, _, err := collectProofEvidence(context.Background(), nil, certificate, []assertionFilter{test.filter})
			if err == nil || !strings.Contains(err.Error(), "domain") {
				t.Fatalf("domains %v: %v", test.domains, err)
			}
		})
	}
	certificate := &extract.CoverageCertificate{Domains: []string{"one", "two"}}
	if _, _, err := collectProofEvidence(context.Background(), nil, certificate, []assertionFilter{{
		Domain: "two", Predicate: "CALLS_OPERATION",
	}}); err != nil {
		t.Fatalf("explicit filter domain was rejected: %v", err)
	}
}

func TestProtocolPackFieldReferenceAdmission(t *testing.T) {
	for _, test := range []struct {
		name       string
		number     int
		wantDomain string
	}{
		{name: "negative", number: -1},
		{name: "Thrift zero", number: 0, wantDomain: "scip-thrift-field"},
		{name: "shared one", number: 1, wantDomain: "scip-proto-field,scip-thrift-field"},
		{name: "shared Thrift max", number: 32_767, wantDomain: "scip-proto-field,scip-thrift-field"},
		{name: "protobuf reserved is Thrift", number: 19_000, wantDomain: "scip-thrift-field"},
		{name: "protobuf only", number: 32_768, wantDomain: "scip-proto-field"},
		{name: "protobuf max", number: 536_870_911, wantDomain: "scip-proto-field"},
		{name: "above all packs", number: 536_870_912},
	} {
		t.Run(test.name, func(t *testing.T) {
			packs := fieldReferencePacks(test.number)
			domains := make([]string, len(packs))
			for index, pack := range packs {
				domains[index] = pack.fieldReferenceDomain
				if pack.fieldReferencePredicate == "" {
					t.Fatalf("pack %q has no predicate", pack.protocol)
				}
			}
			if got := strings.Join(domains, ","); got != test.wantDomain {
				t.Fatalf(
					"field %d domains = %q, want %q",
					test.number,
					got,
					test.wantDomain,
				)
			}
		})
	}
}
