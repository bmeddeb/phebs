package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/indexer"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

func configureT422SelectedJobLeases(selected bool, runners ...*store.Runner) {
	if !selected {
		return
	}
	for _, runner := range runners {
		if runner != nil {
			runner.HeartbeatEvery = 15 * time.Second
			runner.StaleAfter = 60 * time.Second
		}
	}
}

// newServeAuth wires the audit recorder, the auth service, and the audit/
// analytics retention sweep.
func newServeAuth(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	owners := d.startup.Owners()

	// T10.1: one audit recorder feeds the auth surface and the huma middleware.
	// The actor comes from the request principal when the caller did not
	// already resolve it; recording failures never fail the request.
	auditRecord := func(ctx context.Context, event store.AuditEvent) {
		if principal, ok := auth.PrincipalFromContext(ctx); ok {
			if event.ActorID == "" && principal.User != nil {
				event.ActorID, event.ActorEmail = principal.User.ID, principal.User.Email
			}
			if event.APIKeyID == "" {
				event.APIKeyID = principal.APIKeyID
			}
			if event.AuthMethod == "" {
				event.AuthMethod = principal.AuthMethod
			}
		}
		// The action already completed; a client disconnect must not lose it.
		if err := st.AppendAuditEvent(context.WithoutCancel(ctx), event); err != nil {
			log.Printf("audit: %v", err)
		}
	}
	d.auditRecord = auditRecord

	authService, err := auth.New(ctx, auth.Options{Config: cfg.Auth, Store: st, Audit: auditRecord, Owners: owners})
	if err != nil {
		return err
	}
	d.authService = authService
	if owners != nil {
		d.deferFunc(func(*error) {
			d.cancel()
			authService.WaitCleanup()
		})
	}
	// T10.1/T10.2 retention sweep: boot, then twice a day
	auditRetention, usageRetention := cfg.Audit.RetentionFor(), cfg.Analytics.RetentionFor()
	if auditRetention > 0 || usageRetention > 0 {
		d.runBackground(func() {
			ticker := time.NewTicker(12 * time.Hour)
			defer ticker.Stop()
			for {
				turn, err := owners.Enter(ctx)
				if err != nil {
					return
				}
				sweep := func(name string, keep time.Duration, prune func(context.Context, time.Time) (int, error)) {
					if keep <= 0 {
						return
					}
					if n, err := prune(ctx, time.Now().UTC().Add(-keep)); err != nil {
						log.Printf("%s retention: %v", name, err)
					} else if n > 0 {
						log.Printf("%s retention: pruned %d event(s)", name, n)
					}
				}
				sweep("audit", auditRetention, st.PruneAuditEvents)
				sweep("analytics", usageRetention, st.PruneUsageEvents)
				turn.End()
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	}
	if setupToken := authService.SetupToken(); setupToken != "" {
		log.Printf("first-run setup token: %s", setupToken)
	}
	return nil
}

// reconcileServeState replays the startup reconciliation seam: connection
// pruning, abandoned stage cleanup, catalog/publication reconciliation, and
// the artifact trust-boundary reconciliation.
func reconcileServeState(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st

	// sync pipeline: prune membership of dropped connections, enqueue boot
	// syncs, run one jittered poller
	names := make([]string, 0, len(cfg.Connections))
	for _, c := range cfg.Connections {
		names = append(names, c.Name)
	}
	if err := st.PruneConnections(ctx, names); err != nil {
		return fmt.Errorf("prune connections: %w", err)
	}
	// Stages have no committed owner after process restart. Clean them only at
	// this startup seam, before any candidate worker can be active; runtime
	// orphan reconciliation must never race an in-progress plan.
	if removed, err := candidate.CleanupStages(
		ctx, candidatejob.CandidateRoot(cfg.Server.DataDir),
	); err != nil {
		return fmt.Errorf("cleanup abandoned candidate stages: %w", err)
	} else if removed > 0 {
		log.Printf("candidate reconciliation: removed %d abandoned stage(s)", removed)
	}
	if removed, err := callerleaf.CleanupStages(
		ctx, callerexecute.Root(cfg.Server.DataDir),
	); err != nil {
		return fmt.Errorf("cleanup abandoned caller-leaf stages: %w", err)
	} else if removed > 0 {
		log.Printf("caller-leaf reconciliation: removed %d abandoned stage(s)", removed)
	}
	catalogReport, err := resolvercatalog.Reconcile(
		ctx, filepath.Join(cfg.Server.DataDir, "resolver-catalogs"), st,
		d.resolverRegistry.Packs(),
	)
	if err != nil {
		return fmt.Errorf("resolver catalog reconciliation: %w", err)
	}
	if catalogReport.StagesRemoved+catalogReport.MarkersRecovered+
		catalogReport.PublicationsCurrent+catalogReport.ReplacementsQueued+
		catalogReport.PointersCleared+catalogReport.OrphansObserved > 0 {
		log.Printf(
			"resolver catalog reconciliation: stages_removed=%d markers_recovered=%d current=%d replacements_queued=%d pointers_cleared=%d orphans=%d",
			catalogReport.StagesRemoved, catalogReport.MarkersRecovered,
			catalogReport.PublicationsCurrent, catalogReport.ReplacementsQueued,
			catalogReport.PointersCleared, catalogReport.OrphansObserved,
		)
	}
	if !d.callerRegistry.Enabled() {
		if err := callerpublication.ReconcileDeletionMarkers(
			ctx, callerexecute.Root(cfg.Server.DataDir),
			st.CallerPublicationRepositoryEligible,
		); err != nil {
			return fmt.Errorf("caller publication deletion reconciliation: %w", err)
		}
	}
	if d.callerRegistry.Enabled() {
		callerReport, err := callerexecute.ReconcilePublications(
			ctx, cfg.Server.DataDir, st, d.callerRegistry, d.callerPublications,
		)
		if err != nil {
			return fmt.Errorf("caller publication reconciliation: %w", err)
		}
		if callerReport.StagesRemoved+callerReport.MarkersRecovered+
			callerReport.PublicationsCurrent+callerReport.ReplacementsQueued+
			callerReport.PointersCleared+callerReport.OrphansObserved > 0 {
			log.Printf(
				"caller publication reconciliation: stages_removed=%d markers_recovered=%d current=%d replacements_queued=%d pointers_cleared=%d orphans=%d",
				callerReport.StagesRemoved, callerReport.MarkersRecovered,
				callerReport.PublicationsCurrent, callerReport.ReplacementsQueued,
				callerReport.PointersCleared, callerReport.OrphansObserved,
			)
		}
	}
	report, reconcileErr := phebssync.ReconcileArtifactsWithCallerLifecycle(
		ctx, st, cfg.Server.DataDir, cfg.Sync.CleanupOrphans,
		d.callerPublications,
	)
	if reconcileErr != nil {
		// Reconciliation establishes the artifact/search trust boundary. A
		// failed quarantine, revision clear, or credential scrub must not leave
		// the server running against state it could not prove safe.
		return fmt.Errorf("artifact reconciliation: %w", reconcileErr)
	}
	if report.OrphanRepos+report.UntrackedShards+report.UntrackedMirrors+
		report.UntrackedCandidates+report.CredentialsFixed+report.InvalidRepos+
		report.RevisionRepairs+report.LifecycleArtifacts > 0 {
		log.Printf("artifact reconciliation: orphans=%d shards=%d mirrors=%d candidates=%d credentials_scrubbed=%d invalid_repos=%d revision_repairs=%d lifecycle=%d deleted=%d",
			report.OrphanRepos, report.UntrackedShards, report.UntrackedMirrors,
			report.UntrackedCandidates, report.CredentialsFixed,
			report.InvalidRepos, report.RevisionRepairs, report.LifecycleArtifacts,
			report.Deleted)
	}

	reportT4013Startup("artifact_recovery_complete")
	return nil
}

// reconcileServeCatalogs reconciles analysis units and service catalogs and
// wires the service runtime controller with its epoch-gated controls.
func reconcileServeCatalogs(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	exactReadState := d.exact.state
	semanticLaunch := d.semanticLaunch

	analysisUnits := cfg.AnalysisUnitScopes()
	d.analysisUnits = analysisUnits
	unitRepositories := make([]string, 0, len(analysisUnits))
	for repository := range analysisUnits {
		unitRepositories = append(unitRepositories, repository)
	}
	sort.Strings(unitRepositories)
	for _, repository := range unitRepositories {
		state, stateErr := analysisUnits[repository].State()
		if stateErr != nil {
			return fmt.Errorf("analysis unit %s: %w", repository, stateErr)
		}
		logAnalysisUnitPosture(repository, state, d.exs)
	}
	if len(unitRepositories) == 0 {
		logAnalysisUnitPosture("", nil, d.exs)
	}
	if queued, err := indexer.ReconcileAnalysisUnits(ctx, st, analysisUnits); err != nil {
		return fmt.Errorf("reconcile analysis units: %w", err)
	} else if queued > 0 {
		log.Printf("analysis unit reconciliation: queued %d index rebuild(s)", queued)
	}
	catalogReconciler := &servicecatalogingest.Reconciler{
		DataDir: cfg.Server.DataDir, Store: st,
		Selections: legacyServiceCatalogSelections(cfg.ServiceCatalogs),
	}
	v3CatalogReconciler := &servicecatalogingest.V3Reconciler{
		DataDir: cfg.Server.DataDir, Store: st,
		Selections: cfg.ServiceCatalogs,
	}
	if d.exact.reuse != nil {
		v3CatalogReconciler.OnCurrent = d.exact.reuse.observeCatalog
	}
	if semanticLaunch != nil {
		v3CatalogReconciler.RequiredIndexedCommit = semanticLaunch.request.ReturnSourceCommit
	}
	serviceRuntime := newServiceRuntimeController(
		cfg.Server.DataDir, st, cfg.ServiceCatalogs, v3CatalogReconciler,
		d.relationshipRuntime, d.acquireLifecycleMutation, d.searchGenerationPins,
		d.relationshipCache, d.relationshipV3Cache,
	)
	d.catalogReconciler = catalogReconciler
	d.v3CatalogReconciler = v3CatalogReconciler
	d.serviceRuntime = serviceRuntime
	if semanticLaunch != nil && semanticLaunch.request.ServerEpoch == 2 {
		activationControl, err := newT422ActivationControl(ctx, semanticLaunch, serviceRuntime)
		if err != nil {
			return err
		}
		d.activationControl = activationControl
		d.deferFunc(func(*error) { activationControl.cancel() })
		exactReadState.activation = activationControl
	}
	if semanticLaunch != nil && semanticLaunch.request.ServerEpoch == 3 {
		markerControl, err := newT422MarkerControl(ctx, semanticLaunch, d.relationshipRuntime, serviceRuntime, d.acquireObservationTransition)
		if err != nil {
			return err
		}
		d.markerControl = markerControl
		exactReadState.marker = markerControl
		markerControl.workspace = d.lifecycleControl.markerWorkspace
	}
	catalogReconciler.WithMutation = serviceRuntime.withV2Mutation
	d.deferFunc(func(*error) { d.stopBackground(); serviceRuntime.Close() })
	catalogReconciler.OnPublished = func(
		publishedCtx context.Context,
		repository string,
	) error {
		var relationshipErr error
		if d.relationshipRuntime != nil {
			relationshipErr = afterServiceCatalogPublication(
				publishedCtx, repository, d.reconcileRelationship,
				st.EnqueuePending,
				candidatePublicationPresent(cfg.Server.DataDir, repository),
			)
		}
		return errors.Join(
			relationshipErr,
			serviceRuntime.Advance(publishedCtx, repository),
		)
	}
	serviceCatalogReport, err := catalogReconciler.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("reconcile service catalogs: %w", err)
	}
	if serviceCatalogReport.Current+serviceCatalogReport.Published+
		serviceCatalogReport.LegacyImported+serviceCatalogReport.NotReady+
		serviceCatalogReport.Unselected+len(serviceCatalogReport.Failures) > 0 {
		diagnostics.Logf(
			"service catalog reconciliation: current=%d published=%d legacy_imported=%d not_ready=%d unselected=%d failed=%d",
			serviceCatalogReport.Current, serviceCatalogReport.Published,
			serviceCatalogReport.LegacyImported, serviceCatalogReport.NotReady,
			serviceCatalogReport.Unselected, len(serviceCatalogReport.Failures),
		)
	}
	for _, failure := range serviceCatalogReport.Failures {
		diagnostics.Logf(
			"service catalog reconciliation failed: repository=%q error=%v",
			failure.Repository, failure.Err,
		)
	}
	serviceRepositories := make(map[string]struct{},
		len(cfg.ServiceCatalogs)+len(analysisUnits))
	for repository := range cfg.ServiceCatalogs {
		serviceRepositories[repository] = struct{}{}
	}
	for repository := range analysisUnits {
		serviceRepositories[repository] = struct{}{}
	}
	serviceNames := make([]string, 0, len(serviceRepositories))
	for repository := range serviceRepositories {
		serviceNames = append(serviceNames, repository)
	}
	sort.Strings(serviceNames)
	for _, repository := range serviceNames {
		if selection, configured := cfg.ServiceCatalogs[repository]; configured {
			// Reconciler.OnPublished already advanced the selected runtime under
			// its transition fence for v2. V3 deliberately bypasses the legacy
			// 4,000-service decoder and starts through its own reconciler.
			if selection.RuntimeVersion() == config.ServiceCatalogRuntimeV3 {
				if err := serviceRuntime.Advance(ctx, repository); err != nil {
					diagnostics.Logf(
						"service runtime v3 reconciliation unavailable: repository=%q error=%v",
						repository, err,
					)
				}
			}
			continue
		}
		outcome, reconcileErr := reconcileServiceSearchGeneration(
			ctx, st, cfg.Server.DataDir, repository,
		)
		if reconcileErr != nil {
			diagnostics.Logf(
				"service search reconciliation unavailable: repository=%q error=%v",
				repository, reconcileErr,
			)
			continue
		}
		if outcome.Activated > 0 {
			diagnostics.Logf(
				"service search reconciliation: repository=%q activated=%d search_generation=%s",
				repository, outcome.Activated, outcome.Search.Digest,
			)
		}
	}
	if err := serviceRuntime.PinSelections(ctx); err != nil {
		return fmt.Errorf("validate and pin selected service runtimes: %w", err)
	}
	return nil
}

// startServeSyncRunners starts the lifecycle maintenance loop and the sync/
// fetch job runners.
func startServeSyncRunners(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	owners := d.startup.Owners()

	if d.lifecycleController != nil {
		d.runBackground(func() {
			lifecycle.RunWithControl(
				ctx, d.lifecycleController, d.capacityGate,
				lifecycle.DefaultIdleInterval, lifecycle.DefaultBacklogDelay,
				func(result lifecycle.OwnerResult) {
					if d.lifecycleControl != nil {
						d.lifecycleControl.ObserveOwner(result)
					}
					d.lifecycleStatus.ObserveOwner(result)
					if result.Err != nil {
						diagnostics.Logf(
							"lifecycle owner=%q completeness=%s: %v",
							result.Owner, result.Completeness, result.Err,
						)
					} else if result.Deleted > 0 {
						diagnostics.Logf(
							"lifecycle owner=%q completeness=%s scanned=%d deleted=%d backlog=%t",
							result.Owner, result.Completeness, result.Scanned,
							result.Deleted, result.More,
						)
					}
				},
				d.lifecycleStatus.ObserveCapacity,
				owners,
				d.lifecycleRunnerControl,
			)
		})
	}
	if err := phebssync.EnqueueMissing(ctx, st, cfg); err != nil {
		return fmt.Errorf("enqueue sync jobs: %w", err)
	}
	runner := &store.Runner{Store: st, Kind: store.JobSync, Owners: owners,
		Handle: phebssync.HandlerWithLifecycles(
			cfg, st, d.callerPublications, d.serviceRuntime,
		),
		Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
	fetchRunner := &store.Runner{Store: st, Kind: store.JobFetch, Handle: phebssync.FetchHandler(cfg, st), Owners: owners,
		Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
	bindT4013ExactReports(d.exactReports, d.exact.failReport, nil, runner, fetchRunner)
	configureT422SelectedJobLeases(d.semanticLaunch != nil, runner, fetchRunner)
	d.exact.attempts.bindJobs(runner, fetchRunner)
	runStoreRunner(ctx, d.runBackground, runner)
	runStoreRunner(ctx, d.runBackground, fetchRunner)
	if watched := phebssync.Watched(cfg); len(watched) > 0 {
		log.Printf("watch mode: polling %d local repo(s)", len(watched))
		d.runBackground(func() {
			(&phebssync.Watcher{Store: st, Conns: watched, Revisions: cfg.Revisions, Owners: owners}).Run(ctx)
		})
	}
	// T7.5: periodic freshness for remote connections
	if every := cfg.Sync.ResyncEvery(); every > 0 {
		d.runBackground(func() { phebssync.ResyncWithOwners(ctx, st, cfg, every, owners) })
	}
	return nil
}

// startServeObservationSchedulers wires the on-indexed chain and starts the
// observation and relationship generation schedulers.
func startServeObservationSchedulers(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	owners := d.startup.Owners()

	// Candidate planning and extraction are independent queue consumers: they
	// must drain boot backfill even when this binary cannot start new zoekt
	// index children. Each runner processes repositories serially, bounding
	// Git/parser resource use at this integration seam.
	if len(cfg.ServiceCatalogs)+len(d.analysisUnits) > 0 {
		d.onIndexed = func(ctx context.Context, repository, _ string) error {
			selection, selected := cfg.ServiceCatalogs[repository]
			if !selected {
				if _, legacy := d.analysisUnits[repository]; !legacy {
					return nil
				}
			}
			if selected && selection.RuntimeVersion() == config.ServiceCatalogRuntimeV3 {
				return d.serviceRuntime.Advance(ctx, repository)
			}
			if _, err := d.catalogReconciler.ReconcileRepository(ctx, repository); err != nil {
				return err
			}
			if _, configured := cfg.ServiceCatalogs[repository]; configured {
				// The catalog callback already advanced the selected runtime.
				return nil
			}
			_, err := reconcileServiceSearchGeneration(
				ctx, st, cfg.Server.DataDir, repository,
			)
			return err
		}
	}
	catalogAfterIndex := d.onIndexed
	d.onIndexed = chainObservationPlanningAfterIndex(
		catalogAfterIndex,
		func(repository string) bool {
			_, focused := d.analysisUnits[repository]
			return focused
		},
		d.observationRuntime.EnqueuePlanning,
		func(disposition observationpublication.PlanningEnqueue) {
			diagnostics.Logf(
				"observation planning: disposition=%s", disposition,
			)
		},
	)
	if repositories, listErr := st.ListRepos(ctx); listErr != nil {
		return fmt.Errorf("list repositories for observation recovery: %w", listErr)
	} else {
		summary, recoveryErr := enqueueObservationPlanningStartup(
			ctx, repositories, d.observationRuntime.EnqueuePlanning,
		)
		if recoveryErr != nil {
			return recoveryErr
		}
		if summary.total() > 0 {
			diagnostics.Logf(
				"observation planning recovery: current=%d active=%d failed=%d enqueued=%d unavailable=%d",
				summary.Current, summary.Active, summary.Failed,
				summary.Enqueued, summary.Unavailable,
			)
		}
	}
	observationScheduler := &generationscheduler.Scheduler{
		Store: st, Owners: owners,
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{
			store.GenerationResourceIO: {
				Concurrency: observationIOConcurrency,
				Budget: generationscheduler.Budget{
					MaxMemoryBytes: 256 << 20, MaxDescriptors: 8,
				},
				Handle: func(workerCtx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
					switch chunk.Stage {
					case observationpublication.PlanningScheduleStage:
						return d.observationRuntime.HandlePlanning(workerCtx, chunk)
					case observationpublication.InventoryScheduleStageV2:
						return d.observationRuntime.HandleInventoryV2(workerCtx, chunk)
					default:
						return store.WithTerminal(errors.New("unknown observation IO stage"))
					}
				},
			},
			store.GenerationResourceCPU: {
				Concurrency: observationCPUConcurrency,
				Budget: generationscheduler.Budget{
					MaxMemoryBytes: 256 << 20, MaxDescriptors: 8,
				},
				Handle: func(workerCtx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
					switch chunk.Stage {
					case store.ServiceStateV3ReconcileStage,
						store.ServiceStateV3ActivateStage:
						_, err := d.serviceRuntime.ProcessServiceStateV3Chunk(workerCtx, chunk)
						return err
					default:
						return d.observationRuntime.Handle(workerCtx, chunk)
					}
				},
			},
		},
		PollEvery: time.Second, WorkerPrefix: "observation-worker",
		Report: func(err error) {
			diagnostics.Logf("observation scheduler unavailable: %v", err)
		},
	}
	bindT422ExactChunkReports(d.exactReads, d.exact.failRead, observationScheduler)
	d.exact.attempts.bindChunk(observationScheduler)
	if d.activationControl != nil {
		class := observationScheduler.Classes[store.GenerationResourceCPU]
		class.ControlledRelease = d.activationControl.controlledRelease
		observationScheduler.Classes[store.GenerationResourceCPU] = class
	}
	d.runBackground(func() {
		if err := observationScheduler.Run(ctx); err != nil && ctx.Err() == nil {
			diagnostics.Logf("observation scheduler stopped: %v", err)
		}
	})
	if d.relationshipRuntime != nil {
		var markerMeasurement func(context.Context, store.GenerationChunk) bool
		if d.markerControl != nil && d.markerControl.workspace != nil {
			markerMeasurement = d.markerControl.measurementSelected
		}
		relationshipScheduler := &generationscheduler.Scheduler{
			Store: st, Owners: owners,
			Classes: map[store.GenerationResourceClass]generationscheduler.Class{
				store.GenerationResourceMemory: {
					MarkerMeasurement: markerMeasurement,
					Concurrency:       relationshipConcurrency,
					Budget: generationscheduler.Budget{
						MaxMemoryBytes: 1 << 30, MaxDescriptors: 32,
					},
					Handle: func(workerCtx context.Context, chunk store.GenerationChunk, _ generationscheduler.Budget) error {
						var err error
						if chunk.Stage == relationshippublication.ScheduleStageV3 {
							if d.markerControl != nil {
								return d.markerControl.handleInEpoch(workerCtx, chunk)
							}
							err = d.relationshipRuntime.HandleV3(workerCtx, chunk)
						} else {
							err = d.relationshipRuntime.Handle(workerCtx, chunk)
						}
						if err == nil {
							err = d.serviceRuntime.Advance(workerCtx, chunk.Repository)
						}
						return err
					},
				},
			},
			PollEvery: time.Second, WorkerPrefix: "relationship-worker",
			Report: func(err error) {
				diagnostics.Logf("relationship scheduler unavailable: %v", err)
			},
		}
		bindT422ExactChunkReports(d.exactReads, d.exact.failRead, relationshipScheduler)
		d.exact.attempts.bindChunk(relationshipScheduler)
		d.runBackground(func() {
			if err := relationshipScheduler.Run(ctx); err != nil && ctx.Err() == nil {
				diagnostics.Logf("relationship scheduler stopped: %v", err)
			}
		})
	}
	return nil
}

// startServeExtractionPipeline wires the candidate/extraction/partition
// pipeline: compatibility selection, candidate worker, partitioned extraction
// runtime and reconciler, epoch-gated stale/checkpoint controls, and the
// resolver/caller downstream runners.
func startServeExtractionPipeline(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	owners := d.startup.Owners()
	exactReadState := d.exact.state
	semanticLaunch := d.semanticLaunch
	analysisUnits := d.analysisUnits

	var evidenceView store.EvidenceStore
	var proofBundles store.ProofBundleStore
	var compatibility compat.Service
	var partitionRuntime *extractionpublication.Runtime
	var manifestProvider *candidatejob.Provider
	var openPartitionDomain func(
		context.Context, candidate.DomainResultPlan,
	) (*candidate.SparseDomain, error)
	if len(d.exs) == 0 {
		return nil
	}
	if cfg.Experimental.ProvisionalProtoExtraction {
		log.Print("WARNING: experimental provisional protobuf extraction enabled; T11.1/T12.3 validation is not established")
	}
	if cfg.Experimental.ProvisionalThriftExtraction {
		log.Print("WARNING: experimental provisional thrift extraction enabled; validation is the T19.1 rule-gate spike only")
	}
	if cfg.Experimental.ProvisionalThriftFieldExtraction {
		log.Print("WARNING: experimental provisional Thrift field extraction enabled; validation is the T22.1 rule-gate spike only")
	}
	if cfg.Experimental.ProvisionalKafkaExtraction {
		log.Print("WARNING: experimental provisional kafka extraction enabled; validation is the T23.1 rule-gate spike only and topic evidence is abstention-dominant by design")
	}
	evidenceView = st
	proofBundles = st
	selectedCompatibility, compatibilityErr := initializeCompatibilityForLaunch(ctx, semanticLaunch)
	if compatibilityErr != nil {
		log.Printf("WARNING: %v", compatibilityErr)
	}
	compatibility = selectedCompatibility
	candidateWorker, provider, err := candidatejob.New(
		cfg.Server.DataDir, st, d.exs,
	)
	if err != nil {
		return fmt.Errorf("configure candidate planning: %w", err)
	}
	manifestProvider = provider
	worker := &extract.Worker{
		Repos: st, Evidence: st,
		NewCorpus:        extract.GitCorpus(cfg.Server.DataDir),
		Manifests:        manifestProvider,
		Extractors:       d.exs,
		Diagnostics:      cfg.Diagnostics.Extraction,
		ExtractorDetails: cfg.Diagnostics.ExtractorDetails,
	}
	openPartitionCandidate := func(
		openCtx context.Context,
		repository string,
	) (*candidate.Publication, error) {
		var unit *analysisunit.State
		if scope, configured := analysisUnits[repository]; configured {
			state, stateErr := scope.State()
			if stateErr != nil {
				return nil, stateErr
			}
			unit = state
		}
		return manifestProvider.OpenCurrentPublication(openCtx, repository, unit)
	}
	readPartitionCandidateReference := func(
		referenceCtx context.Context,
		repository string,
	) (candidate.State, error) {
		var unit *analysisunit.State
		if scope, configured := analysisUnits[repository]; configured {
			state, stateErr := scope.State()
			if stateErr != nil {
				return candidate.State{}, stateErr
			}
			unit = state
		}
		return manifestProvider.CurrentPublicationState(referenceCtx, repository, unit)
	}
	readPartitionAuthority := func(
		authorityCtx context.Context,
		state candidate.State,
	) (string, string, error) {
		authority, authorityErr := observationpublication.CurrentInventoryAuthorityV2(
			authorityCtx, filepath.Join(cfg.Server.DataDir, "observations"), state.Repository,
		)
		if authorityErr != nil {
			return "", "", authorityErr
		}
		return partitionAuthorityForCandidate(authorityCtx, filepath.Join(cfg.Server.DataDir, "index"), state, authority)
	}
	readPartitionFenceAuthority := func(
		authorityCtx context.Context,
		state candidate.State,
	) (string, string, error) {
		return partitionFenceAuthority(
			authorityCtx, filepath.Join(cfg.Server.DataDir, "observations"), filepath.Join(cfg.Server.DataDir, "index"), state,
		)
	}
	partitionRuntime = &extractionpublication.Runtime{
		Root: d.partitionPublicationRoot, Store: st,
		Executor: &extract.EvidencePartitionExecutor{
			Evidence: st, Extractors: d.exs, StoreAccounting: semanticLaunch != nil,
		},
		Publisher:   extractionpublication.StorePublisher{Store: st},
		Diagnostics: cfg.Diagnostics.Extraction,
	}
	partitionDomains := make([]string, len(d.exs))
	for index, extractor := range d.exs {
		partitionDomains[index] = extractor.Domain()
	}
	partitionPublicationsCurrent := func(
		currentCtx context.Context,
		repository string,
	) bool {
		candidateState, candidateErr := readPartitionCandidateReference(
			currentCtx, repository,
		)
		if candidateErr != nil {
			return false
		}
		source, observation, authorityErr := readPartitionFenceAuthority(
			currentCtx, candidateState,
		)
		if authorityErr != nil {
			return false
		}
		extractionPolicy, policyErr := candidate.ExtractionPolicyDigest(candidateState.PolicyDigest, semanticLaunch != nil)
		if policyErr != nil {
			return false
		}
		return allPartitionDomainsMatch(
			currentCtx, partitionDomains, candidateState.ManifestDigest,
			source, observation, extractionPolicy,
			func(
				domainCtx context.Context, domain string,
			) (candidate.DownstreamDomainAuthority, error) {
				// Store publication is the downstream authority for every
				// settled disposition, including explicit empty, terminal,
				// and retryable domain roots that intentionally have no
				// successful filesystem current pointer.
				return extractionpublication.CurrentDomainAuthority(
					domainCtx, st, repository, domain,
				)
			},
		)
	}
	// Assigned before runners start; both callbacks preserve downstream
	// reconciliation while avoiding jobs for already-current publications.
	var resolverCurrent, callerCurrent func(context.Context, string) (bool, error)
	enqueueDownstream := func(enqueueCtx context.Context, kind store.JobKind, repository string, force bool) (*store.Job, error) {
		var current func(context.Context, string) (bool, error)
		switch kind {
		case store.JobResolverCatalog:
			current = resolverCurrent
		case store.JobCallerLeaf:
			current = callerCurrent
		}
		return enqueueUnlessCurrent(enqueueCtx, kind, repository, force, current, st.EnqueuePending)
	}
	partitionRuntime.OnSettled = func(
		settledCtx context.Context,
		repository string,
	) error {
		partitionsCurrent := partitionPublicationsCurrent(
			settledCtx, repository,
		)
		return afterPartitionExtractionSettlement(
			settledCtx, repository,
			legacyRelationshipReconcileFor(
				repository, cfg.ServiceCatalogs, d.reconcileRelationship,
			),
			enqueueDownstream,
			d.resolverRegistry.Enabled() && partitionsCurrent,
			d.callerRegistry.Enabled() && partitionsCurrent &&
				resolverPublicationPresent(cfg.Server.DataDir, repository),
		)
	}
	partitionReconciler := &extractionpublication.Reconciler{
		Root: d.partitionPublicationRoot, CandidateRoot: candidatejob.CandidateRoot(cfg.Server.DataDir),
		Runtime: partitionRuntime, Evidence: st,
		OpenCandidate: openPartitionCandidate, Authority: readPartitionAuthority,
		CandidateReference: readPartitionCandidateReference,
		AuthorityReference: readPartitionFenceAuthority,
		StoreAccounting:    semanticLaunch != nil,
	}
	openPartitionDomain = partitionReconciler.OpenDomain
	partitionRuntime.Source = extractionpublication.GitSparseSource{
		DataDir: cfg.Server.DataDir, OpenDomain: partitionReconciler.OpenDomain,
	}
	partitionRuntime.Fence = extractionpublication.AuthorityFence{
		Store: st, Acquire: d.acquireObservationTransition,
		Current: func(fenceCtx context.Context, plan candidate.DomainResultPlan) error {
			publication, fenceErr := openPartitionCandidate(fenceCtx, plan.Repository)
			if fenceErr != nil {
				return fenceErr
			}
			state := publication.State()
			policy, policyErr := candidate.ExtractionPolicyDigest(state.PolicyDigest, semanticLaunch != nil)
			if policyErr != nil || policy != plan.ExtractionPolicyDigest {
				return errors.Join(policyErr, extractionpublication.ErrStale)
			}
			if state.ManifestDigest != plan.CandidateManifestDigest ||
				state.GenerationDigest != plan.CandidateGenerationDigest ||
				state.PolicyDigest != plan.CandidatePolicyDigest {
				return extractionpublication.ErrStale
			}
			source, observation, fenceErr := readPartitionFenceAuthority(fenceCtx, state)
			if fenceErr != nil {
				return fenceErr
			}
			if source != plan.SourceGenerationDigest ||
				observation != plan.ObservationGenerationDigest {
				return extractionpublication.ErrStale
			}
			return nil
		},
	}
	if semanticLaunch != nil && semanticLaunch.request.ServerEpoch == 3 {
		staleControl, err := newT422StaleControl(ctx, semanticLaunch, partitionReconciler)
		if err != nil {
			return err
		}
		d.deferFunc(func(*error) { staleControl.cancel() })
		d.staleControl = staleControl
		if d.lifecycleControl != nil {
			staleControl.workspacePreparation = d.lifecycleControl.sampleRecoveryPreparation
		}
		exactReadState.stale = staleControl
		terminalPhase, terminalErr := dispatchadmission.ProductionTerminalPhase()
		if terminalErr != nil {
			return terminalErr
		}
		if terminalPhase != 0 {
			checkpointControl, err := newT422CheckpointControl(ctx, staleControl, d.exact.reuse)
			if err != nil {
				return err
			}
			d.deferFunc(func(*error) { checkpointControl.cancel() })
			d.checkpointControl = checkpointControl
			exactReadState.checkpoint = checkpointControl
			partitionRuntime.OnPartitionCheckpoint = checkpointControl.checkpoint
			if err := dispatchadmission.BindProductionTerminalQuiescence(checkpointControl.quiesceAndReport); err != nil {
				return err
			}
		}
	}
	if semanticLaunch != nil && semanticLaunch.request.CheckpointRecovery != nil {
		checkpointRecovery, err := newT422CheckpointRecoveryControl(ctx, semanticLaunch, partitionReconciler)
		if err != nil {
			return err
		}
		d.deferFunc(func(*error) { checkpointRecovery.cancel() })
		d.checkpointRecovery = checkpointRecovery
		exactReadState.checkpointRecovery = checkpointRecovery
	}
	candidateWorker.Diagnostics = cfg.Diagnostics.Candidates
	if err := enqueueCandidateBackfillWithReadiness(
		ctx, st, candidateWorker.PolicyDigest(), func(repository string) bool {
			return repositoryMirrorPresent(cfg.Server.DataDir, repository)
		},
	); err != nil {
		return err
	}
	var resolverRunner, callerRunner *store.Runner
	if d.resolverRegistry.Enabled() {
		resolverWorker, err := resolvermaterialize.NewWorker(
			cfg.Server.DataDir, st, manifestProvider, d.resolverRegistry,
		)
		if err != nil {
			return fmt.Errorf("configure resolver materialization: %w", err)
		}
		if d.relationshipRuntime != nil {
			resolverWorker.OnPublished = func(
				publishedCtx context.Context,
				repository string,
			) error {
				return afterResolverPublication(
					publishedCtx, repository,
					legacyRelationshipReconcileFor(
						repository, cfg.ServiceCatalogs, d.reconcileRelationship,
					),
					d.serviceRuntime.Advance,
					enqueueDownstream, d.callerRegistry.Enabled(),
				)
			}
		}
		resolverCurrent = resolverWorker.ReconcileCurrent
		resolverRunner = &store.Runner{
			Store: st, Kind: store.JobResolverCatalog, Owners: owners,
			Handle: func(jobCtx context.Context, job store.Job) error {
				return resolverWorker.Handle(jobCtx, job)
			},
			Interval:    cfg.Sync.Interval(),
			Diagnostics: cfg.Diagnostics.Jobs,
		}
	}
	if d.callerRegistry.Enabled() {
		callerWorker, err := callerexecute.NewWorkerWithPublicationRegistry(
			cfg.Server.DataDir, st, manifestProvider, d.callerRegistry,
			d.callerPublications,
		)
		if err != nil {
			return fmt.Errorf("configure caller-leaf execution: %w", err)
		}
		if d.relationshipRuntime != nil {
			callerWorker.OnPublished = func(
				publishedCtx context.Context,
				repository string,
			) error {
				reconcile := legacyRelationshipReconcileFor(
					repository, cfg.ServiceCatalogs, d.reconcileRelationship,
				)
				if reconcile == nil {
					return nil
				}
				return reconcile(publishedCtx, repository)
			}
		}
		callerCurrent = callerWorker.ReconcileCurrent
		callerRunner = &store.Runner{
			Store: st, Kind: store.JobCallerLeaf, Owners: owners,
			Handle: func(jobCtx context.Context, job store.Job) error {
				return callerWorker.Handle(jobCtx, job)
			},
			Interval:    cfg.Sync.Interval(),
			Diagnostics: cfg.Diagnostics.Jobs,
		}
	}
	candidateRunner := &store.Runner{
		Store: st, Kind: store.JobCandidate, Handle: candidateWorker.Handle, Owners: owners,
		Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs,
	}
	exRunner := &store.Runner{Store: st, Kind: store.JobExtract, Owners: owners, Handle: func(
		jobCtx context.Context,
		job store.Job,
	) error {
		if !candidatePublicationPresent(cfg.Server.DataDir, job.Target) {
			diagnostics.Logf(
				"extraction deferred until candidate publication: repository=%q",
				job.Target,
			)
			return nil
		}
		// Focused analysis units still publish through the T30 legacy writer.
		// Whole-repository generations moved to partitioned authority in
		// T40.10-T40.12; running both writers would make the legacy worker hold
		// the repository lock across the entire corpus and starve the v2 plan.
		if legacyExtractionRequired(job.Target, analysisUnits) {
			return worker.Handle(jobCtx, job)
		}
		_, partitionErr := partitionReconciler.Reconcile(jobCtx, job.Target)
		var deferred bool
		partitionErr, deferred = deferPendingPartitionAuthority(partitionErr)
		if deferred {
			diagnostics.Logf(
				"partitioned extraction deferred until observation v2 authority: repository=%q",
				job.Target,
			)
		}
		if partitionErr != nil {
			diagnostics.Logf(
				"partitioned extraction reconcile failed: repository=%q error=%v",
				job.Target, partitionErr,
			)
		}
		return partitionErr
	},
		Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
	bindT4013ExactReports(
		d.exactReports, d.exact.failReport, candidateWorker,
		candidateRunner, exRunner, resolverRunner, callerRunner,
	)
	configureT422SelectedJobLeases(d.semanticLaunch != nil, candidateRunner, exRunner, resolverRunner, callerRunner)
	d.exact.attempts.bindJobs(candidateRunner, exRunner, resolverRunner, callerRunner)
	runStoreRunner(ctx, d.runBackground, candidateRunner)
	runStoreRunner(ctx, d.runBackground, exRunner)
	partitionScheduler := &generationscheduler.Scheduler{
		Store:       st,
		Owners:      owners,
		Diagnostics: cfg.Diagnostics.Extraction,
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{
			store.GenerationResourceExtraction: {
				Concurrency: extractionpublication.ScheduleClassConcurrency,
				Budget: generationscheduler.Budget{
					MaxMemoryBytes: 512 << 20, MaxDescriptors: 8,
				},
				Handle: func(
					workerCtx context.Context,
					chunk store.GenerationChunk,
					_ generationscheduler.Budget,
				) error {
					return partitionRuntime.Handle(workerCtx, chunk)
				},
				OnExhausted: partitionRuntime.OnExhausted,
			},
		},
		PollEvery: time.Second, WorkerPrefix: "extraction-partition-worker",
		Report: func(err error) {
			diagnostics.Logf("partitioned extraction scheduler unavailable: %v", err)
		},
	}
	if d.staleControl != nil {
		class := partitionScheduler.Classes[store.GenerationResourceExtraction]
		class.BeforeLeaseHeartbeat, class.OnStaleLeaseTransition = d.staleControl.beforeHeartbeat, d.staleControl.transition
		class.TerminalHeartbeat = d.checkpointControl != nil
		partitionScheduler.Classes[store.GenerationResourceExtraction] = class
	}
	bindT422ExactChunkReports(d.exactReads, d.exact.failRead, partitionScheduler)
	if d.checkpointRecovery != nil {
		class := partitionScheduler.Classes[store.GenerationResourceExtraction]
		class.OnStaleLeaseTransition = d.checkpointRecovery.transition
		partitionScheduler.Classes[store.GenerationResourceExtraction] = class
	}
	d.exact.attempts.bindChunk(partitionScheduler)
	d.runBackground(func() {
		if d.checkpointRecovery != nil && d.checkpointRecovery.waitForReader(ctx) != nil {
			return
		}
		if err := partitionScheduler.Run(ctx); err != nil && ctx.Err() == nil {
			diagnostics.Logf("partitioned extraction scheduler stopped: %v", err)
		}
	})
	if resolverRunner != nil {
		runStoreRunner(ctx, d.runBackground, resolverRunner)
	}
	if callerRunner != nil {
		runStoreRunner(ctx, d.runBackground, callerRunner)
	}
	catalogAfterIndex := d.onIndexed
	d.onIndexed = func(ctx context.Context, name, commit string) error {
		candidateErr := enqueueCandidateAfterIndex(
			ctx, st, name, commit, cfg.Diagnostics.Candidates,
		)
		var catalogErr error
		if catalogAfterIndex != nil {
			catalogErr = catalogAfterIndex(ctx, name, commit)
		}
		return errors.Join(candidateErr, catalogErr)
	}
	d.evidenceView = evidenceView
	d.proofBundles = proofBundles
	d.compatibility = compatibility
	d.partitionRuntime = partitionRuntime
	d.manifestProvider = manifestProvider
	d.openPartitionDomain = openPartitionDomain
	return nil
}

// startServeMaintenance starts the proof-bundle and evidence maintenance loops.
func startServeMaintenance(d *serveDeps) {
	ctx := d.ctx
	st := d.st
	owners := d.startup.Owners()
	if lifetime := d.cfg.ProofBundles.RetentionFor(); lifetime > 0 {
		d.runBackground(func() {
			runProofBundleMaintenanceWithOwners(
				ctx, st, lifetime, evidenceSweepIdleInterval, evidenceSweepBacklogDelay,
				owners,
			)
		})
	}
	d.runBackground(func() {
		runEvidenceMaintenanceWithOwners(
			ctx, st, evidenceSweepIdleInterval, evidenceSweepBacklogDelay, evidenceStagedMaxAge,
			owners,
		)
	})
}

// startServeIndexPipeline admits the startup indexer and starts the indexing
// job runner.
func startServeIndexPipeline(d *serveDeps) error {
	ctx := d.ctx
	cfg := d.cfg
	st := d.st
	owners := d.startup.Owners()

	// index pipeline: same-SHA zoekt-git-index child consumes indexing_job
	bin, focusedBin, err := admitStartupIndexer(len(d.analysisUnits) > 0)
	if err != nil {
		return err
	}
	if bin != "" {
		ix := &indexer.Indexer{
			DataDir:       cfg.Server.DataDir,
			Bin:           bin,
			FocusedBin:    focusedBin,
			Store:         st,
			Verbose:       cfg.Indexing.Verbose,
			Revisions:     cfg.Revisions,
			AnalysisUnits: d.analysisUnits,
			OnIndexed:     d.onIndexed,
			AdmitDerived: func(admitCtx context.Context, estimatedBytes int64) error {
				capacity, admissionErr := d.capacityGate.Check(admitCtx, estimatedBytes)
				d.lifecycleStatus.ObserveCapacity(capacity, admissionErr)
				if estimatedBytes == 0 && errors.Is(admissionErr, lifecycle.ErrCapacityUnavailable) {
					// T35 workloads fail closed when capacity is unavailable. The
					// pre-existing index pipeline retains its historical behavior,
					// while a measured hard/projected watermark still refuses it.
					diagnostics.Logf("lifecycle capacity unavailable for legacy index admission: %v", admissionErr)
					return nil
				}
				return admissionErr
			},
		}
		if d.exact.reuse != nil {
			ix.OnReuse = d.exact.reuse.observeIndex
		}
		ixRunner := &store.Runner{Store: st, Kind: store.JobIndex, Handle: ix.Handle, Owners: owners,
			Interval: cfg.Sync.Interval(), Diagnostics: cfg.Diagnostics.Jobs}
		bindT4013ExactReports(d.exactReports, d.exact.failReport, nil, ixRunner)
		configureT422SelectedJobLeases(d.semanticLaunch != nil, ixRunner)
		d.exact.attempts.bindJobs(ixRunner)
		runStoreRunner(ctx, d.runBackground, ixRunner)
	}
	return nil
}
