package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

var (
	errServiceRuntimePending               = errors.New("service runtime transition is pending")
	errServiceRuntimeContinuation          = errors.New("service runtime continuation is durable")
	errServiceRuntimeExtractionUnavailable = errors.New(
		"selected service runtime requires provisional protobuf or Thrift extraction",
	)
)

type serviceRuntimeProcessPins struct {
	selector       store.ServiceRuntimeSelector
	search         *focusedindex.SearchGenerationLease
	relationship   *relationshippublication.Lease
	relationshipV3 *relationshippublication.LeaseV3
}

func (pins *serviceRuntimeProcessPins) release() {
	if pins == nil {
		return
	}
	pins.search.Release()
	pins.relationship.Release()
	pins.relationshipV3.Release()
}

type serviceRuntimeController struct {
	// ponytail: one global lock is enough for at most 4,096 rare selector
	// transitions; split by repository only if transition throughput matters.
	mu sync.Mutex

	dataDir             string
	store               *store.Surreal
	selections          map[string]config.ServiceCatalog
	v3Catalog           *servicecatalogingest.V3Reconciler
	relationship        *relationshippublication.Runtime
	acquire             func(context.Context) (func(), error)
	searchPins          *focusedindex.SearchGenerationPins
	relationshipCache   *relationshippublication.Cache
	relationshipV3Cache *relationshippublication.CacheV3
	pins                map[string]*serviceRuntimeProcessPins
	uncertainPins       map[string]*serviceRuntimeProcessPins
	// afterActivationTransitionCommit is installed only by the prospective
	// exact-control path. It owns any reporting failure because the state chunk
	// is already durable when this callback runs.
	afterActivationTransitionCommit func(context.Context, store.GenerationChunk)
}

func newServiceRuntimeController(
	dataDir string,
	st *store.Surreal,
	selections map[string]config.ServiceCatalog,
	v3Catalog *servicecatalogingest.V3Reconciler,
	relationship *relationshippublication.Runtime,
	acquire func(context.Context) (func(), error),
	searchPins *focusedindex.SearchGenerationPins,
	relationshipCache *relationshippublication.Cache,
	relationshipV3Cache *relationshippublication.CacheV3,
) *serviceRuntimeController {
	controller := &serviceRuntimeController{
		dataDir: dataDir, store: st, selections: selections,
		v3Catalog: v3Catalog, relationship: relationship, acquire: acquire,
		searchPins: searchPins, relationshipCache: relationshipCache,
		relationshipV3Cache: relationshipV3Cache,
		pins:                make(map[string]*serviceRuntimeProcessPins),
		uncertainPins:       make(map[string]*serviceRuntimeProcessPins),
	}
	return controller
}

// lockTransition preserves the global lock order used by orphan cleanup:
// shared filesystem mutation exclusion first, then controller serialization.
// Every path that can reach selectLocked enters here.
func (controller *serviceRuntimeController) lockTransition(
	ctx context.Context,
) (func(), error) {
	releaseMutation, err := controller.acquire(ctx)
	if err != nil {
		return nil, err
	}
	controller.mu.Lock()
	return func() {
		controller.mu.Unlock()
		releaseMutation()
	}, nil
}

func (controller *serviceRuntimeController) Advance(
	ctx context.Context,
	repository string,
) error {
	if controller == nil {
		return nil
	}
	if _, configured := controller.selections[repository]; !configured {
		return nil
	}
	release, err := controller.lockTransition(ctx)
	if err != nil {
		return err
	}
	defer release()
	err = controller.advanceLocked(ctx, repository)
	if errors.Is(err, errServiceRuntimePending) {
		return nil
	}
	return err
}

func (controller *serviceRuntimeController) advanceLocked(
	ctx context.Context,
	repository string,
) error {
	selection, configured := controller.selections[repository]
	if !configured {
		return nil
	}
	var err error
	if selection.RuntimeVersion() == config.ServiceCatalogRuntimeV3 {
		err = controller.advanceV3Locked(ctx, repository)
	} else {
		_, currentErr := controller.currentSelector(ctx, repository)
		explicit := !errors.Is(currentErr, store.ErrNotFound)
		err = advanceV2WithHolding(
			explicit,
			func() error { return controller.advanceV2Locked(ctx, repository, explicit) },
			func() error {
				selector, selectorErr := controller.currentSelector(ctx, repository)
				if selectorErr != nil {
					return selectorErr
				}
				_, prepareErr := controller.prepareV3HoldingLocked(
					ctx, repository, selector,
				)
				return prepareErr
			},
		)
	}
	return err
}

// ProcessServiceStateV3Chunk keeps state mutation inside the selector and
// backup fence. The store preserves the selected v3 preimage before mutation.
func (controller *serviceRuntimeController) ProcessServiceStateV3Chunk(
	ctx context.Context,
	chunk store.GenerationChunk,
) (store.ServiceStateV3ChunkResult, error) {
	if controller == nil {
		return store.ServiceStateV3ChunkResult{}, errors.New("service runtime controller is unavailable")
	}
	release, err := controller.lockTransition(ctx)
	if err != nil {
		return store.ServiceStateV3ChunkResult{}, err
	}
	defer release()
	result, err := controller.store.ProcessServiceStateV3Chunk(ctx, chunk)
	if err != nil {
		return result, err
	}
	if !result.Settled {
		controller.reportActivationTransitionCommit(ctx, chunk, result)
		return result, nil
	}
	err = controller.advanceLocked(ctx, chunk.Repository)
	if errors.Is(err, errServiceRuntimeContinuation) {
		err = nil
	} else if errors.Is(err, errServiceRuntimePending) {
		err = store.WithDeferral(err)
	}
	return result, err
}

func (controller *serviceRuntimeController) reportActivationTransitionCommit(
	ctx context.Context,
	chunk store.GenerationChunk,
	result store.ServiceStateV3ChunkResult,
) {
	if controller.afterActivationTransitionCommit == nil ||
		chunk.Stage != store.ServiceStateV3ActivateStage ||
		chunk.Offset != store.ServiceStateV3ActivationTransitionTargetOffset ||
		chunk.Attempt != 0 || result.Read != store.MaxServiceStateV3ChunkRows ||
		result.Applied != 1 {
		return
	}
	controller.afterActivationTransitionCommit(ctx, chunk)
}

func advanceV2WithHolding(
	explicit bool,
	advanceV2 func() error,
	prepareV3 func() error,
) error {
	if err := advanceV2(); err != nil || !explicit {
		return err
	}
	return prepareV3()
}

// withV2Mutation moves an explicit v2 reader to the last complete v3
// authority before the mutable v2 catalog or state rows can change. An absent
// selector is the compatibility-mode v2 authority and has nothing to fence.
func (controller *serviceRuntimeController) withV2Mutation(
	ctx context.Context,
	repository string,
	publication servicecatalog.Publication,
	mutate func() error,
) error {
	if controller == nil {
		return mutate()
	}
	release, err := controller.lockTransition(ctx)
	if err != nil {
		return err
	}
	defer release()
	selector, err := controller.currentSelector(ctx, repository)
	if errors.Is(err, store.ErrNotFound) ||
		err == nil && selector.Backend == store.ServiceRuntimeV3 {
		return mutate()
	}
	if err != nil {
		return err
	}
	summary, err := controller.store.GetServiceStateSummary(ctx, repository)
	if err == nil &&
		summary.CatalogGeneration == publication.GenerationDigest &&
		summary.CatalogControlRevision == publication.ControlRevision {
		return mutate()
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := controller.holdSelectedV2Locked(ctx, repository, selector); err != nil {
		return err
	}
	return mutate()
}

func (controller *serviceRuntimeController) holdSelectedV2Locked(
	ctx context.Context,
	repository string,
	selector *store.ServiceRuntimeSelector,
) error {
	target, err := controller.prepareV3HoldingLocked(
		ctx, repository, selector,
	)
	if err != nil {
		return pendingServiceRuntime(err)
	}
	return controller.selectLocked(
		ctx, repository, store.ServiceRuntimeV3, selector, target,
	)
}

func (controller *serviceRuntimeController) reconcileV2SearchLocked(
	ctx context.Context,
	repository string,
) (string, error) {
	selector, err := controller.currentSelector(ctx, repository)
	if errors.Is(err, store.ErrNotFound) ||
		err == nil && selector.Backend == store.ServiceRuntimeV3 {
		outcome, reconcileErr := reconcileServiceSearchGeneration(
			ctx, controller.store, controller.dataDir, repository,
		)
		return outcome.Search.Digest, reconcileErr
	}
	if err != nil {
		return "", err
	}
	root, err := focusedindex.ReadSearchGenerationRoot(
		filepath.Join(controller.dataDir, "index"), repository,
	)
	if err != nil {
		return "", pendingServiceRuntime(err)
	}
	if root.Current.GenerationDigest == selector.SearchGenerationDigest {
		catalog, catalogErr := controller.store.GetServiceCatalog(ctx, repository)
		if catalogErr != nil {
			return "", catalogErr
		}
		if catalog.GenerationDigest == selector.CatalogGenerationDigest &&
			catalog.ControlRevision == selector.CatalogControlRevision {
			source, sourceErr := servicecatalog.SourceGenerationDigest(*catalog)
			if sourceErr != nil {
				return "", sourceErr
			}
			needed, neededErr := controller.store.ServiceGenerationActivationNeeded(
				ctx, repository, catalog.GenerationDigest, source,
				selector.SearchGenerationDigest,
			)
			if neededErr != nil {
				return "", neededErr
			}
			if !needed {
				return selector.SearchGenerationDigest, nil
			}
		}
	}
	if err := controller.holdSelectedV2Locked(ctx, repository, selector); err != nil {
		return "", err
	}
	outcome, err := reconcileServiceSearchGeneration(
		ctx, controller.store, controller.dataDir, repository,
	)
	return outcome.Search.Digest, err
}

func (controller *serviceRuntimeController) advanceV2Locked(
	ctx context.Context,
	repository string,
	explicit bool,
) error {
	search, err := controller.reconcileV2SearchLocked(ctx, repository)
	if err != nil {
		return pendingServiceRuntime(err)
	}
	if search == "" || controller.relationship == nil {
		return errServiceRuntimePending
	}
	if err := controller.relationship.Reconcile(ctx, repository); err != nil {
		return pendingServiceRuntime(err)
	}
	target, err := controller.v2Target(ctx, repository, search)
	if err != nil {
		return pendingServiceRuntime(err)
	}
	current, err := controller.currentSelector(ctx, repository)
	if errors.Is(err, store.ErrNotFound) && !explicit {
		return nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if explicit && (current == nil || current.Backend != store.ServiceRuntimeV2 ||
		!sameServiceRuntimeTarget(serviceRuntimeTarget(*current), target)) {
		if _, err := controller.v3HoldingGeneration(
			ctx, repository, target.CatalogGenerationDigest,
		); err != nil {
			return fmt.Errorf("admit v2 holding target: %w", err)
		}
	}
	return controller.selectLocked(ctx, repository, store.ServiceRuntimeV2, current, target)
}

func (controller *serviceRuntimeController) advanceV3Locked(
	ctx context.Context,
	repository string,
) error {
	target, err := controller.prepareV3Locked(ctx, repository)
	if err != nil {
		return err
	}
	current, err := controller.currentSelector(ctx, repository)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return controller.selectLocked(ctx, repository, store.ServiceRuntimeV3, current, target)
}

// prepareV3Locked builds the configured v3 candidate before selecting it.
// Explicit v2 uses prepareV3HoldingLocked so mutable configuration cannot
// replace the crash-recovery source.
func (controller *serviceRuntimeController) prepareV3Locked(
	ctx context.Context,
	repository string,
) (store.ServiceRuntimeTarget, error) {
	if controller.v3Catalog == nil || controller.relationship == nil {
		return store.ServiceRuntimeTarget{}, errServiceRuntimePending
	}
	if _, err := controller.v3Catalog.ReconcileRepository(ctx, repository); err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	search, err := controller.currentV3SearchGeneration(ctx, repository)
	if err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	return controller.completeV3Locked(ctx, repository, search)
}

// prepareV3HoldingLocked derives the fallback from the immutable v2
// generation named by the selector. It therefore repairs the post-CAS crash
// window even when the repository or configured catalog already moved on.
func (controller *serviceRuntimeController) prepareV3HoldingLocked(
	ctx context.Context,
	repository string,
	selector *store.ServiceRuntimeSelector,
) (store.ServiceRuntimeTarget, error) {
	if controller.relationship == nil || selector == nil ||
		selector.Repository != repository ||
		selector.Backend != store.ServiceRuntimeV2 {
		return store.ServiceRuntimeTarget{}, errServiceRuntimePending
	}
	generation, err := controller.v3HoldingGeneration(
		ctx, repository, selector.CatalogGenerationDigest,
	)
	if err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	if err := controller.store.PublishServiceCatalogV3Holding(
		ctx, *selector, generation,
	); err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	return controller.completeV3Locked(
		ctx, repository, selector.SearchGenerationDigest,
	)
}

func (controller *serviceRuntimeController) v3HoldingGeneration(
	ctx context.Context,
	repository, catalogGeneration string,
) (servicecatalogv3.Generation, error) {
	publication, err := controller.store.GetServiceCatalogGeneration(
		ctx, repository, catalogGeneration,
	)
	if err != nil {
		return servicecatalogv3.Generation{}, err
	}
	catalog, err := servicecatalog.Decode(publication.Canonical)
	if err != nil {
		return servicecatalogv3.Generation{}, err
	}
	return servicecatalogv3.FromV2(*publication, catalog)
}

func (controller *serviceRuntimeController) completeV3Locked(
	ctx context.Context,
	repository, search string,
) (store.ServiceRuntimeTarget, error) {
	reconcile, err := controller.store.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	if !reconcile.Noop {
		return store.ServiceRuntimeTarget{}, errors.Join(
			errServiceRuntimePending, errServiceRuntimeContinuation,
		)
	}
	if search == "" {
		return store.ServiceRuntimeTarget{}, errServiceRuntimePending
	}
	activation, err := controller.store.BeginServiceStateV3Activation(
		ctx, repository, search,
	)
	if err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	if !activation.Noop {
		return store.ServiceRuntimeTarget{}, errors.Join(
			errServiceRuntimePending, errServiceRuntimeContinuation,
		)
	}
	current, err := controller.relationship.ReconcileV3(ctx, repository)
	if err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	if !current {
		return store.ServiceRuntimeTarget{}, errors.Join(
			errServiceRuntimePending, errServiceRuntimeContinuation,
		)
	}
	target, err := controller.v3Target(ctx, repository, search)
	if err != nil {
		return store.ServiceRuntimeTarget{}, pendingServiceRuntime(err)
	}
	return target, nil
}

// currentV3SearchGeneration reads the shared immutable search controls without
// advancing mutable v2 service state while an explicit v2 selector is serving.
func (controller *serviceRuntimeController) currentV3SearchGeneration(
	ctx context.Context,
	repository string,
) (string, error) {
	repo, err := controller.store.GetRepo(ctx, repository)
	if err != nil {
		return "", err
	}
	if repo.Deleting || repo.IndexedCommitHash == "" ||
		repo.IndexedAnalysisUnit != nil &&
			repo.IndexedAnalysisUnit.SearchIndexPosture == analysisunit.SearchIndexFocused {
		return "", errServiceRuntimePending
	}
	revisions := repo.IndexedRevisions
	if len(revisions) == 0 {
		revisions = []store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: repo.IndexedCommitHash,
		}}
	}
	search, _, err := focusedindex.ReadRepositorySearchGeneration(
		filepath.Join(controller.dataDir, "index"), repository, revisions,
	)
	if err != nil {
		return "", err
	}
	candidate, err := controller.store.GetServiceCatalogV3CandidateRoot(ctx, repository)
	if err != nil {
		return "", err
	}
	if len(search.Revisions) == 0 ||
		search.Revisions[0].Commit != candidate.Root.Binding.Source.Commit {
		return "", errServiceRuntimePending
	}
	return search.Digest, nil
}

func (controller *serviceRuntimeController) v2Target(
	ctx context.Context,
	repository, searchGeneration string,
) (store.ServiceRuntimeTarget, error) {
	catalog, err := controller.store.GetServiceCatalog(ctx, repository)
	if err != nil {
		return store.ServiceRuntimeTarget{}, err
	}
	summary, err := controller.store.GetServiceStateSummary(ctx, repository)
	if err != nil {
		return store.ServiceRuntimeTarget{}, err
	}
	publication, err := relationshippublication.OpenCurrent(
		ctx, filepath.Join(controller.dataDir, "relationships"), repository,
	)
	if err != nil {
		return store.ServiceRuntimeTarget{}, err
	}
	root := publication.Root()
	if root.Authority.CatalogGenerationDigest != catalog.GenerationDigest ||
		root.Authority.ServiceStateSummaryDigest != summary.SummaryDigest ||
		root.Authority.ServiceStateControlRevision != summary.ControlRevision {
		return store.ServiceRuntimeTarget{}, errServiceRuntimePending
	}
	return store.ServiceRuntimeTarget{
		CatalogGenerationDigest:      catalog.GenerationDigest,
		CatalogControlRevision:       catalog.ControlRevision,
		StateControlRevision:         summary.ControlRevision,
		StateSummaryDigest:           summary.SummaryDigest,
		SearchGenerationDigest:       searchGeneration,
		RelationshipGenerationDigest: root.GenerationDigest,
		RelationshipRootDigest:       root.Digest,
	}, nil
}

func (controller *serviceRuntimeController) v3Target(
	ctx context.Context,
	repository, searchGeneration string,
) (store.ServiceRuntimeTarget, error) {
	catalog, err := controller.store.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		return store.ServiceRuntimeTarget{}, err
	}
	summary, err := controller.store.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		return store.ServiceRuntimeTarget{}, err
	}
	publication, err := relationshippublication.OpenCurrentV3(
		ctx, filepath.Join(controller.dataDir, "relationships"), repository,
	)
	if err != nil {
		return store.ServiceRuntimeTarget{}, err
	}
	root := publication.Root()
	if root.Authority.CatalogRootDigest != catalog.RootDigest ||
		root.Authority.CatalogControlRevision != catalog.ControlRevision ||
		root.Authority.ServiceStateSummaryDigest != summary.SummaryDigest ||
		root.Authority.ServiceStateControlRevision != summary.ControlRevision {
		return store.ServiceRuntimeTarget{}, errServiceRuntimePending
	}
	return store.ServiceRuntimeTarget{
		CatalogRootDigest:            catalog.RootDigest,
		CatalogControlRevision:       catalog.ControlRevision,
		StateControlRevision:         summary.ControlRevision,
		StateSummaryDigest:           summary.SummaryDigest,
		SearchGenerationDigest:       searchGeneration,
		RelationshipGenerationDigest: root.GenerationDigest,
		RelationshipRootDigest:       root.Digest,
	}, nil
}

func (controller *serviceRuntimeController) selectLocked(
	ctx context.Context,
	repository, backend string,
	current *store.ServiceRuntimeSelector,
	target store.ServiceRuntimeTarget,
) error {
	controller.resolveUncertainPinsLocked(repository, current)
	if current != nil && current.Backend == backend &&
		sameServiceRuntimeTarget(serviceRuntimeTarget(*current), target) {
		return controller.ensurePinnedLocked(ctx, *current)
	}
	prospective := store.ServiceRuntimeSelector{
		Repository: repository, Backend: backend,
		CatalogGenerationDigest:      target.CatalogGenerationDigest,
		CatalogRootDigest:            target.CatalogRootDigest,
		CatalogControlRevision:       target.CatalogControlRevision,
		StateControlRevision:         target.StateControlRevision,
		StateSummaryDigest:           target.StateSummaryDigest,
		SearchGenerationDigest:       target.SearchGenerationDigest,
		RelationshipGenerationDigest: target.RelationshipGenerationDigest,
		RelationshipRootDigest:       target.RelationshipRootDigest,
	}
	pins, err := controller.acquirePins(ctx, prospective)
	if err != nil {
		return err
	}
	if err := recovery.ValidateServiceRuntimeTarget(
		ctx, controller.dataDir, controller.store, repository, backend, target,
	); err != nil {
		pins.release()
		return err
	}
	request := store.ServiceRuntimeSelectionRequest{Repository: repository, Target: target}
	if current != nil {
		request.ExpectedControlRevision = current.ControlRevision
		request.ExpectedDigest = current.Digest
	}
	var selected store.ServiceRuntimeSelector
	if backend == store.ServiceRuntimeV3 {
		selected, err = controller.store.SelectServiceRuntimeV3(ctx, request)
	} else {
		selected, err = controller.store.SelectServiceRuntimeV2(ctx, request)
	}
	if err != nil {
		confirmCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), 5*time.Second,
		)
		confirmed, confirmErr := controller.store.GetServiceRuntimeSelector(
			confirmCtx, repository,
		)
		cancel()
		if confirmErr != nil && !errors.Is(confirmErr, store.ErrNotFound) {
			if controller.uncertainPins == nil {
				controller.uncertainPins = make(map[string]*serviceRuntimeProcessPins)
			}
			controller.uncertainPins[repository] = pins
			return errors.Join(err, confirmErr)
		}
		if errors.Is(confirmErr, store.ErrNotFound) {
			pins.release()
			return errors.Join(err, confirmErr)
		}
		if confirmed.Backend != backend ||
			!sameServiceRuntimeTarget(serviceRuntimeTarget(confirmed), target) {
			pins.release()
			return errors.Join(err, store.ErrConflict)
		}
		selected = confirmed
	}
	pins.selector = selected
	controller.installPins(repository, pins)
	return nil
}

func (controller *serviceRuntimeController) resolveUncertainPinsLocked(
	repository string,
	current *store.ServiceRuntimeSelector,
) {
	uncertain := controller.uncertainPins[repository]
	if uncertain == nil {
		return
	}
	delete(controller.uncertainPins, repository)
	if current != nil && current.Backend == uncertain.selector.Backend &&
		sameServiceRuntimeTarget(
			serviceRuntimeTarget(*current), serviceRuntimeTarget(uncertain.selector),
		) {
		uncertain.selector = *current
		controller.installPins(repository, uncertain)
		return
	}
	uncertain.release()
}

func (controller *serviceRuntimeController) PinSelections(ctx context.Context) error {
	if controller == nil {
		return nil
	}
	release, err := controller.lockTransition(ctx)
	if err != nil {
		return err
	}
	defer release()
	selectors, err := controller.store.ListServiceRuntimeSelectors(ctx)
	if err != nil {
		return err
	}
	if len(selectors) > 0 && controller.relationship == nil {
		return errServiceRuntimeExtractionUnavailable
	}
	next := make(map[string]*serviceRuntimeProcessPins, len(selectors))
	for _, selector := range selectors {
		pins, err := controller.acquirePins(ctx, selector)
		if err != nil {
			releaseServiceRuntimePins(next)
			return err
		}
		if err := recovery.ValidateServiceRuntimeTarget(
			ctx, controller.dataDir, controller.store,
			selector.Repository, selector.Backend, serviceRuntimeTarget(selector),
		); err != nil {
			pins.release()
			releaseServiceRuntimePins(next)
			return fmt.Errorf("validate selected runtime for %q: %w", selector.Repository, err)
		}
		if err := controller.store.ConfirmServiceRuntimeSelector(ctx, selector); err != nil {
			pins.release()
			releaseServiceRuntimePins(next)
			return err
		}
		pins.selector = selector
		next[selector.Repository] = pins
	}
	releaseServiceRuntimePins(controller.pins)
	controller.pins = next
	releaseServiceRuntimePins(controller.uncertainPins)
	controller.uncertainPins = make(map[string]*serviceRuntimeProcessPins)
	return nil
}

func (controller *serviceRuntimeController) ensurePinnedLocked(
	ctx context.Context,
	selector store.ServiceRuntimeSelector,
) error {
	if current := controller.pins[selector.Repository]; current != nil &&
		current.selector == selector {
		return nil
	}
	pins, err := controller.acquirePins(ctx, selector)
	if err != nil {
		return err
	}
	if err := recovery.ValidateServiceRuntimeTarget(
		ctx, controller.dataDir, controller.store,
		selector.Repository, selector.Backend, serviceRuntimeTarget(selector),
	); err != nil {
		pins.release()
		return err
	}
	if err := controller.store.ConfirmServiceRuntimeSelector(ctx, selector); err != nil {
		pins.release()
		return err
	}
	controller.installPins(selector.Repository, pins)
	return nil
}

func (controller *serviceRuntimeController) acquirePins(
	ctx context.Context,
	selector store.ServiceRuntimeSelector,
) (*serviceRuntimeProcessPins, error) {
	search, err := controller.searchPins.Acquire(
		selector.Repository, selector.SearchGenerationDigest,
	)
	if err != nil {
		return nil, err
	}
	pins := &serviceRuntimeProcessPins{selector: selector, search: search}
	root := filepath.Join(controller.dataDir, "relationships")
	if selector.Backend == store.ServiceRuntimeV3 {
		pins.relationshipV3, err = controller.relationshipV3Cache.AcquireGeneration(
			ctx, root, selector.Repository,
			selector.RelationshipGenerationDigest, selector.RelationshipRootDigest,
		)
	} else {
		pins.relationship, err = controller.relationshipCache.AcquireGeneration(
			ctx, root, selector.Repository,
			selector.RelationshipGenerationDigest, selector.RelationshipRootDigest,
		)
	}
	if err != nil {
		pins.release()
		return nil, err
	}
	return pins, nil
}

func (controller *serviceRuntimeController) installPins(
	repository string,
	pins *serviceRuntimeProcessPins,
) {
	prior := controller.pins[repository]
	controller.pins[repository] = pins
	prior.release()
}

// RetireRepository is called by orphan cleanup after the repository is marked
// deleting and while that cleanup already holds the shared mutation lock. It
// must not reacquire controller.acquire. The durable selector/reference leave
// first; only then may process pins be released for lifecycle collection.
func (controller *serviceRuntimeController) RetireRepository(
	ctx context.Context,
	repository string,
) error {
	if controller == nil {
		return errors.New("service runtime controller is unavailable")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if err := controller.store.RetireServiceRuntimeSelectorForRepositoryDeletion(
		ctx, repository,
	); err != nil {
		return err
	}
	prior := controller.pins[repository]
	delete(controller.pins, repository)
	prior.release()
	uncertain := controller.uncertainPins[repository]
	delete(controller.uncertainPins, repository)
	uncertain.release()
	return nil
}

func (controller *serviceRuntimeController) Close() {
	if controller == nil {
		return
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	releaseServiceRuntimePins(controller.pins)
	controller.pins = make(map[string]*serviceRuntimeProcessPins)
	releaseServiceRuntimePins(controller.uncertainPins)
	controller.uncertainPins = make(map[string]*serviceRuntimeProcessPins)
}

func (controller *serviceRuntimeController) currentSelector(
	ctx context.Context,
	repository string,
) (*store.ServiceRuntimeSelector, error) {
	selector, err := controller.store.GetServiceRuntimeSelector(ctx, repository)
	if err != nil {
		return nil, err
	}
	return &selector, nil
}

func serviceRuntimeTarget(selector store.ServiceRuntimeSelector) store.ServiceRuntimeTarget {
	return store.ServiceRuntimeTarget{
		CatalogGenerationDigest:      selector.CatalogGenerationDigest,
		CatalogRootDigest:            selector.CatalogRootDigest,
		CatalogControlRevision:       selector.CatalogControlRevision,
		StateControlRevision:         selector.StateControlRevision,
		StateSummaryDigest:           selector.StateSummaryDigest,
		SearchGenerationDigest:       selector.SearchGenerationDigest,
		RelationshipGenerationDigest: selector.RelationshipGenerationDigest,
		RelationshipRootDigest:       selector.RelationshipRootDigest,
	}
}

func sameServiceRuntimeTarget(left, right store.ServiceRuntimeTarget) bool {
	return left == right
}

func releaseServiceRuntimePins(values map[string]*serviceRuntimeProcessPins) {
	for _, pins := range values {
		pins.release()
	}
}

func pendingServiceRuntime(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) ||
		errors.Is(err, relationshippublication.ErrNotFound) ||
		errors.Is(err, relationshippublication.ErrPublishing) {
		return errors.Join(errServiceRuntimePending, err)
	}
	return err
}
