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
