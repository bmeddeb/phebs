package callerexecute

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// PublicationReconcileStore is the startup-only state boundary for complete
// caller publications. It deliberately excludes the lease-fenced publication
// writer: startup may clear an exact post-commit marker, but only a claimed
// caller job may promote manifest-before-store bytes.
type PublicationReconcileStore interface {
	store.CallerGenerationPublicationStore
	ListCallerPublicationRepositoriesPage(
		context.Context, string, int,
	) ([]string, error)
	CallerPublicationRepositoryEligible(context.Context, string) (bool, error)
	EnqueuePending(
		context.Context, store.JobKind, string, bool,
	) (*store.Job, error)
}

type publicationReconcileAdapter struct {
	state               PublicationReconcileStore
	extractors          []callerleaf.ExtractorIdentity
	pointers            []store.CallerGenerationPublicationSummary
	pointerByRepository map[string]store.CallerGenerationPublicationSummary
}

// ReconcilePublications validates the complete-publication trust boundary
// before caller workers or readers start.
func ReconcilePublications(
	ctx context.Context,
	dataDir string,
	state PublicationReconcileStore,
	registry *Registry,
	publications *callerpublication.Registry,
) (callerpublication.ReconcileReport, error) {
	if state == nil || registry == nil || publications == nil {
		return callerpublication.ReconcileReport{}, errors.New(
			"caller publication reconciliation is not configured",
		)
	}
	extractors := registry.ExtractorIdentities()
	storedPointers, err := state.ListCallerGenerationPublicationSummaries(ctx)
	if err != nil {
		return callerpublication.ReconcileReport{}, err
	}
	publicationRefs := 0
	canonicalBytes := int64(0)
	for _, pointer := range storedPointers {
		if pointer.PairCount > callerpublication.MaxInstallationPublicationRefs-1-
			publicationRefs || pointer.CanonicalBytes >
			callerpublication.MaxInstallationCanonicalBytes-canonicalBytes {
			return callerpublication.ReconcileReport{}, fmt.Errorf(
				"%w: caller publication installation exceeds startup budget",
				callerleaf.ErrLimit,
			)
		}
		publicationRefs += pointer.PairCount + 1
		canonicalBytes += pointer.CanonicalBytes
	}
	currentPointers := make(
		[]store.CallerGenerationPublicationSummary, 0, len(storedPointers),
	)
	pointerByRepository := make(
		map[string]store.CallerGenerationPublicationSummary, len(storedPointers),
	)
	var retired callerpublication.ReconcileReport
	for _, pointer := range storedPointers {
		if _, stateErr := callerPublicationStateFromStore(
			pointer, extractors,
		); stateErr == nil {
			currentPointers = append(currentPointers, pointer)
			pointerByRepository[pointer.Generation.Repository] = pointer
			continue
		}
		// A previous policy/extractor generation cannot be reconstructed with
		// this binary's frozen semantics. Queue before clearing store authority,
		// then remove only the package-owned repository directory while startup
		// still excludes workers and readers.
		if _, err := state.EnqueuePending(
			ctx, store.JobCallerLeaf, pointer.Generation.Repository, true,
		); err != nil {
			return callerpublication.ReconcileReport{}, err
		}
		retired.ReplacementsQueued++
		if err := state.ClearCallerGenerationPublication(
			ctx, pointer.Generation.Repository,
		); err != nil {
			return callerpublication.ReconcileReport{}, err
		}
		retired.PointersCleared++
		if err := callerleaf.RemoveRepository(
			ctx, Root(dataDir), pointer.Generation.Repository,
		); err != nil {
			return callerpublication.ReconcileReport{}, err
		}
	}
	report, err := callerpublication.Reconcile(
		ctx, Root(dataDir),
		&publicationReconcileAdapter{
			state: state, extractors: extractors, pointers: currentPointers,
			pointerByRepository: pointerByRepository,
		},
		callerpublication.Expected{Extractors: extractors},
		publications,
	)
	report.ReplacementsQueued += retired.ReplacementsQueued
	report.PointersCleared += retired.PointersCleared
	return report, err
}

func (adapter *publicationReconcileAdapter) ListCallerPublicationRepositoriesPage(
	ctx context.Context,
	after string,
	limit int,
) ([]string, error) {
	return adapter.state.ListCallerPublicationRepositoriesPage(ctx, after, limit)
}

func (adapter *publicationReconcileAdapter) CallerPublicationRepositoryEligible(
	ctx context.Context,
	repository string,
) (bool, error) {
	return adapter.state.CallerPublicationRepositoryEligible(ctx, repository)
}

func (adapter *publicationReconcileAdapter) ListCallerPublications(
	_ context.Context,
) ([]callerpublication.State, error) {
	pointers := adapter.pointers
	states := make([]callerpublication.State, len(pointers))
	for index, pointer := range pointers {
		state, err := callerPublicationStateFromStore(
			pointer, adapter.extractors,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"caller publication %d: %w", index, err,
			)
		}
		states[index] = state
	}
	return states, nil
}

func (adapter *publicationReconcileAdapter) CallerPublicationCurrent(
	ctx context.Context,
	wanted callerpublication.State,
) (bool, error) {
	pointer, ok := adapter.pointerByRepository[wanted.Generation.Repository]
	if !ok {
		return false, nil
	}
	observed, err := callerPublicationStateFromStore(
		pointer, adapter.extractors,
	)
	if err != nil || !reflect.DeepEqual(observed, wanted) {
		return false, err
	}
	current, err := adapter.state.CallerGenerationPublicationSummaryCurrent(
		ctx, pointer,
	)
	if errors.Is(err, store.ErrInvalidCallerGenerationPublication) {
		// Pair-payload corruption is deterministic invalid authority. Returning
		// false lets startup preserve the queue-before-clear repair ordering
		// instead of aborting before it can retire the malformed pointer.
		return false, nil
	}
	return current, err
}

func (adapter *publicationReconcileAdapter) ClearCallerPublication(
	ctx context.Context,
	repository string,
) error {
	return adapter.state.ClearCallerGenerationPublication(ctx, repository)
}

func (adapter *publicationReconcileAdapter) ForceCallerReplacement(
	ctx context.Context,
	repository string,
) error {
	_, err := adapter.state.EnqueuePending(
		ctx, store.JobCallerLeaf, repository, true,
	)
	return err
}

func callerPublicationStateFromStore(
	pointer store.CallerGenerationPublicationSummary,
	extractors []callerleaf.ExtractorIdentity,
) (callerpublication.State, error) {
	semantic, err := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository:               pointer.Generation.Repository,
		HeadCommit:               pointer.Generation.HeadCommit,
		UnitDigest:               pointer.Generation.UnitDigest,
		DeclarationSetDigest:     pointer.Generation.DeclarationSetDigest,
		CandidateManifestDigest:  pointer.Generation.CandidateManifestDigest,
		CandidatePolicyDigest:    pointer.Generation.CandidatePolicyDigest,
		ResolverGenerationDigest: pointer.Generation.ResolverGenerationDigest,
		ResolverManifestDigest:   pointer.Generation.ResolverManifestDigest,
		Extractors:               slices.Clone(extractors),
	})
	if err != nil || semantic.Digest != pointer.Generation.Digest ||
		semantic.CallerPolicyDigest != pointer.Generation.CallerPolicyDigest ||
		semantic.ExtractorSetDigest != pointer.Generation.ExtractorSetDigest ||
		semantic.SourceLanePolicy != pointer.Generation.SourceLanePolicy {
		return callerpublication.State{}, fmt.Errorf(
			"%w: store and runtime generation identities differ",
			store.ErrInvalidCallerGenerationPublication,
		)
	}
	state := callerpublication.State{
		Schema: callerpublication.StateSchema, Generation: semantic,
		PairSetDigest: pointer.PairSetDigest,
		Aggregate: callerleaf.AggregateReceipt{
			PairCount: pointer.PairCount, ArtifactCount: pointer.ArtifactCount,
			ResultCount:           pointer.ResultCount,
			AbstentionCount:       pointer.AbstentionCount,
			CoverageRecordCount:   pointer.CoverageRecordCount,
			CoveredCandidateCount: pointer.CoveredCandidateCount,
			CanonicalBytes:        pointer.CanonicalBytes,
			StagingBytes:          pointer.StagingBytes,
			PeakOpenFiles:         pointer.PeakOpenFiles,
		},
		ManifestDigest: pointer.ManifestDigest,
		Manifest:       pointer.ManifestPath,
	}
	if err := callerpublication.ValidateState(state); err != nil {
		return callerpublication.State{}, err
	}
	return state, nil
}

func callerPublicationSummary(
	pointer store.CallerGenerationPublication,
) store.CallerGenerationPublicationSummary {
	return store.CallerGenerationPublicationSummary{
		Generation:        pointer.Generation,
		PairPayloadDigest: pointer.PairPayloadDigest,
		PairSetDigest:     pointer.PairSetDigest,
		PairCount:         pointer.PairCount, ArtifactCount: pointer.ArtifactCount,
		ResultCount:           pointer.ResultCount,
		AbstentionCount:       pointer.AbstentionCount,
		CoverageRecordCount:   pointer.CoverageRecordCount,
		CoveredCandidateCount: pointer.CoveredCandidateCount,
		CanonicalBytes:        pointer.CanonicalBytes, StagingBytes: pointer.StagingBytes,
		PeakOpenFiles:  pointer.PeakOpenFiles,
		ManifestDigest: pointer.ManifestDigest, ManifestPath: pointer.ManifestPath,
		PublicationRevision:    pointer.PublicationRevision,
		PublicationIncarnation: pointer.PublicationIncarnation,
		WriterSchema:           pointer.WriterSchema, PublishedAt: pointer.PublishedAt,
	}
}
