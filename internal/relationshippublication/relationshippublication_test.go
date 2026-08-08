package relationshippublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/store"
)

type fakeResolver struct{ root resolvernamespace.Root }

func (value fakeResolver) Root() resolvernamespace.Root { return value.root }

type fakeRPC struct {
	root   rpccallerposting.Root
	values []rpccallerposting.Posting
}

func (value fakeRPC) Root() rpccallerposting.Root { return value.root }
func (value fakeRPC) WalkPostings(ctx context.Context, visit func(rpccallerposting.Posting) error) error {
	for _, posting := range value.values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(posting); err != nil {
			return err
		}
	}
	return nil
}

type fakeKafka struct {
	root   kafkatopicposting.Root
	values []kafkatopicposting.Posting
}

type fakeDownstreamSource struct {
	authority observationpublication.DownstreamAuthority
}

func (value fakeDownstreamSource) DownstreamAuthority() observationpublication.DownstreamAuthority {
	return value.authority
}

func (fakeDownstreamSource) WalkObserved(
	ctx context.Context,
	visit func(observationpublication.Record, sourceobservation.Observation) error,
) error {
	if visit == nil {
		return errors.New("nil observation visitor")
	}
	return ctx.Err()
}

type fakeRecoveryPins struct {
	pins       []string
	owners     []string
	reconciles int
}

func (value *fakeRecoveryPins) PinPartitionedExtractionRun(
	_ context.Context, runID, owner string,
) error {
	value.pins = append(value.pins, runID+"\x00"+owner)
	return nil
}

func (value *fakeRecoveryPins) ReconcilePartitionedExtractionOwners(
	_ context.Context, owners []string,
) error {
	value.reconciles++
	value.owners = slices.Clone(owners)
	return nil
}

func (value fakeKafka) Root() kafkatopicposting.Root { return value.root }
func (value fakeKafka) WalkPostings(ctx context.Context, visit func(kafkatopicposting.Posting) error) error {
	for _, posting := range value.values {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(posting); err != nil {
			return err
		}
	}
	return nil
}

func TestBuildProjectsSharedUnownedAndTargetAuthorityAtomically(t *testing.T) {
	root := t.TempDir()
	catalog, states := relationshipCatalog(t)
	resolver := fakeResolver{root: resolverRoot(t, catalog.Repository)}
	rpcValues := []rpccallerposting.Posting{
		rpcPosting("a", "resolved", "grpc", "orders.v1.Orders/Get", "services/orders/call.go", "services/payments/api.proto"),
		rpcPosting("b", "resolved", "grpc", "shared.v1.Shared/Get", "shared/util.go", "misc/free.go"),
		rpcPosting("c", "unresolved", "grpc", "", "misc/free.go", ""),
	}
	kafkaValues := []kafkatopicposting.Posting{
		kafkaPosting("d", "producer", "literal", "orders-v1", "services/orders/kafka.go"),
		kafkaPosting("e", "consumer", "literal", "orders-v1", "services/payments/kafka.go"),
		kafkaPosting("f", "producer", "unresolved", "", "misc/free.go"),
	}
	rpc := fakeRPC{values: rpcValues}
	rpc.root = rpcRoot(t, catalog.Repository, resolver.root, rpcValues)
	kafka := fakeKafka{values: kafkaValues}
	kafka.root = kafkaRoot(t, catalog.Repository, rpc.root.Authority, kafkaValues)
	prepared, err := buildSources(t.Context(), root, catalog, states, resolver, rpc, kafka)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rootValue := publication.Root()
	if !rootValue.RepositoryComplete || !rootValue.AllServicesComplete ||
		rootValue.ProjectionCount != 6 || rootValue.ServiceCount != 2 ||
		rootValue.CompleteServiceCount != 2 || rootValue.FailedServiceCount != 0 {
		t.Fatalf("root = %+v", rootValue)
	}
	ordersReceipt, orders, err := publication.OpenService(t.Context(), "orders")
	if err != nil || ordersReceipt.State != "complete" || len(orders.References) != 3 {
		t.Fatalf("orders = %+v %+v, %v", ordersReceipt, orders, err)
	}
	paymentsReceipt, payments, err := publication.OpenService(t.Context(), "payments")
	if err != nil || paymentsReceipt.State != "complete" || len(payments.References) != 3 {
		t.Fatalf("payments = %+v %+v, %v", paymentsReceipt, payments, err)
	}
	foundShared := false
	foundUnownedTarget := false
	projectionDigests := []string{}
	for _, receipt := range rootValue.RepositoryMembers {
		member, err := publication.openRepositoryMember(t.Context(), receipt)
		if err != nil {
			t.Fatal(err)
		}
		for _, projection := range member.Projections {
			if len(projectionDigests) < 2 {
				projectionDigests = append(projectionDigests, projection.Digest)
			}
			if projection.PostingDigest == rpcValues[1].Digest {
				foundShared = len(projection.Source.Claims) == 2
				foundUnownedTarget = projection.Target != nil && projection.Target.Unowned &&
					len(projection.Target.Claims) == 1 &&
					projection.Target.Claims[0].Disposition == servicecatalog.DispositionRejected
			}
		}
	}
	if !foundShared || !foundUnownedTarget {
		t.Fatalf("shared=%t unowned-target=%t", foundShared, foundUnownedTarget)
	}
	projections, err := publication.ReadProjections(t.Context(), projectionDigests)
	if err != nil || len(projections) != len(projectionDigests) {
		t.Fatalf("batched projections = %d, %v", len(projections), err)
	}
	cache := &Cache{}
	lease, err := cache.AcquireGeneration(
		t.Context(), root, catalog.Repository,
		rootValue.GenerationDigest, rootValue.Digest,
	)
	if err != nil || !cache.Pinned(catalog.Repository, rootValue.GenerationDigest) {
		t.Fatalf("historical lease = %v, pinned=%t", err, cache.Pinned(catalog.Repository, rootValue.GenerationDigest))
	}
	if _, member, err := lease.Publication().ReadService(t.Context(), "payments"); err != nil || len(member.References) != 3 {
		t.Fatalf("historical service read = %+v, %v", member, err)
	}
	lease.Release()
	releaseRetire, admitted := cache.BeginRetire(catalog.Repository, rootValue.GenerationDigest)
	if !admitted {
		t.Fatal("unleased historical generation was not admitted for retirement")
	}
	if _, err := cache.AcquireGeneration(
		t.Context(), root, catalog.Repository,
		rootValue.GenerationDigest, rootValue.Digest,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retiring historical generation admitted a new lease: %v", err)
	}
	if _, err := cache.Acquire(t.Context(), root, catalog.Repository); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retiring generation admitted a delayed current-root lease: %v", err)
	}
	releaseRetire()
	reopened, err := cache.AcquireGeneration(
		t.Context(), root, catalog.Repository,
		rootValue.GenerationDigest, rootValue.Digest,
	)
	if err != nil {
		t.Fatalf("released retirement guard blocked historical lease: %v", err)
	}
	reopened.Release()
	collected := publication.directory + ".collected"
	if err := os.Rename(publication.directory, collected); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.AcquireGeneration(
		t.Context(), root, catalog.Repository,
		rootValue.GenerationDigest, rootValue.Digest,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released historical cache entry survived collection: %v", err)
	}
	if err := os.Rename(collected, publication.directory); err != nil {
		t.Fatal(err)
	}

	// A corrupt service partition cannot block its sibling's sparse read.
	if err := os.WriteFile(filepath.Join(publication.directory, ordersReceipt.Name), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, member, err := publication.OpenService(t.Context(), "payments"); err != nil || len(member.References) != 3 {
		t.Fatalf("sibling read = %+v, %v", member, err)
	}
	if _, _, err := publication.OpenService(t.Context(), "orders"); err == nil {
		t.Fatal("corrupt selected service partition was accepted")
	}
}

func TestServiceIncarnationChangesRootIdentity(t *testing.T) {
	root := t.TempDir()
	catalog, states := relationshipCatalog(t)
	resolver := fakeResolver{root: resolverRoot(t, catalog.Repository)}
	rpc := fakeRPC{values: []rpccallerposting.Posting{}}
	rpc.root = rpcRoot(t, catalog.Repository, resolver.root, rpc.values)
	kafka := fakeKafka{values: []kafkatopicposting.Posting{}}
	kafka.root = kafkaRoot(t, catalog.Repository, rpc.root.Authority, kafka.values)
	first, err := buildSources(t.Context(), root, catalog, states, resolver, rpc, kafka)
	if err != nil {
		t.Fatal(err)
	}
	projections, err := servicecatalog.ProjectServices(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for index := range states {
		if states[index].ServiceKey != "orders" {
			continue
		}
		states[index].Incarnation = 2
		for _, projection := range projections {
			if projection.Service.Key == "orders" {
				states[index].DesiredGeneration, err = servicecatalog.ServiceDesiredGeneration(projection, 2)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := servicecatalog.SetServiceStateDigest(&states[index]); err != nil {
			t.Fatal(err)
		}
	}
	second, err := buildSources(t.Context(), root, catalog, states, resolver, rpc, kafka)
	if err != nil {
		t.Fatal(err)
	}
	if first.rootValue.GenerationDigest == second.rootValue.GenerationDigest ||
		first.rootValue.Authority.ServiceStateSetDigest == second.rootValue.Authority.ServiceStateSetDigest {
		t.Fatal("removal/re-add incarnation reused prior relationship authority")
	}
}

func TestServiceLimitPublishesFailedPartitionWithoutBlockingSibling(t *testing.T) {
	root := t.TempDir()
	authority := testAuthority(t, "example.com/acme/mono")
	desiredOne := fixedDigest("1")
	desiredTwo := fixedDigest("2")
	accumulator := &buildAccumulator{
		repository: map[int][]Projection{}, seen: map[string]struct{}{},
		services: map[string]*serviceAccumulator{
			"orders":   {state: servicecatalog.ServiceState{ServiceKey: "orders", Incarnation: 1, DesiredGeneration: desiredOne}, refs: []ServiceReference{}},
			"payments": {state: servicecatalog.ServiceState{ServiceKey: "payments", Incarnation: 1, DesiredGeneration: desiredTwo}, refs: []ServiceReference{}},
		},
		serviceRefLimit: 1, totalRefLimit: 10, residentLimit: MaxResidentChargeBytes,
	}
	first := testReference(t, "1")
	second := testReference(t, "2")
	if err := accumulator.addServiceReference("orders", first); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.addServiceReference("orders", second); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.addServiceReference("payments", first); err != nil {
		t.Fatal(err)
	}
	stateSet, err := serviceStateSetDigest(accumulator.services)
	if err != nil {
		t.Fatal(err)
	}
	authority.ServiceStateSetDigest = stateSet
	authorityDigest := mustDigest(t, authority)
	prepared, err := writePublicationStage(t.Context(), root, authority, authorityDigest, accumulator)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rootValue := publication.Root()
	if rootValue.AllServicesComplete || rootValue.FailedServiceCount != 1 || rootValue.CompleteServiceCount != 1 {
		t.Fatalf("partial root = %+v", rootValue)
	}
	failed, _, err := publication.OpenService(t.Context(), "orders")
	if !errors.Is(err, ErrServiceUnavailable) || failed.Reason != "reference_limit" {
		t.Fatalf("failed partition = %+v, %v", failed, err)
	}
	if _, member, err := publication.OpenService(t.Context(), "payments"); err != nil || len(member.References) != 1 {
		t.Fatalf("healthy sibling = %+v, %v", member, err)
	}
}

func TestResidentFenceFailsOnlyCrossingService(t *testing.T) {
	root := t.TempDir()
	authority := testAuthority(t, "example.com/acme/resident")
	first := testReference(t, "1")
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	accumulator := &buildAccumulator{
		repository: map[int][]Projection{}, seen: map[string]struct{}{},
		services: map[string]*serviceAccumulator{
			"orders": {state: servicecatalog.ServiceState{
				ServiceKey: "orders", Incarnation: 1, DesiredGeneration: fixedDigest("1"),
			}, refs: []ServiceReference{}},
			"payments": {state: servicecatalog.ServiceState{
				ServiceKey: "payments", Incarnation: 1, DesiredGeneration: fixedDigest("2"),
			}, refs: []ServiceReference{}},
		},
		serviceRefLimit: 10, totalRefLimit: 10,
		residentLimit: int64(len(raw)+256) + 1,
	}
	if err := accumulator.addServiceReference("orders", first); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.addServiceReference("orders", testReference(t, "2")); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.addServiceReference("payments", first); err != nil {
		t.Fatal(err)
	}
	authority.ServiceStateSetDigest, err = serviceStateSetDigest(accumulator.services)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := writePublicationStage(
		t.Context(), root, authority, mustDigest(t, authority), accumulator,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	failed, _, err := publication.OpenService(t.Context(), "orders")
	if !errors.Is(err, ErrServiceUnavailable) || failed.Reason != "resident_limit" {
		t.Fatalf("resident failure = %+v, %v", failed, err)
	}
	complete, member, err := publication.OpenService(t.Context(), "payments")
	if err != nil || complete.State != "complete" || len(member.References) != 1 {
		t.Fatalf("sibling partition = %+v, %+v, %v", complete, member, err)
	}
}

func TestAuthorizationPrecedesRepositoryValidationAndFilesystem(t *testing.T) {
	called := false
	_, _, err := OpenAuthorizedRoot(t.Context(), "/does/not/exist", "not canonical", func(_ context.Context, repository string) (bool, error) {
		called = true
		if repository != "not canonical" {
			t.Fatalf("repository = %q", repository)
		}
		return false, nil
	})
	if !called || !errors.Is(err, ErrDenied) {
		t.Fatalf("authorization-first result = called %t, err %v", called, err)
	}
}

func TestScheduleIdentityReusesActiveTargetAndDistinguishesABA(t *testing.T) {
	repository := "example.com/acme/schedule"
	target, err := runtimeTarget(
		repository, fixedDigest("1"), fixedDigest("2"), fixedDigest("6"), fixedDigest("3"),
	)
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := fixedDigest("4")
	currentDigest := fixedDigest("5")
	runtime := &Runtime{
		DataDir: t.TempDir(),
		Store: &scheduleIdentityStore{schedule: &store.GenerationSchedule{
			Repository: repository, Stage: ScheduleStage,
			Generation: currentGeneration, Digest: currentDigest,
			Status: store.GenerationScheduleActive,
		}},
	}
	binding := scheduleBinding{
		Schema: BindingSchema, Repository: repository,
		ScheduleGeneration: currentGeneration, TargetGeneration: target,
		ObservationGeneration: fixedDigest("1"), CatalogGeneration: fixedDigest("2"),
		ServiceStateSet: fixedDigest("6"), ResolverGeneration: fixedDigest("3"),
	}
	if err := setBindingDigest(&binding); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeBinding(binding); err != nil {
		t.Fatal(err)
	}
	generation, prior, err := runtime.scheduleIdentity(t.Context(), repository, target)
	if err != nil || generation != currentGeneration || prior != "" {
		t.Fatalf("active identity = %q, %q, %v", generation, prior, err)
	}
	runtime.Store.(*scheduleIdentityStore).schedule.Status = store.GenerationScheduleSettled
	generation, prior, err = runtime.scheduleIdentity(t.Context(), repository, target)
	if err != nil || generation == target || generation == currentGeneration || prior != currentDigest {
		t.Fatalf("recovery identity = %q, %q, %v", generation, prior, err)
	}
	repeated, repeatedPrior, err := runtime.scheduleIdentity(t.Context(), repository, target)
	if err != nil || repeated != generation || repeatedPrior != prior {
		t.Fatalf("deterministic recovery = %q, %q, %v", repeated, repeatedPrior, err)
	}
}

func TestDomainAuthorityFromPriorObservationIsAbsentFromNewAggregate(t *testing.T) {
	required := downstreamauthority.DomainIdentity{Domain: "proto-contract", Version: "v1"}
	observation := observationpublication.DownstreamAuthority{
		SourceGenerationDigest: fixedDigest("1"), ObservationGenerationDigest: fixedDigest("2"),
	}
	domain := candidate.DownstreamDomainAuthority{
		Domain: required.Domain, Version: required.Version,
		SourceGenerationDigest: fixedDigest("1"), ObservationGenerationDigest: fixedDigest("0"),
	}
	if domainMatchesObservation(required, domain, observation) {
		t.Fatal("prior-observation domain remained eligible for the new relationship aggregate")
	}
	domain.ObservationGenerationDigest = observation.ObservationGenerationDigest
	if !domainMatchesObservation(required, domain, observation) {
		t.Fatal("exact current-observation domain was not eligible")
	}
}

type scheduleIdentityStore struct {
	RuntimeStore
	schedule *store.GenerationSchedule
}

func (state *scheduleIdentityStore) GetGenerationSchedule(
	_ context.Context, _, _ string,
) (*store.GenerationSchedule, error) {
	if state.schedule == nil {
		return nil, store.ErrNotFound
	}
	value := *state.schedule
	return &value, nil
}

func TestRecoveryValidatesCompleteGenerationBeforePointerSwap(t *testing.T) {
	root := t.TempDir()
	catalog, states := relationshipCatalog(t)
	resolver := fakeResolver{root: resolverRoot(t, catalog.Repository)}
	rpc := fakeRPC{values: []rpccallerposting.Posting{rpcPosting("a", "unresolved", "grpc", "", "misc/free.go", "")}}
	rpc.root = rpcRoot(t, catalog.Repository, resolver.root, rpc.values)
	kafka := fakeKafka{values: []kafkatopicposting.Posting{}}
	kafka.root = kafkaRoot(t, catalog.Repository, rpc.root.Authority, kafka.values)
	prepared, err := buildSources(t.Context(), root, catalog, states, resolver, rpc, kafka)
	if err != nil {
		t.Fatal(err)
	}
	// Install the immutable directory and marker without swapping current.
	target := generationPath(root, catalog.Repository, prepared.rootValue.GenerationDigest)
	if err := os.Rename(prepared.directory, target); err != nil {
		t.Fatal(err)
	}
	pointer, _ := newPointer(prepared.rootValue)
	marker := Marker{Schema: MarkerSchema, Pointer: pointer}
	marker.Digest = mustDigest(t, Marker{Schema: marker.Schema, Pointer: marker.Pointer})
	raw, _ := json.Marshal(marker)
	if err := replaceFile(filepath.Join(repositoryRoot(root, catalog.Repository), "publishing.json"), raw); err != nil {
		t.Fatal(err)
	}
	if recovered, err := Recover(t.Context(), root, catalog.Repository); err != nil || !recovered {
		t.Fatalf("recover = %t, %v", recovered, err)
	}
	if _, err := OpenCurrent(t.Context(), root, catalog.Repository); err != nil {
		t.Fatal(err)
	}
	// A subsequent corrupt marked generation cannot replace the current root.
	currentPath := filepath.Join(repositoryRoot(root, catalog.Repository), "current.json")
	priorCurrent, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	badPointer := Pointer{Schema: PointerSchema, Repository: catalog.Repository, GenerationDigest: fixedDigest("d"), RootDigest: fixedDigest("e"), RootName: "root.json"}
	badPointer.Digest = mustDigest(t, badPointer)
	badMarker := Marker{Schema: MarkerSchema, Pointer: badPointer}
	badMarker.Digest = mustDigest(t, badMarker)
	badDirectory := generationPath(root, catalog.Repository, badPointer.GenerationDigest)
	if err := os.Mkdir(badDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDirectory, "root.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	badRaw, _ := json.Marshal(badMarker)
	if err := replaceFile(filepath.Join(repositoryRoot(root, catalog.Repository), "publishing.json"), badRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(t.Context(), root, catalog.Repository); err == nil {
		t.Fatal("corrupt marked generation recovered")
	}
	afterCurrent, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(priorCurrent, afterCurrent) {
		t.Fatal("corrupt recovery displaced prior pointer")
	}
}

func TestLifecyclePreservesCurrentRollbackFloorAndReaderLease(t *testing.T) {
	root := t.TempDir()
	repository := "example.com/acme/lifecycle"
	dataDir := filepath.Join(root, "data")
	relationshipRoot := filepath.Join(dataDir, "relationships")
	if err := os.MkdirAll(relationshipRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	var publications []*Publication
	cache := &Cache{}
	var lease *Lease
	for index, suffix := range []string{"a", "b", "c", "d"} {
		authority := testAuthority(t, repository)
		authority.KafkaGenerationDigest = fixedDigest(suffix)
		authorityDigest := mustDigest(t, authority)
		accumulator := &buildAccumulator{
			repository: map[int][]Projection{}, services: map[string]*serviceAccumulator{},
			seen: map[string]struct{}{}, serviceRefLimit: MaxServiceReferences,
			totalRefLimit: MaxTotalServiceReferences, residentLimit: MaxResidentChargeBytes,
		}
		stage, err := writePublicationStage(
			t.Context(), relationshipRoot, authority, authorityDigest, accumulator,
		)
		if err != nil {
			t.Fatal(err)
		}
		publication, err := stage.Publish(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		publications = append(publications, publication)
		if index == 1 {
			lease, err = cache.Acquire(t.Context(), relationshipRoot, repository)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	defer lease.Release()
	// Pin generation B explicitly while D is current. A is the only first-turn
	// candidate, proving current plus one rollback generation remain rooted.
	result, err := SweepLifecycle(
		t.Context(), dataDir, time.Now().UTC(), "", cache, 64,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 0 || result.ReleasedPinOwner == "" {
		t.Fatalf("rename-before-unpin lifecycle result = %+v", result)
	}
	// A transient store-unpin failure leaves the collecting identity durable
	// and returns the same owner on the next turn.
	retry, err := SweepLifecycle(
		t.Context(), dataDir, time.Now().UTC(), result.Cursor, cache, 64,
	)
	if err != nil || retry.ReleasedPinOwner != result.ReleasedPinOwner || retry.Deleted != 0 {
		t.Fatalf("unconfirmed unpin retry = %+v, %v", retry, err)
	}
	if err := ConfirmLifecycleUnpin(
		t.Context(), dataDir, result.Cursor, result.ReleasedPinOwner,
	); err != nil {
		t.Fatal(err)
	}
	result, err = SweepLifecycle(
		t.Context(), dataDir, time.Now().UTC(), result.Cursor, cache, 64,
	)
	if err != nil || result.Deleted == 0 || result.ReleasedPinOwner != "" {
		t.Fatalf("confirmed collection drain = %+v, %v", result, err)
	}
	if _, err := os.Lstat(generationPath(
		relationshipRoot, repository, publications[0].Root().GenerationDigest,
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest generation remains: %v", err)
	}
	for _, publication := range publications[1:] {
		if _, err := os.Lstat(generationPath(
			relationshipRoot, repository, publication.Root().GenerationDigest,
		)); err != nil {
			t.Fatalf("protected generation missing: %v", err)
		}
	}
	current, err := OpenCurrent(t.Context(), relationshipRoot, repository)
	if err != nil || current.Root().Digest != publications[3].Root().Digest {
		t.Fatalf("current relationship changed: %v", err)
	}
}

func TestLifecycleRepairsCrashStageAndOverLimitGenerationInventory(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	repository := "example.com/acme/repair-pressure"
	base := repositoryRoot(root, repository)
	stage := filepath.Join(base, ".stage-crash")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "partial.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SweepLifecycle(t.Context(), dataDir, time.Now().UTC(), "", &Cache{}, 8)
	if err != nil || result.Deleted == 0 {
		t.Fatalf("stage repair = %+v, %v", result, err)
	}
	if _, err := os.Lstat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage-only repository remains: %v", err)
	}
	stage = filepath.Join(base, ".stage-restart")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "partial.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPins{}
	report, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil || report.Invalid != 0 {
		t.Fatalf("startup stage repair = %+v, %v", report, err)
	}
	if _, err := os.Lstat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup stage-only repository remains: %v", err)
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	authority := testAuthority(t, repository)
	accumulator := &buildAccumulator{
		repository: map[int][]Projection{}, services: map[string]*serviceAccumulator{},
		seen: map[string]struct{}{}, serviceRefLimit: MaxServiceReferences,
		totalRefLimit: MaxTotalServiceReferences, residentLimit: MaxResidentChargeBytes,
	}
	prepared, err := writePublicationStage(
		t.Context(), root, authority, mustDigest(t, authority), accumulator,
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, err := os.ReadFile(filepath.Join(publication.directory, "root.json"))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxRepositoryGenerations; index++ {
		name := fmt.Sprintf("%064x", index+1)
		directory := filepath.Join(base, name)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "root.json"), rootRaw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pressureCache := &Cache{}
	result, err = SweepLifecycle(t.Context(), dataDir, time.Now().UTC(), "", pressureCache, 8)
	if err != nil || result.ReleasedPinOwner == "" || result.Deleted != 0 {
		t.Fatalf("over-limit generation retirement = %+v, %v", result, err)
	}
	if err := ConfirmLifecycleUnpin(t.Context(), dataDir, result.Cursor, result.ReleasedPinOwner); err != nil {
		t.Fatal(err)
	}
	result, err = SweepLifecycle(t.Context(), dataDir, time.Now().UTC(), result.Cursor, pressureCache, 8)
	if err != nil || result.Deleted == 0 {
		t.Fatalf("over-limit generation repair = %+v, %v", result, err)
	}
}

func TestConfirmedCollectionRetainsMarkerAcrossOneDeleteTurns(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "root.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, collectionUnpinnedName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deleted, complete, err := drainUnpinnedCollection(directory, 1)
	if err != nil || deleted != 1 || complete {
		t.Fatalf("first one-delete turn = deleted %d complete %t err %v", deleted, complete, err)
	}
	if unpinned, err := collectionUnpinned(directory); err != nil || !unpinned {
		t.Fatalf("collection marker after partial drain = %t, %v", unpinned, err)
	}
	deleted, complete, err = drainUnpinnedCollection(directory, 1)
	if err != nil || deleted != 1 || !complete {
		t.Fatalf("final one-delete turn = deleted %d complete %t err %v", deleted, complete, err)
	}
}

func TestRecoveryDiscoversAndRemovesComponentOnlyCrashNamespaces(t *testing.T) {
	dataDir := t.TempDir()
	repository := "example.com/acme/component-only-recovery"
	hash := repositoryHash(repository)
	directories := []struct {
		path       string
		generation string
	}{
		{
			path:       filepath.Join(dataDir, "relationships", "relationship-publications", hash),
			generation: ".stage-relationship",
		},
		{
			path:       filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces", hash),
			generation: ".stage-resolver",
		},
		{
			path:       filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings", hash),
			generation: strings.Repeat("a", 64),
		},
		{
			path:       filepath.Join(dataDir, "relationship-kafka-postings", "kafka-topic-postings", hash),
			generation: strings.Repeat("b", 64),
		},
	}
	for _, directory := range directories {
		generation := filepath.Join(directory.path, directory.generation)
		if err := os.MkdirAll(generation, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(generation, "partial.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pins := &fakeRecoveryPins{}
	report, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil || report.Repositories != 0 || report.Invalid != 0 || len(pins.owners) != 0 {
		t.Fatalf("component-only recovery = %+v, owners=%q, %v", report, pins.owners, err)
	}
	for _, directory := range directories {
		if _, err := os.Lstat(directory.path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan component namespace remains at %q: %v", directory.path, err)
		}
	}
}

func TestRecoveryDiscoveryReservesUnionHeadroomAtRelationshipCeiling(t *testing.T) {
	dataDir := t.TempDir()
	relationshipBase := filepath.Join(dataDir, "relationships", "relationship-publications")
	componentBase := filepath.Join(dataDir, "relationship-rpc-postings", "rpc-caller-postings")
	if err := os.MkdirAll(relationshipBase, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxLifecycleRepositories; index++ {
		if err := os.Mkdir(filepath.Join(relationshipBase, fmt.Sprintf("%064x", index)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	extra := fmt.Sprintf("%064x", MaxLifecycleRepositories)
	if err := os.MkdirAll(filepath.Join(componentBase, extra), 0o700); err != nil {
		t.Fatal(err)
	}
	hashes, _, invalid, err := discoverRecoveryNamespaces(dataDir)
	if err != nil || invalid != 0 || len(hashes) != MaxLifecycleRepositories+1 || hashes[len(hashes)-1] != extra {
		t.Fatalf("recovery union = %d namespaces, invalid=%d, err=%v", len(hashes), invalid, err)
	}
}

func TestRecoveryOwnerSelectionIsIndependentOfRepairGenerationEnvelope(t *testing.T) {
	directory := t.TempDir()
	for index := 0; index <= MaxRepositoryGenerations; index++ {
		name := fmt.Sprintf("%064x", index+1)
		if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := boundedLifecycleDirectory(directory, MaxRepositoryRepairEntries)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := recoveryProtectedGenerations(directory, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(protected) != RetainedGenerations-1 || MaxRecoveryOwners != 16_384 {
		t.Fatalf("protected owners = %d, global bound = %d", len(protected), MaxRecoveryOwners)
	}
}

func TestUnavailableAuthorityRetiresFallbackAndIsVisiblyOmitted(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	catalog, states := relationshipCatalog(t)
	resolver := fakeResolver{root: resolverRoot(t, catalog.Repository)}
	rpc := fakeRPC{}
	rpc.root = rpcRoot(t, catalog.Repository, resolver.root, nil)
	kafka := fakeKafka{}
	kafka.root = kafkaRoot(t, catalog.Repository, rpc.root.Authority, nil)
	prepared, err := buildSources(t.Context(), root, catalog, states, resolver, rpc, kafka)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: catalog.Repository,
		SourceGenerationDigest: fixedDigest("1"), SourceRootDigest: fixedDigest("2"),
		ObservationGenerationDigest: fixedDigest("3"), ObservationRootDigest: fixedDigest("4"),
		PartitionPolicyDigest: fixedDigest("5"), ObservationPolicyDigest: fixedDigest("6"),
		InventoryPolicyDigest: fixedDigest("7"), RecordCount: 1, ObservedCount: 1,
	}
	upstream, err := downstreamauthority.BuildRequired(
		observation,
		[]downstreamauthority.DomainIdentity{{Domain: "proto-contract", Version: "v1"}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := MarkUnavailable(t.Context(), root, catalog.Repository, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Prior == nil || unavailable.Prior.GenerationDigest != publication.Root().GenerationDigest {
		t.Fatalf("unavailable rollback floor = %+v", unavailable.Prior)
	}
	repeated, err := MarkUnavailable(t.Context(), root, catalog.Repository, upstream)
	if err != nil || repeated.Prior == nil ||
		repeated.Prior.GenerationDigest != publication.Root().GenerationDigest {
		t.Fatalf("repeated unavailable rollback floor = %+v, %v", repeated.Prior, err)
	}
	marker := Marker{Schema: MarkerSchema, Pointer: publication.pointer}
	marker.Digest, err = digestValue(Marker{Schema: marker.Schema, Pointer: marker.Pointer})
	if err != nil {
		t.Fatal(err)
	}
	markerRaw, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(repositoryRoot(root, catalog.Repository), "publishing.json"), markerRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCurrent(t.Context(), root, catalog.Repository); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unavailable fallback open = %v, want not found", err)
	}
	if recovered, err := Recover(t.Context(), root, catalog.Repository); err != nil || recovered {
		t.Fatalf("unavailable recovery = %t, %v", recovered, err)
	}
	if _, err := os.Lstat(filepath.Join(repositoryRoot(root, catalog.Repository), "publishing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale publishing marker survived unavailable recovery: %v", err)
	}
	retained, present, err := readUnavailable(root, catalog.Repository)
	if err != nil || !present || retained.Prior == nil ||
		retained.Prior.GenerationDigest != publication.Root().GenerationDigest {
		t.Fatalf("recovered unavailable rollback floor = %+v, %t, %v", retained.Prior, present, err)
	}
	if _, err := os.Lstat(generationPath(root, catalog.Repository, publication.Root().GenerationDigest)); err != nil {
		t.Fatalf("unavailable rollback floor missing: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "relationships.tar")
	report, err := CreateArchive(t.Context(), dataDir, archive)
	if err != nil {
		t.Fatal(err)
	}
	if report.Publications != 0 || report.Omitted != 1 || report.Files != 0 {
		t.Fatalf("unavailable archive report = %+v", report)
	}
}

func TestRecoverAllRepinsOnlyValidatedV2RelationshipRoots(t *testing.T) {
	dataDir := t.TempDir()
	catalog, states := relationshipCatalog(t)
	repository := catalog.Repository
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: repository,
		SourceGenerationDigest: fixedDigest("1"), SourceRootDigest: fixedDigest("2"),
		ObservationGenerationDigest: fixedDigest("3"), ObservationRootDigest: fixedDigest("4"),
		PartitionPolicyDigest: fixedDigest("5"), ObservationPolicyDigest: fixedDigest("6"),
		InventoryPolicyDigest: fixedDigest("7"),
	}
	domain := candidate.DownstreamDomainAuthority{
		Domain: "proto-contract", Version: "v1", PlanDigest: fixedDigest("8"),
		RootDigest: fixedDigest("9"), RunID: "partition-run", Disposition: candidate.PartitionResultEmpty,
		CandidateManifestDigest: fixedDigest("a"), CandidatePartitionRootDigest: fixedDigest("b"),
		CandidatePolicyDigest: fixedDigest("c"), SourceGenerationDigest: observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      fixedDigest("d"), DomainIndexDigest: fixedDigest("e"),
		DomainScheduleDigest: fixedDigest("f"),
	}
	upstream, err := downstreamauthority.Build(observation, []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{
		"relationships", "relationship-resolver-namespaces",
		"relationship-rpc-postings", "relationship-kafka-postings",
	} {
		if err := os.Mkdir(filepath.Join(dataDir, root), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resolverStage, err := resolvernamespace.BuildV2(t.Context(), resolvernamespace.BuildRequestV2{
		BuildRequest: resolvernamespace.BuildRequest{
			Root: filepath.Join(dataDir, "relationship-resolver-namespaces"), Repository: repository,
			Commit: strings.Repeat("a", 40), ResolverGenerationDigest: fixedDigest("1"),
			ResolverManifestDigest: fixedDigest("2"), Descriptors: []gocaller.DirectDescriptor{},
		},
		Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := resolverStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source := fakeDownstreamSource{authority: observation}
	rpcStage, err := rpccallerposting.BuildV2(t.Context(), rpccallerposting.BuildRequestV2{
		Root: filepath.Join(dataDir, "relationship-rpc-postings"), Observations: source, Resolver: resolver,
		Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := rpcStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	kafkaStage, err := kafkatopicposting.BuildV2(t.Context(), kafkatopicposting.BuildRequestV2{
		Root: filepath.Join(dataDir, "relationship-kafka-postings"), Observations: source,
		Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	kafka, err := kafkaStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	summary := relationshipSummary(t, catalog, states)
	stage, err := BuildV2(t.Context(), BuildRequestV2{
		BuildRequest: BuildRequest{
			Root: filepath.Join(dataDir, "relationships"), Catalog: catalog, States: states,
			Resolver: resolver, RPC: rpc, Kafka: kafka,
		},
		Upstream: upstream, ServiceSummary: summary,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := stage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !matchesCurrentV2Authority(publication.Root(), upstream, summary, resolver.Root().GenerationDigest) {
		t.Fatal("exact point controls did not recognize the current v2 relationship root")
	}
	changedSummary := summary
	changedSummary.ControlRevision++
	if matchesCurrentV2Authority(publication.Root(), upstream, changedSummary, resolver.Root().GenerationDigest) {
		t.Fatal("changed service-summary revision incorrectly reused the current relationship root")
	}

	pins := &fakeRecoveryPins{}
	report, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil {
		t.Fatal(err)
	}
	owner := "relationship:" + publication.Root().GenerationDigest
	if report.Repositories != 1 || len(pins.pins) != 1 ||
		pins.pins[0] != domain.RunID+"\x00"+owner || !slices.Equal(pins.owners, []string{owner}) {
		t.Fatalf("recovery = %+v pins=%q owners=%q", report, pins.pins, pins.owners)
	}
	malformedStage := filepath.Join(
		filepath.Join(dataDir, "relationship-resolver-namespaces", "resolver-namespaces", repositoryHash(repository)),
		".stage-malformed",
	)
	if err := os.WriteFile(malformedStage, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins.reconciles = 0
	incomplete, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil || incomplete.Invalid == 0 || pins.reconciles != 0 ||
		!slices.Equal(pins.owners, []string{owner}) {
		t.Fatalf("incomplete audit = %+v reconciles=%d owners=%q err=%v",
			incomplete, pins.reconciles, pins.owners, err)
	}
	if err := os.Remove(malformedStage); err != nil {
		t.Fatal(err)
	}

	corrupt := filepath.Join(
		repositoryRoot(filepath.Join(dataDir, "relationships"), repository), strings.Repeat("0", 64),
	)
	if err := os.Mkdir(corrupt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corrupt, "root.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins.pins = nil
	pins.reconciles = 0
	corruptReport, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil {
		t.Fatal(err)
	}
	if corruptReport.Invalid == 0 || pins.reconciles != 0 || len(pins.pins) != 1 ||
		!slices.Equal(pins.owners, []string{owner}) {
		t.Fatalf("corrupt generation audit: report=%+v reconciles=%d pins=%q owners=%q",
			corruptReport, pins.reconciles, pins.pins, pins.owners)
	}
}

func TestArchiveRoundTripPreservesExactCompositeAuthority(t *testing.T) {
	dataDir := t.TempDir()
	catalog, states := relationshipCatalog(t)
	repository := catalog.Repository
	resolverBase := filepath.Join(dataDir, "relationship-resolver-namespaces")
	for _, root := range []string{
		resolverBase,
		filepath.Join(dataDir, "relationship-rpc-postings"),
		filepath.Join(dataDir, "relationship-kafka-postings"),
		filepath.Join(dataDir, "relationships"),
	} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resolverStage, err := resolvernamespace.Build(t.Context(), resolvernamespace.BuildRequest{
		Root: resolverBase, Repository: repository, Commit: strings.Repeat("a", 40),
		ResolverGenerationDigest: fixedDigest("a"), ResolverManifestDigest: fixedDigest("b"),
		Descriptors: []gocaller.DirectDescriptor{},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolverPublication, err := resolverStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rpcValue := rpcRoot(t, repository, resolverPublication.Root(), nil)
	kafkaValue := kafkaRoot(t, repository, rpcValue.Authority, nil)
	writeTestComponentRoot(
		t,
		filepath.Join(
			dataDir, "relationship-rpc-postings", "rpc-caller-postings",
			repositoryHash(repository), strings.TrimPrefix(rpcValue.GenerationDigest, "sha256:"),
		),
		rpcValue,
	)
	writeTestComponentRoot(
		t,
		filepath.Join(
			dataDir, "relationship-kafka-postings", "kafka-topic-postings",
			repositoryHash(repository), strings.TrimPrefix(kafkaValue.GenerationDigest, "sha256:"),
		),
		kafkaValue,
	)
	prepared, err := buildSources(
		t.Context(), filepath.Join(dataDir, "relationships"), catalog, states,
		resolverPublication,
		fakeRPC{root: rpcValue, values: []rpccallerposting.Posting{}},
		fakeKafka{root: kafkaValue, values: []kafkatopicposting.Posting{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "relationships.tar")
	report, err := CreateArchive(t.Context(), dataDir, archive)
	if err != nil {
		t.Fatal(err)
	}
	if report.Publications != 1 || report.Files < 5 || report.Omitted != 0 {
		t.Fatalf("archive report = %+v", report)
	}
	restored := t.TempDir()
	if err := RestoreArchive(t.Context(), archive, restored); err != nil {
		t.Fatal(err)
	}
	current, err := OpenCurrent(
		t.Context(), filepath.Join(restored, "relationships"), repository,
	)
	if err != nil || current.Root().Digest != publication.Root().Digest {
		t.Fatalf("restored relationship = %+v, %v", current, err)
	}
	verified, err := VerifyArchive(t.Context(), archive)
	if err != nil || verified.Publications != 1 || verified.Files != report.Files ||
		verified.Bytes != report.Bytes {
		t.Fatalf("verified report = %+v, %v", verified, err)
	}
}

func writeTestComponentRoot(t *testing.T, directory string, value any) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "root.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func relationshipCatalog(t *testing.T) (servicecatalog.Publication, []servicecatalog.ServiceState) {
	t.Helper()
	commit := strings.Repeat("a", 40)
	catalog := servicecatalog.Catalog{
		Schema:    servicecatalog.Schema,
		Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: commit},
		Services: []servicecatalog.Service{
			{Key: "legacy", DisplayName: "Legacy", Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase, Reason: "retired", Successors: []string{"orders"}},
			{Key: "orders", DisplayName: "Orders", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
			{Key: "payments", DisplayName: "Payments", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
		},
		Memberships: []servicecatalog.Membership{
			{ServiceKey: "legacy", Path: "misc/free.go", Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase},
			{ServiceKey: "orders", Path: "services/orders", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
			{ServiceKey: "orders", Path: "shared", Role: servicecatalog.RoleShared, Origin: servicecatalog.OriginBase},
			{ServiceKey: "payments", Path: "services/payments", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
			{ServiceKey: "payments", Path: "shared", Role: servicecatalog.RoleShared, Origin: servicecatalog.OriginBase},
		},
		Unowned: []servicecatalog.UnownedPlacement{{Path: "misc/free.go", Origin: servicecatalog.OriginBase}},
	}
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: "example.com/acme/mono",
		SourceKind: servicecatalog.SourceCommitted, SourcePath: "/catalog.json", SourceCommit: commit,
		SourceCensusDigest: fixedDigest("b"), SourceFileCount: 6, AcceptedFileCount: 5, UnownedFileCount: 1,
		Authority: catalog.Authority, CatalogDigest: catalogDigest, Canonical: canonical,
		ControlRevision: 1, PublishedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidatePublication(publication, true); err != nil {
		t.Fatal(err)
	}
	projections, err := servicecatalog.ProjectServices(publication)
	if err != nil {
		t.Fatal(err)
	}
	states := []servicecatalog.ServiceState{}
	for _, projection := range projections {
		if projection.Service.Disposition != servicecatalog.DispositionAccepted {
			continue
		}
		desired, err := servicecatalog.ServiceDesiredGeneration(projection, 1)
		if err != nil {
			t.Fatal(err)
		}
		state := servicecatalog.ServiceState{
			Schema: servicecatalog.ServiceStateSchema, Repository: publication.Repository,
			ServiceKey: projection.Service.Key, DisplayName: projection.Service.DisplayName,
			Disposition: projection.Service.Disposition, Origin: projection.Service.Origin,
			Successors: []string{}, Incarnation: 1, DesiredGeneration: desired,
			DesiredSourceGeneration:  projection.SourceGeneration,
			DesiredCatalogGeneration: projection.CatalogGeneration,
			Status:                   servicecatalog.StatusUnavailable, ControlRevision: 1,
			ChangedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
		}
		if err := servicecatalog.SetServiceStateDigest(&state); err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
	}
	return publication, states
}

func relationshipSummary(
	t *testing.T,
	publication servicecatalog.Publication,
	states []servicecatalog.ServiceState,
) servicecatalog.RepositoryState {
	t.Helper()
	summary := servicecatalog.RepositoryState{
		Schema: servicecatalog.RepositoryStateSchema, Repository: publication.Repository,
		CatalogGeneration: publication.GenerationDigest, CatalogControlRevision: publication.ControlRevision,
		CatalogServiceCount: 3, LiveServiceCount: len(states), UnavailableCount: len(states),
		TombstoneCount: 1, ControlRevision: 1,
		UpdatedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
	}
	if err := servicecatalog.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidateRepositoryState(summary, true); err != nil {
		t.Fatal(err)
	}
	return summary
}

func resolverRoot(t *testing.T, repository string) resolvernamespace.Root {
	t.Helper()
	policy := resolvernamespace.FrozenPolicy()
	value := resolvernamespace.Root{
		Schema: resolvernamespace.RootSchema,
		Authority: resolvernamespace.Authority{
			Repository: repository, Commit: strings.Repeat("a", 40),
			ResolverGenerationDigest: fixedDigest("1"), ResolverManifestDigest: fixedDigest("2"),
			PolicyDigest: mustDigest(t, policy),
		},
		Policy: policy, Namespaces: []resolvernamespace.NamespaceReceipt{},
	}
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := resolvernamespace.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func rpcRoot(t *testing.T, repository string, resolver resolvernamespace.Root, values []rpccallerposting.Posting) rpccallerposting.Root {
	t.Helper()
	policy := rpccallerposting.FrozenPolicy()
	value := rpccallerposting.Root{
		Schema: rpccallerposting.RootSchema,
		Authority: rpccallerposting.Authority{
			Repository: repository, ObservationGenerationDigest: fixedDigest("3"),
			ObservationManifestDigest: fixedDigest("4"), ObservationSourceDigest: fixedDigest("5"),
			ResolverCommit: resolver.Authority.Commit, ResolverGenerationDigest: resolver.GenerationDigest,
			ResolverRootDigest: resolver.Digest, PolicyDigest: mustDigest(t, policy),
		},
		Policy: policy, Members: []rpccallerposting.MemberReceipt{}, PostingCount: len(values),
	}
	groups := map[string]int{}
	for _, posting := range values {
		bucket := rpcBucket(posting)
		key := fmt.Sprintf("%s\x00%03d", posting.Protocol, bucket)
		groups[key]++
		switch posting.Class {
		case "resolved":
			value.ResolvedCount++
		case "name_match":
			value.NameMatchCount++
		default:
			value.UnresolvedCount++
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		parts := strings.Split(key, "\x00")
		var bucket int
		_, _ = fmt.Sscanf(parts[1], "%03d", &bucket)
		value.Members = append(value.Members, rpccallerposting.MemberReceipt{Protocol: parts[0], Bucket: bucket, Name: fmt.Sprintf("%s-%02x.json", parts[0], bucket), PostingCount: groups[key], ContentBytes: 1, ContentDigest: fixedDigest("6")})
		value.EncodedMemberBytes++
	}
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := rpccallerposting.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func kafkaRoot(t *testing.T, repository string, observation rpccallerposting.Authority, values []kafkatopicposting.Posting) kafkatopicposting.Root {
	t.Helper()
	policy := kafkatopicposting.FrozenPolicy()
	value := kafkatopicposting.Root{
		Schema: kafkatopicposting.RootSchema,
		Authority: kafkatopicposting.Authority{
			Repository: repository, ObservationGenerationDigest: observation.ObservationGenerationDigest,
			ObservationManifestDigest: observation.ObservationManifestDigest,
			ObservationSourceDigest:   observation.ObservationSourceDigest, PolicyDigest: mustDigest(t, policy),
		},
		Policy: policy, Members: []kafkatopicposting.MemberReceipt{}, PostingCount: len(values),
	}
	groups := map[string]int{}
	for _, posting := range values {
		bucket := kafkaBucket(posting)
		key := fmt.Sprintf("%s\x00%03d", posting.Plane, bucket)
		groups[key]++
		if posting.Plane == "producer" {
			value.ProducerCount++
		} else {
			value.ConsumerCount++
		}
		if posting.Class == "literal" {
			value.LiteralCount++
		} else {
			value.UnresolvedCount++
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		parts := strings.Split(key, "\x00")
		var bucket int
		_, _ = fmt.Sscanf(parts[1], "%03d", &bucket)
		value.Members = append(value.Members, kafkatopicposting.MemberReceipt{Plane: parts[0], Bucket: bucket, Name: fmt.Sprintf("%s-%03d.json", parts[0], bucket), PostingCount: groups[key], ContentBytes: 1, ContentDigest: fixedDigest("7")})
		value.EncodedMemberBytes++
	}
	value.GenerationDigest = mustDigest(t, value)
	value.Digest = mustDigest(t, value)
	if err := kafkatopicposting.ValidateRoot(value); err != nil {
		t.Fatal(err)
	}
	return value
}

func rpcPosting(seed, class, protocol, operation, path, declaration string) rpccallerposting.Posting {
	value := rpccallerposting.Posting{Schema: rpccallerposting.PostingSchema, Protocol: protocol, Class: class, LookupOperation: operation, Operation: operation, CandidateOperations: []string{}, ObjectID: strings.Repeat(seed, 40), ContentDigest: fixedDigest(seed), Path: path, Mode: "100644", Revisions: []int{0}, Span: sourceobservation.Span{StartByte: 1, EndByte: 2, StartLine: 1, EndLine: 1}, SourceRole: "production", DeclarationPath: declaration, DeclarationLineage: "lineage", ResolverRecordDigests: []string{fixedDigest(seed)}}
	if class == "unresolved" {
		value.Reason = "dynamic_receiver"
		value.LookupOperation, value.Operation, value.DeclarationPath, value.DeclarationLineage = "", "", "", ""
	}
	value.Digest = postingDigest(value)
	return value
}

func kafkaPosting(seed, plane, class, topic, path string) kafkatopicposting.Posting {
	value := kafkatopicposting.Posting{Schema: kafkatopicposting.PostingSchema, Plane: plane, Class: class, TopicSpelling: topic, Library: "sarama", ImportPath: "github.com/IBM/sarama", Shape: "fixture", ObjectID: strings.Repeat(seed, 40), ContentDigest: fixedDigest(seed), Path: path, Mode: "100644", Revisions: []int{0}, Span: sourceobservation.Span{StartByte: 1, EndByte: 2, StartLine: 1, EndLine: 1}, SourceRole: "production"}
	if class == "literal" {
		value.Binding = "literal"
	} else {
		value.Reason = "dynamic_topic"
		value.SourceReason = "unresolved-ident"
	}
	value.Digest = postingDigest(value)
	return value
}

func postingDigest[T any](value T) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func testAuthority(t *testing.T, repository string) Authority {
	t.Helper()
	emptySet, err := digestServiceSet([]serviceSetIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	return Authority{Repository: repository, CatalogGenerationDigest: fixedDigest("1"), CatalogDigest: fixedDigest("2"), CatalogSourceGeneration: fixedDigest("3"), ServiceStateSetDigest: emptySet, ObservationGenerationDigest: fixedDigest("4"), ObservationManifestDigest: fixedDigest("5"), ObservationSourceDigest: fixedDigest("6"), ResolverGenerationDigest: fixedDigest("7"), ResolverRootDigest: fixedDigest("8"), RPCGenerationDigest: fixedDigest("9"), RPCRootDigest: fixedDigest("a"), KafkaGenerationDigest: fixedDigest("b"), KafkaRootDigest: fixedDigest("c"), PolicyDigest: mustDigest(t, FrozenPolicy())}
}

func testReference(t *testing.T, seed string) ServiceReference {
	t.Helper()
	value := ServiceReference{Schema: ServiceReferenceSchema, ProjectionDigest: fixedDigest(seed), PostingDigest: fixedDigest(seed), Kind: "rpc", Plane: "grpc", Participation: []string{"source"}}
	value.Digest = mustDigest(t, value)
	return value
}

func mustDigest(t *testing.T, value any) string {
	t.Helper()
	digest, err := digestValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
func fixedDigest(value string) string { return "sha256:" + strings.Repeat(value, 64) }

func rpcBucket(posting rpccallerposting.Posting) int {
	operation := posting.LookupOperation
	if posting.Class == "unresolved" {
		operation = "__unresolved__"
	}
	sum := sha256.Sum256([]byte("phebs-rpc-caller-operation-v1\x00" + operation))
	return int(sum[0])
}
func kafkaBucket(posting kafkatopicposting.Posting) int {
	identity := posting.TopicSpelling
	if posting.Class == "unresolved" {
		identity = "__unresolved__"
	}
	sum := sha256.Sum256([]byte("phebs-kafka-topic-bucket-v1\x00" + identity))
	return int(sum[0])
}
