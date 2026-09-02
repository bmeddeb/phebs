package relationshippublication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
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
