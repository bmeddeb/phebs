package t4110

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// prepareRelationshipRuntimeInputs derives the real repository-shared
// authorities used by the production v3 relationship runtime. The frozen
// target has 10,000 .proto-named neutral fixture blobs. They produce exact
// unsupported observation and candidate controls but no observed source,
// resolver declaration, or relationship evidence.
func (h *liveHarness) prepareRelationshipRuntimeInputs(
	ctx context.Context,
	cost *PhaseCost,
) error {
	if cost == nil {
		return errors.New("target relationship runtime cost is required")
	}
	repositoryDirectory := phebssync.RepoDir(h.dataDir, liveRepository)
	observationRoot := filepath.Join(h.dataDir, "observations")
	transition, err := observationpublication.BeginInventoryPublicationV2(
		observationRoot, liveRepository,
	)
	if err != nil {
		return fmt.Errorf("begin target observation publication: %w", err)
	}
	sourceDirectory := filepath.Join(h.root, "relationship-source")
	source, err := repositoryindex.BuildSourceGeneration(
		ctx,
		repositoryDirectory,
		sourceDirectory,
		liveRepository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: h.commit}},
	)
	if err != nil {
		return fmt.Errorf("build target observation source generation: %w", err)
	}
	if source.OwnerCount < 0 || source.PlacementCount < 0 ||
		source.RegularDeclaredBytes < 0 {
		return errors.New("target source census contains negative accounting")
	}
	cost.SourceCensusRecords = uint64(source.OwnerCount)
	cost.SourceCensusMembers = uint64(len(source.Members))
	cost.SourceCensusPlacements = uint64(source.PlacementCount)
	cost.SourceCensusDeclaredBytes = uint64(source.RegularDeclaredBytes)
	sourceRoot, err := sourcepartition.BuildSuperRoot(ctx, sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory,
		OutputDirectory: transition.SourceDirectory,
		Repository:      liveRepository,
		Source:          source,
		Policy: sourcepartition.Policy{
			Schema:  sourcepartition.PolicySchema,
			Name:    "t4110-neutral-proto-only",
			Version: "1.0.0",
			IncludeSuffixes: []string{
				".proto",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("build target observation source root: %w", err)
	}
	if sourceRoot.MemberCount != targetRelationshipObservationMembers ||
		sourceRoot.BlobCount != targetRelationshipRecords ||
		sourceRoot.PlacementCount != targetRelationshipRecords ||
		sourceRoot.DeclaredBytes != targetRelationshipDeclaredBytes {
		return fmt.Errorf(
			"target observation source shape members=%d blobs=%d placements=%d bytes=%d",
			sourceRoot.MemberCount, sourceRoot.BlobCount,
			sourceRoot.PlacementCount, sourceRoot.DeclaredBytes,
		)
	}
	cost.ObservationInputMembers = uint64(sourceRoot.MemberCount)
	plan, err := sourcepartition.OpenSuperRoot(
		ctx, transition.SourceDirectory, sourceRoot,
	)
	if err != nil {
		return fmt.Errorf("open target observation source root: %w", err)
	}
	inventory, err := observationpublication.BuildInventoryStageV2(
		ctx,
		observationpublication.InventoryBuildRequestV2{
			OutputDirectory:     transition.InventoryDirectory,
			RepositoryDirectory: repositoryDirectory,
			Plan:                plan,
		},
	)
	if err != nil {
		return fmt.Errorf("build target observation inventory: %w", err)
	}
	operation := inventory.OperationReceipt
	if inventory.MemberCount != targetRelationshipObservationMembers ||
		inventory.RecordCount != targetRelationshipRecords ||
		inventory.ObservedCount != 0 ||
		inventory.UnsupportedCount != targetRelationshipRecords ||
		inventory.ObservationCount != 0 || inventory.ObservationBytes != 0 ||
		operation.InputBlobs != targetRelationshipRecords ||
		operation.SourceBlobReads != targetRelationshipRecords ||
		operation.ObservedBlobs != 0 || operation.ParsedObservations != 0 ||
		operation.ReusedObservations != 0 ||
		operation.UnsupportedBlobs != targetRelationshipRecords ||
		len(operation.UnsupportedReasons) != 1 ||
		operation.UnsupportedReasons[0].Reason != "parse_error" ||
		operation.UnsupportedReasons[0].Count != targetRelationshipRecords {
		return fmt.Errorf(
			"target observation inventory shape members=%d records=%d observed=%d unsupported=%d reads=%d",
			inventory.MemberCount, inventory.RecordCount, inventory.ObservedCount,
			inventory.UnsupportedCount, operation.SourceBlobReads,
		)
	}
	cost.ObservationMembers = uint64(inventory.MemberCount)
	cost.ObservationRecords = uint64(inventory.RecordCount)
	cost.ObservationObservedRecords = uint64(inventory.ObservedCount)
	cost.ObservationSourceBlobReads = uint64(inventory.OperationReceipt.SourceBlobReads)
	if _, err := observationpublication.CompleteInventoryPublicationV2(
		ctx, observationRoot, liveRepository, transition.TransitionID, nil,
	); err != nil {
		return fmt.Errorf("publish target observation inventory: %w", err)
	}
	observation, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, observationRoot, liveRepository,
	)
	if err != nil {
		return fmt.Errorf("read target observation authority: %w", err)
	}
	if observation.SourceRootDigest != sourceRoot.Digest ||
		observation.ObservationRootDigest != inventory.Digest ||
		observation.RecordCount != targetRelationshipRecords ||
		observation.ObservedCount != 0 {
		return fmt.Errorf(
			"target observation authority records=%d observed=%d",
			observation.RecordCount, observation.ObservedCount,
		)
	}

	candidateWorker, _, err := candidatejob.New(
		h.dataDir,
		h.state,
		[]extract.Extractor{protodecl.New()},
	)
	if err != nil {
		return fmt.Errorf("construct target candidate worker: %w", err)
	}
	var candidateReport candidatejob.CandidateOperationReport
	reports := 0
	candidateWorker.OperationReports = func(raw []byte) error {
		if reports != 0 {
			return errors.New("target candidate worker emitted multiple operation reports")
		}
		reports++
		if err := json.Unmarshal(raw, &candidateReport); err != nil {
			return err
		}
		return nil
	}
	candidateWorker.OperationReportFailure = func(error) {}
	if err := candidateWorker.Handle(ctx, store.Job{
		Kind: store.JobCandidate, Target: liveRepository,
	}); err != nil {
		return fmt.Errorf("publish target candidate manifest: %w", err)
	}
	if reports != 1 || candidateReport.Schema != candidatejob.CandidateOperationSchema ||
		candidateReport.Decision != "rebuild" || candidateReport.Outcome != "done" ||
		!candidateReport.ManifestSummaryPresent ||
		candidateReport.Planes.Repository.Members != targetRelationshipCandidateMembers ||
		candidateReport.Planes.Repository.Records != targetRelationshipRecords ||
		candidateReport.Planes.Repository.DeclaredBytes != targetRelationshipDeclaredBytes ||
		candidateReport.Planes.Repository.CanonicalBytes <= 0 ||
		candidateReport.Planes.Local.Members != 0 || candidateReport.Planes.Local.Records != 0 ||
		candidateReport.Planes.Caller.Members != 0 || candidateReport.Planes.Caller.Records != 0 ||
		candidateReport.DeclaredSourceBytes != h.corpus.Profile.Fixture.ContentBytes {
		return fmt.Errorf(
			"target candidate operation shape repository=%d/%d local=%d/%d caller=%d/%d selected_bytes=%d source_bytes=%d",
			candidateReport.Planes.Repository.Members,
			candidateReport.Planes.Repository.Records,
			candidateReport.Planes.Local.Members,
			candidateReport.Planes.Local.Records,
			candidateReport.Planes.Caller.Members,
			candidateReport.Planes.Caller.Records,
			candidateReport.Planes.Repository.DeclaredBytes,
			candidateReport.DeclaredSourceBytes,
		)
	}
	cost.CandidateInputReadsUnavailable = true
	cost.CandidateResultMembers = uint64(
		candidateReport.Planes.Repository.Members +
			candidateReport.Planes.Local.Members + candidateReport.Planes.Caller.Members,
	)
	cost.CandidateResultRecords = uint64(
		candidateReport.Planes.Repository.Records +
			candidateReport.Planes.Local.Records + candidateReport.Planes.Caller.Records,
	)
	cost.CandidateDeclaredBytes = uint64(candidateReport.DeclaredSourceBytes)
	candidate, err := h.state.GetCandidateManifestPublication(ctx, liveRepository)
	if err != nil {
		return fmt.Errorf("read target candidate manifest: %w", err)
	}
	if candidate.HeadCommit != h.commit {
		return errors.New("target candidate manifest is not bound to fixture HEAD")
	}

	identity, err := resolvercatalog.NewIdentity(
		liveRepository,
		h.commit,
		"",
		candidate.ManifestDigest,
		[]resolvercatalog.DeclarationPublication{},
		[]resolvercatalog.ResolverPack{},
	)
	if err != nil {
		return fmt.Errorf("construct empty target resolver identity: %w", err)
	}
	stage, err := resolvercatalog.NewStage(
		filepath.Join(h.dataDir, "resolver-catalogs"), identity,
	)
	if err != nil {
		return fmt.Errorf("begin empty target resolver publication: %w", err)
	}
	prepared, err := stage.Seal(ctx)
	if err != nil {
		return errors.Join(
			fmt.Errorf("seal empty target resolver publication: %w", err),
			stage.Discard(),
		)
	}
	if _, err := prepared.Publish(ctx, func(ctx context.Context, state resolvercatalog.State) error {
		return h.state.PublishResolverCatalog(ctx, resolverCatalogStoreState(state))
	}); err != nil {
		return fmt.Errorf("publish empty target resolver catalog: %w", err)
	}

	h.relationshipRuntime = &relationshippublication.Runtime{
		DataDir:        h.dataDir,
		Store:          h.state,
		Cache:          &observationpublication.Cache{},
		InventoryCache: &observationpublication.InventoryCacheV2{},
		Domains:        []downstreamauthority.DomainIdentity{},
		Acquire: func(ctx context.Context) (func(), error) {
			return focusedindex.AcquireMutationLock(
				ctx, filepath.Join(h.dataDir, "index"),
			)
		},
	}
	return nil
}

func resolverCatalogStoreState(state resolvercatalog.State) store.ResolverCatalogPublication {
	declarations := make(
		[]store.ResolverCatalogDeclarationPublication, len(state.Declarations),
	)
	for index, declaration := range state.Declarations {
		declarations[index] = store.ResolverCatalogDeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
			AuthoritySchema:  declaration.AuthoritySchema,
			PlanDigest:       declaration.PlanDigest,
			RootDigest:       declaration.RootDigest,
		}
	}
	packs := make([]store.ResolverCatalogPack, len(state.ResolverPacks))
	for index, pack := range state.ResolverPacks {
		packs[index] = store.ResolverCatalogPack{Name: pack.Name, Version: pack.Version}
	}
	return store.ResolverCatalogPublication{
		Repository: state.Repository, HeadCommit: state.Commit,
		UnitDigest:              state.UnitDigest,
		Declarations:            declarations,
		DeclarationSetDigest:    state.DeclarationSetDigest,
		CandidateManifestDigest: state.CandidateManifestDigest,
		SourceLanePolicy:        state.SourceLanePolicy,
		ResolverPacks:           packs,
		ResolverPackSetDigest:   state.ResolverPackSetDigest,
		CatalogPolicyDigest:     state.CatalogPolicyDigest,
		GenerationDigest:        state.GenerationDigest,
		ManifestDigest:          state.ManifestDigest,
		AuthorityDigest:         state.AuthorityDigest,
		ManifestPath:            state.Manifest,
	}
}

// reconcileEmptyRelationshipV3 uses the same reconcile, durable claim,
// handler, and completion boundaries as the production memory runner. An
// already-current target must remain a control-only no-op.
func (h *liveHarness) reconcileEmptyRelationshipV3(
	ctx context.Context,
	cost *PhaseCost,
) (relationshippublication.RootV3, error) {
	if cost == nil {
		return relationshippublication.RootV3{}, errors.New("target relationship cost is required")
	}
	runtime := h.relationshipRuntime
	if runtime == nil || runtime.Store != h.state || runtime.DataDir != h.dataDir {
		return relationshippublication.RootV3{}, errors.New("target relationship runtime is not current")
	}
	var priorRoot *relationshippublication.RootV3
	prior, priorErr := relationshippublication.OpenCurrentV3(
		ctx, filepath.Join(h.dataDir, "relationships"), liveRepository,
	)
	if priorErr == nil {
		value := prior.Root()
		priorRoot = &value
	} else if !errors.Is(priorErr, relationshippublication.ErrNotFound) {
		return relationshippublication.RootV3{}, priorErr
	}
	current, err := runtime.ReconcileV3(ctx, liveRepository)
	if err != nil {
		return relationshippublication.RootV3{}, err
	}
	if !current {
		chunk, claimErr := h.state.ClaimGenerationChunk(
			ctx, store.GenerationResourceMemory, "t4110-relationship-v3",
		)
		if claimErr != nil {
			return relationshippublication.RootV3{}, fmt.Errorf(
				"claim target relationship v3 chunk: %w", claimErr,
			)
		}
		if chunk.Repository != liveRepository ||
			chunk.Stage != relationshippublication.ScheduleStageV3 ||
			chunk.Offset != 0 || chunk.Length != 1 {
			return relationshippublication.RootV3{}, errors.New(
				"claimed chunk is not the target relationship v3 schedule",
			)
		}
		if err := runtime.HandleV3(ctx, *chunk); err != nil {
			return relationshippublication.RootV3{}, fmt.Errorf(
				"handle target relationship v3 chunk: %w", err,
			)
		}
		if err := h.state.CompleteGenerationChunk(ctx, *chunk); err != nil {
			return relationshippublication.RootV3{}, fmt.Errorf(
				"complete target relationship v3 chunk: %w", err,
			)
		}
		cost.RelationshipScheduleChunks++
		cost.RelationshipPublishes++
		current, err = runtime.ReconcileV3(ctx, liveRepository)
		if err != nil {
			return relationshippublication.RootV3{}, err
		}
		if !current {
			return relationshippublication.RootV3{}, errors.New(
				"target relationship v3 did not become current after its claimed chunk",
			)
		}
	}
	publication, err := relationshippublication.OpenCurrentV3(
		ctx, filepath.Join(h.dataDir, "relationships"), liveRepository,
	)
	if err != nil {
		return relationshippublication.RootV3{}, err
	}
	root := publication.Root()
	pointer, err := h.state.GetServiceCatalogV3CandidatePointer(ctx, liveRepository)
	if err != nil {
		return relationshippublication.RootV3{}, err
	}
	summary, err := h.state.GetServiceStateV3Summary(ctx, liveRepository)
	if err != nil {
		return relationshippublication.RootV3{}, err
	}
	if root.Authority.CatalogRootDigest != h.generation.Root.Digest ||
		root.Authority.CatalogControlRevision != pointer.ControlRevision ||
		root.Authority.ServiceStateSummaryDigest != summary.SummaryDigest ||
		root.Authority.ServiceStateControlRevision != summary.ControlRevision ||
		root.ServiceCount != h.generation.Root.Dispositions.Accepted ||
		!root.RepositoryComplete || !root.AllServicesComplete ||
		root.ProjectionCount != 0 || root.ProjectionFragmentCount != 0 ||
		root.CompleteServiceCount != 0 || root.EmptyServiceCount != root.ServiceCount ||
		root.FailedServiceCount != 0 || root.ServiceReferenceCount != 0 {
		return relationshippublication.RootV3{}, errors.New(
			"target relationship v3 is not the exact empty production-runtime projection",
		)
	}
	service, err := publication.ReadService(ctx, firstAcceptedKey(h.catalog))
	if err != nil || service.State != "empty" || len(service.References) != 0 {
		return relationshippublication.RootV3{}, errors.Join(
			errors.New("target relationship v3 service query is not exactly empty"), err,
		)
	}
	if cost.RelationshipPublishes != 0 {
		cost.RelationshipServiceMembers += uint64(len(root.ServiceMembers))
		cost.RelationshipServiceRecords += uint64(root.ServiceCount)
		cost.RelationshipProjectionMembers += uint64(len(root.RepositoryMembers))
		cost.RelationshipProjectionRecords += uint64(root.ProjectionCount)
		if err := h.recordRelationshipComponentPublicationCost(
			ctx, cost, priorRoot, root,
		); err != nil {
			return relationshippublication.RootV3{}, err
		}
	}
	return root, nil
}

func (h *liveHarness) recordRelationshipComponentPublicationCost(
	ctx context.Context,
	cost *PhaseCost,
	prior *relationshippublication.RootV3,
	current relationshippublication.RootV3,
) error {
	resolverChanged := prior == nil ||
		prior.Authority.ResolverGenerationDigest != current.Authority.ResolverGenerationDigest ||
		prior.Authority.ResolverRootDigest != current.Authority.ResolverRootDigest
	if resolverChanged {
		publication, err := resolvernamespace.OpenGeneration(
			ctx,
			filepath.Join(h.dataDir, "relationship-resolver-namespaces"),
			liveRepository,
			current.Authority.ResolverGenerationDigest,
			current.Authority.ResolverRootDigest,
		)
		if err != nil {
			return fmt.Errorf("open published target resolver namespace: %w", err)
		}
		root := publication.Root()
		cost.RelationshipComponentPublishes++
		cost.RelationshipComponentMembers += uint64(len(root.Namespaces))
		cost.RelationshipComponentRecords += uint64(root.RecordCount)
	}
	rpcChanged := prior == nil ||
		prior.Authority.RPCGenerationDigest != current.Authority.RPCGenerationDigest ||
		prior.Authority.RPCRootDigest != current.Authority.RPCRootDigest
	if rpcChanged {
		publication, err := rpccallerposting.OpenGeneration(
			ctx,
			filepath.Join(h.dataDir, "relationship-rpc-postings"),
			liveRepository,
			current.Authority.RPCGenerationDigest,
			current.Authority.RPCRootDigest,
		)
		if err != nil {
			return fmt.Errorf("open published target RPC postings: %w", err)
		}
		root := publication.Root()
		cost.RelationshipComponentPublishes++
		cost.RelationshipComponentMembers += uint64(len(root.Members))
		cost.RelationshipComponentRecords += uint64(root.PostingCount)
	}
	kafkaChanged := prior == nil ||
		prior.Authority.KafkaGenerationDigest != current.Authority.KafkaGenerationDigest ||
		prior.Authority.KafkaRootDigest != current.Authority.KafkaRootDigest
	if kafkaChanged {
		publication, err := kafkatopicposting.OpenGeneration(
			ctx,
			filepath.Join(h.dataDir, "relationship-kafka-postings"),
			liveRepository,
			current.Authority.KafkaGenerationDigest,
			current.Authority.KafkaRootDigest,
		)
		if err != nil {
			return fmt.Errorf("open published target Kafka postings: %w", err)
		}
		root := publication.Root()
		cost.RelationshipComponentPublishes++
		cost.RelationshipComponentMembers += uint64(len(root.Members))
		cost.RelationshipComponentRecords += uint64(root.PostingCount)
	}
	return nil
}

func (h *liveHarness) drainHistoricalRelationships(
	ctx context.Context,
	cost *PhaseCost,
) (int, error) {
	owner := lifecycle.RelationshipGenerationOwnerV3{
		DataDir: h.dataDir,
		Pins:    &relationshippublication.CacheV3{},
		AcquireExclusive: func(ctx context.Context) (func(), error) {
			return focusedindex.AcquireExclusiveMutationLock(
				ctx, filepath.Join(h.dataDir, "index"),
			)
		},
		Store: h.state,
	}
	cursor := ""
	deleted := 0
	converged := false
	for range 4_096 {
		result := owner.Sweep(ctx, time.Now().UTC(), cursor, lifecycle.DefaultLimits())
		if result.Err != nil || result.Completeness == lifecycle.Unavailable {
			return deleted, errors.Join(errors.New("run target relationship v3 lifecycle"), result.Err)
		}
		if result.Deleted < 0 || result.Deleted > lifecycle.MaxDeletesPerTick {
			return deleted, errors.New("relationship lifecycle exceeded its per-turn delete bound")
		}
		deleted += result.Deleted
		cost.LifecycleRecordsDeleted += uint64(result.Deleted)
		cursor = result.Cursor
		if !result.More {
			converged = true
			break
		}
	}
	if !converged {
		return deleted, errors.New("relationship lifecycle did not converge within 4,096 bounded turns")
	}
	if _, err := h.reconcileEmptyRelationshipV3(ctx, cost); err != nil {
		return deleted, fmt.Errorf("confirm target relationship after lifecycle: %w", err)
	}
	return deleted, nil
}
