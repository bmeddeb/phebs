package t4110

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/recovery"
	phebssearch "github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func (h *liveHarness) archiveRestore(ctx context.Context, cost *PhaseCost) error {
	var relationshipLifecycleCost PhaseCost
	deletedRelationships, err := h.drainHistoricalRelationships(
		ctx, &relationshipLifecycleCost,
	)
	if err != nil {
		return err
	}
	if deletedRelationships == 0 {
		return errors.New("target relationship lifecycle did not collect historical custody")
	}
	if err := recovery.ValidateServiceRuntimeSelections(ctx, h.dataDir, h.state); err != nil {
		return fmt.Errorf("validate pre-backup runtime: %w", err)
	}
	backup := filepath.Join(h.root, "backup")
	backupManifest, err := recovery.Create(ctx, recovery.BackupOptions{
		Options: recovery.Options{
			DataDir: h.dataDir, Config: h.config,
			PhebsBinary: h.phebsBinary, PhebsVersion: h.phebsVersion,
		},
		Output: backup,
	})
	if err != nil {
		return fmt.Errorf("create live backup: %w", err)
	}
	archiveArtifacts, archiveBytes, err := recoveryManifestCost(backupManifest)
	if err != nil {
		return fmt.Errorf("account live backup manifest: %w", err)
	}
	prior := h.selector
	if err := closeLiveStore(h.state); err != nil {
		return fmt.Errorf("close pre-restore store: %w", err)
	}
	h.state = nil
	restored := filepath.Join(h.root, "restored")
	restoreManifest, err := recovery.Restore(ctx, recovery.RestoreOptions{
		Options: recovery.Options{
			DataDir: restored, Config: h.config,
			PhebsBinary: h.phebsBinary, PhebsVersion: h.phebsVersion,
		},
		Backup: backup,
	})
	if err != nil {
		return fmt.Errorf("restore live backup: %w", err)
	}
	if !reflect.DeepEqual(restoreManifest, backupManifest) {
		return errors.New("restored manifest differs from the exact created manifest")
	}
	restoreArtifacts, restoreBytes, err := recoveryManifestCost(restoreManifest)
	if err != nil {
		return fmt.Errorf("account live restore manifest: %w", err)
	}
	if restoreArtifacts != archiveArtifacts || restoreBytes != archiveBytes {
		return errors.New("restored artifact cost differs from the exact created inventory")
	}
	state, err := store.OpenLocalWithConfig(ctx, restored, recovery.ConfigDigest(h.config))
	if err != nil {
		return fmt.Errorf("open restored live store: %w", err)
	}
	h.state = state
	h.dataDir = restored
	if h.relationshipRuntime == nil {
		return errors.New("restored target relationship runtime is absent")
	}
	h.relationshipRuntime.DataDir = h.dataDir
	h.relationshipRuntime.Store = h.state
	h.relationshipRuntime.Cache = &observationpublication.Cache{}
	h.relationshipRuntime.InventoryCache = &observationpublication.InventoryCacheV2{}
	if err := recovery.ValidateServiceRuntimeSelections(ctx, h.dataDir, h.state); err != nil {
		return fmt.Errorf("validate restored runtime: %w", err)
	}
	selected, err := h.state.GetServiceRuntimeSelector(ctx, liveRepository)
	if err != nil {
		return err
	}
	if selected != prior {
		return errors.New("restored runtime selector differs from pre-backup authority")
	}
	h.selector = selected
	query, err := querySelectedServices(
		ctx,
		h.state,
		h.selector,
		[]string{firstAcceptedKey(h.catalog)},
		true,
	)
	if err != nil || query.Matches != 1 {
		return errors.Join(errors.New("restored selected reader did not match"), err)
	}
	*cost = query.Cost
	mergePhaseCosts(cost, relationshipLifecycleCost)
	cost.ArchiveArtifactCount = archiveArtifacts
	cost.ArchiveArtifactBytes = archiveBytes
	cost.RestoredArtifactCount = restoreArtifacts
	cost.RestoredArtifactBytes = restoreBytes
	return nil
}

func recoveryManifestCost(manifest recovery.Manifest) (uint64, uint64, error) {
	expectedPaths := [...]string{
		recovery.DatabaseName,
		recovery.FocusedIndexName,
		recovery.ResolverCatalogName,
		recovery.CallerPublicationName,
		recovery.ObservationPublicationName,
		recovery.RelationshipPublicationName,
	}
	if len(manifest.Inventory) != recoveryArtifactCount {
		return 0, 0, errors.New("recovery manifest artifact inventory is incomplete")
	}
	var total uint64
	for index, artifact := range manifest.Inventory {
		if artifact.Path != expectedPaths[index] || artifact.Size <= 0 {
			return 0, 0, errors.New("recovery manifest artifact identity is invalid")
		}
		limit := uint64(1 << 40)
		if artifact.Path == recovery.CallerPublicationName {
			limit = 4 << 40
		}
		size := uint64(artifact.Size)
		if size > limit || total > maxRecoveryArtifactBytes-size {
			return 0, 0, errors.New("recovery manifest artifact bytes exceed the closed bound")
		}
		total += size
	}
	if total == 0 || total > maxRecoveryArtifactBytes {
		return 0, 0, errors.New("recovery manifest byte total is invalid")
	}
	return uint64(len(manifest.Inventory)), total, nil
}

func (h *liveHarness) collection(ctx context.Context, cost *PhaseCost) error {
	_, err := h.drainStalePreimages(ctx, cost, true)
	if err != nil {
		return err
	}
	if cost.PreimageRowsCollected == 0 || cost.PreimageSummariesCollected == 0 {
		return errors.New("collection did not retire any exact sparse preimages")
	}
	query, err := querySelectedServices(
		ctx,
		h.state,
		h.selector,
		[]string{firstAcceptedKey(h.catalog)},
		false,
	)
	if err != nil || query.Matches != 1 {
		return errors.Join(errors.New("selected reader failed after collection"), err)
	}
	mergePhaseCosts(cost, query.Cost)
	return nil
}

// drainStalePreimages runs the public bounded lifecycle only when the
// disposable one-repository store has an older state snapshot. The current
// selector is checked first; production lifecycle rules remain responsible
// for deciding which historical roots are no longer pinned.
func (h *liveHarness) drainStalePreimages(
	ctx context.Context,
	cost *PhaseCost,
	pressureRecovery bool,
) (preimageInventory, error) {
	before, err := h.preimageInventory(ctx)
	if err != nil {
		return preimageInventory{}, err
	}
	if before.rows == 0 && before.summaries == 0 && before.collectingRoots == 0 {
		return before, nil
	}
	selected, err := h.state.GetServiceRuntimeSelector(ctx, liveRepository)
	if err != nil {
		return preimageInventory{}, err
	}
	if selected != h.selector {
		return preimageInventory{}, errors.New("preimage drain selector differs from live authority")
	}
	controller, err := lifecycle.NewController(
		h.state,
		lifecycle.CatalogV3GenerationOwner{
			Store: h.state,
			Acquire: func(context.Context) (func(), error) {
				return func() {}, nil
			},
		},
	)
	if err != nil {
		return preimageInventory{}, err
	}
	addBytes := func(total *uint64, value int64) bool {
		if value < 0 || uint64(value) > ^uint64(0)-*total {
			return false
		}
		*total += uint64(value)
		return true
	}
	validateResult := func(result lifecycle.OwnerResult) error {
		switch {
		case result.Err != nil || result.Owner != lifecycle.CatalogV3Owner:
			return errors.Join(
				fmt.Errorf("run catalog v3 lifecycle owner %q", result.Owner), result.Err,
			)
		case !result.CycleStart || !result.CycleComplete:
			return errors.New("catalog lifecycle did not report its single-owner cycle boundary")
		case result.Deleted < 0 || result.Deleted > lifecycle.MaxDeletesPerTick:
			return errors.New("catalog lifecycle exceeded its per-turn delete bound")
		case result.More && result.Completeness != lifecycle.LowerBound:
			return errors.New("catalog lifecycle backlog was not lower-bound evidence")
		case !result.More && (result.Cursor != "" || result.Completeness != lifecycle.Exact):
			return errors.New("catalog lifecycle terminal turn was not exact")
		case !addBytes(&cost.LifecycleRetiredLogicalBytes, result.LogicalBytes) ||
			!addBytes(&cost.LifecycleDeletedRootBytes, result.RootBytes) ||
			!addBytes(&cost.LifecycleDeletedMemberBytes, result.MemberBytes):
			return errors.New("catalog lifecycle byte cost is invalid")
		}
		cost.LifecycleRecordsDeleted += uint64(result.Deleted)
		return nil
	}
	if !pressureRecovery {
		converged := false
		for range 4_096 {
			result := controller.Tick(ctx)
			if err := validateResult(result); err != nil {
				return preimageInventory{}, err
			}
			if !result.More {
				converged = true
				break
			}
		}
		if !converged {
			return preimageInventory{}, errors.New("catalog lifecycle did not converge within 4,096 bounded turns")
		}
	} else {
		capacityChecks := 0
		gate := lifecycle.NewGateWithProbe(
			h.dataDir,
			func(ctx context.Context, _ string) (lifecycle.Capacity, error) {
				if err := ctx.Err(); err != nil {
					return lifecycle.Capacity{}, err
				}
				capacityChecks++
				used := int64(700)
				if capacityChecks == 1 {
					used = 800
				}
				return lifecycle.Capacity{
					TotalBytes: 1_000, AvailableBytes: 1_000 - used, UsedBytes: used,
				}, nil
			},
		)
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var latest lifecycle.OwnerResult
		var runErr error
		pressureSeen := false
		priorExactNormal := false
		lifecycle.Run(
			runCtx,
			controller,
			gate,
			lifecycle.DefaultIdleInterval,
			lifecycle.DefaultBacklogDelay,
			func(result lifecycle.OwnerResult) {
				latest = result
				cost.LifecycleOwnerTurns++
				if cost.LifecycleOwnerTurns > 4_096 {
					runErr = errors.New("catalog lifecycle did not converge within 4,096 bounded turns")
				} else {
					runErr = validateResult(result)
				}
				if runErr != nil {
					cancel()
				}
			},
			func(capacity lifecycle.Capacity, capacityErr error) {
				if runErr != nil {
					return
				}
				if capacityErr != nil {
					runErr = fmt.Errorf("observe catalog lifecycle capacity: %w", capacityErr)
					cancel()
					return
				}
				switch capacity.Pressure {
				case lifecycle.PressureCollect:
					cost.LifecyclePressureCollectObservations++
					pressureSeen = true
					priorExactNormal = false
				case lifecycle.PressureNormal:
					cost.LifecyclePressureNormalObservations++
					if pressureSeen && priorExactNormal && !latest.More && latest.Err == nil &&
						latest.CycleComplete {
						cancel()
					}
					priorExactNormal = true
				default:
					runErr = fmt.Errorf("catalog lifecycle capacity posture %q is not exact", capacity.Pressure)
					cancel()
				}
			},
		)
		if runErr != nil {
			return preimageInventory{}, runErr
		}
		if err := ctx.Err(); err != nil {
			return preimageInventory{}, err
		}
		if !pressureSeen || cost.LifecyclePressureCollectObservations != 1 ||
			cost.LifecyclePressureNormalObservations < 2 {
			return preimageInventory{}, errors.New("catalog lifecycle did not complete deterministic pressure recovery")
		}
	}
	after, err := h.preimageInventory(ctx)
	if err != nil {
		return preimageInventory{}, err
	}
	if after.rows > before.rows || after.summaries > before.summaries {
		return preimageInventory{}, errors.New("collection increased sparse preimage custody")
	}
	if after.rows != 0 || after.summaries != 0 || after.collectingRoots != 0 {
		return preimageInventory{}, errors.New("collection did not reach a valid referenced-root fixed point")
	}
	cost.PreimageRowsCollected = before.rows - after.rows
	cost.PreimageSummariesCollected = before.summaries - after.summaries
	if cost.PreimageRowsCollected == 0 || cost.PreimageSummariesCollected == 0 {
		return preimageInventory{}, errors.New("collection did not retire any exact sparse preimages")
	}
	return after, nil
}

func (h *liveHarness) finalProductReads(ctx context.Context, cost *PhaseCost) error {
	serviceKey := firstAcceptedKey(h.catalog)
	return h.querySelectedProductTransports(ctx, serviceKey, cost)
}

func (h *liveHarness) validateSelectedSearchResultWithState(
	serviceKey string,
	expectedState servicecatalog.ServiceState,
	result *phebssearch.Result,
) error {
	expectedPaths := make(map[string]struct{})
	for _, membership := range h.catalog.Memberships {
		if membership.ServiceKey == serviceKey {
			expectedPaths[membership.Path] = struct{}{}
		}
	}
	if len(expectedPaths) == 0 || len(expectedPaths) > 10 {
		return errors.New("selected search oracle path set is invalid")
	}
	if result == nil || result.Scope == nil || result.Scope.Authority == nil ||
		result.Scope.Kind != phebssearch.ScopeService ||
		result.Scope.Repository != liveRepository || result.Scope.ServiceKey != serviceKey ||
		len(result.Files) != len(expectedPaths) || result.Stats.FileCount != len(expectedPaths) ||
		result.Stats.MatchCount != len(expectedPaths) ||
		result.Scope.ResultFiles != len(expectedPaths) ||
		result.Scope.ResultMatches != len(expectedPaths) {
		return errors.New("selected production search result set is not exact")
	}
	authority := result.Scope.Authority
	if authority.Repository != liveRepository || authority.ServiceKey != serviceKey ||
		authority.Status != expectedState.Status ||
		authority.Incarnation != expectedState.Incarnation ||
		authority.RevisionCommit != h.commit ||
		authority.CurrentCatalogGeneration != h.selector.CatalogRootDigest ||
		authority.CatalogControlRevision != h.selector.CatalogControlRevision ||
		authority.ActiveCatalogGeneration != expectedState.ActiveCatalogGeneration ||
		authority.ActiveSourceGeneration != expectedState.ActiveSourceGeneration ||
		authority.ActiveDesiredGeneration != expectedState.ActiveDesiredGeneration ||
		authority.ServiceStateDigest != expectedState.StateDigest ||
		authority.ServiceStateRevision != expectedState.ControlRevision ||
		authority.StateSummaryDigest != h.selector.StateSummaryDigest ||
		authority.StateSummaryRevision != h.selector.StateControlRevision ||
		authority.RepositorySearchGeneration != h.selector.SearchGenerationDigest ||
		authority.PathCount != len(expectedPaths) {
		return errors.New("selected production search authority is not exact")
	}
	for _, file := range result.Files {
		if _, ok := expectedPaths[file.Path]; !ok || file.Repo != liveRepository ||
			file.Ref != h.commit {
			return errors.New("selected production search emitted an unexpected citation")
		}
		if err := h.validateFixtureSearchCitation(file); err != nil {
			return err
		}
		delete(expectedPaths, file.Path)
	}
	if len(expectedPaths) != 0 {
		return errors.New("selected production search omitted expected citations")
	}
	return nil
}

func (h *liveHarness) validateFixtureSearchCitation(file phebssearch.FileResult) error {
	const marker = "t411-neutral-fixture-v1"
	index := sort.Search(len(h.corpus.Files), func(index int) bool {
		return h.corpus.Files[index].Path >= file.Path
	})
	if index == len(h.corpus.Files) || h.corpus.Files[index].Path != file.Path {
		return errors.New("selected production search citation is absent from the frozen fixture")
	}
	fixture := h.corpus.Files[index]
	if len(file.Chunks) != 1 || len(file.Chunks[0].Ranges) != 1 {
		return errors.New("selected production search citation shape is not exact")
	}
	chunk := file.Chunks[0]
	firstLine, _, found := strings.Cut(chunk.Content, "\n")
	match := chunk.Ranges[0]
	if !found || chunk.Content != string(fixture.Content) || firstLine != marker ||
		chunk.StartLine != 1 || match.StartLine != 1 || match.StartCol != 1 ||
		match.EndLine != 1 || match.EndCol != len(marker)+1 {
		return errors.New("selected production search citation content or range is not exact")
	}
	return nil
}
