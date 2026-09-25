package search

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

// ErrWholeWarmUnavailable means the selected repository cannot be warmed
// through the whole-repository cache: no runtime selector, a focused posture,
// a publication in progress, or a searcher without store authority.
var ErrWholeWarmUnavailable = errors.New("whole-repository search warm is unavailable")

// WholeWarmObservation is the cache state a warm left behind. SharedValidated
// means all-code search will use the validated startup binding; otherwise
// SharedExactDigest names the exact reader all-code search will reuse.
type WholeWarmObservation struct {
	Repository           string
	SharedValidated      bool
	SharedExactDigest    string
	SelectedDirectory    string
	SelectedSearchDigest string
	SelectedRevisions    []store.IndexedRevision
}

// WarmSelectedWholeRepository completes, under the caller's deadline, the
// cache-owned whole-repository work that a first all-code search and a first
// selected service search would otherwise start inside their per-query wall
// time: shared startup-binding validation (or its exact-reader fallback) and
// the selected generation's exact reader. It resolves the selected generation
// exactly as selected service search does. The warm adds bounded selector,
// repository and control reads; the queries still perform their own authority
// checks. It reuses the existing cache path and adds no cache kind or query.
func (s *Searcher) WarmSelectedWholeRepository(
	ctx context.Context, repository string,
) (WholeWarmObservation, error) {
	if s == nil || s.st == nil || s.whole == nil || ctx == nil || repository == "" {
		return WholeWarmObservation{}, ErrWholeWarmUnavailable
	}
	runtimeStore, ok := s.st.(servicequery.RuntimeSelectorStore)
	if !ok {
		return WholeWarmObservation{}, ErrWholeWarmUnavailable
	}
	selector, err := runtimeStore.GetServiceRuntimeSelector(ctx, repository)
	if err != nil {
		return WholeWarmObservation{}, fmt.Errorf("search warm: runtime selector: %w", err)
	}
	if selector.Repository != repository || selector.SearchGenerationDigest == "" {
		return WholeWarmObservation{}, fmt.Errorf(
			"%w: runtime selector does not name a search generation", ErrWholeWarmUnavailable,
		)
	}
	controls, err := focusedindex.ReadSearchGenerationControls(
		ctx, s.indexDir, repository, selector.SearchGenerationDigest,
	)
	if err != nil {
		return WholeWarmObservation{}, fmt.Errorf("search warm: selected search generation: %w", err)
	}
	repo, err := s.st.GetRepo(ctx, repository)
	if err != nil {
		return WholeWarmObservation{}, fmt.Errorf("search warm: repository: %w", err)
	}
	if repo == nil || repo.Deleting || repo.IndexedCommitHash == "" {
		return WholeWarmObservation{}, fmt.Errorf("%w: repository is not indexed", ErrWholeWarmUnavailable)
	}
	if repo.IndexedAnalysisUnit != nil &&
		repo.IndexedAnalysisUnit.SearchIndexPosture == analysisunit.SearchIndexFocused {
		// Same posture test as compile: focused repositories use the focused
		// cache, not these whole-repository readers.
		return WholeWarmObservation{}, fmt.Errorf("%w: repository has a focused posture", ErrWholeWarmUnavailable)
	}
	return s.warmWholeGeneration(
		ctx, repository, canonicalWholeRevisions(*repo),
		controls.Directory, controls.Search.Digest, controls.Search.Revisions,
	)
}

// warmWholeGeneration performs the two cache acquisitions in the order a
// corridor needs them. Both fills are cache-owned and bounded by
// WholeGenerationWarmingTimeout; the caller's deadline only bounds the wait.
func (s *Searcher) warmWholeGeneration(
	ctx context.Context,
	repository string,
	sharedRevisions []store.IndexedRevision,
	directory, digest string,
	selectedRevisions []store.IndexedRevision,
) (WholeWarmObservation, error) {
	observation := WholeWarmObservation{Repository: repository}
	if directory == "" || digest == "" || len(sharedRevisions) == 0 || len(selectedRevisions) == 0 {
		return observation, fmt.Errorf("%w: incomplete generation binding", ErrWholeWarmUnavailable)
	}
	if focusedindex.IsPublishing(s.indexDir, repository) {
		return observation, fmt.Errorf("%w: publication is in progress", ErrWholeWarmUnavailable)
	}
	shared, err := s.whole.acquireIfStale(ctx, s.z, repository, sharedRevisions)
	if err != nil {
		return observation, fmt.Errorf("search warm: all-code reader: %w", err)
	}
	if shared == nil {
		observation.SharedValidated = true
	} else {
		observation.SharedExactDigest = shared.entry.searchDigest
		shared.release()
	}
	selected, err := s.whole.acquireSelected(ctx, repository, directory, digest, selectedRevisions)
	if err != nil {
		return observation, fmt.Errorf("search warm: selected reader: %w", err)
	}
	if selected == nil {
		return observation, fmt.Errorf("%w: selected reader was pruned", ErrWholeWarmUnavailable)
	}
	defer selected.release()
	if !selected.matchesSearchDigest(digest) {
		selected.invalidate()
		return observation, fmt.Errorf("%w: selected reader lacks the exact generation", ErrWholeWarmUnavailable)
	}
	observation.SelectedDirectory = directory
	observation.SelectedSearchDigest = digest
	observation.SelectedRevisions = slices.Clone(selectedRevisions)
	return observation, nil
}
