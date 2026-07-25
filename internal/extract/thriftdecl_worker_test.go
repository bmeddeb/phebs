package extract

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/store"
)

// Regression for T19.2: the real worker publishes a thrift-contract run whose
// manifest reconciles declaration facts, per-file implicit-ID gaps, and the
// candidate inventory (non-.thrift files are outside the candidate
// population). Mirrors the grpcgo worker regression.
func TestThriftDeclPublishesThroughWorker(t *testing.T) {
	files := map[string]string{
		"idl/registry.thrift": `namespace go acme.registry
struct Tag { 1: required string key }
service Registry {
  Tag get(1: string key)
  oneway void ping(1: i32 nonce)
}
`,
		"idl/legacy.thrift": `struct Old {
  string unnumbered
}
`,
		"main.go": "package main\n",
	}
	repo := &store.Repo{Name: "example.com/idl", IndexedCommitHash: unitCommit}
	evidence := newMemoryEvidence()
	worker := Worker{
		Repos: readyRepoGetter(repo), Evidence: evidence,
		NewCorpus:  grpcWorkerCorpusFactory{files: files},
		Extractors: []Extractor{thriftdecl.New()},
	}
	if err := worker.Handle(context.Background(), store.Job{Target: repo.Name}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !evidence.published || evidence.aborted {
		t.Fatalf("run state: published=%v aborted=%v", evidence.published, evidence.aborted)
	}
	coverage := evidence.publishedWith
	// registry.thrift: service + 2 operations + get args/result synthetics
	// (2 messages, 2 fields: key arg + success) + ping args synthetic
	// (1 message, 1 field) + Tag struct (1 message, 1 field) = 11 facts.
	// legacy.thrift: 1 implicit-ID gap.
	if coverage.UnresolvedCount != 1 || coverage.AssertionCount != 12 ||
		coverage.CandidateFileCount != 2 || coverage.ReadFileCount != 2 {
		t.Fatalf("coverage = %+v", coverage)
	}
	var gaps, operations int
	for _, batch := range evidence.batches {
		for _, assertion := range batch.asserts {
			switch assertion.Predicate {
			case "THRIFT_EXTRACTION_GAP":
				gaps++
				if assertion.Subject != "idl/legacy.thrift" ||
					assertion.Object != "implicit_field_id" ||
					assertion.Tier != store.TierUnresolved {
					t.Fatalf("gap assertion = %+v", assertion)
				}
			case "DECLARES_OPERATION":
				operations++
				if assertion.Tier != store.TierExact || assertion.Lineage == "" {
					t.Fatalf("operation assertion = %+v", assertion)
				}
			}
		}
	}
	if gaps != 1 || operations != 2 {
		t.Fatalf("gaps = %d, operations = %d", gaps, operations)
	}
}
