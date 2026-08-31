package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

type serviceStateV3ReadCounter struct {
	*Surreal
	pointers  atomic.Int64
	roots     atomic.Int64
	members   atomic.Int64
	summaries atomic.Int64
	points    atomic.Int64
	pages     atomic.Int64
	accepted  atomic.Int64
	confirms  atomic.Int64
}

func (counter *serviceStateV3ReadCounter) GetServiceCatalogV3CandidatePointer(
	ctx context.Context, repository string,
) (ServiceCatalogV3Pointer, error) {
	counter.pointers.Add(1)
	return counter.Surreal.GetServiceCatalogV3CandidatePointer(ctx, repository)
}

func (counter *serviceStateV3ReadCounter) ReadServiceCatalogV3Root(
	ctx context.Context, repository, digest string,
) (servicecatalogv3.Root, error) {
	counter.roots.Add(1)
	return counter.Surreal.ReadServiceCatalogV3Root(ctx, repository, digest)
}

func (counter *serviceStateV3ReadCounter) ReadServiceCatalogV3Member(
	ctx context.Context, descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	counter.members.Add(1)
	return counter.Surreal.ReadServiceCatalogV3Member(ctx, descriptor)
}

func (counter *serviceStateV3ReadCounter) GetServiceStateV3SummaryPoint(
	ctx context.Context, repository string,
) (servicecatalog.RepositoryState, error) {
	counter.summaries.Add(1)
	return counter.Surreal.GetServiceStateV3SummaryPoint(ctx, repository)
}

func (counter *serviceStateV3ReadCounter) GetServiceStateV3Point(
	ctx context.Context, repository, key string,
) (servicecatalog.ServiceState, error) {
	counter.points.Add(1)
	return counter.Surreal.GetServiceStateV3Point(ctx, repository, key)
}

func (counter *serviceStateV3ReadCounter) ListServiceStateV3Rows(
	ctx context.Context, repository, after string, limit int,
) ([]servicecatalog.ServiceState, error) {
	counter.pages.Add(1)
	return counter.Surreal.ListServiceStateV3Rows(ctx, repository, after, limit)
}

func (counter *serviceStateV3ReadCounter) ListAcceptedServiceStateV3Rows(
	ctx context.Context, repository string, limit int,
) ([]servicecatalog.ServiceState, error) {
	counter.accepted.Add(1)
	return counter.Surreal.ListAcceptedServiceStateV3Rows(ctx, repository, limit)
}

func (counter *serviceStateV3ReadCounter) ConfirmServiceStateV3Snapshot(
	ctx context.Context,
	pointer ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) error {
	counter.confirms.Add(1)
	return counter.Surreal.ConfirmServiceStateV3Snapshot(ctx, pointer, summary)
}

func newServiceStateV3CountingReader(
	t *testing.T, s *Surreal,
) (*serviceStateV3ReadCounter, *ServiceStateV3Reader, *servicecatalogv3.ReadCache) {
	t.Helper()
	counter := &serviceStateV3ReadCounter{Surreal: s}
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := NewServiceStateV3Reader(counter, cache)
	if err != nil {
		t.Fatal(err)
	}
	return counter, reader, cache
}

func TestServiceStateV3ReconcileActivationAndSuccessorFence(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3"
	commit := strings.Repeat("7", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	first := serviceStateV3Generation(t, repository, commit, "a", []servicecatalog.Service{{
		Key: "orders", DisplayName: "Orders", Disposition: servicecatalog.DispositionAccepted,
		Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || reconcile.Noop || reconcile.Plan == nil {
		t.Fatalf("begin reconcile = %+v, %v", reconcile, err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.UnavailableCount != 1 || summary.CurrentCount != 0 {
		t.Fatalf("reconciled summary = %+v, %v", summary, err)
	}
	search := "sha256:" + strings.Repeat("9", 64)
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil || activation.Noop || activation.Plan == nil {
		t.Fatalf("begin activation = %+v, %v", activation, err)
	}
	runServiceStateV3Plan(t, s, activation)
	summary, err = s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.CurrentCount != 1 || summary.UnavailableCount != 0 {
		t.Fatalf("activated summary = %+v, %v", summary, err)
	}
	noop, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil || !noop.Noop || noop.Plan != nil || noop.Schedule != nil {
		t.Fatalf("activation no-op = %+v, %v", noop, err)
	}
	report, err := s.ValidateServiceCatalogV3Precious(ctx)
	if err != nil || report.StateRows != 1 || report.StateSummaries != 1 || report.StatePlans != 2 {
		t.Fatalf("precious state report = %+v, %v", report, err)
	}

	second := serviceStateV3Generation(t, repository, commit, "b", []servicecatalog.Service{{
		Key: "orders", DisplayName: "Orders B", Disposition: servicecatalog.DispositionAccepted,
		Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	unsettled, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || unsettled.Plan == nil {
		t.Fatalf("begin successor = %+v, %v", unsettled, err)
	}
	third := serviceStateV3Generation(t, repository, commit, "c", []servicecatalog.Service{{
		Key: "orders", DisplayName: "Orders C", Disposition: servicecatalog.DispositionAccepted,
		Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, third); !errors.Is(err, ErrConflict) {
		t.Fatalf("unsettled successor publication = %v", err)
	}
}

func runServiceStateV3Plan(t *testing.T, s *Surreal, begin ServiceStateV3Begin) {
	t.Helper()
	ctx := t.Context()
	schedule := begin.Schedule
	for schedule.NextOffset < schedule.TotalItems {
		var err error
		schedule, err = s.ExpandGenerationSchedule(
			ctx, schedule.Repository, schedule.Stage, schedule.Generation,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for {
		chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "service-state-v3-test")
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.ProcessServiceStateV3Chunk(ctx, *chunk)
		if err != nil {
			t.Fatalf("process chunk offset %d: %v", chunk.Offset, err)
		}
		if result.Settled {
			return
		}
	}
}

func serviceStateV3Generation(
	t *testing.T,
	repository, commit, version string,
	services []servicecatalog.Service,
) servicecatalogv3.Generation {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "service-state-v3", Version: version,
	}
	memberships := make([]servicecatalog.Membership, 0, len(services))
	for index, service := range services {
		if service.Disposition == servicecatalog.DispositionRejected {
			continue
		}
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: service.Key, Path: fmt.Sprintf("svc/%05d", index),
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator, Path: "/catalog.json", Commit: commit,
			CensusDigest: "sha256:" + strings.Repeat("c", 64),
			FileCount:    1, AcceptedFileCount: 1,
		},
		Authority: authority,
	}, servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: services, Memberships: memberships,
	})
	if err != nil {
		t.Fatal(err)
	}
	return generation
}

func serviceStateV2Publication(
	t *testing.T,
	repository, commit string,
) servicecatalog.Publication {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "service-state-v2", Version: "v1",
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "V2 Orders",
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
		Unowned: []servicecatalog.UnownedPlacement{},
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
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind: servicecatalog.SourceOperator, SourcePath: "/catalog-v2.json",
		SourceCommit:       commit,
		SourceCensusDigest: "sha256:" + strings.Repeat("4", 64),
		SourceFileCount:    1, AcceptedFileCount: 1,
		Authority: authority, CatalogDigest: catalogDigest, Canonical: canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidatePublication(publication, false); err != nil {
		t.Fatal(err)
	}
	return publication
}

func TestServiceStateV3SchemaMigrationIdempotent(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	results, err := surrealdb.Query[any](t.Context(), s.db, `
DELETE $marker;
DEFINE FIELD OVERWRITE max_chunk_rows ON service_state_v3_plan TYPE string;`, map[string]any{
		"marker": serviceStateV3SchemaMigrationID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	if err := s.migrateServiceStateV3Schema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.getRawServiceStateV3Summary(t.Context(), "example.com/acme/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing v3 summary = %v", err)
	}
}

func TestServiceStateV3LeavesV1V2AuthorityByteIdentical(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-isolation"
	commit := strings.Repeat("6", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	v2 := serviceStateV2Publication(t, repository, commit)
	if err := s.PublishServiceCatalog(ctx, v2); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileServiceStates(ctx, *current); err != nil {
		t.Fatal(err)
	}
	beforeCatalog, _ := s.GetServiceCatalog(ctx, repository)
	beforeState, _ := s.GetServiceState(ctx, repository, "orders")
	beforeSummary, _ := s.GetServiceStateSummary(ctx, repository)

	v3 := serviceStateV3Generation(t, repository, commit, "isolation", []servicecatalog.Service{{
		Key: "orders", DisplayName: "V3 Orders",
		Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
	}})
	if err := s.PublishServiceCatalogV3Candidate(ctx, v3); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	activation, err := s.BeginServiceStateV3Activation(
		ctx, repository, "sha256:"+strings.Repeat("5", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)

	afterCatalog, _ := s.GetServiceCatalog(ctx, repository)
	afterState, _ := s.GetServiceState(ctx, repository, "orders")
	afterSummary, _ := s.GetServiceStateSummary(ctx, repository)
	if !reflect.DeepEqual(afterCatalog, beforeCatalog) ||
		!reflect.DeepEqual(afterState, beforeState) ||
		!reflect.DeepEqual(afterSummary, beforeSummary) {
		t.Fatal("v3 reconcile or activation changed v1/v2 catalog/state authority")
	}
}

func TestServiceStateV3ChunkFailureRollsBackRowsAndPlan(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-rollback"
	commit := strings.Repeat("3", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	generation := serviceStateV3Generation(t, repository, commit, "rollback", []servicecatalog.Service{
		{Key: "orders", DisplayName: "Orders", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
		{Key: "users", DisplayName: "Users", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
	})
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	results, err := surrealdb.Query[any](ctx, s.db, `
DEFINE EVENT service_state_v3_rollback_trap ON TABLE service_state_v3_current
	WHEN $event != 'DELETE' AND $after.service_key = 'users'
	THEN { THROW 'phebs-permanent: injected service state v3 chunk failure' };`, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *chunk); err == nil ||
		!strings.Contains(err.Error(), "injected service state v3 chunk failure") {
		t.Fatalf("injected chunk failure = %v", err)
	}
	countResults, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db, `RETURN [{ count: array::len(
		SELECT id FROM service_state_v3_current WHERE repository = $repository
	) }];`, map[string]any{"repository": repository})
	if err != nil {
		t.Fatal(err)
	}
	counts := firstDomainRows(countResults)
	plan, err := s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
	if err != nil || len(counts) != 1 || counts[0].Count != 0 || plan.NextChunk != 0 {
		t.Fatalf("rolled-back chunk rows=%+v plan=%+v err=%v", counts, plan, err)
	}
}

func TestServiceStateV3PlanUsesGenerationScheduleLifecycle(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-lifecycle"
	commit := strings.Repeat("2", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	for index := range 4 {
		generation := serviceStateV3Generation(
			t, repository, commit, fmt.Sprintf("lifecycle-%d", index),
			[]servicecatalog.Service{{
				Key: "orders", DisplayName: fmt.Sprintf("Orders %d", index),
				Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
			}},
		)
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatal(err)
		}
		begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
		if err != nil {
			t.Fatal(err)
		}
		runServiceStateV3Plan(t, s, begin)
	}
	if count := serviceStateV3PlanCount(t, s); count != 4 {
		t.Fatalf("plan count before lifecycle = %d", count)
	}
	cursor := ""
	for range 16 {
		sweep, err := s.SweepGenerationScheduleLifecycle(ctx, cursor, 16, 64, 3)
		if err != nil {
			t.Fatal(err)
		}
		cursor = sweep.Cursor
		if serviceStateV3PlanCount(t, s) == 3 {
			break
		}
	}
	if count := serviceStateV3PlanCount(t, s); count != 3 {
		t.Fatalf("plan count after lifecycle = %d", count)
	}
}

func TestServiceStateV3CatalogSuccessorSupersedesActivationLease(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-activation-supersede"
	commit := strings.Repeat("1", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	service := func(display string) []servicecatalog.Service {
		return []servicecatalog.Service{{
			Key: "orders", DisplayName: display,
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}}
	}
	first := serviceStateV3Generation(t, repository, commit, "a", service("Orders A"))
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	activation, err := s.BeginServiceStateV3Activation(
		ctx, repository, "sha256:"+strings.Repeat("2", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, activation)
	claimed, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "superseded-activation")
	if err != nil {
		t.Fatal(err)
	}
	second := serviceStateV3Generation(t, repository, commit, "b", service("Orders B"))
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *claimed); err == nil {
		t.Fatal("superseded activation lease mutated the successor catalog state")
	}
	plan, err := s.getServiceStateV3Plan(ctx, activation.Plan.Digest)
	if err != nil || plan.State != serviceStateV3Superseded {
		t.Fatalf("superseded activation plan = %+v, %v", plan, err)
	}
	if _, _, err := s.currentServiceStateV3Plan(ctx, repository, serviceStateV3Activate); !errors.Is(err, ErrNotFound) && err != nil {
		t.Fatalf("superseded activation current = %v", err)
	}
	if _, err := s.GetServiceStateV3Summary(ctx, repository); err == nil {
		t.Fatal("catalog successor exposed the prior summary")
	}
}

func TestServiceStateV3CrashReplay(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-replay"
	commit := strings.Repeat("8", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 600)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key:         fmt.Sprintf("service-%04d", index),
			DisplayName: fmt.Sprintf("Service %04d", index),
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}
	}
	generation := serviceStateV3Generation(t, repository, commit, "repair", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	first, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "crash-replay")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := s.GetServiceCatalogV3CandidateRoot(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.processServiceStateV3ReconcileChunk(
		ctx, *first, *begin.Plan, *opened, 0,
	)
	if err != nil || result.Applied == 0 {
		t.Fatalf("pre-crash apply = %+v, %v", result, err)
	}
	if _, err := s.GetServiceStateV3Summary(ctx, repository); err == nil {
		t.Fatal("partial reconcile exposed a strict v3 summary")
	}
	if err := s.ReleaseGenerationChunk(ctx, *first, "injected crash after state commit"); err != nil {
		t.Fatal(err)
	}
	replayed := false
	for {
		chunk, claimErr := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "crash-replay")
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		result, err = s.ProcessServiceStateV3Chunk(ctx, *chunk)
		if err != nil {
			t.Fatal(err)
		}
		if chunk.Offset == first.Offset {
			if result.Applied != 0 {
				t.Fatalf("replayed chunk applied rows = %+v", result)
			}
			replayed = true
		}
		if result.Settled {
			break
		}
	}
	if !replayed {
		t.Fatal("released committed chunk was not replayed")
	}
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.LiveServiceCount != len(services) {
		t.Fatalf("replayed summary = %+v, %v", summary, err)
	}
}

func TestServiceStateV3TerminalRepairContinues(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-repair"
	commit := strings.Repeat("b", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 600)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%04d", index), DisplayName: fmt.Sprintf("Service %04d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	generation := serviceStateV3Generation(t, repository, commit, "repair", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, begin)
	first, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "repair-first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProcessServiceStateV3Chunk(ctx, *first); err != nil {
		t.Fatal(err)
	}
	for {
		schedule, scheduleErr := s.GetGenerationSchedule(ctx, repository, ServiceStateV3ReconcileStage)
		if scheduleErr != nil {
			t.Fatal(scheduleErr)
		}
		if schedule.Status == GenerationScheduleSettled {
			break
		}
		chunk, claimErr := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "terminal-failure")
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if failErr := s.FailGenerationChunk(ctx, *chunk, "injected terminal failure"); failErr != nil {
			t.Fatal(failErr)
		}
	}
	repair, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || repair.Plan == nil || repair.Plan.Repair != 1 ||
		repair.Plan.BaseChunk != 1 || repair.Plan.NextChunk != 1 {
		t.Fatalf("repair plan = %+v, %v", repair, err)
	}
	runServiceStateV3Plan(t, s, repair)
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.LiveServiceCount != len(services) ||
		summary.UnavailableCount != len(services) {
		t.Fatalf("repaired summary = %+v, %v", summary, err)
	}
}

func TestServiceStateV3RemovalReaddAndABA(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-aba"
	commit := strings.Repeat("a", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	service := func(key, display string) servicecatalog.Service {
		return servicecatalog.Service{
			Key: key, DisplayName: display,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}
	}
	first := serviceStateV3Generation(t, repository, commit, "a", []servicecatalog.Service{
		service("orders", "Orders A"), service("users", "Users A"),
	})
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	ordersA := serviceStateV3Row(t, s, repository, "orders")
	usersA := serviceStateV3Row(t, s, repository, "users")

	second := serviceStateV3Generation(t, repository, commit, "b", []servicecatalog.Service{
		service("orders", "Orders B"),
	})
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	begin, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	removed := serviceStateV3Row(t, s, repository, "users")
	if !removed.Removed || removed.Incarnation != usersA.Incarnation {
		t.Fatalf("removed users = %+v", removed)
	}

	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	begin, err = s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	ordersAgain := serviceStateV3Row(t, s, repository, "orders")
	usersAgain := serviceStateV3Row(t, s, repository, "users")
	if ordersAgain.Incarnation != ordersA.Incarnation ||
		ordersAgain.DesiredGeneration != ordersA.DesiredGeneration ||
		usersAgain.Incarnation != usersA.Incarnation+1 || usersAgain.Removed {
		t.Fatalf("A-B-A states orders=%+v users=%+v", ordersAgain, usersAgain)
	}
}

func TestServiceStateV3PartialActivationKeepsMatchingSummaryReadable(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-partial-activation"
	commit := strings.Repeat("f", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 600)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%04d", index), DisplayName: fmt.Sprintf("Service %04d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	generation := serviceStateV3Generation(t, repository, commit, "partial-activation", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	activation, err := s.BeginServiceStateV3Activation(
		ctx, repository, "sha256:"+strings.Repeat("1", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, s, activation)
	chunk, err := s.ClaimGenerationChunk(ctx, GenerationResourceCPU, "partial-activation")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ProcessServiceStateV3Chunk(ctx, *chunk)
	if err != nil || result.Applied != servicecatalogv3.MaxServicesPerMember || result.Settled {
		t.Fatalf("first activation chunk = %+v, %v", result, err)
	}
	summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil || summary.CurrentCount != servicecatalogv3.MaxServicesPerMember ||
		summary.UnavailableCount != len(services)-servicecatalogv3.MaxServicesPerMember {
		t.Fatalf("partial activation summary = %+v, %v", summary, err)
	}
	runServiceStateV3Plan(t, s, activation)
}

func TestServiceStateV3TenThousandBoundedColdNoopDeltaAndActivation(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-ten-thousand"
	commit := strings.Repeat("d", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	const servicesCount = 10_000
	services := make([]servicecatalog.Service, servicesCount)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%05d", index), DisplayName: fmt.Sprintf("Service %05d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	started := time.Now()
	first := serviceStateV3Generation(t, repository, commit, "ten-thousand-a", services)
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	cold, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, cold)
	assertServiceStateV3PlanBounds(t, s, cold.Plan.Digest, servicesCount*2, servicesCount)
	if time.Since(started) > 10*time.Minute {
		t.Fatalf("10,000-service cold reconcile exceeded ten minutes: %s", time.Since(started))
	}
	noop, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil || !noop.Noop {
		t.Fatalf("10,000-service reconcile no-op = %+v, %v", noop, err)
	}
	search := "sha256:" + strings.Repeat("e", 64)
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	assertServiceStateV3PlanBounds(t, s, activation.Plan.Digest, servicesCount, servicesCount)

	counter, reader, cache := newServiceStateV3CountingReader(t, s)
	const concurrentReaders = 8
	errs := make([]error, concurrentReaders)
	var wait sync.WaitGroup
	for index := range concurrentReaders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			read, readErr := reader.OpenService(ctx, repository, "service-05123")
			if readErr == nil {
				readErr = reader.Confirm(ctx, read)
			}
			if read != nil {
				read.Close()
			}
			errs[index] = readErr
		}()
	}
	wait.Wait()
	for _, readErr := range errs {
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	stats := cache.Stats()
	if counter.pointers.Load() != concurrentReaders || counter.roots.Load() != 1 ||
		counter.members.Load() != 1 || counter.summaries.Load() != concurrentReaders ||
		counter.points.Load() != concurrentReaders || counter.confirms.Load() != concurrentReaders ||
		stats.RootReads != 1 || stats.MemberReads != 1 ||
		stats.RootValidations != 1 || stats.MemberValidations != 1 {
		t.Fatalf("10,000-service concurrent cold counts = %+v", stats)
	}
	warm, err := reader.OpenService(ctx, repository, "service-05123")
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Confirm(ctx, warm); err != nil {
		t.Fatal(err)
	}
	warm.Close()
	stats = cache.Stats()
	if counter.roots.Load() != 1 || counter.members.Load() != 1 ||
		stats.RootReads != 1 || stats.MemberReads != 1 ||
		stats.RootLeases != 0 || stats.MemberLeases != 0 {
		t.Fatalf("10,000-service warm/cache counts = %+v", stats)
	}

	pageCounter, pageReader, pageCache := newServiceStateV3CountingReader(t, s)
	anchor, err := s.GetServiceStateV3Point(ctx, repository, "service-00499")
	if err != nil {
		t.Fatal(err)
	}
	page, err := pageReader.ListServices(
		ctx, repository, ServiceStateFilter{}, ServiceStatePosition{
			ServiceKey: anchor.ServiceKey, Incarnation: anchor.Incarnation,
		}, MaxServiceStateReadPage,
	)
	if err != nil {
		t.Fatal(err)
	}
	pageStats := pageCache.Stats()
	if len(page.Entries) != MaxServiceStateReadPage ||
		page.Entries[0].State.ServiceKey != "service-00500" ||
		pageCounter.pointers.Load() != 1 || pageCounter.roots.Load() != 1 ||
		pageCounter.members.Load() != 2 || pageCounter.summaries.Load() != 1 ||
		pageCounter.points.Load() != 1 || pageCounter.pages.Load() != 1 ||
		pageCounter.confirms.Load() != 1 || pageStats.MemberReads != 2 {
		t.Fatalf("10,000-service page counts = %+v / %+v", pageStats, page)
	}

	batchCounter, batchReader, batchCache := newServiceStateV3CountingReader(t, s)
	snapshot, err := batchReader.AcceptedSnapshot(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	batchStats := batchCache.Stats()
	if len(snapshot.States) != servicesCount || batchCounter.pointers.Load() != 1 ||
		batchCounter.roots.Load() != 1 ||
		batchCounter.members.Load() != int64(len(first.Root.ServiceMembers)) ||
		batchCounter.summaries.Load() != 1 || batchCounter.accepted.Load() != 1 ||
		batchCounter.confirms.Load() != 1 ||
		batchStats.MemberValidations != uint64(len(first.Root.ServiceMembers)) {
		t.Fatalf("10,000-service batch counts = %+v", batchStats)
	}

	_, heldReader, heldCache := newServiceStateV3CountingReader(t, s)
	held, err := heldReader.OpenService(ctx, repository, "service-09999")
	if err != nil {
		t.Fatal(err)
	}

	deltaServices := append([]servicecatalog.Service(nil), services...)
	deltaServices[len(deltaServices)-1].DisplayName = "Changed service"
	second := serviceStateV3Generation(t, repository, commit, "ten-thousand-b", deltaServices)
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatal(err)
	}
	_, seamReader, _ := newServiceStateV3CountingReader(t, s)
	if opened, seamErr := seamReader.OpenService(
		ctx, repository, "service-09999",
	); seamErr == nil {
		opened.Close()
		t.Fatal("concurrent catalog publication escaped unreconciled summary")
	}
	if err := heldReader.Confirm(ctx, held); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked v3 read confirmation = %v", err)
	}
	if stats := heldCache.Stats(); stats.RootLeases != 1 || stats.MemberLeases != 1 {
		t.Fatalf("revoked v3 read retired before lease close = %+v", stats)
	}
	held.Close()
	if stats := heldCache.Stats(); stats.RootLeases != 0 || stats.MemberLeases != 0 {
		t.Fatalf("revoked v3 read lease remained = %+v", stats)
	}
	delta, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, delta)
	assertServiceStateV3PlanBounds(t, s, delta.Plan.Digest, servicesCount*2, 1)

	staleCounter, staleReader, staleCache := newServiceStateV3CountingReader(t, s)
	stale, err := staleReader.OpenService(ctx, repository, "service-09999")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Entry.State.Status != servicecatalog.StatusStale ||
		staleCounter.roots.Load() != 2 || staleCounter.members.Load() != 2 {
		t.Fatalf("stale v3 detail counts = %+v / %+v", staleCache.Stats(), stale.Entry.State)
	}
	stale.Close()

	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	aba, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, aba)
	assertServiceStateV3PlanBounds(t, s, aba.Plan.Digest, servicesCount*2, 1)
	if time.Since(started) > 10*time.Minute {
		t.Fatalf("10,000-service cold/no-op/delta/A-B-A exceeded ten minutes: %s", time.Since(started))
	}
}

func TestServiceStateV3ReadKeepsMaximumSuccessorRowBounded(t *testing.T) {
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-state-v3-successors"
	commit := strings.Repeat("8", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)
	services := make([]servicecatalog.Service, 1, servicecatalogv3.MaxServiceSuccessors+1)
	services[0] = servicecatalog.Service{
		Key: "owner", DisplayName: "Owner", Disposition: servicecatalog.DispositionRejected,
		Origin: servicecatalog.OriginBase, Reason: "split",
		Successors: make([]string, servicecatalogv3.MaxServiceSuccessors),
	}
	for index := range servicecatalogv3.MaxServiceSuccessors {
		key := fmt.Sprintf("successor-%03d", index)
		services[0].Successors[index] = key
		services = append(services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
	}
	generation := serviceStateV3Generation(
		t, repository, commit, "maximum-successors", services,
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, begin)
	_, reader, _ := newServiceStateV3CountingReader(t, s)
	read, err := reader.OpenService(ctx, repository, "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	encoded, err := json.Marshal(read.Entry.State)
	if err != nil || len(read.Entry.State.Successors) != servicecatalogv3.MaxServiceSuccessors ||
		len(encoded) > 64<<10 {
		t.Fatalf("maximum-successor state = %d successors, %d bytes, %v",
			len(read.Entry.State.Successors), len(encoded), err)
	}
}

func assertServiceStateV3PlanBounds(
	t *testing.T,
	s *Surreal,
	digest string,
	maxReads int64,
	writes int64,
) {
	t.Helper()
	plan, err := s.getServiceStateV3Plan(t.Context(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MaxChunkRows > MaxServiceStateV3ChunkRows || plan.RowsRead > maxReads ||
		plan.RowsWritten != writes || plan.BytesWritten > 64<<20 ||
		plan.TotalChunks > servicecatalogv3.MaxMembers*2+1 {
		t.Fatalf("service state v3 plan bounds = %+v", plan)
	}
}

func expandServiceStateV3Plan(t *testing.T, s *Surreal, begin ServiceStateV3Begin) {
	t.Helper()
	schedule := begin.Schedule
	for schedule.NextOffset < schedule.TotalItems {
		var err error
		schedule, err = s.ExpandGenerationSchedule(
			t.Context(), schedule.Repository, schedule.Stage, schedule.Generation,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func serviceStateV3Row(
	t *testing.T,
	s *Surreal,
	repository, serviceKey string,
) servicecatalog.ServiceState {
	t.Helper()
	results, err := surrealdb.Query[[]serviceStateRec](
		t.Context(), s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3ID(repository, serviceKey)},
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("service state v3 row %q = %+v", serviceKey, rows)
	}
	state, err := serviceStateV3FromRec(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	return *state
}

func serviceStateV3PlanCount(t *testing.T, s *Surreal) int {
	t.Helper()
	results, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](t.Context(), s.db, `RETURN [{ count: array::len(
		SELECT id FROM service_state_v3_plan
	) }];`, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("service state v3 plan count = %+v", rows)
	}
	return rows[0].Count
}
