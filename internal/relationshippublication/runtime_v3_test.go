package relationshippublication

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type runtimeTestStoreV3 struct {
	*runtimeHandleCleanupStore

	candidate     *store.ServiceCatalogV3Candidate
	summary       servicecatalog.RepositoryState
	states        []servicecatalog.ServiceState
	schedule      *store.GenerationSchedule
	enqueues      []store.GenerationScheduleSpec
	staleEnqueues int

	confirmCalls int
	failConfirm  int
	relationship []string
	relUnpins    int
}

func (state *runtimeTestStoreV3) GetServiceCatalogV3CandidatePointer(
	_ context.Context,
	repository string,
) (store.ServiceCatalogV3Pointer, error) {
	if state.candidate == nil ||
		state.candidate.Generation.Root.Binding.Repository != repository {
		return store.ServiceCatalogV3Pointer{}, store.ErrNotFound
	}
	return store.ServiceCatalogV3Pointer{
		Repository: repository, RootDigest: state.candidate.Generation.Root.Digest,
		ControlRevision: state.candidate.ControlRevision,
		PublishedAt:     state.candidate.PublishedAt,
	}, nil
}

func (state *runtimeTestStoreV3) GetServiceCatalogV3Candidate(
	_ context.Context,
	repository string,
) (*store.ServiceCatalogV3Candidate, error) {
	if state.candidate == nil ||
		state.candidate.Generation.Root.Binding.Repository != repository {
		return nil, store.ErrNotFound
	}
	value := *state.candidate
	return &value, nil
}

func (state *runtimeTestStoreV3) GetServiceStateV3SummaryPoint(
	_ context.Context,
	repository string,
) (servicecatalog.RepositoryState, error) {
	if state.summary.Repository != repository {
		return servicecatalog.RepositoryState{}, store.ErrNotFound
	}
	return state.summary, nil
}

func (state *runtimeTestStoreV3) ListAcceptedServiceStateV3Rows(
	_ context.Context,
	repository string,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	if state.summary.Repository != repository || len(state.states) > limit {
		return nil, store.ErrNotFound
	}
	return cloneRuntimeStatesV3(state.states), nil
}

func (state *runtimeTestStoreV3) ConfirmServiceStateV3Snapshot(
	_ context.Context,
	pointer store.ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) error {
	state.confirmCalls++
	current, err := state.GetServiceCatalogV3CandidatePointer(context.Background(), pointer.Repository)
	if err != nil || current.RootDigest != pointer.RootDigest ||
		current.ControlRevision != pointer.ControlRevision ||
		state.summary.SummaryDigest != summary.SummaryDigest ||
		state.summary.ControlRevision != summary.ControlRevision {
		return errors.New("v3 snapshot changed")
	}
	if state.failConfirm != 0 && state.confirmCalls == state.failConfirm {
		return errors.Join(store.ErrConflict, errors.New("injected v3 snapshot fence"))
	}
	return nil
}

func (state *runtimeTestStoreV3) GetGenerationSchedule(
	ctx context.Context,
	repository, stage string,
) (*store.GenerationSchedule, error) {
	if stage != ScheduleStageV3 {
		return state.runtimeHandleCleanupStore.GetGenerationSchedule(ctx, repository, stage)
	}
	if state.schedule == nil || state.schedule.Repository != repository {
		return nil, store.ErrNotFound
	}
	value := *state.schedule
	return &value, nil
}

func (state *runtimeTestStoreV3) EnqueueGenerationSchedule(
	_ context.Context,
	spec store.GenerationScheduleSpec,
) (*store.GenerationSchedule, error) {
	state.enqueues = append(state.enqueues, spec)
	if state.staleEnqueues > 0 {
		state.staleEnqueues--
		return nil, store.ErrGenerationStale
	}
	state.schedule = &store.GenerationSchedule{
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ResourceClass: spec.ResourceClass, TotalItems: spec.TotalItems,
		ChunkItems: spec.ChunkItems, MaxAttempts: spec.MaxAttempts,
		RepositoryTokens: spec.RepositoryTokens, Status: store.GenerationScheduleActive,
	}
	value := *state.schedule
	return &value, nil
}

func TestRuntimeV3DirectBuildDoesNotRequireV2Relationship(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{
		fixture.runtime.relationshipRoot(), fixture.runtime.resolverNamespaceRoot(),
		fixture.runtime.rpcRoot(), fixture.runtime.kafkaRoot(),
	} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.runtime.ensureBuildRoots(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCurrent(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed v2 relationship source = %v", err)
	}

	catalog, generation := relationshipCatalogV3Test(t, fixture.chunk.Repository, 2)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	v3Store := &runtimeTestStoreV3{
		runtimeHandleCleanupStore: fixture.store,
		candidate: &store.ServiceCatalogV3Candidate{
			Generation: generation, ControlRevision: summary.CatalogControlRevision,
			PublishedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		},
		summary: summary, states: states,
	}
	fixture.runtime.Store = v3Store
	if current, err := fixture.runtime.ReconcileV3(
		t.Context(), fixture.chunk.Repository,
	); err != nil || current {
		t.Fatalf("direct v3 reconcile current=%t err=%v", current, err)
	}
	if len(v3Store.enqueues) != 1 {
		t.Fatalf("direct v3 schedules = %d", len(v3Store.enqueues))
	}
	binding, err := fixture.runtime.readRuntimeBindingV3(
		fixture.chunk.Repository, v3Store.enqueues[0].Generation,
	)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Schema != runtimeBindingSchemaV3Direct || binding.Upstream == nil ||
		binding.ResolverGeneration != fixture.store.resolver.GenerationDigest ||
		binding.SourceGeneration != "" || binding.SourceRoot != "" {
		t.Fatalf("direct v3 binding = %+v", binding)
	}
	if err := fixture.runtime.HandleV3(t.Context(), store.GenerationChunk{
		Repository: fixture.chunk.Repository, Stage: ScheduleStageV3,
		Generation: binding.ScheduleGeneration, Offset: 0, Length: 1,
	}); err != nil {
		t.Fatal(err)
	}
	publication, err := OpenCurrentV3(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	if publication.Root().Authority.UpstreamDigest != fixture.upstream.Digest {
		t.Fatalf("direct v3 upstream = %+v", publication.Root().Authority.Upstream)
	}
	if _, err := OpenCurrent(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("direct v3 build recreated v2 relationship = %v", err)
	}
}

func TestBuildV3AcceptsSparseSuccessorWithMixedCatalogProvenance(t *testing.T) {
	repository := "example.com/acme/relationship-v3-sparse-successor"
	catalogA, generationA := relationshipCatalogV3Test(t, repository, 100)
	statesA, _ := relationshipStatesV3Test(t, generationA.Root, catalogA)
	catalogB := catalogA
	catalogB.Services = append([]servicecatalog.Service(nil), catalogA.Services...)
	catalogB.Memberships = append(
		[]servicecatalog.Membership(nil), catalogA.Memberships...,
	)
	catalogB.Services[len(catalogB.Services)-1].DisplayName = "Changed service"
	generationB, err := servicecatalogv3.Build(generationA.Root.Binding, catalogB)
	if err != nil {
		t.Fatal(err)
	}
	openedB, err := generationB.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	statesB, summaryB := relationshipStatesV3Test(t, generationB.Root, openedB)
	for index := 0; index < len(statesB)-1; index++ {
		statesB[index] = statesA[index]
	}
	if statesB[0].DesiredCatalogGeneration != generationA.Root.Digest ||
		statesB[len(statesB)-1].DesiredCatalogGeneration != generationB.Root.Digest {
		t.Fatalf("mixed successor provenance = first %q last %q",
			statesB[0].DesiredCatalogGeneration,
			statesB[len(statesB)-1].DesiredCatalogGeneration,
		)
	}
	upstream := relationshipUpstreamV3Test(t, repository)
	resolver := relationshipResolverV3Test(t, repository, upstream)
	rpc := fakeRPC{root: relationshipRPCV3Test(t, repository, resolver.root, upstream, nil)}
	kafka := fakeKafka{
		root: relationshipKafkaV3Test(t, repository, rpc.root.Authority, upstream, nil),
	}
	prepared, err := BuildV3(t.Context(), BuildRequestV3{
		Root: t.TempDir(), Catalog: generationB, States: statesB,
		ServiceSummary: summaryB, Resolver: resolver, RPC: rpc, Kafka: kafka,
		Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Root().Authority.CatalogRootDigest != generationB.Root.Digest ||
		prepared.Root().ServiceCount != len(statesB) {
		t.Fatalf("mixed successor build = %+v", prepared.Root())
	}
}

func TestRuntimeV3V1ScheduleRemainsRestartable(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	_, sourceRoot, err := fixture.runtime.openCurrentV2Source(
		t.Context(), fixture.chunk.Repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, generation := relationshipCatalogV3Test(t, fixture.chunk.Repository, 1)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	policy, err := digestValue(FrozenPolicyV3())
	if err != nil {
		t.Fatal(err)
	}
	target, err := runtimeTargetShadowV3(
		fixture.chunk.Repository, sourceRoot.GenerationDigest, sourceRoot.Digest,
		generation.Root.Digest, summary.CatalogControlRevision,
		summary.SummaryDigest, summary.ControlRevision, policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := runtimeBindingV3{
		Schema: runtimeBindingSchemaV3, Repository: fixture.chunk.Repository,
		ScheduleGeneration: target, TargetGeneration: target,
		SourceGeneration: sourceRoot.GenerationDigest, SourceRoot: sourceRoot.Digest,
		CatalogRoot:     generation.Root.Digest,
		CatalogRevision: summary.CatalogControlRevision,
		StateSummary:    summary.SummaryDigest, StateRevision: summary.ControlRevision,
		PolicyDigest: policy,
	}
	if err := setRuntimeBindingDigestV3(&binding); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.writeRuntimeBindingV3(binding); err != nil {
		t.Fatal(err)
	}
	v3Store := &runtimeTestStoreV3{
		runtimeHandleCleanupStore: fixture.store,
		candidate: &store.ServiceCatalogV3Candidate{
			Generation: generation, ControlRevision: summary.CatalogControlRevision,
		},
		summary: summary, states: states,
		schedule: &store.GenerationSchedule{
			Repository: fixture.chunk.Repository, Stage: ScheduleStageV3,
			Generation: target, Status: store.GenerationScheduleActive,
		},
	}
	fixture.runtime.Store = v3Store
	if current, err := fixture.runtime.ReconcileV3(
		t.Context(), fixture.chunk.Repository,
	); err != nil || current || len(v3Store.enqueues) != 0 ||
		v3Store.schedule.Generation != target {
		t.Fatalf(
			"resume v1 schedule reconcile current=%t enqueues=%d schedule=%+v err=%v",
			current, len(v3Store.enqueues), v3Store.schedule, err,
		)
	}
	if err := fixture.runtime.HandleV3(t.Context(), store.GenerationChunk{
		Repository: fixture.chunk.Repository, Stage: ScheduleStageV3,
		Generation: target, Offset: 0, Length: 1,
	}); err != nil {
		t.Fatalf("resume v1 v3 schedule: %v", err)
	}
	publication, err := OpenCurrentV3(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	)
	if err != nil || publication.Root().Authority.CatalogRootDigest != generation.Root.Digest {
		t.Fatalf("v1 schedule publication = %+v, %v", publication, err)
	}
}

func TestRuntimeV3DirectScheduleFencesComponentAuthorityDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*runtimeHandleCleanupStore)
	}{
		{
			name: "upstream provenance",
			mutate: func(state *runtimeHandleCleanupStore) {
				domain := state.domains["proto-contract"]
				domain.RunID += "-replacement"
				state.domains[domain.Domain] = domain
			},
		},
		{
			name: "resolver generation",
			mutate: func(state *runtimeHandleCleanupStore) {
				state.resolver.GenerationDigest = fixedDigest("9")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRuntimeHandleCleanupFixture(t)
			catalog, generation := relationshipCatalogV3Test(
				t, fixture.chunk.Repository, 1,
			)
			states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
			v3Store := &runtimeTestStoreV3{
				runtimeHandleCleanupStore: fixture.store,
				candidate: &store.ServiceCatalogV3Candidate{
					Generation: generation, ControlRevision: summary.CatalogControlRevision,
				},
				summary: summary, states: states,
			}
			fixture.runtime.Store = v3Store
			if _, err := fixture.runtime.ReconcileV3(
				t.Context(), fixture.chunk.Repository,
			); err != nil {
				t.Fatal(err)
			}
			binding, err := fixture.runtime.readRuntimeBindingV3(
				fixture.chunk.Repository, v3Store.schedule.Generation,
			)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(fixture.store)
			err = fixture.runtime.HandleV3(t.Context(), store.GenerationChunk{
				Repository: fixture.chunk.Repository, Stage: ScheduleStageV3,
				Generation: binding.ScheduleGeneration, Offset: 0, Length: 1,
			})
			if !errors.Is(err, ErrPublishing) {
				t.Fatalf("direct component drift = %v", err)
			}
			if _, err := OpenCurrentV3(
				t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
			); !errors.Is(err, ErrNotFound) {
				t.Fatalf("component drift published v3 current: %v", err)
			}
		})
	}
}

func TestRuntimeV3ReconcileSkipsRetainedScheduleIdentity(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	catalog, generation := relationshipCatalogV3Test(t, fixture.chunk.Repository, 1)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	v3Store := &runtimeTestStoreV3{
		runtimeHandleCleanupStore: fixture.store,
		candidate: &store.ServiceCatalogV3Candidate{
			Generation: generation, ControlRevision: summary.CatalogControlRevision,
		},
		summary: summary, states: states, staleEnqueues: 1,
	}
	fixture.runtime.Store = v3Store
	if current, err := fixture.runtime.ReconcileV3(
		t.Context(), fixture.chunk.Repository,
	); err != nil || current {
		t.Fatalf("scheduled v3 reconcile current=%t err=%v", current, err)
	}
	if len(v3Store.enqueues) != 2 ||
		v3Store.enqueues[0].Generation == v3Store.enqueues[1].Generation ||
		v3Store.schedule == nil ||
		v3Store.schedule.Generation != v3Store.enqueues[1].Generation {
		t.Fatalf("retained schedule recovery = %+v", v3Store.enqueues)
	}
	oldBinding, err := fixture.runtime.readRuntimeBindingV3(
		fixture.chunk.Repository, v3Store.enqueues[0].Generation,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.HandleV3(t.Context(), store.GenerationChunk{
		Repository: fixture.chunk.Repository, Stage: ScheduleStageV3,
		Generation: oldBinding.ScheduleGeneration, Offset: 0, Length: 1,
	}); !errors.Is(err, ErrPublishing) {
		t.Fatalf("retained pre-delete chunk fence = %v", err)
	}
	if _, err := OpenCurrentV3(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retained pre-delete chunk published current: %v", err)
	}
}

func (state *runtimeTestStoreV3) PinRelationshipPublicationV3(
	_ context.Context,
	repository, generation, root, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) error {
	state.relationship = append(state.relationship,
		repository+"\x00"+generation+"\x00"+root+"\x00"+catalogRoot+"\x00"+stateSummary,
	)
	if catalogRevision == 0 || stateRevision == 0 {
		return errors.New("invalid relationship v3 pin")
	}
	return nil
}

func (state *runtimeTestStoreV3) UnpinRelationshipPublicationV3(
	_ context.Context,
	_, _, _, _ string,
	_, _ uint64,
	_ string,
) error {
	state.relUnpins++
	return nil
}

func TestRuntimeV3ReconcileHandleAndNoop(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	v2, err := OpenCurrent(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	)
	if err != nil || v2.Root().Schema != RootSchemaV2 {
		t.Fatalf("v2 source = %+v, %v", v2, err)
	}
	v2Root := v2.Root()
	_, generation := relationshipCatalogV3Test(t, fixture.chunk.Repository, 2)
	catalog, err := generation.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	v3Store := &runtimeTestStoreV3{
		runtimeHandleCleanupStore: fixture.store,
		candidate: &store.ServiceCatalogV3Candidate{
			Generation: generation, ControlRevision: summary.CatalogControlRevision,
			PublishedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		},
		summary: summary, states: states,
	}
	fixture.runtime.Store = v3Store

	if _, err := fixture.runtime.ReconcileV3(t.Context(), fixture.chunk.Repository); err != nil {
		t.Fatal(err)
	}
	if len(v3Store.enqueues) != 1 {
		t.Fatalf("v3 enqueue count = %d", len(v3Store.enqueues))
	}
	spec := v3Store.enqueues[0]
	if spec.Stage != ScheduleStageV3 || spec.TotalItems != 1 || spec.ChunkItems != 1 ||
		spec.ResourceClass != store.GenerationResourceMemory {
		t.Fatalf("v3 schedule = %+v", spec)
	}
	chunk := store.GenerationChunk{
		Repository: fixture.chunk.Repository, Stage: ScheduleStageV3,
		Generation: spec.Generation, Offset: 0, Length: 1,
	}
	if err := fixture.runtime.HandleV3(t.Context(), chunk); err != nil {
		t.Fatal(err)
	}
	publication, err := OpenCurrentV3(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	root := publication.Root()
	authority := root.Authority
	if authority.CatalogRootDigest != generation.Root.Digest ||
		authority.CatalogControlRevision != summary.CatalogControlRevision ||
		authority.ServiceStateSummaryDigest != summary.SummaryDigest ||
		authority.ServiceStateControlRevision != summary.ControlRevision ||
		authority.ResolverGenerationDigest != v2Root.Authority.ResolverGenerationDigest ||
		authority.ResolverRootDigest != v2Root.Authority.ResolverRootDigest ||
		authority.RPCGenerationDigest != v2Root.Authority.RPCGenerationDigest ||
		authority.RPCRootDigest != v2Root.Authority.RPCRootDigest ||
		authority.KafkaGenerationDigest != v2Root.Authority.KafkaGenerationDigest ||
		authority.KafkaRootDigest != v2Root.Authority.KafkaRootDigest ||
		v2Root.Authority.Upstream == nil || authority.UpstreamDigest != v2Root.Authority.Upstream.Digest {
		t.Fatalf("v3 authority = %+v\nv2 authority = %+v", authority, v2Root.Authority)
	}
	if len(v3Store.relationship) != 1 ||
		len(fixture.store.pinCalls) != 2*len(fixture.upstream.Domains) {
		t.Fatalf("v3 pins = relationship %q extraction %q",
			v3Store.relationship, fixture.store.pinCalls)
	}
	if _, err := publication.OpenEvidenceReader(t.Context(), fixture.runtime.DataDir); err != nil {
		t.Fatalf("open v3 evidence reader: %v", err)
	}
	missingEvidence := Projection{
		Schema: ProjectionSchema, Kind: "rpc", PostingDigest: fixedDigest("0"),
		Class: "unresolved", Plane: "grpc",
		Source: Placement{Path: "main.go", Unowned: true, Claims: []ServiceClaim{}},
	}
	missingEvidence.Digest, err = digestValue(missingEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publication.ReadEvidence(
		t.Context(), fixture.runtime.DataDir, missingEvidence,
	); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("read missing v3 evidence = %v", err)
	}
	if err := v2.ConfirmCurrent(); err != nil {
		t.Fatalf("v3 publication moved v2 current: %v", err)
	}
	if current, err := fixture.runtime.ReconcileV3(
		t.Context(), fixture.chunk.Repository,
	); err != nil || !current {
		t.Fatal(err)
	}
	if len(v3Store.enqueues) != 1 || len(v3Store.relationship) != 1 {
		t.Fatalf("v3 no-op changed work: enqueues=%d pins=%d",
			len(v3Store.enqueues), len(v3Store.relationship))
	}
}

func TestRuntimeV3LateStateFenceDoesNotPublish(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	catalog, generation := relationshipCatalogV3Test(t, fixture.chunk.Repository, 1)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	v3Store := &runtimeTestStoreV3{
		runtimeHandleCleanupStore: fixture.store,
		candidate: &store.ServiceCatalogV3Candidate{
			Generation: generation, ControlRevision: summary.CatalogControlRevision,
		},
		summary: summary, states: states,
	}
	fixture.runtime.Store = v3Store
	if _, err := fixture.runtime.ReconcileV3(t.Context(), fixture.chunk.Repository); err != nil {
		t.Fatal(err)
	}
	v3Store.failConfirm = v3Store.confirmCalls + 2
	chunk := store.GenerationChunk{
		Repository: fixture.chunk.Repository, Stage: ScheduleStageV3,
		Generation: v3Store.schedule.Generation, Offset: 0, Length: 1,
	}
	err := fixture.runtime.HandleV3(t.Context(), chunk)
	if !errors.Is(err, ErrPublishing) {
		t.Fatalf("late v3 state fence = %v", err)
	}
	if len(v3Store.relationship) != 0 {
		t.Fatalf("late v3 state fence reached relationship pins: %q", v3Store.relationship)
	}
	if _, err := OpenCurrentV3(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late v3 state fence published current: %v", err)
	}
}

func TestPublicationV3ReadProjections(t *testing.T) {
	rootPath := t.TempDir()
	repository := "example.com/acme/v3-page"
	catalog, generation := relationshipCatalogV3Test(t, repository, 2)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	upstream := relationshipUpstreamV3Test(t, repository)
	resolver := relationshipResolverV3Test(t, repository, upstream)
	values := []rpccallerposting.Posting{
		rpcPosting("1", "resolved", "grpc", "first.v1/Get", "services/00000/call.go", ""),
		rpcPosting("2", "resolved", "grpc", "second.v1/Get", "services/00001/call.go", ""),
	}
	rpc := fakeRPC{
		root:   relationshipRPCV3Test(t, repository, resolver.root, upstream, values),
		values: values,
	}
	kafka := fakeKafka{
		root: relationshipKafkaV3Test(t, repository, rpc.root.Authority, upstream, nil),
	}
	prepared, err := BuildV3(t.Context(), BuildRequestV3{
		Root: rootPath, Catalog: generation, States: states, ServiceSummary: summary,
		Resolver: resolver, RPC: rpc, Kafka: kafka, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := PublishV3(t.Context(), prepared, &testPublishPinsV3{})
	if err != nil {
		t.Fatal(err)
	}
	digests := make([]string, 0, 2)
	for _, serviceKey := range []string{"service-00000", "service-00001"} {
		record, readErr := publication.ReadService(t.Context(), serviceKey)
		if readErr != nil || len(record.References) != 1 {
			t.Fatalf("service %q references = %+v, %v", serviceKey, record, readErr)
		}
		digests = append(digests, record.References[0].ProjectionDigest)
	}
	projections, err := publication.ReadProjections(t.Context(), digests)
	if err != nil || len(projections) != len(digests) {
		t.Fatalf("v3 projection page = %+v, %v", projections, err)
	}
	for index, digest := range digests {
		if projections[digest].PostingDigest != values[index].Digest {
			t.Fatalf("v3 projection %q = %+v", digest, projections[digest])
		}
	}
	if _, err := publication.ReadProjections(
		t.Context(), []string{digests[0], digests[0]},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate v3 projection page = %v", err)
	}
}
