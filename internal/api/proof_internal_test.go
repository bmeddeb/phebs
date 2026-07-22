package api

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
)

func TestCollectProofEvidenceRequiresExactlyOneFilteredDomain(t *testing.T) {
	t.Parallel()
	for _, domains := range [][]string{nil, {"one", "two"}} {
		certificate := &extract.CoverageCertificate{Domains: domains}
		_, _, err := collectProofEvidence(context.Background(), nil, certificate, assertionFilter{Predicate: "CALLS_OPERATION"})
		if err == nil || !strings.Contains(err.Error(), "exactly one coverage domain") {
			t.Fatalf("domains %v: %v", domains, err)
		}
	}
}
