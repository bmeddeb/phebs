package api

import (
	"context"
	"errors"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
)

const evidenceViewAssertionLimit = 5000

// EvidenceRepositoryView is one caller-visible repository's latest published
// run for the requested domain. Assertions are complete for that run or the
// whole request fails; this endpoint never returns a truncated partial view.
type EvidenceRepositoryView struct {
	Repository     string              `json:"repository"`
	Run            store.ExtractionRun `json:"run"`
	Assertions     []store.Assertion   `json:"assertions"`
	AssertionCount int                 `json:"assertion_count"`
}

// EvidenceView is computed only after repository permission and deleting
// filters have run. Counts therefore describe the caller's visible universe,
// never the underlying global evidence population.
type EvidenceView struct {
	Domain                 string                   `json:"domain"`
	VisibleRepositoryCount int                      `json:"visible_repository_count"`
	PublishedRunCount      int                      `json:"published_run_count"`
	AssertionCount         int                      `json:"assertion_count"`
	Repositories           []EvidenceRepositoryView `json:"repositories"`
}

func registerEvidence(api huma.API, opts Options) {
	if opts.Evidence == nil {
		return
	}

	type evidenceIn struct {
		Domain string `query:"domain" required:"true" minLength:"1" maxLength:"512" example:"proto-contract"`
	}
	type evidenceOut struct {
		Body EvidenceView
	}

	huma.Get(api, "/api/evidence", func(ctx context.Context, in *evidenceIn) (*evidenceOut, error) {
		visible, err := visibleRepositories(ctx, opts)
		if err != nil {
			return nil, err
		}

		view := EvidenceView{
			Domain:                 in.Domain,
			VisibleRepositoryCount: len(visible),
			Repositories:           make([]EvidenceRepositoryView, 0, len(visible)),
		}
		for _, repo := range visible {
			run, err := opts.Evidence.LatestPublishedRun(ctx, repo.Name, in.Domain)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, huma.Error500InternalServerError("read latest published evidence run", err)
			}
			if run == nil || run.ID == "" || run.Repo != repo.Name || run.Domain != in.Domain || run.Status != "published" {
				return nil, huma.Error500InternalServerError("latest published evidence run is inconsistent")
			}

			assertions, err := opts.Evidence.ListAssertions(ctx, store.AssertionQuery{
				Repo: repo.Name, RunID: run.ID, Limit: evidenceViewAssertionLimit,
			})
			if err != nil {
				return nil, huma.Error500InternalServerError("read published evidence assertions", err)
			}
			if len(assertions) != run.Coverage.AssertionCount {
				return nil, huma.Error500InternalServerError("published evidence coverage is inconsistent")
			}
			for _, assertion := range assertions {
				if assertion.Repo != repo.Name || assertion.RunID != run.ID {
					return nil, huma.Error500InternalServerError("published evidence assertion scope is inconsistent")
				}
			}

			runCopy := deterministicRun(*run)
			assertionCopies := deterministicAssertions(assertions)
			view.Repositories = append(view.Repositories, EvidenceRepositoryView{
				Repository: repo.Name, Run: runCopy, Assertions: assertionCopies,
				AssertionCount: len(assertionCopies),
			})
			view.PublishedRunCount++
			view.AssertionCount += len(assertionCopies)
		}
		return &evidenceOut{Body: view}, nil
	})
	registerProofAPI(api, opts)
	registerContractCatalogAPI(api, opts)
}

// visibleRepositories is the authorization boundary for every evidence and
// proof response. No evidence lookup, count, or bundle construction may occur
// before this function has filtered the global repository list.
func visibleRepositories(ctx context.Context, opts Options) ([]store.Repo, error) {
	allow := repoFilter(ctx, opts)
	repos, err := opts.Store.ListRepos(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("list evidence repositories", err)
	}
	visible := make([]store.Repo, 0, len(repos))
	for _, repo := range repos {
		if repo.Deleting || allow != nil && !allow(repo) {
			continue
		}
		visible = append(visible, repo)
	}
	sort.Slice(visible, func(i, j int) bool { return visible[i].Name < visible[j].Name })
	for i, repo := range visible {
		if repo.Name == "" || i > 0 && visible[i-1].Name == repo.Name {
			return nil, huma.Error500InternalServerError("repository visibility set is inconsistent")
		}
	}
	return visible, nil
}

func deterministicRun(run store.ExtractionRun) store.ExtractionRun {
	run.Coverage.Protocols = append([]string(nil), run.Coverage.Protocols...)
	run.Coverage.Failures = append([]string(nil), run.Coverage.Failures...)
	sort.Strings(run.Coverage.Protocols)
	sort.Strings(run.Coverage.Failures)
	return run
}

func deterministicAssertions(assertions []store.Assertion) []store.Assertion {
	result := make([]store.Assertion, len(assertions))
	for i, assertion := range assertions {
		assertion.Supporting = append([]string(nil), assertion.Supporting...)
		assertion.Contradicting = append([]string(nil), assertion.Contradicting...)
		sort.Strings(assertion.Supporting)
		sort.Strings(assertion.Contradicting)
		result[i] = assertion
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Predicate != b.Predicate {
			return a.Predicate < b.Predicate
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.Object < b.Object
	})
	return result
}
