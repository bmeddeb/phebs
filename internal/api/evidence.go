package api

import (
	"context"
	"errors"
	"reflect"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/store"
)

const evidenceViewAssertionLimit = 5000

// EvidenceRepositoryView is one caller-visible repository's latest published
// run for the requested domain. Assertions are complete for that run or the
// whole request fails; this endpoint never returns a truncated partial view.
type EvidenceRepositoryView struct {
	Repository     string                  `json:"repository"`
	Run            store.ExtractionRun     `json:"run"`
	Assertions     []store.Assertion       `json:"assertions"`
	AssertionCount int                     `json:"assertion_count"`
	CandidateScope *EvidenceCandidateScope `json:"candidate_scope,omitempty"`
}

// EvidenceCandidateScope is the additive projection that distinguishes a
// legacy run with no candidate publication from a candidate-bound run whose
// exact scope happens to contain zero files or bytes.
type EvidenceCandidateScope struct {
	ScopePosture         string              `json:"scope_posture"`
	ManifestDigest       string              `json:"manifest_digest"`
	Plane                string              `json:"plane"`
	CorpusFileCount      int                 `json:"corpus_file_count"`
	CorpusDeclaredBytes  int64               `json:"corpus_declared_bytes"`
	CorpusDigest         string              `json:"corpus_digest"`
	PlannedFileCount     int                 `json:"planned_file_count"`
	PlannedRequiredCount int                 `json:"planned_required_file_count"`
	PlannedDeclaredBytes int64               `json:"planned_declared_bytes"`
	PlannedDigest        string              `json:"planned_digest"`
	TypedInput           *EvidenceTypedInput `json:"typed_input,omitempty"`
}

// EvidenceTypedInput preserves applicability independently of presence:
// Present=false with a Kind is an explicit, measured absence.
type EvidenceTypedInput struct {
	Kind          string `json:"kind"`
	Path          string `json:"path,omitempty"`
	ObjectID      string `json:"object_id,omitempty"`
	DeclaredBytes int64  `json:"declared_bytes"`
	Digest        string `json:"digest,omitempty"`
	Present       bool   `json:"present"`
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
		publicationSnapshot := make(
			map[string]*store.ExtractionRun,
			len(visible),
		)
		for _, repo := range visible {
			scope := currentEvidenceScope(repo, in.Domain)
			run, err := opts.Evidence.LatestPublishedRun(ctx, scope)
			if errors.Is(err, store.ErrNotFound) {
				publicationSnapshot[repo.Name] = nil
				continue
			}
			if err != nil {
				return nil, huma.Error500InternalServerError("read latest published evidence run", err)
			}
			if run == nil || run.ID == "" ||
				run.Repo != scope.Repository ||
				run.Commit != scope.Commit ||
				run.UnitDigest != scope.UnitDigest ||
				run.Domain != scope.Domain ||
				run.Status != "published" {
				return nil, huma.Error500InternalServerError("latest published evidence run is inconsistent")
			}
			snapshot := copyEvidenceRun(*run)
			publicationSnapshot[repo.Name] = &snapshot

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
				CandidateScope: evidenceCandidateScope(run.Coverage),
			})
			view.PublishedRunCount++
			view.AssertionCount += len(assertionCopies)
		}
		if err := confirmEvidencePublicationSnapshot(
			ctx,
			opts.Evidence,
			visible,
			in.Domain,
			publicationSnapshot,
		); err != nil {
			return nil, err
		}
		confirmedVisible, err := visibleRepositories(ctx, opts)
		if err != nil {
			return nil, err
		}
		if !sameVisibleEvidenceScopes(visible, confirmedVisible) {
			return nil, huma.Error409Conflict(
				"repository authorization or indexed scope changed while building the evidence response; retry",
			)
		}
		return &evidenceOut{Body: view}, nil
	})
	registerProofAPI(api, opts)
}

func evidenceCandidateScope(
	coverage store.CoverageManifest,
) *EvidenceCandidateScope {
	if coverage.CandidateManifestDigest == "" {
		return nil
	}
	scope := &EvidenceCandidateScope{
		ScopePosture:         coverage.ScopePosture,
		ManifestDigest:       coverage.CandidateManifestDigest,
		Plane:                coverage.CandidatePlane,
		CorpusFileCount:      coverage.ScopeCorpusFileCount,
		CorpusDeclaredBytes:  coverage.ScopeCorpusDeclaredBytes,
		CorpusDigest:         coverage.ScopeCorpusDigest,
		PlannedFileCount:     coverage.PlannedFileCount,
		PlannedRequiredCount: coverage.PlannedRequiredFileCount,
		PlannedDeclaredBytes: coverage.PlannedDeclaredBytes,
		PlannedDigest:        coverage.PlannedScopeDigest,
	}
	if coverage.TypedInputKind != "" {
		scope.TypedInput = &EvidenceTypedInput{
			Kind:          coverage.TypedInputKind,
			Path:          coverage.TypedInputPath,
			ObjectID:      coverage.TypedInputObjectID,
			DeclaredBytes: coverage.TypedInputDeclaredBytes,
			Digest:        coverage.TypedInputDigest,
			Present:       coverage.TypedInputPresent,
		}
	}
	return scope
}

func currentEvidenceScope(repo store.Repo, domain string) store.ExtractionScope {
	scope := store.ExtractionScope{
		Repository: repo.Name,
		Commit:     repo.IndexedCommitHash,
		Domain:     domain,
	}
	if repo.IndexedAnalysisUnit != nil {
		scope.UnitDigest = repo.IndexedAnalysisUnit.Digest
	}
	return scope
}

// sameVisibleEvidenceScopes is the final response fence shared by raw
// evidence and newly built proof bundles. Repository names bind the
// authorization result; the complete committed state additionally catches a
// same-HEAD typed-index designation change, which is deliberately excluded
// from the semantic unit digest.
func sameVisibleEvidenceScopes(left, right []store.Repo) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name ||
			left[index].IndexedCommitHash != right[index].IndexedCommitHash ||
			left[index].EvidenceRevision != right[index].EvidenceRevision ||
			!analysisunit.EqualState(
				left[index].IndexedAnalysisUnit,
				right[index].IndexedAnalysisUnit,
			) {
			return false
		}
	}
	return true
}

func cloneVisibleEvidenceScopes(repos []store.Repo) []store.Repo {
	cloned := append([]store.Repo(nil), repos...)
	for index := range cloned {
		cloned[index].IndexedAnalysisUnit = analysisunit.CloneState(
			cloned[index].IndexedAnalysisUnit,
		)
	}
	return cloned
}

func confirmEvidencePublicationSnapshot(
	ctx context.Context,
	evidence store.EvidenceStore,
	visible []store.Repo,
	domain string,
	snapshot map[string]*store.ExtractionRun,
) error {
	for _, repo := range visible {
		initial, ok := snapshot[repo.Name]
		if !ok {
			return huma.Error409Conflict(
				"evidence publication set changed while building the response; retry",
			)
		}
		current, err := evidence.LatestPublishedRun(
			ctx,
			currentEvidenceScope(repo, domain),
		)
		if errors.Is(err, store.ErrNotFound) {
			if initial == nil {
				continue
			}
			return huma.Error409Conflict(
				"evidence publication changed while building the response; retry",
			)
		}
		if err != nil {
			return huma.Error500InternalServerError(
				"confirm latest published evidence run",
				err,
			)
		}
		if initial == nil || !sameEvidenceRunReceipt(initial, current) {
			return huma.Error409Conflict(
				"evidence publication changed while building the response; retry",
			)
		}
	}
	return nil
}

func sameEvidenceRunReceipt(left, right *store.ExtractionRun) bool {
	if left == nil || right == nil ||
		left.ID != right.ID ||
		left.Repo != right.Repo ||
		left.Commit != right.Commit ||
		left.UnitDigest != right.UnitDigest ||
		left.Domain != right.Domain ||
		left.Extractor != right.Extractor ||
		left.Status != right.Status ||
		!reflect.DeepEqual(left.Coverage, right.Coverage) {
		return false
	}
	switch {
	case left.PublishedAt == nil || right.PublishedAt == nil:
		return left.PublishedAt == nil && right.PublishedAt == nil
	default:
		return left.PublishedAt.Equal(*right.PublishedAt)
	}
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
	run = copyEvidenceRun(run)
	sort.Strings(run.Coverage.Protocols)
	sort.Strings(run.Coverage.Failures)
	return run
}

func copyEvidenceRun(run store.ExtractionRun) store.ExtractionRun {
	run.Coverage.Protocols = append([]string(nil), run.Coverage.Protocols...)
	run.Coverage.Failures = append([]string(nil), run.Coverage.Failures...)
	run.Coverage.GitlinkSamplePaths = append(
		[]string(nil),
		run.Coverage.GitlinkSamplePaths...,
	)
	if run.PublishedAt != nil {
		publishedAt := *run.PublishedAt
		run.PublishedAt = &publishedAt
	}
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
