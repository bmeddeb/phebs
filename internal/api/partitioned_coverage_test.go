package api

import (
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
)

// PHEBS-37: a domain served from its current partitioned root counts as
// current publication for freshness and impact state, and its run is pinned
// by the proof bundles that cite it. A refused root stays visibly degraded.
func TestPartitionedRunsAreCurrentCoverage(t *testing.T) {
	served := extract.CertificateRun{
		Domain: "scip-thrift-field", Status: "partitioned", RunID: "run-served",
		Commit: "c", Fresh: true, Disposition: "success",
	}
	refused := served
	refused.RunID, refused.Disposition = "run-refused", "terminal_refusal"
	refused.Failures = []string{"partition_terminal_refusal"}
	unpublished := extract.CertificateRun{Domain: "scip-proto-field", Status: "unpublished"}

	bundle := func(runs ...extract.CertificateRun) ProofBundle {
		return ProofBundle{Coverage: extract.CoverageCertificate{
			Repositories: []extract.CertificateRepository{{Repository: "r", Runs: runs}},
		}}
	}
	if got := proofFreshness(bundle(served)); got != "current" {
		t.Fatalf("partitioned freshness = %q, want current", got)
	}
	if got := proofFreshness(bundle(refused)); got != "stale" {
		t.Fatalf("refused partitioned freshness = %q, want stale", got)
	}
	if got := proofFreshness(bundle(served, unpublished)); got != "unavailable" {
		t.Fatalf("freshness with an unpublished domain = %q, want unavailable", got)
	}

	repository := extract.CertificateRepository{Repository: "r", SCIPIndex: "present"}
	if state, _ := impactCoverageState(repository, served); state != "covered" {
		t.Fatalf("partitioned impact state = %q, want covered", state)
	}
	if state, _ := impactCoverageState(repository, refused); state != "failed" {
		t.Fatalf("refused partitioned impact state = %q, want failed", state)
	}

	certificate := bundle(served, refused, unpublished).Coverage
	repositories, runIDs := certificateScopes(&certificate)
	if !reflect.DeepEqual(repositories, []string{"r"}) ||
		!reflect.DeepEqual(runIDs, []string{"run-refused", "run-served"}) {
		t.Fatalf("bundle scopes = %v %v, want both partitioned runs pinned", repositories, runIDs)
	}
}
