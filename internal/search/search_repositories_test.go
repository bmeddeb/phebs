package search

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

type repositoryObservationStore struct {
	resourceRepoStore
	lists int
	late  bool
}

func (s *repositoryObservationStore) ListRepos(ctx context.Context) ([]store.Repo, error) {
	s.lists++
	return s.resourceRepoStore.ListRepos(ctx)
}

func (s *repositoryObservationStore) GetRepo(ctx context.Context, name string) (*store.Repo, error) {
	repo, err := s.resourceRepoStore.GetRepo(ctx, name)
	if repo != nil && s.late {
		repo.IndexedCommitHash = "2222222222222222222222222222222222222222"
	}
	return repo, err
}

type repositoryObservationBackend struct {
	resultTestStreamer
	before func()
}

func (s *repositoryObservationBackend) Search(ctx context.Context, q query.Q, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	if s.before != nil {
		s.before()
	}
	return s.resultTestStreamer.Search(ctx, q, opts)
}

// This uses the actual ListRepos/compiler/filter/result fence with an explicit
// compatibility backend, not a real index, SDK, or selected server proof.
func TestSearchRepositoriesActualCompiledSet(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	for _, name := range []string{"two_visible_one_hit", "revision_filter", "zero", "canceled", "late_current_refusal"} {
		t.Run(name, func(t *testing.T) {
			st := &repositoryObservationStore{resourceRepoStore: resourceRepoStore{repos: []store.Repo{
				{Name: "visible-a", IndexedCommitHash: commit}, {Name: "visible-b", IndexedCommitHash: commit},
				{Name: "hidden", IndexedCommitHash: commit}, {Name: "deleting", IndexedCommitHash: commit, Deleting: true},
				{Name: "unindexed"},
			}}}
			raw := "T401Fixture"
			if name == "revision_filter" {
				raw += " rev:release"
				st.repos[0].IndexedRevisions = []store.IndexedRevision{
					{Selector: "HEAD", Branch: "HEAD", Commit: commit},
					{Selector: "release", Branch: "release", Commit: commit},
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, err = readaccounting.WithSearchRepositories(ctx)
			if err != nil {
				t.Fatal(err)
			}
			backend := &repositoryObservationBackend{resultTestStreamer: resultTestStreamer{result: resourceSearchResult("visible-a", commit, 0, 1)}}
			// The general accumulator fixture includes a non-repository metadata
			// key; actual completed legacy results must not contain that key.
			delete(backend.result.RepoURLs, "shared")
			delete(backend.result.LineFragments, "shared")
			if name == "canceled" {
				backend.before = cancel
			}
			if name == "late_current_refusal" {
				backend.before = func() { st.late = true }
			}
			s := &Searcher{st: st, z: backend, focused: newFocusedCache(t.TempDir()), indexDir: t.TempDir(),
				Visible: func(context.Context) func(store.Repo) bool {
					return func(repo store.Repo) bool { return name != "zero" && repo.Name != "hidden" }
				},
			}
			result, searchErr := s.Search(ctx, raw, Options{MaxMatches: 1})
			_, finishErr := ledger.Finish()
			if st.lists != 1 {
				t.Fatalf("ListRepos = %d, want existing one", st.lists)
			}
			if name == "canceled" || name == "late_current_refusal" {
				if searchErr == nil || finishErr == nil || ledger.SearchRepositories() != nil {
					t.Fatalf("failed search certified set: %v / %v", searchErr, finishErr)
				}
				return
			}
			if searchErr != nil || finishErr != nil {
				t.Fatalf("search = %v / %v", searchErr, finishErr)
			}
			want := uint64(2)
			if name == "zero" {
				want = 0
			}
			if name == "revision_filter" {
				want = 1
			}
			if got := ledger.SearchRepositories(); got == nil || *got != want {
				t.Fatalf("observed %v, want %d", got, want)
			}
			if name != "zero" && len(result.Files) != 1 {
				t.Fatalf("displayed files = %d", len(result.Files))
			}
		})
	}
}
