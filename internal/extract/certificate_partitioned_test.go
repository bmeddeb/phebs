package extract

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

// partitionedCertificateSource adds the optional current-root reads to the
// legacy fake, as *store.Surreal does in production.
type partitionedCertificateSource struct {
	*certificateRunSource
	roots     map[string]store.PartitionedExtractionDomainReference
	manifests map[string]store.CandidateManifestPublication
	// fenced names runs the store fence refuses (quarantined, unsealed, or
	// of another extractor version); probes records each fenced probe.
	fenced map[string]bool
	probes []store.PartitionedAssertionAuthority
}

func (source *partitionedCertificateSource) ListPartitionedAssertions(
	_ context.Context, query store.AssertionQuery, authority store.PartitionedAssertionAuthority,
) ([]store.Assertion, error) {
	source.probes = append(source.probes, authority)
	if query.Limit != 1 || query.RunID != authority.RunID || query.Repo != authority.Repository {
		return nil, store.ErrConflict
	}
	if source.fenced[authority.RunID] || authority.ExpectedExtractor == "refused-version" {
		return nil, store.ErrNotFound
	}
	return nil, nil
}

func (source *partitionedCertificateSource) GetPartitionedExtractionDomainReference(
	_ context.Context, repository, domain string,
) (store.PartitionedExtractionDomainReference, error) {
	root, ok := source.roots[certKey(repository, domain)]
	if !ok {
		return store.PartitionedExtractionDomainReference{}, store.ErrNotFound
	}
	return root, nil
}

func (source *partitionedCertificateSource) GetCandidateManifestPublication(
	_ context.Context, repository string,
) (*store.CandidateManifestPublication, error) {
	manifest, ok := source.manifests[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &manifest, nil
}

// PHEBS-37: a domain served from its current partitioned root is reported as
// partitioned, fresh and pinnable, never as unpublished; a refused or
// undisclosed root carries a failure; a root of another candidate is not
// current; a legacy published run still wins.
func TestCoverageCertificateReportsCurrentPartitionedRoots(t *testing.T) {
	digest := func(fill string) string { return "sha256:" + strings.Repeat(fill, 64) }
	root := func(repository, domain, runID, disposition string) store.PartitionedExtractionDomainReference {
		return store.PartitionedExtractionDomainReference{
			Schema: store.PartitionedExtractionDomainSchema, Repository: repository, Domain: domain,
			RunID: runID, PlanDigest: digest("1"), RootDigest: digest("2"), CandidateDigest: digest("3"),
			SourceDigest: digest("4"), ObservationDigest: digest("5"), Disposition: disposition,
		}
	}
	source := &partitionedCertificateSource{
		certificateRunSource: &certificateRunSource{runs: map[string]store.ExtractionRun{
			certKey("legacy", "scip-thrift-field"): certRun("legacy", "scip-thrift-field", commitA, store.CoverageManifest{
				Protocols: []string{"scip"}, AssertionCount: 1, AtomCount: 1,
			}),
		}},
		roots: map[string]store.PartitionedExtractionDomainReference{
			certKey("served", "scip-thrift-field"):  root("served", "scip-thrift-field", "run-served", "success"),
			certKey("served", "scip-proto-field"):   root("served", "scip-proto-field", "run-empty", "empty"),
			certKey("refused", "scip-thrift-field"): root("refused", "scip-thrift-field", "run-refused", "terminal_refusal"),
			certKey("refused", "scip-proto-field"):  root("refused", "scip-proto-field", "run-old", ""),
			certKey("moved", "scip-thrift-field"):   root("moved", "scip-thrift-field", "run-moved", "success"),
			certKey("legacy", "scip-thrift-field"):  root("legacy", "scip-thrift-field", "run-shadow", "success"),
		},
		manifests: map[string]store.CandidateManifestPublication{
			"served":  {Repository: "served", HeadCommit: commitA, ManifestDigest: digest("3")},
			"refused": {Repository: "refused", HeadCommit: commitA, ManifestDigest: digest("3")},
			// A newer head: the root belongs to an older candidate generation.
			"moved":  {Repository: "moved", HeadCommit: commitB, ManifestDigest: digest("3")},
			"legacy": {Repository: "legacy", HeadCommit: commitA, ManifestDigest: digest("3")},
		},
	}
	visible := []store.Repo{
		{Name: "served", IndexedCommitHash: commitA},
		{Name: "refused", IndexedCommitHash: commitA},
		{Name: "moved", IndexedCommitHash: commitA},
		{Name: "legacy", IndexedCommitHash: commitA},
	}
	certificate, err := BuildCoverageCertificate(
		context.Background(), source, visible, []string{"scip-thrift-field", "scip-proto-field"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := certificate.ValidateCanonicalShape(); err != nil {
		t.Fatal(err)
	}
	runs := make(map[string]CertificateRun)
	for _, repository := range certificate.Repositories {
		for _, run := range repository.Runs {
			runs[certKey(repository.Repository, run.Domain)] = run
		}
	}
	served := runs[certKey("served", "scip-thrift-field")]
	if served.Status != "partitioned" || !served.Served() || !served.Fresh || served.RunID != "run-served" ||
		served.Commit != commitA || served.PlanDigest != digest("1") || served.RootDigest != digest("2") ||
		served.Disposition != "success" || len(served.Failures) != 0 {
		t.Fatalf("served partitioned run = %+v", served)
	}
	if empty := runs[certKey("served", "scip-proto-field")]; empty.Status != "partitioned" || len(empty.Failures) != 0 {
		t.Fatalf("empty partitioned run = %+v", empty)
	}
	if refused := runs[certKey("refused", "scip-thrift-field")]; refused.Status != "partitioned" ||
		len(refused.Failures) != 1 || refused.Failures[0] != "partition_terminal_refusal" {
		t.Fatalf("refused partitioned run = %+v", refused)
	}
	if unknown := runs[certKey("refused", "scip-proto-field")]; unknown.Status != "partitioned" ||
		len(unknown.Failures) != 1 || unknown.Failures[0] != "partition_disposition_unknown" {
		t.Fatalf("undisclosed partitioned run = %+v", unknown)
	}
	if moved := runs[certKey("moved", "scip-thrift-field")]; moved.Status != "unpublished" || moved.Served() {
		t.Fatalf("a root of another candidate generation was reported current: %+v", moved)
	}
	if legacy := runs[certKey("legacy", "scip-thrift-field")]; legacy.Status != "published" || legacy.RunID == "run-shadow" {
		t.Fatalf("the legacy published run must win: %+v", legacy)
	}
	if quarantined := runs[certKey("legacy", "scip-proto-field")]; quarantined.Status != "unpublished" || quarantined.Served() {
		t.Fatalf("a root the store fence refuses was reported current: %+v", quarantined)
	}

	// A version-fenced reader: the probe carries the admitted version, and a
	// root of another version stays unpublished, as its exact reader sees it.
	source.probes = nil
	versioned, err := BuildCoverageCertificateForVersions(
		context.Background(), source, visible[:1], []string{"scip-thrift-field", "scip-proto-field"},
		map[string]string{"scip-thrift-field": "admitted-version", "scip-proto-field": "refused-version"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.probes) != 2 {
		t.Fatalf("versioned probes = %+v", source.probes)
	}
	for _, run := range versioned.Repositories[0].Runs {
		switch run.Domain {
		case "scip-thrift-field":
			if run.Status != "partitioned" || run.Extractor != "admitted-version" {
				t.Fatalf("admitted version run = %+v", run)
			}
		case "scip-proto-field":
			if run.Status != "unpublished" || run.RunID != "" || run.Fresh {
				t.Fatalf("unadmitted version run = %+v", run)
			}
		}
	}

	// A source without the optional reads keeps the legacy certificate.
	plain, err := BuildCoverageCertificate(
		context.Background(), source.certificateRunSource, visible[:1], []string{"scip-thrift-field"},
	)
	if err != nil || plain.Repositories[0].Runs[0].Status != "unpublished" {
		t.Fatalf("legacy-only source = %+v, %v", plain, err)
	}
}
