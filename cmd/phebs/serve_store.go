package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// newServeExactScaffolding wires the exact-report and exact-read failure
// latches, report sinks, and accounting state. The archive close is deferred
// through d at the same position the inline defer had.
func newServeExactScaffolding(d *serveDeps) error {
	ctx, cancel := d.ctx, d.cancel
	exact := &serveExact{}
	d.exact = exact
	if d.exactReports {
		exact.reportFailed = make(chan struct{})
		var exactReportFailureOnce sync.Once
		exact.failReport = func(error) {
			exactReportFailureOnce.Do(func() { close(exact.reportFailed) })
			cancel()
		}
	}
	attempts, err := newT422AttemptSinks(exact.failReport)
	if err != nil {
		return err
	}
	exact.attempts = attempts
	if ctx, err = bindT422SourceReports(ctx, exact.failReport); err != nil {
		return err
	}
	if ctx, err = bindT422ObservationReports(ctx, exact.failReport); err != nil {
		return err
	}
	if ctx, err = bindT422IndexReports(ctx, exact.failReport); err != nil {
		return err
	}
	d.ctx = ctx
	if d.exactReads {
		exact.readFailed = make(chan error, 1)
		exact.failRead = func(failure error) {
			if exact.state != nil {
				exact.state.markFailed()
			}
			select {
			case exact.readFailed <- failure:
				cancel()
			default:
			}
		}
		exact.state = t421NewExactReadAccountingState(
			t4013ExactReportSink("exact read accounting: "), exact.failRead,
		)
		exact.state.semantic = d.semanticLaunch
		if d.semanticLaunch != nil {
			d.semanticLaunch.fail = exact.failRead
			if d.semanticLaunch.request.Archive != nil {
				exact.state.archive, err = newT422ArchiveControl(ctx, d.semanticLaunch)
				if err != nil {
					return err
				}
				d.deferFunc(exact.closeArchive)
			}
		}
	}
	exact.reuse, err = newT422ReuseControl(d.semanticLaunch, exact.failReport)
	if err != nil {
		return err
	}
	if exact.state != nil {
		exact.state.reuse = exact.reuse
	}
	return nil
}

// openServeStore creates the data directory, opens the store, and configures
// the caller publication reader. The store close is deferred through d before
// the reader is built, so a reader failure still closes the store.
func openServeStore(d *serveDeps) error {
	cfg := d.cfg
	if err := os.MkdirAll(cfg.Server.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	reportT4013Startup("data_directory_ready")
	st, err := openStoreAfterRetentionWarning(
		func(code string) {
			log.Printf("WARNING: %s", code)
		},
		func() (*store.Surreal, error) {
			open := store.OpenLocalWithConfig
			if d.semanticLaunch != nil && d.semanticLaunch.request.LogicalStoreWork != "" {
				open = store.OpenLocalWithConfigAndBoundedJobClaims
			}
			return open(
				d.ctx,
				cfg.Server.DataDir,
				recovery.ConfigDigest(d.rawConfig),
			)
		},
	)
	if err != nil {
		return err
	}
	reportT4013Startup("store_opened")
	d.st = st
	d.deferFunc(func(retErr *error) {
		*retErr = errors.Join(*retErr, st.Close(context.Background()))
	})
	var callerReader *callerexecute.PublicationReader
	if d.callerRegistry.Enabled() {
		callerReader, err = callerexecute.NewPublicationReader(
			cfg.Server.DataDir, st, d.callerRegistry, d.callerPublications,
		)
		if err != nil {
			return fmt.Errorf("configure caller publication reader: %w", err)
		}
	}
	d.callerReader = callerReader
	return nil
}

// wireServeLifecycle builds the capacity gate, lifecycle owners, publication
// caches, and the lifecycle status monitor.
func wireServeLifecycle(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	exactReadState := d.exact.state
	semanticLaunch := d.semanticLaunch

	d.capacityGate = lifecycle.NewGate(cfg.Server.DataDir)
	d.partitionPublicationRoot = filepath.Join(cfg.Server.DataDir, "extraction-publications")
	d.acquireLifecycleMutation = func(lockCtx context.Context) (func(), error) {
		return focusedindex.AcquireMutationLock(
			lockCtx, filepath.Join(cfg.Server.DataDir, "index"),
		)
	}
	d.acquireObservationTransition = func(lockCtx context.Context) (func(), error) {
		return focusedindex.AcquireExclusiveMutationLock(
			lockCtx, filepath.Join(cfg.Server.DataDir, "index"),
		)
	}
	if err := prepareServeTypedIndex(d); err != nil {
		return err
	}
	releaseCatalogRepair, err := d.acquireLifecycleMutation(ctx)
	if err != nil {
		return fmt.Errorf("acquire catalog v3 startup repair lock: %w", err)
	}
	catalogRepair, repairErr := st.RepairServiceCatalogV3Startup(ctx)
	releaseCatalogRepair()
	if repairErr != nil {
		return fmt.Errorf("repair catalog v3 startup state: %w", repairErr)
	}
	if catalogRepair.More {
		log.Printf(
			"catalog v3 startup orphan backlog: scanned=%d deleted=%d",
			catalogRepair.OrphansScanned, catalogRepair.OrphansDeleted,
		)
	}
	lifecycleOwners := []lifecycle.Owner{
		lifecycle.CatalogGenerationOwner{Store: st, Acquire: d.acquireLifecycleMutation},
		lifecycle.CatalogV3GenerationOwner{Store: st, Acquire: d.acquireLifecycleMutation},
		lifecycle.GenerationOwner{Store: st, Acquire: d.acquireLifecycleMutation},
		lifecycle.JobOwnerImpl{Store: st, Acquire: d.acquireLifecycleMutation},
	}
	d.searchGenerationPins = &focusedindex.SearchGenerationPins{}
	searchGenerationOwner := lifecycle.SearchGenerationOwnerImpl{
		IndexDir: filepath.Join(cfg.Server.DataDir, "index"),
		Pins:     d.searchGenerationPins, Acquire: d.acquireLifecycleMutation,
	}
	lifecycleOwners = append(lifecycleOwners, searchGenerationOwner)
	if semanticLaunch != nil && semanticLaunch.request.SelectorHandoffCleanup != "" && semanticLaunch.request.ServerEpoch <= 3 {
		cleanup, cleanupErr := newT422SelectorCleanupControl(ctx, semanticLaunch, st, d.acquireObservationTransition)
		if cleanupErr != nil {
			return cleanupErr
		}
		exactReadState.selectorCleanup = cleanup
	}
	if semanticLaunch != nil && semanticLaunch.request.ServerEpoch == 1 {
		retention, retentionErr := newT422RetentionControl(ctx, semanticLaunch, searchGenerationOwner, d.searchGenerationPins)
		if retentionErr != nil {
			return retentionErr
		}
		exactReadState.retention = retention
		d.retention = retention
		d.deferFunc(func(*error) { d.stopBackground(); retention.Close() })
	}
	d.observationCache = &observationpublication.Cache{}
	d.observationInventoryCache = &observationpublication.InventoryCacheV2{}
	d.relationshipCache = &relationshippublication.Cache{}
	d.relationshipV3Cache = &relationshippublication.CacheV3{}
	lifecycleOwners = append(lifecycleOwners, lifecycle.ObservationGenerationOwner{
		Root: filepath.Join(cfg.Server.DataDir, "observations"),
		Pins: d.observationCache, Acquire: d.acquireLifecycleMutation,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.ObservationInventoryOwnerV2{
		Root:    filepath.Join(cfg.Server.DataDir, "observations"),
		Pins:    observationpublication.InventoryPinsV2{Cache: d.observationInventoryCache},
		Acquire: d.acquireLifecycleMutation,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.RelationshipGenerationOwner{
		DataDir: cfg.Server.DataDir, Pins: d.relationshipCache,
		AcquireExclusive: d.acquireObservationTransition, Store: st,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.RelationshipGenerationOwnerV3{
		DataDir: cfg.Server.DataDir, Pins: d.relationshipV3Cache,
		AcquireExclusive: d.acquireObservationTransition, Store: st,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.ExtractionStageOwner{
		Root: d.partitionPublicationRoot, Acquire: d.acquireLifecycleMutation,
	})
	lifecycleOwners = append(lifecycleOwners, lifecycle.ClosedOwners()...)
	if d.typedRuntime != nil {
		lifecycleOwners = append(lifecycleOwners, d.typedRuntime)
	}
	newLifecycleStatus := lifecycle.NewStatusMonitor
	if semanticLaunch != nil && (semanticLaunch.request.ServerEpoch == 4 || semanticLaunch.request.ServerEpoch == 5) {
		newLifecycleStatus = lifecycle.NewSelectedCleanupStatusMonitor
	}
	lifecycleStatus, lifecycleErr := newLifecycleStatus(
		cfg.Lifecycle.EnabledFor(), lifecycleOwners,
	)
	if lifecycleErr != nil {
		return fmt.Errorf("configure lifecycle status: %w", lifecycleErr)
	}
	d.lifecycleOwners = lifecycleOwners
	d.lifecycleStatus = lifecycleStatus
	return nil
}

// recoverServePublications wires the observation and relationship publication
// runtimes and replays their startup recovery.
func recoverServePublications(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	reuseControl := d.exact.reuse

	observationRuntime := &observationpublication.Runtime{
		DataDir: cfg.Server.DataDir, Store: st, Cache: d.observationCache,
		AcquireTransition: d.acquireObservationTransition,
		InventoryV2:       true,
		Admit: func(admitCtx context.Context) error {
			capacity, admissionErr := d.capacityGate.Check(admitCtx, 0)
			d.lifecycleStatus.ObserveCapacity(capacity, admissionErr)
			return admissionErr
		},
	}
	if reuseControl != nil {
		observationRuntime.OnPlanningCurrent = reuseControl.observeObservation
	}
	var relationshipRuntime *relationshippublication.Runtime
	var reconcileRelationship func(context.Context, string) error
	if d.resolverRegistry.Enabled() {
		relationshipDomains := make([]downstreamauthority.DomainIdentity, 0, len(d.exs))
		for _, extractor := range d.exs {
			relationshipDomains = append(relationshipDomains, downstreamauthority.DomainIdentity{
				Domain: extractor.Domain(), Version: extractor.Version(),
			})
		}
		relationshipRuntime = &relationshippublication.Runtime{
			DataDir: cfg.Server.DataDir, Store: st, Cache: d.observationCache,
			InventoryCache: d.observationInventoryCache, Domains: relationshipDomains,
			Acquire: d.acquireLifecycleMutation,
			Admit: func(admitCtx context.Context) error {
				capacity, admissionErr := d.capacityGate.Check(admitCtx, 0)
				d.lifecycleStatus.ObserveCapacity(capacity, admissionErr)
				return admissionErr
			},
		}
		if reuseControl != nil {
			relationshipRuntime.OnV3Current = reuseControl.observeRelationship
		}
		reconcileRelationship = func(reconcileCtx context.Context, repository string) error {
			err := relationshipRuntime.Reconcile(reconcileCtx, repository)
			if errors.Is(err, relationshippublication.ErrNotFound) {
				diagnostics.Logf(
					"relationship authority not ready: repository=%q error=%v",
					repository, err,
				)
				return nil
			}
			return err
		}
		observationRuntime.OnPublished = reconcileRelationship
	}
	if len(d.exs) > 0 {
		observationAfterPublication := observationRuntime.OnPublished
		observationRuntime.OnPublished = func(
			publishedCtx context.Context,
			repository string,
		) error {
			return afterObservationPublication(
				publishedCtx,
				repository,
				legacyRelationshipReconcileFor(
					repository, cfg.ServiceCatalogs, observationAfterPublication,
				),
				st.EnqueuePending,
			)
		}
	}
	d.observationRuntime = observationRuntime
	d.relationshipRuntime = relationshipRuntime
	d.reconcileRelationship = reconcileRelationship

	releaseObservationRecovery, recoveryLockErr := d.acquireObservationTransition(ctx)
	if recoveryLockErr != nil {
		return fmt.Errorf("acquire observation v2 startup recovery lock: %w", recoveryLockErr)
	}
	observationV2Recovery, observationV2RecoveryErr :=
		observationpublication.RecoverInventoryPublicationsV2(
			ctx, filepath.Join(cfg.Server.DataDir, "observations"),
		)
	relationshipRecovery, relationshipRecoveryErr := relationshippublication.RecoverAll(
		ctx, cfg.Server.DataDir, st,
	)
	releaseObservationRecovery()
	if observationV2RecoveryErr != nil {
		return fmt.Errorf("recover observation v2 publications: %w", observationV2RecoveryErr)
	}
	if observationV2Recovery.Repositories > 0 {
		diagnostics.Logf(
			"observation v2 recovery: repositories=%d completed=%d incomplete=%d",
			observationV2Recovery.Repositories, observationV2Recovery.Completed,
			observationV2Recovery.Incomplete,
		)
	}
	if relationshipRecoveryErr != nil {
		return fmt.Errorf("recover relationship publications: %w", relationshipRecoveryErr)
	}
	if relationshipRecovery.Repositories > 0 {
		diagnostics.Logf(
			"relationship recovery: repositories=%d completed=%d unavailable=%d invalid=%d",
			relationshipRecovery.Repositories, relationshipRecovery.Completed,
			relationshipRecovery.Unavailable, relationshipRecovery.Invalid,
		)
	}

	releaseExtractionStageRecovery, extractionStageRecoveryLockErr :=
		d.acquireLifecycleMutation(ctx)
	if extractionStageRecoveryLockErr != nil {
		return fmt.Errorf("lock extraction publication stage recovery: %w", extractionStageRecoveryLockErr)
	}
	var extractionStageRecovery extractionpublication.StageRecoveryReport
	var extractionStageRecoveryErr error
	func() {
		defer releaseExtractionStageRecovery()
		extractionStageRecovery, extractionStageRecoveryErr =
			extractionpublication.RecoverStages(ctx, d.partitionPublicationRoot)
	}()
	if extractionStageRecoveryErr != nil {
		return fmt.Errorf("recover extraction publication stages: %w", extractionStageRecoveryErr)
	}
	if extractionStageRecovery.Repositories > 0 {
		diagnostics.Logf(
			"extraction stage recovery: repositories=%d retired=%d work=%d",
			extractionStageRecovery.Repositories, extractionStageRecovery.Retired,
			extractionStageRecovery.Work,
		)
	}

	reportT4013Startup("authority_recovery_complete")
	return nil
}

// startServeLifecycleController builds the lifecycle maintenance controller
// and the exact-mode lifecycle control surface.
func startServeLifecycleController(d *serveDeps) error {
	cfg := d.cfg
	st := d.st
	if cfg.Lifecycle.EnabledFor() {
		lifecycleController, lifecycleErr := lifecycle.NewController(
			st, d.lifecycleOwners...,
		)
		if lifecycleErr != nil {
			return fmt.Errorf("configure lifecycle maintenance: %w", lifecycleErr)
		}
		d.lifecycleController = lifecycleController
	}
	if d.semanticLaunch != nil {
		if d.lifecycleController == nil {
			return errT422LifecycleControl
		}
		lifecycleControl, err := newT422LifecycleControl(d.ctx, d.semanticLaunch, d.lifecycleOwners)
		if err != nil {
			return err
		}
		if err = lifecycleControl.bindWorkspaceBytes(st); err != nil {
			return err
		}
		d.lifecycleControl = lifecycleControl
		d.lifecycleRunnerControl = lifecycleControl.runner
		d.exact.state.lifecycle = lifecycleControl
	}
	return nil
}
