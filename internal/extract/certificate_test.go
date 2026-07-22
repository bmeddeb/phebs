package extract

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

type certificateRunSource struct {
	runs    map[string]store.ExtractionRun
	queried []string
}

func certKey(repo, domain string) string { return repo + "\x00" + domain }

func (f *certificateRunSource) LatestPublishedRun(_ context.Context, repo, domain string) (*store.ExtractionRun, error) {
	f.queried = append(f.queried, certKey(repo, domain))
	run, ok := f.runs[certKey(repo, domain)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copied := run
	return &copied, nil
}

func certRun(repo, domain, commit string, coverage store.CoverageManifest) store.ExtractionRun {
	published := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	return store.ExtractionRun{
		ID: "run-" + repo + "-" + domain, Repo: repo, Commit: commit,
		Domain: domain, Extractor: domain + "@1.0.0", Status: "published",
		PublishedAt: &published, Coverage: coverage,
	}
}

var (
	commitA = strings.Repeat("a", 40)
	commitB = strings.Repeat("b", 40)
)

func TestCoverageCertificateDeterministicOverVisibleUniverse(t *testing.T) {
	source := &certificateRunSource{runs: map[string]store.ExtractionRun{
		certKey("alpha", "proto-contract"): certRun("alpha", "proto-contract", commitA, store.CoverageManifest{
			Protocols: []string{"protobuf"}, Failures: []string{"bad.proto: parse"},
			UnresolvedCount: 2, AssertionCount: 5, AtomCount: 7,
		}),
		certKey("alpha", "scip-proto-field"): certRun("alpha", "scip-proto-field", commitA, store.CoverageManifest{
			Protocols: []string{"scip", "protobuf-generated-accessor-v1"}, AssertionCount: 1, AtomCount: 1,
		}),
		certKey("gamma", "scip-proto-field"): certRun("gamma", "scip-proto-field", commitB, store.CoverageManifest{
			Protocols: []string{"scip-index-absent"},
		}),
	}}
	visible := []store.Repo{
		{Name: "gamma", IndexedCommitHash: commitB},
		{Name: "alpha", IndexedCommitHash: commitA},
		{Name: "beta", IndexedCommitHash: commitB},
	}
	domains := []string{"scip-proto-field", "proto-contract"}
	first, err := BuildCoverageCertificate(context.Background(), source, visible, domains)
	if err != nil {
		t.Fatalf("BuildCoverageCertificate: %v", err)
	}
	second, err := BuildCoverageCertificate(context.Background(), source, visible, domains)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two builds over equal state differ:\n%+v\n%+v", first, second)
	}
	if first.SchemaVersion != "coverage-certificate-v1" || !strings.HasPrefix(first.Digest, "sha256:") {
		t.Fatalf("schema/digest = %q %q", first.SchemaVersion, first.Digest)
	}
	if got, want := first.Domains, []string{"proto-contract", "scip-proto-field"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("domains = %v, want sorted %v", got, want)
	}
	if first.RepositoryCount != 3 || len(first.Repositories) != 3 {
		t.Fatalf("repository count = %d/%d, want the whole visible universe", first.RepositoryCount, len(first.Repositories))
	}
	rows := []struct {
		index     string
		scipIndex string
		statuses  []string
	}{
		{index: "alpha", scipIndex: "present", statuses: []string{"published", "published"}},
		{index: "beta", scipIndex: "unknown", statuses: []string{"unpublished", "unpublished"}},
		{index: "gamma", scipIndex: "absent", statuses: []string{"unpublished", "published"}},
	}
	for i, row := range rows {
		repo := first.Repositories[i]
		if repo.Repository != row.index || repo.SCIPIndex != row.scipIndex {
			t.Fatalf("repository[%d] = %q scip=%q, want %q scip=%q", i, repo.Repository, repo.SCIPIndex, row.index, row.scipIndex)
		}
		for j, status := range row.statuses {
			if repo.Runs[j].Status != status {
				t.Fatalf("%s run[%d] status = %q, want %q", row.index, j, repo.Runs[j].Status, status)
			}
		}
	}
	alpha := first.Repositories[0].Runs[0]
	if alpha.Domain != "proto-contract" || !alpha.Fresh || alpha.UnresolvedCount != 2 ||
		!reflect.DeepEqual(alpha.Failures, []string{"bad.proto: parse"}) {
		t.Fatalf("alpha proto-contract run = %+v", alpha)
	}
}

// AC (T13.3): the certificate provably changes when one repository's
// extraction fails. A failed extraction never publishes, so failure surfaces
// as an absent run, a stale run at the superseded commit, or recorded partial
// failures — each must move the digest.
func TestCoverageCertificateChangesWhenExtractionFails(t *testing.T) {
	visible := []store.Repo{{Name: "alpha", IndexedCommitHash: commitB}}
	domains := []string{"scip-proto-field"}
	healthy := map[string]store.ExtractionRun{
		certKey("alpha", "scip-proto-field"): certRun("alpha", "scip-proto-field", commitB, store.CoverageManifest{
			Protocols: []string{"scip"}, AssertionCount: 3, AtomCount: 3,
		}),
	}
	baseline, err := BuildCoverageCertificate(context.Background(), &certificateRunSource{runs: healthy}, visible, domains)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	rows := []struct {
		name  string
		runs  map[string]store.ExtractionRun
		check func(t *testing.T, run CertificateRun)
	}{
		{
			name: "failed run never published",
			runs: nil,
			check: func(t *testing.T, run CertificateRun) {
				if run.Status != "unpublished" {
					t.Fatalf("status = %q, want unpublished", run.Status)
				}
			},
		},
		{
			name: "failed replacement leaves stale run",
			runs: map[string]store.ExtractionRun{
				certKey("alpha", "scip-proto-field"): certRun("alpha", "scip-proto-field", commitA, store.CoverageManifest{
					Protocols: []string{"scip"}, AssertionCount: 3, AtomCount: 3,
				}),
			},
			check: func(t *testing.T, run CertificateRun) {
				if run.Fresh || run.Commit != commitA {
					t.Fatalf("run = %+v, want stale at superseded commit", run)
				}
			},
		},
		{
			name: "partial failures recorded in published coverage",
			runs: map[string]store.ExtractionRun{
				certKey("alpha", "scip-proto-field"): certRun("alpha", "scip-proto-field", commitB, store.CoverageManifest{
					Protocols: []string{"scip"}, Failures: []string{"consumer/use.go: not UTF-8"},
					AssertionCount: 3, AtomCount: 3,
				}),
			},
			check: func(t *testing.T, run CertificateRun) {
				if len(run.Failures) != 1 {
					t.Fatalf("failures = %v, want the recorded failure", run.Failures)
				}
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			failed, err := BuildCoverageCertificate(context.Background(), &certificateRunSource{runs: row.runs}, visible, domains)
			if err != nil {
				t.Fatalf("BuildCoverageCertificate: %v", err)
			}
			if failed.Digest == baseline.Digest {
				t.Fatal("failure did not change the certificate digest")
			}
			row.check(t, failed.Repositories[0].Runs[0])
		})
	}
}

// AC (T13.3, adversarial): an invisible repository must not leak through
// names or counts. Whatever happens to hidden state, the visible caller's
// certificate stays byte-identical, never names the hidden repository, and
// the builder never even queries it.
func TestCoverageCertificateNoInvisibleRepoLeakage(t *testing.T) {
	visible := []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}
	domains := []string{"scip-proto-field", "proto-contract"}
	alphaRun := certRun("alpha", "scip-proto-field", commitA, store.CoverageManifest{Protocols: []string{"scip"}})

	rows := []struct {
		name string
		runs map[string]store.ExtractionRun
	}{
		{name: "hidden repo absent", runs: map[string]store.ExtractionRun{
			certKey("alpha", "scip-proto-field"): alphaRun,
		}},
		{name: "hidden repo published", runs: map[string]store.ExtractionRun{
			certKey("alpha", "scip-proto-field"): alphaRun,
			certKey("omega-secret", "scip-proto-field"): certRun("omega-secret", "scip-proto-field", commitB, store.CoverageManifest{
				Protocols: []string{"scip"}, AssertionCount: 999_999, AtomCount: 999_999,
			}),
		}},
		{name: "hidden repo failing with distinctive failures", runs: map[string]store.ExtractionRun{
			certKey("alpha", "scip-proto-field"): alphaRun,
			certKey("omega-secret", "proto-contract"): certRun("omega-secret", "proto-contract", commitB, store.CoverageManifest{
				Failures: []string{"omega-secret/topsecret.proto: parse"}, UnresolvedCount: 12345,
			}),
		}},
	}
	var serialized []string
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			source := &certificateRunSource{runs: row.runs}
			certificate, err := BuildCoverageCertificate(context.Background(), source, visible, domains)
			if err != nil {
				t.Fatalf("BuildCoverageCertificate: %v", err)
			}
			data, err := json.Marshal(certificate)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(data), "omega") {
				t.Fatalf("certificate names an invisible repository: %s", data)
			}
			if certificate.RepositoryCount != 1 {
				t.Fatalf("repository count = %d, want the visible universe only", certificate.RepositoryCount)
			}
			for _, key := range source.queried {
				if !strings.HasPrefix(key, "alpha\x00") {
					t.Fatalf("builder queried an invisible repository: %q", key)
				}
			}
			serialized = append(serialized, string(data))
		})
	}
	for i := 1; i < len(serialized); i++ {
		if serialized[i] != serialized[0] {
			t.Fatalf("hidden state changed the visible certificate:\n%s\n%s", serialized[0], serialized[i])
		}
	}
}

func TestCoverageCertificateRejectsInconsistentInput(t *testing.T) {
	run := certRun("alpha", "d", commitA, store.CoverageManifest{})
	rows := []struct {
		name    string
		visible []store.Repo
		domains []string
		runs    map[string]store.ExtractionRun
	}{
		{name: "no domains", visible: []store.Repo{{Name: "alpha"}}, domains: nil},
		{name: "empty domain", visible: []store.Repo{{Name: "alpha"}}, domains: []string{""}},
		{name: "duplicate domain", visible: []store.Repo{{Name: "alpha"}}, domains: []string{"d", "d"}},
		{name: "empty repo name", visible: []store.Repo{{Name: ""}}, domains: []string{"d"}},
		{name: "duplicate repo", visible: []store.Repo{{Name: "alpha"}, {Name: "alpha"}}, domains: []string{"d"}},
		{name: "deleting repo", visible: []store.Repo{{Name: "alpha", Deleting: true}}, domains: []string{"d"}},
		{
			name: "run repo mismatch", visible: []store.Repo{{Name: "alpha"}}, domains: []string{"d"},
			runs: map[string]store.ExtractionRun{certKey("alpha", "d"): func() store.ExtractionRun {
				bad := run
				bad.Repo = "other"
				return bad
			}()},
		},
		{
			name: "unpublished run status", visible: []store.Repo{{Name: "alpha"}}, domains: []string{"d"},
			runs: map[string]store.ExtractionRun{certKey("alpha", "d"): func() store.ExtractionRun {
				bad := run
				bad.Status = "staged"
				return bad
			}()},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			source := &certificateRunSource{runs: row.runs}
			if _, err := BuildCoverageCertificate(context.Background(), source, row.visible, row.domains); err == nil {
				t.Fatal("inconsistent input built a certificate")
			}
		})
	}
}
