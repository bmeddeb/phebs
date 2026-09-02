package t4110

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t411"
)

func (h *liveHarness) warmNoop(ctx context.Context, cost *PhaseCost) error {
	priorSelector := h.selector
	priorSearch, err := focusedindex.ReadSearchGenerationRoot(
		filepath.Join(h.dataDir, "index"), liveRepository,
	)
	if err != nil {
		return err
	}
	priorRelationship, err := relationshippublication.ReadPointerV3(
		ctx, filepath.Join(h.dataDir, "relationships"), liveRepository,
	)
	if err != nil {
		return err
	}
	index := &indexer.Indexer{
		DataDir: h.dataDir, Bin: h.zoektBinary,
		BinSHA256: h.zoektSHA256, Store: h.state,
	}
	if err := index.Index(ctx, store.Repo{Name: liveRepository}, false); err != nil {
		return fmt.Errorf("warm index no-op: %w", err)
	}
	reconciler := servicecatalogingest.V3Reconciler{
		DataDir: h.dataDir,
		Store:   h.state,
		Selections: map[string]config.ServiceCatalog{
			liveRepository: {
				Kind: h.catalog.Authority.Kind, ID: h.catalog.Authority.ID,
				Version: h.catalog.Authority.Version, Path: h.catalogPath,
				Runtime: config.ServiceCatalogRuntimeV3,
			},
		},
	}
	outcome, err := reconciler.ReconcileRepository(ctx, liveRepository)
	if err != nil || outcome != servicecatalogingest.OutcomeCurrent {
		return errors.Join(fmt.Errorf("warm catalog outcome %q", outcome), err)
	}
	reconcile, err := h.state.BeginServiceStateV3Reconcile(ctx, liveRepository)
	if err != nil || !reconcile.Noop {
		return errors.Join(errors.New("warm reconcile was not a no-op"), err)
	}
	activation, err := h.state.BeginServiceStateV3Activation(
		ctx, liveRepository, h.searchDigest,
	)
	if err != nil || !activation.Noop {
		return errors.Join(errors.New("warm activation was not a no-op"), err)
	}
	readCost, _, _, err := h.acceptedSnapshot(ctx)
	if err != nil {
		return err
	}
	*cost = readCost
	relationship, err := h.reconcileEmptyRelationshipV3(ctx, cost)
	if err != nil {
		return fmt.Errorf("warm relationship runtime no-op: %w", err)
	}
	if relationship.GenerationDigest != priorRelationship.GenerationDigest ||
		relationship.Digest != priorRelationship.RootDigest {
		return errors.New("warm relationship authority changed")
	}
	if err := h.state.ConfirmServiceRuntimeSelector(ctx, priorSelector); err != nil {
		return fmt.Errorf("warm runtime selector changed: %w", err)
	}
	query, err := querySelectedServices(
		ctx, h.state, h.selector, []string{firstAcceptedKey(h.catalog)}, false,
	)
	if err != nil || query.Matches != 1 {
		return errors.Join(errors.New("warm selected query did not match"), err)
	}
	mergePhaseCosts(cost, query.Cost)
	afterSearch, err := focusedindex.ReadSearchGenerationRoot(
		filepath.Join(h.dataDir, "index"), liveRepository,
	)
	if err != nil || !reflect.DeepEqual(afterSearch, priorSearch) {
		return errors.Join(errors.New("warm search authority changed"), err)
	}
	afterRelationship, err := relationshippublication.ReadPointerV3(
		ctx, filepath.Join(h.dataDir, "relationships"), liveRepository,
	)
	if err != nil || afterRelationship != priorRelationship {
		return errors.Join(errors.New("warm relationship pointer changed"), err)
	}
	return nil
}

func (h *liveHarness) oneServiceDelta(ctx context.Context, cost *PhaseCost) error {
	next := cloneCatalog(h.catalog)
	next.Authority.Version = "t4110-one-service-v1"
	index := len(next.Services) / 2
	before, err := h.state.GetServiceStateV3PointSnapshot(
		ctx, liveRepository, next.Services[index].Key,
		h.selector.StateControlRevision, h.selector.StateSummaryDigest,
	)
	if err != nil {
		return err
	}
	next.Services[index].DisplayName += "-one"
	measured, err := h.applySuccessor(ctx, next, 1, "one")
	if err != nil {
		return err
	}
	exact, err := verifySelectedService(
		ctx, h.state, h.selector, next, next.Services[index].Key, before.Incarnation,
	)
	if err != nil {
		return err
	}
	mergePhaseCosts(&measured, exact)
	*cost = measured
	return nil
}

func (h *liveHarness) percentDelta(ctx context.Context, cost *PhaseCost) error {
	next := cloneCatalog(h.catalog)
	next.Authority.Version = "t4110-percent-v1"
	incarnations := make(map[string]uint64, 100)
	for index := range 100 {
		state, err := h.state.GetServiceStateV3PointSnapshot(
			ctx, liveRepository, next.Services[index].Key,
			h.selector.StateControlRevision, h.selector.StateSummaryDigest,
		)
		if err != nil {
			return err
		}
		incarnations[next.Services[index].Key] = state.Incarnation
		next.Services[index].DisplayName += "-percent"
	}
	measured, err := h.applySuccessor(ctx, next, 100, "percent")
	if err != nil {
		return err
	}
	for index := range 100 {
		exact, verifyErr := verifySelectedService(
			ctx, h.state, h.selector, next, next.Services[index].Key,
			incarnations[next.Services[index].Key],
		)
		if verifyErr != nil {
			return verifyErr
		}
		mergePhaseCosts(&measured, exact)
	}
	*cost = measured
	return nil
}

func (h *liveHarness) removalReadd(ctx context.Context, cost *PhaseCost) error {
	base := cloneCatalog(h.catalog)
	serviceKey := base.Services[len(base.Services)/2].Key
	before, err := h.state.GetServiceStateV3Point(ctx, liveRepository, serviceKey)
	if err != nil {
		return err
	}
	removed := cloneCatalog(base)
	removed.Authority.Version = "t4110-remove-v1"
	removed.Services = removeService(removed.Services, serviceKey)
	removed.Memberships = removeMemberships(removed.Memberships, serviceKey)
	first, err := h.applySuccessor(ctx, removed, 1, "remove")
	if err != nil {
		return err
	}
	removedRead, err := verifySelectedService(
		ctx, h.state, h.selector, removed, serviceKey, before.Incarnation,
	)
	if err != nil {
		return err
	}
	mergePhaseCosts(&first, removedRead)
	readded := cloneCatalog(base)
	readded.Authority.Version = "t4110-readd-v1"
	second, err := h.applySuccessor(ctx, readded, 1, "readd")
	if err != nil {
		return err
	}
	readdedRead, err := verifySelectedService(
		ctx, h.state, h.selector, readded, serviceKey, before.Incarnation+1,
	)
	if err != nil {
		return err
	}
	mergePhaseCosts(&second, readdedRead)
	after, err := h.state.GetServiceStateV3Point(ctx, liveRepository, serviceKey)
	if err != nil || after.Removed || after.Incarnation != before.Incarnation+1 {
		return errors.Join(errors.New("removal/re-add incarnation did not advance once"), err)
	}
	mergePhaseCosts(cost, first)
	mergePhaseCosts(cost, second)
	return nil
}

func (h *liveHarness) aba(ctx context.Context, cost *PhaseCost) error {
	base := cloneCatalog(h.catalog)
	serviceKey := base.Services[len(base.Services)/3].Key
	before, err := h.state.GetServiceStateV3Point(ctx, liveRepository, serviceKey)
	if err != nil {
		return err
	}
	b := cloneCatalog(base)
	b.Authority.Version = "t4110-aba-b-v1"
	for index := range b.Services {
		if b.Services[index].Key == serviceKey {
			b.Services[index].DisplayName += "-b"
			break
		}
	}
	first, err := h.applySuccessor(ctx, b, 1, "aba-b")
	if err != nil {
		return err
	}
	bRead, err := verifySelectedService(
		ctx, h.state, h.selector, b, serviceKey, before.Incarnation,
	)
	if err != nil {
		return err
	}
	mergePhaseCosts(&first, bRead)
	a := cloneCatalog(base)
	a.Authority.Version = "t4110-aba-a-return-v1"
	second, err := h.applySuccessor(ctx, a, 1, "aba-a")
	if err != nil {
		return err
	}
	aRead, err := verifySelectedService(
		ctx, h.state, h.selector, a, serviceKey, before.Incarnation,
	)
	if err != nil {
		return err
	}
	mergePhaseCosts(&second, aRead)
	after, err := h.state.GetServiceStateV3Point(ctx, liveRepository, serviceKey)
	if err != nil || after.Incarnation != before.Incarnation || after.Removed {
		return errors.Join(errors.New("A-B-A changed service incarnation"), err)
	}
	mergePhaseCosts(cost, first)
	mergePhaseCosts(cost, second)
	return nil
}

func (h *liveHarness) transitionProfile(ctx context.Context, cost *PhaseCost) error {
	base := cloneCatalog(h.catalog)
	if len(h.transition.Profile.Revisions) != 3 {
		return errors.New("frozen transition profile does not contain three revisions")
	}
	keys := serviceKeys(base)
	if len(keys) < 5 || len(h.transition.Profile.Revisions[0].Catalog.Services) != 5 {
		return errors.New("transition profile cannot be mapped onto the target corpus")
	}
	mapping := make(map[string]string, 5)
	for index, service := range h.transition.Profile.Revisions[0].Catalog.Services {
		mapping[service.Key] = keys[index]
	}
	sourceKeys := make([]string, 0, len(mapping))
	incarnations := make(map[string]uint64, len(mapping))
	for sourceKey, targetKey := range mapping {
		sourceKeys = append(sourceKeys, sourceKey)
		state, err := h.state.GetServiceStateV3PointSnapshot(
			ctx, liveRepository, targetKey,
			h.selector.StateControlRevision, h.selector.StateSummaryDigest,
		)
		if err != nil {
			return err
		}
		incarnations[targetKey] = state.Incarnation
	}
	slices.Sort(sourceKeys)
	wants := []uint64{3, 2, 2}
	prior := base
	for index, revision := range h.transition.Profile.Revisions {
		next, err := mappedTransitionCatalog(base, revision, mapping)
		if err != nil {
			return err
		}
		measured, err := h.applySuccessor(
			ctx, next, wants[index], "transition-"+revision.Name,
		)
		if err != nil {
			return err
		}
		advanceTransitionIncarnations(prior, next, mapping, incarnations)
		for _, sourceKey := range sourceKeys {
			targetKey := mapping[sourceKey]
			exact, verifyErr := verifySelectedService(
				ctx, h.state, h.selector, next, targetKey, incarnations[targetKey],
			)
			if verifyErr != nil {
				return verifyErr
			}
			mergePhaseCosts(&measured, exact)
		}
		mergePhaseCosts(cost, measured)
		prior = next
	}
	final := cloneCatalog(base)
	final.Authority.Version = "t4110-transition-final-v1"
	measured, err := h.applySuccessor(ctx, final, 3, "transition-final")
	if err != nil {
		return err
	}
	advanceTransitionIncarnations(prior, final, mapping, incarnations)
	for _, sourceKey := range sourceKeys {
		targetKey := mapping[sourceKey]
		exact, verifyErr := verifySelectedService(
			ctx, h.state, h.selector, final, targetKey, incarnations[targetKey],
		)
		if verifyErr != nil {
			return verifyErr
		}
		mergePhaseCosts(&measured, exact)
	}
	mergePhaseCosts(cost, measured)
	readdKey, ok := mapping["svc.readd"]
	if !ok {
		return errors.New("frozen transition profile omits the re-add case")
	}
	state, err := h.state.GetServiceStateV3Point(ctx, liveRepository, readdKey)
	if err != nil || state.Incarnation != incarnations[readdKey] || state.Removed {
		return errors.Join(errors.New("transition profile re-add did not survive"), err)
	}
	return nil
}

func advanceTransitionIncarnations(
	prior, next servicecatalog.Catalog,
	mapping map[string]string,
	incarnations map[string]uint64,
) {
	for _, targetKey := range mapping {
		if transitionServiceRemoved(prior, targetKey) &&
			!transitionServiceRemoved(next, targetKey) {
			incarnations[targetKey]++
		}
	}
}

func transitionServiceRemoved(catalog servicecatalog.Catalog, serviceKey string) bool {
	for _, service := range catalog.Services {
		if service.Key == serviceKey {
			return service.Disposition == servicecatalog.DispositionRejected
		}
	}
	return true
}

func mappedTransitionCatalog(
	base servicecatalog.Catalog,
	revision t411.TransitionRevision,
	mapping map[string]string,
) (servicecatalog.Catalog, error) {
	next := cloneCatalog(base)
	next.Authority.Version = "t4110-transition-" + revision.Name
	services := make(map[string]servicecatalog.Service, len(revision.Catalog.Services))
	member := make(map[string]bool, len(revision.Catalog.Memberships))
	for _, service := range revision.Catalog.Services {
		services[service.Key] = service
	}
	for _, membership := range revision.Catalog.Memberships {
		member[membership.ServiceKey] = true
	}
	for sourceKey, targetKey := range mapping {
		source, present := services[sourceKey]
		if !present {
			next.Services = removeService(next.Services, targetKey)
			next.Memberships = removeMemberships(next.Memberships, targetKey)
			continue
		}
		found := false
		for index := range next.Services {
			if next.Services[index].Key != targetKey {
				continue
			}
			found = true
			next.Services[index].Disposition = source.Disposition
			next.Services[index].Reason = source.Reason
			next.Services[index].Successors = make([]string, len(source.Successors))
			for successorIndex, successor := range source.Successors {
				mapped, ok := mapping[successor]
				if !ok {
					return servicecatalog.Catalog{}, fmt.Errorf(
						"transition successor %q has no target mapping", successor,
					)
				}
				next.Services[index].Successors[successorIndex] = mapped
			}
			break
		}
		if !found {
			return servicecatalog.Catalog{}, fmt.Errorf(
				"transition target service %q is absent", targetKey,
			)
		}
		if !member[sourceKey] {
			next.Memberships = removeMemberships(next.Memberships, targetKey)
		}
	}
	if err := servicecatalogv3.ValidateCatalog(next); err != nil {
		return servicecatalog.Catalog{}, fmt.Errorf("validate mapped transition: %w", err)
	}
	return next, nil
}

func (h *liveHarness) applySuccessor(
	ctx context.Context,
	next servicecatalog.Catalog,
	wantChanged uint64,
	worker string,
) (PhaseCost, error) {
	var measured PhaseCost
	priorCatalog := cloneCatalog(h.catalog)
	next, binding, err := h.bindSuccessorSource(next)
	if err != nil {
		return PhaseCost{}, err
	}
	changedKey, err := firstCatalogServiceDifference(priorCatalog, next)
	if err != nil {
		return PhaseCost{}, err
	}
	before, err := h.drainStalePreimages(ctx, &measured, false)
	if err != nil {
		return PhaseCost{}, err
	}
	generation, err := servicecatalogv3.Build(binding, next)
	if err != nil {
		return PhaseCost{}, fmt.Errorf("build %s successor: %w", worker, err)
	}
	if err := h.state.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		return PhaseCost{}, fmt.Errorf("publish %s successor: %w", worker, err)
	}
	reconcile, err := h.state.BeginServiceStateV3Reconcile(ctx, liveRepository)
	if err != nil {
		return PhaseCost{}, err
	}
	reconcileCost, err := runServiceStatePlan(
		ctx, h.state, reconcile, "t4110-"+worker+"-reconcile",
	)
	if err != nil {
		return PhaseCost{}, err
	}
	mergeStateCost(&measured, reconcileCost, true)
	if measured.ChangedRows != wantChanged {
		return PhaseCost{}, fmt.Errorf(
			"%s changed rows = %d, want %d", worker, measured.ChangedRows, wantChanged,
		)
	}
	oldQuery, err := verifySelectedService(
		ctx, h.state, h.selector, priorCatalog, changedKey, 0,
	)
	if err != nil {
		return PhaseCost{}, fmt.Errorf("prior selector failed after reconcile: %w", err)
	}
	mergePhaseCosts(&measured, oldQuery)

	activation, err := h.state.BeginServiceStateV3Activation(
		ctx, liveRepository, h.searchDigest,
	)
	if err != nil {
		return PhaseCost{}, err
	}
	activationCost, err := runServiceStatePlanWithFirstChunkHook(
		ctx,
		h.state,
		activation,
		"t4110-"+worker+"-activate",
		func(ctx context.Context) error {
			partial, queryErr := verifySelectedService(
				ctx, h.state, h.selector, priorCatalog, changedKey, 0,
			)
			if queryErr != nil {
				return fmt.Errorf("prior selector failed during activation: %w", queryErr)
			}
			mergePhaseCosts(&measured, partial)
			return nil
		},
	)
	if err != nil {
		return PhaseCost{}, err
	}
	mergeStateCost(&measured, activationCost, false)
	h.generation = generation
	readCost, _, _, err := h.acceptedSnapshot(ctx)
	if err != nil {
		return PhaseCost{}, err
	}
	mergePhaseCosts(&measured, readCost)
	relationship, err := h.reconcileEmptyRelationshipV3(ctx, &measured)
	if err != nil {
		return PhaseCost{}, fmt.Errorf("reconcile %s relationship: %w", worker, err)
	}
	nextSelector, err := h.selectRuntime(ctx, h.selector, relationship)
	if err != nil {
		return PhaseCost{}, err
	}
	newQuery, err := verifySelectedService(
		ctx, h.state, nextSelector, next, changedKey, 0,
	)
	if err != nil {
		return PhaseCost{}, fmt.Errorf("successor selector query failed: %w", err)
	}
	mergePhaseCosts(&measured, newQuery)
	h.selector = nextSelector
	h.catalog = cloneCatalog(next)
	after, err := h.preimageInventory(ctx)
	if err != nil {
		return PhaseCost{}, err
	}
	if after.rows < before.rows || after.summaries < before.summaries {
		return PhaseCost{}, errors.New("sparse preimage inventory moved backward")
	}
	measured.PreimageRowsWritten = after.rows - before.rows
	measured.PreimageSummariesWritten = after.summaries - before.summaries
	if measured.PreimageRowsWritten != wantChanged ||
		measured.PreimageSummariesWritten != 1 {
		return PhaseCost{}, fmt.Errorf(
			"%s preimages rows=%d summaries=%d",
			worker,
			measured.PreimageRowsWritten,
			measured.PreimageSummariesWritten,
		)
	}
	return measured, nil
}

func (h *liveHarness) bindSuccessorSource(
	next servicecatalog.Catalog,
) (servicecatalog.Catalog, servicecatalogv3.Binding, error) {
	binding := h.generation.Root.Binding
	if binding.Source.FileCount != len(h.corpus.Files) {
		return servicecatalog.Catalog{}, servicecatalogv3.Binding{},
			errors.New("successor source inventory differs from the frozen corpus")
	}
	dispositions := make(map[string]string, len(next.Services))
	for _, service := range next.Services {
		dispositions[service.Key] = service.Disposition
	}
	acceptedPaths := make(map[string]struct{}, len(next.Memberships))
	placementHits := make(map[string]bool, len(next.Memberships))
	for _, membership := range next.Memberships {
		placementHits[membership.Path] = false
		if dispositions[membership.ServiceKey] == servicecatalog.DispositionAccepted {
			acceptedPaths[membership.Path] = struct{}{}
		}
	}
	next.Unowned = next.Unowned[:0]
	acceptedFiles := 0
	for _, file := range h.corpus.Files {
		accepted := false
		for current := file.Path; current != "."; current = path.Dir(current) {
			if _, exists := placementHits[current]; exists {
				placementHits[current] = true
			}
			if _, exists := acceptedPaths[current]; exists {
				accepted = true
			}
		}
		if accepted {
			acceptedFiles++
			continue
		}
		next.Unowned = append(next.Unowned, servicecatalog.UnownedPlacement{
			Path: file.Path, Origin: servicecatalog.OriginBase,
		})
	}
	missing := ""
	for placement, hit := range placementHits {
		if !hit && (missing == "" || placement < missing) {
			missing = placement
		}
	}
	if missing != "" {
		return servicecatalog.Catalog{}, servicecatalogv3.Binding{},
			fmt.Errorf("catalog placement %q is absent from the frozen corpus", missing)
	}
	if err := servicecatalogv3.ValidateCatalog(next); err != nil {
		return servicecatalog.Catalog{}, servicecatalogv3.Binding{},
			fmt.Errorf("validate source-bound successor: %w", err)
	}
	binding.Authority = next.Authority
	binding.Override = next.Override
	binding.Source.AcceptedFileCount = acceptedFiles
	binding.Source.UnownedFileCount = len(h.corpus.Files) - acceptedFiles
	return next, binding, nil
}

func firstCatalogServiceDifference(
	left, right servicecatalog.Catalog,
) (string, error) {
	keys := make(map[string]struct{}, len(left.Services)+len(right.Services))
	for _, service := range left.Services {
		keys[service.Key] = struct{}{}
	}
	for _, service := range right.Services {
		keys[service.Key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	slices.Sort(ordered)
	serviceAndMemberships := func(catalog servicecatalog.Catalog, key string) (
		*servicecatalog.Service,
		[]servicecatalog.Membership,
	) {
		var service *servicecatalog.Service
		for index := range catalog.Services {
			if catalog.Services[index].Key == key {
				copy := catalog.Services[index]
				service = &copy
				break
			}
		}
		memberships := make([]servicecatalog.Membership, 0)
		for _, membership := range catalog.Memberships {
			if membership.ServiceKey == key {
				memberships = append(memberships, membership)
			}
		}
		return service, memberships
	}
	for _, key := range ordered {
		leftService, leftMemberships := serviceAndMemberships(left, key)
		rightService, rightMemberships := serviceAndMemberships(right, key)
		sameService := leftService == nil && rightService == nil ||
			leftService != nil && rightService != nil &&
				sameCatalogService(*leftService, *rightService)
		if !sameService || !slices.Equal(leftMemberships, rightMemberships) {
			return key, nil
		}
	}
	return "", errors.New("T41.10 successor has no service-local change")
}

func (h *liveHarness) acceptedSnapshot(
	ctx context.Context,
) (PhaseCost, []servicecatalog.ServiceState, servicecatalog.RepositoryState, error) {
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := store.NewServiceStateV3Reader(h.state, cache)
	if err != nil {
		return PhaseCost{}, nil, servicecatalog.RepositoryState{}, err
	}
	snapshot, err := reader.AcceptedSnapshot(ctx, liveRepository)
	if err != nil {
		return PhaseCost{}, nil, servicecatalog.RepositoryState{}, err
	}
	stats := cache.Stats()
	if stats.RootLeases != 0 || stats.MemberLeases != 0 {
		return PhaseCost{}, nil, servicecatalog.RepositoryState{}, errors.New("accepted snapshot retained cache leases")
	}
	return PhaseCost{
		SelectedStateRootReads:         stats.RootReads,
		SelectedStateMemberReads:       stats.MemberReads,
		SelectedStateRootValidations:   stats.RootValidations,
		SelectedStateMemberValidations: stats.MemberValidations,
	}, snapshot.States, snapshot.Summary, nil
}

func (h *liveHarness) selectRuntime(
	ctx context.Context,
	prior store.ServiceRuntimeSelector,
	relationship relationshippublication.RootV3,
) (store.ServiceRuntimeSelector, error) {
	pointer, err := h.state.GetServiceCatalogV3CandidatePointer(ctx, liveRepository)
	if err != nil {
		return store.ServiceRuntimeSelector{}, err
	}
	summary, err := h.state.GetServiceStateV3Summary(ctx, liveRepository)
	if err != nil {
		return store.ServiceRuntimeSelector{}, err
	}
	target := store.ServiceRuntimeTarget{
		CatalogRootDigest:            pointer.RootDigest,
		CatalogControlRevision:       pointer.ControlRevision,
		StateControlRevision:         summary.ControlRevision,
		StateSummaryDigest:           summary.SummaryDigest,
		SearchGenerationDigest:       h.searchDigest,
		RelationshipGenerationDigest: relationship.GenerationDigest,
		RelationshipRootDigest:       relationship.Digest,
	}
	if err := recovery.ValidateServiceRuntimeTarget(
		ctx, h.dataDir, h.state, liveRepository, store.ServiceRuntimeV3, target,
	); err != nil {
		return store.ServiceRuntimeSelector{}, fmt.Errorf("validate successor runtime: %w", err)
	}
	request := store.ServiceRuntimeSelectionRequest{Repository: liveRepository, Target: target}
	if prior.ControlRevision != 0 {
		request.ExpectedControlRevision = prior.ControlRevision
		request.ExpectedDigest = prior.Digest
	}
	selected, err := h.state.SelectServiceRuntimeV3(ctx, request)
	if err != nil {
		return store.ServiceRuntimeSelector{}, fmt.Errorf("select successor runtime: %w", err)
	}
	return selected, nil
}

func (h *liveHarness) preimageInventory(ctx context.Context) (preimageInventory, error) {
	repositories, err := h.state.ListRepos(ctx)
	if err != nil {
		return preimageInventory{}, err
	}
	if len(repositories) != 1 || repositories[0].Name != liveRepository {
		return preimageInventory{}, errors.New("preimage inventory requires exactly one live repository")
	}
	report, err := h.state.ValidateServiceCatalogV3Precious(ctx)
	if err != nil {
		return preimageInventory{}, err
	}
	summary, err := h.state.GetServiceStateV3Summary(ctx, liveRepository)
	if err != nil {
		return preimageInventory{}, err
	}
	currentRows := summary.LiveServiceCount + summary.TombstoneCount
	if report.StateRows < currentRows || report.StateSummaries < 1 {
		return preimageInventory{}, errors.New("precious state inventory is smaller than current authority")
	}
	return preimageInventory{
		rows:            uint64(report.StateRows - currentRows),
		summaries:       uint64(report.StateSummaries - 1),
		collectingRoots: report.CollectingRoots,
	}, nil
}

func mergeStateCost(target *PhaseCost, source PhaseCost, logicalChanges bool) {
	target.StateChunkTransactions += source.StateChunkTransactions
	target.StateRowsRead += source.StateRowsRead
	target.StateRowsApplied += source.StateRowsApplied
	if logicalChanges {
		target.ChangedRows += source.ChangedRows
	}
}

func mergePhaseCosts(target *PhaseCost, source PhaseCost) {
	target.SelectedStateRootReads += source.SelectedStateRootReads
	target.SelectedStateMemberReads += source.SelectedStateMemberReads
	target.SelectedStateRootValidations += source.SelectedStateRootValidations
	target.SelectedStateMemberValidations += source.SelectedStateMemberValidations
	target.StateChunkTransactions += source.StateChunkTransactions
	target.StateRowsRead += source.StateRowsRead
	target.StateRowsApplied += source.StateRowsApplied
	target.SearchFilesOffered += source.SearchFilesOffered
	target.SearchContentReads += source.SearchContentReads
	target.SearchDeclaredBytes += source.SearchDeclaredBytes
	target.SourceCensusRecords += source.SourceCensusRecords
	target.SourceCensusMembers += source.SourceCensusMembers
	target.SourceCensusPlacements += source.SourceCensusPlacements
	target.SourceCensusDeclaredBytes += source.SourceCensusDeclaredBytes
	target.ObservationInputMembers += source.ObservationInputMembers
	target.ObservationMembers += source.ObservationMembers
	target.ObservationRecords += source.ObservationRecords
	target.ObservationObservedRecords += source.ObservationObservedRecords
	target.ObservationSourceBlobReads += source.ObservationSourceBlobReads
	target.CandidateInputReadsUnavailable = target.CandidateInputReadsUnavailable ||
		source.CandidateInputReadsUnavailable
	target.CandidateResultMembers += source.CandidateResultMembers
	target.CandidateResultRecords += source.CandidateResultRecords
	target.CandidateDeclaredBytes += source.CandidateDeclaredBytes
	target.RelationshipScheduleChunks += source.RelationshipScheduleChunks
	target.RelationshipComponentPublishes += source.RelationshipComponentPublishes
	target.RelationshipComponentMembers += source.RelationshipComponentMembers
	target.RelationshipComponentRecords += source.RelationshipComponentRecords
	target.RelationshipPublishes += source.RelationshipPublishes
	target.RelationshipServiceMembers += source.RelationshipServiceMembers
	target.RelationshipServiceRecords += source.RelationshipServiceRecords
	target.RelationshipProjectionMembers += source.RelationshipProjectionMembers
	target.RelationshipProjectionRecords += source.RelationshipProjectionRecords
	target.LifecycleRecordsDeleted += source.LifecycleRecordsDeleted
	target.LifecycleRetiredLogicalBytes += source.LifecycleRetiredLogicalBytes
	target.LifecycleDeletedRootBytes += source.LifecycleDeletedRootBytes
	target.LifecycleDeletedMemberBytes += source.LifecycleDeletedMemberBytes
	target.LifecycleOwnerTurns += source.LifecycleOwnerTurns
	target.LifecyclePressureCollectObservations += source.LifecyclePressureCollectObservations
	target.LifecyclePressureNormalObservations += source.LifecyclePressureNormalObservations
	target.ArchiveArtifactCount += source.ArchiveArtifactCount
	target.ArchiveArtifactBytes += source.ArchiveArtifactBytes
	target.RestoredArtifactCount += source.RestoredArtifactCount
	target.RestoredArtifactBytes += source.RestoredArtifactBytes
	target.ProductQueries += source.ProductQueries
	target.ChangedRows += source.ChangedRows
	target.PreimageRowsWritten += source.PreimageRowsWritten
	target.PreimageSummariesWritten += source.PreimageSummariesWritten
	target.PreimageRowsCollected += source.PreimageRowsCollected
	target.PreimageSummariesCollected += source.PreimageSummariesCollected
}

func cloneCatalog(value servicecatalog.Catalog) servicecatalog.Catalog {
	result := value
	result.Services = slices.Clone(value.Services)
	for index := range result.Services {
		result.Services[index].Successors = slices.Clone(result.Services[index].Successors)
	}
	result.Memberships = slices.Clone(value.Memberships)
	result.Unowned = slices.Clone(value.Unowned)
	if value.Override != nil {
		copy := *value.Override
		result.Override = &copy
	}
	return result
}

func serviceKeys(catalog servicecatalog.Catalog) []string {
	result := make([]string, len(catalog.Services))
	for index, service := range catalog.Services {
		result[index] = service.Key
	}
	return result
}

func firstAcceptedKey(catalog servicecatalog.Catalog) string {
	for _, service := range catalog.Services {
		if service.Disposition == servicecatalog.DispositionAccepted {
			return service.Key
		}
	}
	return ""
}

func removeService(values []servicecatalog.Service, key string) []servicecatalog.Service {
	result := make([]servicecatalog.Service, 0, len(values)-1)
	for _, value := range values {
		if value.Key != key {
			result = append(result, value)
		}
	}
	return result
}

func removeMemberships(values []servicecatalog.Membership, key string) []servicecatalog.Membership {
	result := make([]servicecatalog.Membership, 0, len(values))
	for _, value := range values {
		if value.ServiceKey != key {
			result = append(result, value)
		}
	}
	return result
}
