package api

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestThriftFieldRetainedWitnessRequired(t *testing.T) {
	for _, test := range []struct {
		name, domain, version, atomVersion string
		wantError                          bool
	}{
		{"current", "scip-thrift-field", "selected", "selected", false},
		{"absent witness", "", "", "", true},
		{"foreign domain", "scip-proto-field", "selected", "selected", true},
		{"foreign version", "scip-thrift-field", "old", "old", true},
		{"foreign citation", "scip-thrift-field", "selected", "old", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := ProofBundle{Assertions: []store.Assertion{{Predicate: "REFERENCES_THRIFT_FIELD", Repo: "repo", RunID: "run"}}, Evidence: []BundleEvidence{{Repository: "repo", RunID: "run", Atom: store.EvidenceAtom{ExtractorVersion: test.atomVersion}}}}
			if test.domain != "" {
				bundle.ExtractorVersions = []BundleExtractor{{Repository: "repo", Domain: test.domain, RunID: "run", Extractor: test.version}}
			}
			err := validateExpectedEvidenceVersions(Options{EvidenceExtractorVersions: map[string]string{"scip-thrift-field": "selected"}}, &bundle)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v wantError=%v", err, test.wantError)
			}
		})
	}
}
