package api

import (
	"fmt"
	"strings"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/store"
)

type workbenchRepositoryScope struct {
	posture           string
	unitDigest        string
	primary           []string
	supporting        []string
	typedIndexPosture string
	typedIndexKind    string
	typedIndexPath    string
}

func workbenchScopeForRepository(
	repository store.Repo,
) (workbenchRepositoryScope, error) {
	state := repository.IndexedAnalysisUnit
	if state == nil {
		return workbenchRepositoryScope{
			posture: analysisunit.SearchIndexWholeRepository,
		}, nil
	}
	if err := state.Validate(repository.Name); err != nil {
		return workbenchRepositoryScope{}, fmt.Errorf(
			"repository %q has invalid committed analysis-unit state: %w",
			repository.Name,
			err,
		)
	}
	if state.SearchIndexPosture == analysisunit.SearchIndexWholeRepository {
		return workbenchRepositoryScope{
			posture: analysisunit.SearchIndexWholeRepository,
		}, nil
	}
	if state.SearchIndexPosture != analysisunit.SearchIndexFocused {
		return workbenchRepositoryScope{}, fmt.Errorf(
			"repository %q has unknown analysis-unit posture %q",
			repository.Name,
			state.SearchIndexPosture,
		)
	}
	scope := workbenchRepositoryScope{
		posture:           analysisunit.SearchIndexFocused,
		unitDigest:        state.Digest,
		primary:           append([]string(nil), state.PrimaryPaths...),
		supporting:        append([]string(nil), state.SupportingPaths...),
		typedIndexPosture: state.TypedIndexPosture,
	}
	if state.TypedIndex != nil {
		scope.typedIndexKind = state.TypedIndex.Kind
		scope.typedIndexPath = state.TypedIndex.Path
	}
	return scope, nil
}

func (scope workbenchRepositoryScope) admits(filePath string) bool {
	if repopath.Validate(filePath) != nil {
		return false
	}
	if scope.posture != analysisunit.SearchIndexFocused {
		return true
	}
	for _, selected := range scope.primary {
		if workbenchScopePathContains(selected, filePath) {
			return true
		}
	}
	for _, selected := range scope.supporting {
		if workbenchScopePathContains(selected, filePath) {
			return true
		}
	}
	return false
}

func workbenchScopePathContains(selected, filePath string) bool {
	return selected == filePath ||
		strings.HasPrefix(filePath, selected+"/")
}

func (scope workbenchRepositoryScope) snapshot(
	name, commit string,
) store.WorkbenchRepositorySnapshot {
	return store.WorkbenchRepositorySnapshot{
		Name:         name,
		Commit:       commit,
		ScopePosture: scope.posture,
		UnitDigest:   scope.unitDigest,
	}
}

func (scope workbenchRepositoryScope) matchesSnapshot(
	snapshot store.WorkbenchRepositorySnapshot,
) bool {
	posture := snapshot.ScopePosture
	if posture == "" && snapshot.UnitDigest == "" {
		posture = analysisunit.SearchIndexWholeRepository
	}
	return posture == scope.posture &&
		snapshot.UnitDigest == scope.unitDigest
}
