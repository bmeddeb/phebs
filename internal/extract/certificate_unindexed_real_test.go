package extract_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestCoverageCertificateDiscoveredRepositoryRealStore(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	const repository = "evaluation.local/discovered"
	if err := s.UpsertRepo(ctx, store.Repo{Name: repository, CloneURL: "https://example.test/discovered.git"}); err != nil {
		t.Fatal(err)
	}
	visible, err := s.ListRepos(ctx)
	if err != nil || len(visible) != 1 || visible[0].IndexedCommitHash != "" {
		t.Fatalf("discovered repository=%+v err=%v", visible, err)
	}
	if _, err := s.LatestPublishedRun(ctx, store.ExtractionScope{Repository: repository, Domain: "scip-proto-field"}); err == nil || !strings.Contains(err.Error(), "commit is not valid bounded UTF-8") {
		t.Fatalf("strict store scope behavior changed: %v", err)
	}
	domains := []string{"scip-proto-field", "kafka-producer", "proto-contract"}
	before, err := extract.BuildCoverageCertificate(ctx, s, visible, domains)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range before.Repositories[0].Runs {
		if run.Status != "unpublished" || run.RunID != "" || run.Fresh || run.LatestAttempt != nil || run.Outcome != nil {
			t.Fatalf("discovery claimed extraction: %+v", run)
		}
	}
	if err := s.SetRepoIndexed(ctx, repository, commitA, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	visible, err = s.ListRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := extract.BuildCoverageCertificate(ctx, s, visible, domains)
	if err != nil || after.Digest == before.Digest || after.Repositories[0].IndexedCommit != commitA {
		t.Fatalf("indexing transition did not change source identity: certificate=%+v err=%v", after, err)
	}
}
