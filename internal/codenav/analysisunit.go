package codenav

import (
	"fmt"

	"github.com/bmeddeb/phebs/internal/analysisunit"
)

// BindingFromAnalysisUnit projects committed repository state into the
// resolver contract. Callers still own the store lookup and authorization;
// this helper keeps the focused no-fallback posture identical at every wiring
// site.
func BindingFromAnalysisUnit(
	repository, revision string,
	state *analysisunit.State,
) (TypedIndexBinding, error) {
	if state == nil {
		return TypedIndexBinding{Commit: revision}, nil
	}
	if err := state.Validate(repository); err != nil {
		return TypedIndexBinding{}, fmt.Errorf(
			"validate committed analysis-unit state: %w",
			err,
		)
	}
	if state.SearchIndexPosture == analysisunit.SearchIndexWholeRepository {
		return TypedIndexBinding{Commit: revision}, nil
	}
	if state.SearchIndexPosture != analysisunit.SearchIndexFocused {
		return TypedIndexBinding{}, fmt.Errorf(
			"unsupported search-index posture %q: %w",
			state.SearchIndexPosture,
			ErrTypedIndexBinding,
		)
	}
	binding := TypedIndexBinding{
		Focused:         true,
		Commit:          revision,
		UnitDigest:      state.Digest,
		PrimaryPaths:    append([]string(nil), state.PrimaryPaths...),
		SupportingPaths: append([]string(nil), state.SupportingPaths...),
	}
	if state.TypedIndex == nil {
		return binding, nil
	}
	if state.TypedIndexPosture != analysisunit.TypedIndexUnitBound ||
		state.TypedIndex.Kind != analysisunit.TypedIndexKindSCIP {
		return TypedIndexBinding{}, fmt.Errorf(
			"unsupported focused typed-index posture: %w",
			ErrTypedIndexBinding,
		)
	}
	binding.Path = state.TypedIndex.Path
	return binding, nil
}
