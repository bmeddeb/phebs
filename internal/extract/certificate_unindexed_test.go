package extract

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestCoverageCertificateUnindexedRepository(t *testing.T) {
	for _, domains := range [][]string{{"scip-proto-field"}, {"kafka-consumer", "kafka-producer"}, {"proto-contract", "grpc-caller"}} {
		t.Run(strings.Join(domains, "+"), func(t *testing.T) {
			poison := errors.New("unindexed scope must not reach storage")
			source := &certificateRunSource{runErrors: map[string]error{}}
			for _, domain := range domains {
				source.runErrors[certKey("discovered", domain)] = poison
			}
			visible := []store.Repo{{Name: "discovered"}}
			first, err := BuildCoverageCertificate(t.Context(), source, visible, domains)
			if err != nil {
				t.Fatal(err)
			}
			second, err := BuildCoverageCertificate(t.Context(), source, visible, domains)
			if err != nil || !reflect.DeepEqual(first, second) || len(source.queried) != 0 {
				t.Fatalf("certificate=%+v err=%v reads=%v", second, err, source.queried)
			}
			if first.RepositoryCount != 1 || len(first.Repositories) != 1 || first.Digest == "" || first.ValidateCanonicalShape() != nil {
				t.Fatalf("certificate=%+v", first)
			}
			repository := first.Repositories[0]
			if repository.IndexedCommit != "" || repository.SCIPIndex != "unknown" || len(repository.Runs) != len(domains) {
				t.Fatalf("repository=%+v", repository)
			}
			for index, run := range repository.Runs {
				if !reflect.DeepEqual(run, CertificateRun{Domain: first.Domains[index], Status: "unpublished"}) {
					t.Fatalf("unindexed repository claimed evidence: %+v", run)
				}
			}
		})
	}
}

func TestCoverageCertificateUnindexedStillValidatesScope(t *testing.T) {
	source := &certificateRunSource{}
	_, err := BuildCoverageCertificate(t.Context(), source, []store.Repo{{Name: "discovered", IndexedAnalysisUnit: &analysisunit.State{}}}, []string{"scip-proto-field"})
	if err == nil || !strings.Contains(err.Error(), "invalid analysis-unit state") || len(source.queried) != 0 {
		t.Fatalf("invalid committed scope was hidden: err=%v reads=%v", err, source.queried)
	}
}

func TestCoverageCertificateUnindexedDoesNotHideIndexedPublication(t *testing.T) {
	const domain = "scip-proto-field"
	source := &certificateRunSource{
		runs:      map[string]store.ExtractionRun{certKey("indexed", domain): certRun("indexed", domain, commitA, store.CoverageManifest{Protocols: []string{"scip"}})},
		runErrors: map[string]error{certKey("discovered", domain): errors.New("unindexed scope must not reach storage")},
	}
	indexed := store.Repo{Name: "indexed", IndexedCommitHash: commitA}
	before, err := BuildCoverageCertificate(t.Context(), source, []store.Repo{indexed}, []string{domain})
	if err != nil {
		t.Fatal(err)
	}
	source.queried = nil
	after, err := BuildCoverageCertificate(t.Context(), source, []store.Repo{{Name: "discovered"}, indexed}, []string{domain})
	if err != nil || !reflect.DeepEqual(before.Repositories[0], after.Repositories[1]) || before.Digest == after.Digest || len(source.queried) != 3 {
		t.Fatalf("indexed publication changed: before=%+v after=%+v err=%v reads=%v", before, after, err, source.queried)
	}
}

func TestCoverageCertificateNonemptyCommitErrorsRemainErrors(t *testing.T) {
	// These use the real store validator. Invalid scope is refused before any
	// database access, so no opened store or child is necessary.
	for _, commit := range []string{" ", string([]byte{0xff}), strings.Repeat("x", (64<<10)+1)} {
		certificate, err := BuildCoverageCertificate(t.Context(), &store.Surreal{}, []store.Repo{{Name: "discovered", IndexedCommitHash: commit}}, []string{"scip-proto-field"})
		if err == nil || certificate != nil || !strings.Contains(err.Error(), "commit is not valid bounded UTF-8") {
			t.Fatalf("malformed nonempty commit accepted: certificate=%+v err=%v", certificate, err)
		}
	}
	failure := errors.New("storage unavailable")
	source := &certificateRunSource{runErrors: map[string]error{certKey("discovered", "scip-proto-field"): failure}}
	certificate, err := BuildCoverageCertificate(t.Context(), source, []store.Repo{{Name: "discovered", IndexedCommitHash: commitA}}, []string{"scip-proto-field"})
	if !errors.Is(err, failure) || certificate != nil || len(source.queried) != 1 {
		t.Fatalf("storage failure hidden: certificate=%+v err=%v reads=%v", certificate, err, source.queried)
	}
}
