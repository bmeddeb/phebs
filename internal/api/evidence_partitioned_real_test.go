package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/store"
)

// Keep the real native page, then move its authority before API confirmation.
type movingRawEvidenceStore struct {
	*store.Surreal
	afterPage func()
}

func (s *movingRawEvidenceStore) ListPartitionedAssertions(ctx context.Context, q store.AssertionQuery, a store.PartitionedAssertionAuthority) ([]store.Assertion, error) {
	rows, err := s.Surreal.ListPartitionedAssertions(ctx, q, a)
	if err == nil && q.Limit == 5000 && s.afterPage != nil {
		move := s.afterPage
		s.afterPage = nil
		move()
	}
	return rows, err
}

func TestEvidencePartitionedRealRoot(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	database, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()
	st := &movingRawEvidenceStore{Surreal: database}
	scope := store.ExtractionScope{Repository: "synthetic.invalid/raw-evidence", Commit: strings.Repeat("a", 40), Domain: "proto-contract"}
	if err := st.UpsertRepo(ctx, store.Repo{Name: scope.Repository, CloneURL: "https://example.invalid/raw-evidence.git"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, scope.Repository, scope.Commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	digest := func(n int) string { return fmt.Sprintf("sha256:%064x", n) }
	candidate := store.CandidateManifestPublication{Repository: scope.Repository, HeadCommit: scope.Commit, PolicyDigest: digest(1), ManifestDigest: digest(2), GenerationDigest: digest(3), ManifestPath: candidateid.ManifestName(scope.Repository)}
	if err := st.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	publish := func(ordinal int) store.PartitionedExtractionDomain {
		run, err := st.BeginPartitionedExtractionRun(ctx, scope, "raw-view-test-v1", digest(ordinal*10+1), candidate.ManifestDigest, "", store.PartitionedExtractionRunLimits{Facts: 2, Rows: 4, References: 2})
		if err != nil {
			t.Fatal(err)
		}
		for i := range 2 {
			atom := store.EvidenceAtom{SchemaVersion: "raw-view-test-v1", BlobDigest: digest(i + 100), StartByte: i * 8, EndByte: i*8 + 4, RuleID: "raw-view-rule-v1", ExtractorVersion: "raw-view-test-v1", AdapterConfigDigest: digest(0), FactFingerprint: fmt.Sprintf("fact-%d", i)}
			atom.ID = store.ComputeAtomID(atom)
			path := fmt.Sprintf("proto/service_%d.proto", i)
			occurrence := store.SnapshotEvidence{AtomID: atom.ID, Repo: scope.Repository, Commit: scope.Commit, Path: path, StartLine: i + 1, EndLine: i + 1, VisibilityScope: "repo:" + scope.Repository}
			assertion := store.Assertion{Predicate: "DECLARES_OPERATION", Subject: path, Object: fmt.Sprintf("demo.Service/Call%d", i), Tier: store.TierExact, Repo: scope.Repository, Supporting: []string{atom.ID}}
			if err := st.AddEvidenceChunk(ctx, run.ID, digest(i+200), 1, []store.EvidenceAtom{atom}, []store.SnapshotEvidence{occurrence}, []store.Assertion{assertion}); err != nil {
				t.Fatal(err)
			}
		}
		root := store.PartitionedExtractionDomain{Schema: store.PartitionedExtractionDomainSchema, Repository: scope.Repository, Domain: scope.Domain, RunID: run.ID, PlanDigest: digest(ordinal*10 + 1), RootDigest: digest(ordinal*10 + 2), CandidateDigest: candidate.ManifestDigest, SourceDigest: digest(ordinal*10 + 3), ObservationDigest: digest(ordinal*10 + 4), Facts: 2, Rows: 4, References: 2, Plan: `{}`, Root: `{}`}
		if err := st.PublishPartitionedExtractionDomain(ctx, root); err != nil {
			t.Fatal(err)
		}
		return root
	}
	first := publish(1)
	read := func(visible func(context.Context) func(store.Repo) bool) *httptest.ResponseRecorder {
		handler := api.New(api.Options{Version: "test", Store: st, Evidence: st, Visible: visible})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
		return response
	}
	response := read(nil)
	if response.Code != http.StatusOK {
		t.Fatalf("real root=%d %s", response.Code, response.Body)
	}
	var view api.EvidenceView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.PartitionedRootCount != 1 || view.PublishedRunCount != 0 || view.AssertionCount != 2 || len(view.Repositories) != 1 || view.Repositories[0].Run != nil || view.Repositories[0].ExactAuthority == nil || view.Repositories[0].ExactAuthority.RunID != first.RunID {
		t.Fatalf("real view=%+v", view)
	}
	if _, err := st.LatestPublishedRun(ctx, scope); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("native root became legacy publication: %v", err)
	}
	authority := store.PartitionedAssertionAuthority{Repository: scope.Repository, Domain: scope.Domain, RunID: first.RunID, PlanDigest: first.PlanDigest, RootDigest: first.RootDigest, Commit: scope.Commit, CandidateManifestDigest: candidate.ManifestDigest, CandidatePolicyDigest: candidate.PolicyDigest}
	for _, assertion := range view.Repositories[0].Assertions {
		for _, atom := range assertion.Supporting {
			located, err := st.ResolvePartitionedEvidence(ctx, authority, atom)
			if err != nil || len(located.Occurrences) != 1 || located.Occurrences[0].RunID != first.RunID || located.Occurrences[0].Repo != scope.Repository || located.Occurrences[0].Commit != scope.Commit {
				t.Fatalf("native locator=%+v %v", located, err)
			}
			if _, err := st.ResolveEvidence(ctx, scope.Repository, first.RunID, atom); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("legacy locator served staged evidence: %v", err)
			}
		}
	}
	denied := read(func(context.Context) func(store.Repo) bool { return func(store.Repo) bool { return false } })
	if denied.Code != http.StatusOK || strings.Contains(denied.Body.String(), first.RunID) || strings.Contains(denied.Body.String(), scope.Repository) {
		t.Fatalf("denied source=%d %s", denied.Code, denied.Body)
	}
	second := publish(2)
	if err := st.PublishPartitionedExtractionDomain(ctx, first); err != nil {
		t.Fatal(err)
	}
	st.afterPage = func() {
		if err := st.PublishPartitionedExtractionDomain(ctx, second); err != nil {
			t.Fatal(err)
		}
	}
	moved := read(nil)
	if moved.Code != http.StatusConflict || strings.Contains(moved.Body.String(), first.RunID) || strings.Contains(moved.Body.String(), "demo.Service") {
		t.Fatalf("real moved root=%d %s", moved.Code, moved.Body)
	}
	candidate.ManifestDigest, candidate.PolicyDigest, candidate.GenerationDigest = digest(500), digest(501), digest(502)
	if err := st.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	stale := read(nil)
	if stale.Code != http.StatusOK || strings.Contains(stale.Body.String(), second.RunID) || strings.Contains(stale.Body.String(), "demo.Service") {
		t.Fatalf("real stale candidate=%d %s", stale.Code, stale.Body)
	}
}
