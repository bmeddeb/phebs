package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

type serviceRuntimeSelectorFixture struct {
	store      *Surreal
	repository string
	v2         ServiceRuntimeTarget
	v3         ServiceRuntimeTarget
}

// currentOnlyServiceStateV3ReadSource deliberately exposes only the original
// current-state interface, even when its concrete source also implements
// selected-revision reads.
type currentOnlyServiceStateV3ReadSource struct {
	ServiceStateV3ReadSource
}

func newServiceRuntimeSelectorFixture(t *testing.T) serviceRuntimeSelectorFixture {
	t.Helper()
	s := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/service-runtime-" + strings.ToLower(t.Name())
	commit := strings.Repeat("7", 40)
	seedServiceCatalogV3Repo(t, s, repository, commit)

	v2Publication := serviceStateV2Publication(t, repository, commit)
	if err := s.PublishServiceCatalog(ctx, v2Publication); err != nil {
		t.Fatal(err)
	}
	v2Current, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileServiceStates(ctx, *v2Current); err != nil {
		t.Fatal(err)
	}
	v2Source, err := servicecatalog.SourceGenerationDigest(*v2Current)
	if err != nil {
		t.Fatal(err)
	}
	v2Search := selectorTestDigest("1")
	if _, err := s.ActivateServiceGeneration(
		ctx, repository, v2Current.GenerationDigest, v2Source, v2Search,
	); err != nil {
		t.Fatal(err)
	}
	v2Summary, err := s.GetServiceStateSummary(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}

	v3Generation := serviceStateV3Generation(
		t, repository, commit, "runtime-v3", []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders V3",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	if err := s.PublishServiceCatalogV3Candidate(ctx, v3Generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err := s.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, reconcile)
	v3Search := selectorTestDigest("2")
	activation, err := s.BeginServiceStateV3Activation(ctx, repository, v3Search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, s, activation)
	v3Pointer, err := s.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	v3Summary, err := s.GetServiceStateV3Summary(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	v3RelationshipGeneration := selectorTestDigest("3")
	v3RelationshipRoot := selectorTestDigest("4")
	v3Reference := ServiceCatalogV3RelationshipReference{
		Repository:                   repository,
		RelationshipGenerationDigest: v3RelationshipGeneration,
		RelationshipRootDigest:       v3RelationshipRoot,
		CatalogRootDigest:            v3Pointer.RootDigest,
		CatalogControlRevision:       v3Pointer.ControlRevision,
		StateControlRevision:         v3Summary.ControlRevision,
		StateSummaryDigest:           v3Summary.SummaryDigest,
	}
	if err := s.PinServiceCatalogV3RelationshipReference(ctx, v3Reference); err != nil {
		t.Fatal(err)
	}
	return serviceRuntimeSelectorFixture{
		store: s, repository: repository,
		v2: ServiceRuntimeTarget{
			CatalogGenerationDigest:      v2Current.GenerationDigest,
			CatalogControlRevision:       v2Current.ControlRevision,
			StateControlRevision:         v2Summary.ControlRevision,
			StateSummaryDigest:           v2Summary.SummaryDigest,
			SearchGenerationDigest:       v2Search,
			RelationshipGenerationDigest: selectorTestDigest("5"),
			RelationshipRootDigest:       selectorTestDigest("6"),
		},
		v3: ServiceRuntimeTarget{
			CatalogRootDigest:            v3Pointer.RootDigest,
			CatalogControlRevision:       v3Pointer.ControlRevision,
			StateControlRevision:         v3Summary.ControlRevision,
			StateSummaryDigest:           v3Summary.SummaryDigest,
			SearchGenerationDigest:       v3Search,
			RelationshipGenerationDigest: v3RelationshipGeneration,
			RelationshipRootDigest:       v3RelationshipRoot,
		},
	}
}

func selectorTestDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64)
}

func TestServiceRuntimeSelectorCASAndReverseAreMonotonic(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	if _, err := fixture.store.GetServiceRuntimeSelector(
		ctx, fixture.repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent selector = %v", err)
	}
	selectedV3, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	)
	if err != nil {
		t.Fatalf("select v3: %v", err)
	}
	if selectedV3.Backend != ServiceRuntimeV3 || selectedV3.ControlRevision != 1 ||
		!validSHA256Digest(selectedV3.Digest) {
		t.Fatalf("selected v3 = %+v", selectedV3)
	}
	if err := fixture.store.ConfirmServiceRuntimeSelector(ctx, selectedV3); err != nil {
		t.Fatalf("confirm v3: %v", err)
	}
	if _, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeat absent CAS = %v", err)
	}

	selectedV2, err := fixture.store.SelectServiceRuntimeV2(
		ctx, ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selectedV3.ControlRevision,
			ExpectedDigest:          selectedV3.Digest,
			Target:                  fixture.v2,
		},
	)
	if err != nil {
		t.Fatalf("reverse to v2: %v", err)
	}
	if selectedV2.Backend != ServiceRuntimeV2 || selectedV2.ControlRevision != 2 ||
		selectedV2.Digest == selectedV3.Digest {
		t.Fatalf("selected v2 = %+v; v3 = %+v", selectedV2, selectedV3)
	}
	if err := fixture.store.ConfirmServiceRuntimeSelector(
		ctx, selectedV3,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("old selector confirmation = %v", err)
	}
	if _, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selectedV3.ControlRevision,
			ExpectedDigest:          selectedV3.Digest,
			Target:                  fixture.v3,
		},
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("ABA CAS = %v", err)
	}
	selectedAgain, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selectedV2.ControlRevision,
			ExpectedDigest:          selectedV2.Digest,
			Target:                  fixture.v3,
		},
	)
	if err != nil || selectedAgain.ControlRevision != 3 ||
		selectedAgain.Digest == selectedV3.Digest {
		t.Fatalf("select v3 again = %+v, %v", selectedAgain, err)
	}
	if err := fixture.store.validateServiceRuntimeSelectorStore(ctx); err != nil {
		t.Fatalf("selector store integrity: %v", err)
	}
}

func TestSelectedV3SnapshotSurvivesSparseSuccessor(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	selected, err := fixture.store.SelectServiceRuntimeV3(
		ctx,
		ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewServiceStateV3Reader(
		fixture.store,
		servicecatalogv3.NewDefaultReadCache(),
	)
	if err != nil {
		t.Fatal(err)
	}
	before, err := reader.OpenServiceSelected(ctx, selected, "orders")
	if err != nil {
		t.Fatal(err)
	}
	original := before.Entry.State
	before.Close()

	commit := strings.Repeat("7", 40)
	successorServices := make([]servicecatalog.Service, 0, 601)
	successorServices = append(successorServices, servicecatalog.Service{
		Key:         "orders",
		DisplayName: "Orders V3 successor",
		Disposition: servicecatalog.DispositionAccepted,
		Origin:      servicecatalog.OriginBase,
	})
	for index := range 600 {
		successorServices = append(successorServices, servicecatalog.Service{
			Key:         fmt.Sprintf("z-service-%03d", index),
			DisplayName: fmt.Sprintf("Successor service %03d", index),
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		})
	}
	successor := serviceStateV3Generation(
		t,
		fixture.repository,
		commit,
		"runtime-v3-successor",
		successorServices,
	)
	if err := fixture.store.PublishServiceCatalogV3Candidate(ctx, successor); err != nil {
		t.Fatal(err)
	}
	reconcile, err := fixture.store.BeginServiceStateV3Reconcile(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, fixture.store, reconcile)
	firstChunk, err := fixture.store.ClaimGenerationChunk(
		ctx,
		GenerationResourceCPU,
		"service-runtime-snapshot-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	firstResult, err := fixture.store.ProcessServiceStateV3Chunk(ctx, *firstChunk)
	if err != nil || firstResult.Settled {
		t.Fatalf("partial successor chunk = %+v, %v", firstResult, err)
	}
	if err := fixture.store.CompleteGenerationChunk(ctx, *firstChunk); err != nil {
		t.Fatal(err)
	}
	partialSummary, err := fixture.store.GetServiceStateV3SummaryPoint(
		ctx,
		fixture.repository,
	)
	if err != nil || partialSummary.ControlRevision != selected.StateControlRevision ||
		partialSummary.SummaryDigest != selected.StateSummaryDigest {
		t.Fatalf("partial selected summary = %+v, %v", partialSummary, err)
	}
	partialPreimages, err := surrealdb.Query[[]serviceRepositoryStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository`, map[string]any{"repository": fixture.repository})
	if err != nil || len(firstDomainRows(partialPreimages)) != 1 {
		t.Fatalf("partial summary preimage = %+v, %v", firstDomainRows(partialPreimages), err)
	}
	if report, err := fixture.store.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("partial precious preimage validation = %+v, %v", report, err)
	}
	runServiceStateV3Plan(t, fixture.store, reconcile)
	nextSearch := selectorTestDigest("b")
	activation, err := fixture.store.BeginServiceStateV3Activation(
		ctx,
		fixture.repository,
		nextSearch,
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, activation)

	if err := fixture.store.ValidateServiceRuntimeDatabaseTarget(
		ctx,
		fixture.repository,
		ServiceRuntimeV3,
		fixture.v3,
	); err != nil {
		t.Fatalf("historical selected target: %v", err)
	}
	oldRead, err := reader.OpenServiceSelected(ctx, selected, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if oldRead.Entry.State.StateDigest != original.StateDigest ||
		oldRead.Entry.State.DisplayName != original.DisplayName {
		t.Fatalf("selected state changed = %+v; want %+v", oldRead.Entry.State, original)
	}
	if err := reader.Confirm(ctx, oldRead); err != nil {
		t.Fatal(err)
	}
	oldRead.Close()
	oldPage, err := reader.ListServicesSelected(
		ctx,
		selected,
		ServiceStateFilter{},
		ServiceStatePosition{},
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldPage.Entries) != 1 ||
		oldPage.Entries[0].State.ServiceKey != "orders" ||
		oldPage.Entries[0].State.StateDigest != original.StateDigest {
		t.Fatalf("selected page = %+v", oldPage.Entries)
	}
	oldPage.Close()

	rowResults, err := surrealdb.Query[[]serviceStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_preimage
	WHERE repository = $repository`, map[string]any{"repository": fixture.repository})
	if err != nil {
		t.Fatal(err)
	}
	if rows := firstDomainRows(rowResults); len(rows) != 1 ||
		rows[0].SnapshotRevision != selected.StateControlRevision ||
		rows[0].SnapshotDigest != selected.StateSummaryDigest {
		t.Fatalf("sparse preimages = %+v", rows)
	}
	if report, err := fixture.store.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("precious preimage validation = %+v, %v", report, err)
	}

	nextPointer, err := fixture.store.GetServiceCatalogV3CandidatePointer(
		ctx,
		fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	nextSummary, err := fixture.store.GetServiceStateV3SummaryPoint(
		ctx,
		fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	nextRelationshipGeneration := selectorTestDigest("c")
	nextRelationshipRoot := selectorTestDigest("d")
	if err := fixture.store.PinServiceCatalogV3RelationshipReference(
		ctx,
		ServiceCatalogV3RelationshipReference{
			Repository:                   fixture.repository,
			RelationshipGenerationDigest: nextRelationshipGeneration,
			RelationshipRootDigest:       nextRelationshipRoot,
			CatalogRootDigest:            nextPointer.RootDigest,
			CatalogControlRevision:       nextPointer.ControlRevision,
			StateControlRevision:         nextSummary.ControlRevision,
			StateSummaryDigest:           nextSummary.SummaryDigest,
		},
	); err != nil {
		t.Fatal(err)
	}
	next, err := fixture.store.SelectServiceRuntimeV3(
		ctx,
		ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selected.ControlRevision,
			ExpectedDigest:          selected.Digest,
			Target: ServiceRuntimeTarget{
				CatalogRootDigest:            nextPointer.RootDigest,
				CatalogControlRevision:       nextPointer.ControlRevision,
				StateControlRevision:         nextSummary.ControlRevision,
				StateSummaryDigest:           nextSummary.SummaryDigest,
				SearchGenerationDigest:       nextSearch,
				RelationshipGenerationDigest: nextRelationshipGeneration,
				RelationshipRootDigest:       nextRelationshipRoot,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	nextRead, err := reader.OpenServiceSelected(ctx, next, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if nextRead.Entry.State.DisplayName != "Orders V3 successor" ||
		nextRead.Entry.State.StateDigest == original.StateDigest {
		t.Fatalf("successor state = %+v", nextRead.Entry.State)
	}
	nextRead.Close()

	cursor := ""
	remaining := 2
	for turn := range 16 {
		sweep, sweepErr := fixture.store.SweepServiceCatalogV3Lifecycle(
			ctx,
			cursor,
			64,
			1,
			8,
		)
		if sweepErr != nil {
			t.Fatalf("preimage lifecycle turn %d: %v", turn, sweepErr)
		}
		cursor = sweep.Cursor
		counts, countErr := surrealdb.Query[[]struct {
			Count int `json:"count"`
		}](ctx, fixture.store.db, `
RETURN [{ count: array::len(SELECT id FROM service_state_v3_preimage
	WHERE repository = $repository) + array::len(
		SELECT id FROM service_state_v3_repository_preimage
			WHERE repository = $repository) }]`, map[string]any{
			"repository": fixture.repository,
		})
		if countErr != nil {
			t.Fatal(countErr)
		}
		countRows := firstDomainRows(counts)
		if len(countRows) != 1 || countRows[0].Count < 0 ||
			remaining-countRows[0].Count > 1 {
			t.Fatalf("bounded preimage cleanup = %+v; prior %d", countRows, remaining)
		}
		deleted := remaining - countRows[0].Count
		if sweep.Deleted != deleted {
			t.Fatalf("reported preimage cleanup = %d; actual %d", sweep.Deleted, deleted)
		}
		remaining = countRows[0].Count
		if remaining == 0 {
			if report, validateErr := fixture.store.ValidateServiceCatalogV3Precious(ctx); validateErr != nil {
				t.Fatalf("post-cleanup precious validation = %+v, %v", report, validateErr)
			}
			return
		}
	}
	t.Fatalf("preimage lifecycle did not converge: %d records", remaining)
}

func TestSelectedV3ReaderRefusesCurrentOnlySource(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	selected, err := fixture.store.SelectServiceRuntimeV3(
		t.Context(),
		ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewServiceStateV3Reader(
		currentOnlyServiceStateV3ReadSource{ServiceStateV3ReadSource: fixture.store},
		servicecatalogv3.NewDefaultReadCache(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.OpenServiceSelected(
		t.Context(), selected, "orders",
	); !errors.Is(err, ErrInvalidServiceStateV3) || !errors.Is(err, ErrConflict) {
		t.Fatalf("selected current-only source = %v", err)
	}
}

func TestSelectedV3SuccessorDefersUntilPriorSnapshotDrains(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	selectedA, err := fixture.store.SelectServiceRuntimeV3(
		ctx,
		ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("7", 40)
	servicesB := []servicecatalog.Service{
		{
			Key: "orders", DisplayName: "Orders V3",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		},
		{
			Key: "users", DisplayName: "Users",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		},
	}
	generationB := serviceStateV3Generation(
		t, fixture.repository, commit, "snapshot-b", servicesB,
	)
	if err := fixture.store.PublishServiceCatalogV3Candidate(ctx, generationB); err != nil {
		t.Fatal(err)
	}
	reconcileB, err := fixture.store.BeginServiceStateV3Reconcile(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, reconcileB)
	activationB, err := fixture.store.BeginServiceStateV3Activation(
		ctx, fixture.repository, fixture.v3.SearchGenerationDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, activationB)
	pointerB, err := fixture.store.GetServiceCatalogV3CandidatePointer(
		ctx, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	summaryB, err := fixture.store.GetServiceStateV3SummaryPoint(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	targetB := ServiceRuntimeTarget{
		CatalogRootDigest:            pointerB.RootDigest,
		CatalogControlRevision:       pointerB.ControlRevision,
		StateControlRevision:         summaryB.ControlRevision,
		StateSummaryDigest:           summaryB.SummaryDigest,
		SearchGenerationDigest:       fixture.v3.SearchGenerationDigest,
		RelationshipGenerationDigest: selectorTestDigest("a"),
		RelationshipRootDigest:       selectorTestDigest("b"),
	}
	if err := fixture.store.PinServiceCatalogV3RelationshipReference(
		ctx,
		ServiceCatalogV3RelationshipReference{
			Repository:                   fixture.repository,
			RelationshipGenerationDigest: targetB.RelationshipGenerationDigest,
			RelationshipRootDigest:       targetB.RelationshipRootDigest,
			CatalogRootDigest:            targetB.CatalogRootDigest,
			CatalogControlRevision:       targetB.CatalogControlRevision,
			StateControlRevision:         targetB.StateControlRevision,
			StateSummaryDigest:           targetB.StateSummaryDigest,
		},
	); err != nil {
		t.Fatal(err)
	}
	selectedB, err := fixture.store.SelectServiceRuntimeV3(
		ctx,
		ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selectedA.ControlRevision,
			ExpectedDigest:          selectedA.Digest,
			Target:                  targetB,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.UnpinServiceCatalogV3RelationshipReference(
		ctx,
		ServiceCatalogV3RelationshipReference{
			Repository:                   fixture.repository,
			RelationshipGenerationDigest: fixture.v3.RelationshipGenerationDigest,
			RelationshipRootDigest:       fixture.v3.RelationshipRootDigest,
			CatalogRootDigest:            fixture.v3.CatalogRootDigest,
			CatalogControlRevision:       fixture.v3.CatalogControlRevision,
			StateControlRevision:         fixture.v3.StateControlRevision,
			StateSummaryDigest:           fixture.v3.StateSummaryDigest,
		},
	); err != nil {
		t.Fatal(err)
	}
	ordersB := serviceStateV3Row(t, fixture.store, fixture.repository, "orders")
	if ordersB.DesiredCatalogGeneration != fixture.v3.CatalogRootDigest ||
		ordersB.ActiveCatalogGeneration != fixture.v3.CatalogRootDigest {
		t.Fatalf("B unchanged orders provenance = %+v", ordersB)
	}

	servicesC := slices.Clone(servicesB)
	servicesC[0].DisplayName = "Orders C"
	generationC := serviceStateV3Generation(
		t, fixture.repository, commit, "snapshot-c", servicesC,
	)
	if err := fixture.store.PublishServiceCatalogV3Candidate(ctx, generationC); err != nil {
		t.Fatal(err)
	}
	reconcileC, err := fixture.store.BeginServiceStateV3Reconcile(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, fixture.store, reconcileC)
	chunk, err := fixture.store.ClaimGenerationChunk(
		ctx, GenerationResourceCPU, "snapshot-c",
	)
	if err != nil {
		t.Fatal(err)
	}
	planBefore, err := fixture.store.getServiceStateV3Plan(ctx, reconcileC.Plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ProcessServiceStateV3Chunk(
		ctx, *chunk,
	); !IsDeferral(err) || !errors.Is(err, ErrConflict) {
		t.Fatalf("successor before stale snapshot cleanup = %v", err)
	}
	planAfter, err := fixture.store.getServiceStateV3Plan(ctx, reconcileC.Plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	ordersAfterDeferral := serviceStateV3Row(t, fixture.store, fixture.repository, "orders")
	if planAfter.NextChunk != planBefore.NextChunk ||
		planAfter.RowsWritten != planBefore.RowsWritten ||
		ordersAfterDeferral.StateDigest != ordersB.StateDigest {
		t.Fatalf("deferred successor mutated plan/state: before=%+v after=%+v state=%+v",
			planBefore, planAfter, ordersAfterDeferral,
		)
	}
	candidateA := serviceCatalogV3LifecycleRecord(
		t, fixture.store, fixture.v3.CatalogRootDigest,
	)
	deleted, err := fixture.store.drainServiceStateV3Preimages(ctx, candidateA, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("drain A snapshot = %d, %v", deleted, err)
	}
	result, err := fixture.store.ProcessServiceStateV3Chunk(ctx, *chunk)
	if err != nil || result.Applied != 1 {
		t.Fatalf("successor after stale snapshot cleanup = %+v, %v", result, err)
	}
	if err := fixture.store.CompleteGenerationChunk(ctx, *chunk); err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, reconcileC)
	activationC, err := fixture.store.BeginServiceStateV3Activation(
		ctx, fixture.repository, fixture.v3.SearchGenerationDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, activationC)

	preimageRows, err := surrealdb.Query[[]serviceStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_preimage WHERE repository = $repository`, map[string]any{
		"repository": fixture.repository,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(preimageRows)
	preimageSummaries, err := surrealdb.Query[[]serviceRepositoryStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository`, map[string]any{"repository": fixture.repository})
	if err != nil {
		t.Fatal(err)
	}
	summaries := firstDomainRows(preimageSummaries)
	if len(rows) != 1 || len(summaries) != 1 ||
		rows[0].DesiredCatalogGeneration != fixture.v3.CatalogRootDigest ||
		rows[0].ActiveCatalogGeneration != fixture.v3.CatalogRootDigest ||
		summaries[0].CatalogGeneration != generationB.Root.Digest {
		t.Fatalf("mixed B-owned snapshot = rows %+v summaries %+v", rows, summaries)
	}
	pointerC, err := fixture.store.GetServiceCatalogV3CandidatePointer(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	summaryC, err := fixture.store.GetServiceStateV3SummaryPoint(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	targetC := ServiceRuntimeTarget{
		CatalogRootDigest:            pointerC.RootDigest,
		CatalogControlRevision:       pointerC.ControlRevision,
		StateControlRevision:         summaryC.ControlRevision,
		StateSummaryDigest:           summaryC.SummaryDigest,
		SearchGenerationDigest:       fixture.v3.SearchGenerationDigest,
		RelationshipGenerationDigest: selectorTestDigest("c"),
		RelationshipRootDigest:       selectorTestDigest("d"),
	}
	if err := fixture.store.PinServiceCatalogV3RelationshipReference(
		ctx,
		ServiceCatalogV3RelationshipReference{
			Repository:                   fixture.repository,
			RelationshipGenerationDigest: targetC.RelationshipGenerationDigest,
			RelationshipRootDigest:       targetC.RelationshipRootDigest,
			CatalogRootDigest:            targetC.CatalogRootDigest,
			CatalogControlRevision:       targetC.CatalogControlRevision,
			StateControlRevision:         targetC.StateControlRevision,
			StateSummaryDigest:           targetC.StateSummaryDigest,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.SelectServiceRuntimeV3(
		ctx,
		ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selectedB.ControlRevision,
			ExpectedDigest:          selectedB.Digest,
			Target:                  targetC,
		},
	); err != nil {
		t.Fatal(err)
	}
	if deleted, err := fixture.store.drainServiceStateV3Preimages(
		ctx, candidateA, 1,
	); err != nil || deleted != 0 {
		t.Fatalf("A drained B-owned snapshot = %d, %v", deleted, err)
	}
	if retired, err := fixture.store.retireServiceCatalogV3Generation(
		ctx, candidateA, 1,
	); err != nil || retired {
		t.Fatalf("A retired before B-owned snapshot drained = %t, %v", retired, err)
	}
	candidateB := serviceCatalogV3LifecycleRecord(t, fixture.store, generationB.Root.Digest)
	for turn := range 2 {
		deleted, err := fixture.store.drainServiceStateV3Preimages(ctx, candidateB, 1)
		if err != nil || deleted != 1 {
			t.Fatalf("drain B snapshot turn %d = %d, %v", turn, deleted, err)
		}
	}
	currentRows, err := surrealdb.Query[[]serviceStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_current`, nil)
	if err != nil {
		t.Fatal(err)
	}
	preimageRows, err = surrealdb.Query[[]serviceStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_preimage`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstDomainRows(preimageRows)) > len(firstDomainRows(currentRows)) {
		t.Fatalf("preimage row ceiling: current=%d preimage=%d",
			len(firstDomainRows(currentRows)), len(firstDomainRows(preimageRows)),
		)
	}
	if report, err := fixture.store.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("post-drain precious = %+v, %v", report, err)
	}
	if retired, err := fixture.store.retireServiceCatalogV3Generation(
		ctx, candidateA, 1,
	); err != nil || !retired {
		t.Fatalf("A retirement after B snapshot drain = %t, %v", retired, err)
	}
}

func TestSelectedV3SuccessorMutationRefusesCorruptSelector(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	if _, err := fixture.store.SelectServiceRuntimeV3(
		ctx,
		ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	); err != nil {
		t.Fatal(err)
	}
	prior := serviceStateV3Row(t, fixture.store, fixture.repository, "orders")
	successor := serviceStateV3Generation(
		t,
		fixture.repository,
		strings.Repeat("7", 40),
		"corrupt-selector-successor",
		[]servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders successor",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	if err := fixture.store.PublishServiceCatalogV3Candidate(ctx, successor); err != nil {
		t.Fatal(err)
	}
	reconcile, err := fixture.store.BeginServiceStateV3Reconcile(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	expandServiceStateV3Plan(t, fixture.store, reconcile)
	chunk, err := fixture.store.ClaimGenerationChunk(
		ctx, GenerationResourceCPU, "corrupt-selector",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, fixture.store.db, `
UPDATE $rid SET schema = 'corrupt' RETURN NONE`, map[string]any{
		"rid": serviceRuntimeSelectorID(fixture.repository),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ProcessServiceStateV3Chunk(
		ctx, *chunk,
	); !errors.Is(err, ErrInvalidServiceRuntimeSelector) {
		t.Fatalf("corrupt selector mutation = %v", err)
	}
	after := serviceStateV3Row(t, fixture.store, fixture.repository, "orders")
	if after.StateDigest != prior.StateDigest ||
		after.ControlRevision != prior.ControlRevision {
		t.Fatalf("corrupt selector mutated state: prior=%+v after=%+v", prior, after)
	}
	preimages, err := surrealdb.Query[[]serviceStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_preimage WHERE repository = $repository`, map[string]any{
		"repository": fixture.repository,
	})
	if err != nil || len(firstDomainRows(preimages)) != 0 {
		t.Fatalf("corrupt selector created preimages = %+v, %v", firstDomainRows(preimages), err)
	}
}

func TestSelectV3RefusesStateOutsideTargetSnapshot(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	if _, err := surrealdb.Query[any](ctx, fixture.store.db, `
UPDATE $rid SET visible_from = $visible_from RETURN NONE`, map[string]any{
		"rid":          serviceStateV3ID(fixture.repository, "orders"),
		"visible_from": fixture.v3.StateControlRevision + 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.SelectServiceRuntimeV3(
		ctx,
		ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("state outside selected snapshot = %v", err)
	}
	if report, err := fixture.store.ValidateServiceCatalogV3Precious(
		ctx,
	); !errors.Is(err, ErrInvalidServiceStateV3) {
		t.Fatalf("precious visible boundary = %+v, %v", report, err)
	}
}

func TestServiceStateV3PreciousRejectsPreimageCardinalityCorruption(t *testing.T) {
	type snapshotFixture struct {
		serviceRuntimeSelectorFixture
		summary serviceRepositoryStateRec
		row     serviceStateRec
	}
	setup := func(t *testing.T) snapshotFixture {
		t.Helper()
		fixture := newServiceRuntimeSelectorFixture(t)
		ctx := t.Context()
		if _, err := fixture.store.SelectServiceRuntimeV3(
			ctx,
			ServiceRuntimeSelectionRequest{
				Repository: fixture.repository,
				Target:     fixture.v3,
			},
		); err != nil {
			t.Fatal(err)
		}
		successor := serviceStateV3Generation(
			t,
			fixture.repository,
			strings.Repeat("7", 40),
			"preimage-cardinality",
			[]servicecatalog.Service{{
				Key: "orders", DisplayName: "Orders successor",
				Disposition: servicecatalog.DispositionAccepted,
				Origin:      servicecatalog.OriginBase,
			}},
		)
		if err := fixture.store.PublishServiceCatalogV3Candidate(ctx, successor); err != nil {
			t.Fatal(err)
		}
		reconcile, err := fixture.store.BeginServiceStateV3Reconcile(
			ctx, fixture.repository,
		)
		if err != nil {
			t.Fatal(err)
		}
		expandServiceStateV3Plan(t, fixture.store, reconcile)
		chunk, err := fixture.store.ClaimGenerationChunk(
			ctx, GenerationResourceCPU, "preimage-cardinality",
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.ProcessServiceStateV3Chunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		summaryResults, err := surrealdb.Query[[]serviceRepositoryStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository`, map[string]any{"repository": fixture.repository})
		if err != nil {
			t.Fatal(err)
		}
		rowResults, err := surrealdb.Query[[]serviceStateRec](ctx, fixture.store.db, `
SELECT * FROM service_state_v3_preimage
	WHERE repository = $repository`, map[string]any{"repository": fixture.repository})
		if err != nil {
			t.Fatal(err)
		}
		summaries := firstDomainRows(summaryResults)
		rows := firstDomainRows(rowResults)
		if len(summaries) != 1 || len(rows) != 1 {
			t.Fatalf("seeded snapshot = summaries %+v rows %+v", summaries, rows)
		}
		if report, err := fixture.store.ValidateServiceCatalogV3Precious(ctx); err != nil {
			t.Fatalf("seeded precious = %+v, %v", report, err)
		}
		return snapshotFixture{
			serviceRuntimeSelectorFixture: fixture,
			summary:                       summaries[0],
			row:                           rows[0],
		}
	}
	t.Run("second snapshot summary", func(t *testing.T) {
		fixture := setup(t)
		summary, err := serviceStateV3RepositoryFromRec(fixture.summary)
		if err != nil {
			t.Fatal(err)
		}
		extra := *summary
		extra.ControlRevision++
		if err := servicecatalogv3.SetRepositoryStateDigest(&extra); err != nil {
			t.Fatal(err)
		}
		content := serviceRepositoryStateContent(extra)
		content["snapshot_revision"] = extra.ControlRevision
		content["snapshot_digest"] = extra.SummaryDigest
		if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
CREATE service_state_v3_repository_preimage CONTENT $content RETURN NONE`, map[string]any{
			"content": content,
		}); err != nil {
			t.Fatal(err)
		}
		if report, err := fixture.store.ValidateServiceCatalogV3Precious(
			t.Context(),
		); !errors.Is(err, ErrInvalidServiceStateV3) {
			t.Fatalf("second summary precious = %+v, %v", report, err)
		}
	})
	t.Run("row without current counterpart", func(t *testing.T) {
		fixture := setup(t)
		state, err := serviceStateV3FromRec(fixture.row)
		if err != nil {
			t.Fatal(err)
		}
		orphan := *state
		orphan.ServiceKey = "orphan"
		orphan.DisplayName = "Orphan"
		orphan.Successors = slices.Clone(state.Successors)
		orphan.ControlRevision++
		if err := servicecatalogv3.SetServiceStateDigest(&orphan); err != nil {
			t.Fatal(err)
		}
		content := serviceStateContent(orphan)
		content["visible_from"] = fixture.row.VisibleFrom
		content["snapshot_revision"] = fixture.row.SnapshotRevision
		content["snapshot_digest"] = fixture.row.SnapshotDigest
		if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
CREATE service_state_v3_preimage CONTENT $content RETURN NONE`, map[string]any{
			"content": content,
		}); err != nil {
			t.Fatal(err)
		}
		if report, err := fixture.store.ValidateServiceCatalogV3Precious(
			t.Context(),
		); !errors.Is(err, ErrInvalidServiceStateV3) {
			t.Fatalf("orphan row precious = %+v, %v", report, err)
		}
	})
	t.Run("row without preimage summary", func(t *testing.T) {
		fixture := newServiceRuntimeSelectorFixture(t)
		rowResults, err := surrealdb.Query[[]serviceStateRec](t.Context(), fixture.store.db, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository AND service_key = 'orders'`, map[string]any{
			"repository": fixture.repository,
		})
		if err != nil {
			t.Fatal(err)
		}
		rows := firstDomainRows(rowResults)
		if len(rows) != 1 {
			t.Fatalf("current state rows = %+v", rows)
		}
		state, err := serviceStateV3FromRec(rows[0])
		if err != nil {
			t.Fatal(err)
		}
		content := serviceStateContent(*state)
		content["visible_from"] = rows[0].VisibleFrom
		content["snapshot_revision"] = fixture.v3.StateControlRevision
		content["snapshot_digest"] = fixture.v3.StateSummaryDigest
		if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
CREATE service_state_v3_preimage CONTENT $content RETURN NONE`, map[string]any{
			"content": content,
		}); err != nil {
			t.Fatal(err)
		}
		if report, err := fixture.store.ValidateServiceCatalogV3Precious(
			t.Context(),
		); !errors.Is(err, ErrInvalidServiceStateV3) {
			t.Fatalf("ownerless row precious = %+v, %v", report, err)
		}
	})
}

func TestServiceRuntimeSelectorReconcilesLostCommitResponseAfterCancellation(
	t *testing.T,
) {
	fixture := newServiceRuntimeSelectorFixture(t)
	selected, err := fixture.store.SelectServiceRuntimeV3(
		t.Context(), ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	reconciled, err := fixture.store.reconcileServiceRuntimeSelection(
		canceled, ServiceRuntimeV3, selected, errors.New("lost commit response"),
	)
	if err != nil || reconciled != selected {
		t.Fatalf("reconciled selector = %+v, %v", reconciled, err)
	}
}

func TestSelectedV2CrashRecoveryParksHoldingBeforeSuccessor(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	selectedV3, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	selectedV2, err := fixture.store.SelectServiceRuntimeV2(
		ctx, ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selectedV3.ControlRevision,
			ExpectedDigest:          selectedV3.Digest,
			Target:                  fixture.v2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := fixture.store.GetServiceCatalogGeneration(
		ctx, fixture.repository, selectedV2.CatalogGenerationDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := servicecatalog.Decode(publication.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	holding, err := servicecatalogv3.FromV2(*publication, catalog)
	if err != nil {
		t.Fatal(err)
	}
	wrongHolding, err := fixture.store.GetServiceCatalogV3Candidate(
		ctx, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetRepoIndexed(
		ctx, fixture.repository, strings.Repeat("8", 40), selectedV2.ChangedAt,
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.PublishServiceCatalogV3Holding(
		ctx, selectedV2, wrongHolding.Generation,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("holding candidate outside selected v2 generation = %v", err)
	}
	if err := fixture.store.PublishServiceCatalogV3Candidate(
		ctx, holding,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary historical candidate publication = %v", err)
	}
	if err := fixture.store.PublishServiceCatalogV3Holding(
		ctx, selectedV2, holding,
	); err != nil {
		t.Fatalf("repair selected v2 holding candidate: %v", err)
	}
	pointer, err := fixture.store.GetServiceCatalogV3CandidatePointer(
		ctx, fixture.repository,
	)
	if err != nil || pointer.RootDigest != holding.Root.Digest {
		t.Fatalf("repaired holding pointer = %+v, %v", pointer, err)
	}
	reconcile, err := fixture.store.BeginServiceStateV3Reconcile(
		ctx, fixture.repository,
	)
	if err != nil || reconcile.Noop || reconcile.Plan == nil ||
		reconcile.Plan.CatalogRoot != holding.Root.Digest {
		t.Fatalf("holding reconcile = %+v, %v", reconcile, err)
	}
	runServiceStateV3Plan(t, fixture.store, reconcile)
	activation, err := fixture.store.BeginServiceStateV3Activation(
		ctx, fixture.repository, selectedV2.SearchGenerationDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, activation)
	summary, err := fixture.store.GetServiceStateV3SummaryPoint(
		ctx, fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	holdingRelationshipGeneration := selectorTestDigest("9")
	holdingRelationshipRoot := selectorTestDigest("a")
	if err := fixture.store.PinServiceCatalogV3RelationshipReference(
		ctx, ServiceCatalogV3RelationshipReference{
			Repository:                   fixture.repository,
			RelationshipGenerationDigest: holdingRelationshipGeneration,
			RelationshipRootDigest:       holdingRelationshipRoot,
			CatalogRootDigest:            pointer.RootDigest,
			CatalogControlRevision:       pointer.ControlRevision,
			StateControlRevision:         summary.ControlRevision,
			StateSummaryDigest:           summary.SummaryDigest,
		},
	); err != nil {
		t.Fatal(err)
	}
	parked, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selectedV2.ControlRevision,
			ExpectedDigest:          selectedV2.Digest,
			Target: ServiceRuntimeTarget{
				CatalogRootDigest:            pointer.RootDigest,
				CatalogControlRevision:       pointer.ControlRevision,
				StateControlRevision:         summary.ControlRevision,
				StateSummaryDigest:           summary.SummaryDigest,
				SearchGenerationDigest:       selectedV2.SearchGenerationDigest,
				RelationshipGenerationDigest: holdingRelationshipGeneration,
				RelationshipRootDigest:       holdingRelationshipRoot,
			},
		},
	)
	if err != nil || parked.Backend != ServiceRuntimeV3 ||
		parked.CatalogRootDigest != holding.Root.Digest {
		t.Fatalf("park on repaired holding target = %+v, %v", parked, err)
	}
	successor := serviceStateV2Publication(
		t, fixture.repository, strings.Repeat("8", 40),
	)
	if err := fixture.store.PublishServiceCatalog(ctx, successor); err != nil {
		t.Fatalf("publish v2 successor after parking: %v", err)
	}
	current, err := fixture.store.GetServiceCatalog(ctx, fixture.repository)
	if err != nil || current.SourceCommit != strings.Repeat("8", 40) {
		t.Fatalf("v2 successor current = %+v, %v", current, err)
	}
}

func TestServiceRuntimeSelectorConcurrentCASHasOneWinner(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	start := make(chan struct{})
	type result struct {
		selector ServiceRuntimeSelector
		err      error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	selectRuntime := func(backend string, target ServiceRuntimeTarget) {
		ready.Done()
		<-start
		request := ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     target,
		}
		var selected ServiceRuntimeSelector
		var err error
		if backend == ServiceRuntimeV2 {
			selected, err = fixture.store.SelectServiceRuntimeV2(t.Context(), request)
		} else {
			selected, err = fixture.store.SelectServiceRuntimeV3(t.Context(), request)
		}
		results <- result{selector: selected, err: err}
	}
	go selectRuntime(ServiceRuntimeV2, fixture.v2)
	go selectRuntime(ServiceRuntimeV3, fixture.v3)
	ready.Wait()
	close(start)
	first, second := <-results, <-results

	successes := 0
	conflicts := 0
	for _, got := range []result{first, second} {
		switch {
		case got.err == nil:
			successes++
		case errors.Is(got.err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent selector CAS = %+v, %v", got.selector, got.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent selector CAS successes=%d conflicts=%d", successes, conflicts)
	}
	selected, err := fixture.store.GetServiceRuntimeSelector(t.Context(), fixture.repository)
	if err != nil || selected.ControlRevision != 1 {
		t.Fatalf("selected winner = %+v, %v", selected, err)
	}
}

func TestServiceRuntimeSelectorRetainsHistoricalV3Selection(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	selected, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	next := serviceStateV3Generation(
		t, fixture.repository, strings.Repeat("7", 40), "runtime-v3-next",
		[]servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders V3 Next",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
	)
	if err := fixture.store.PublishServiceCatalogV3Candidate(ctx, next); err != nil {
		t.Fatalf("advance dark candidate: %v", err)
	}
	candidate, err := fixture.store.GetServiceCatalogV3CandidatePointer(ctx, fixture.repository)
	if err != nil || candidate.RootDigest == selected.CatalogRootDigest {
		t.Fatalf("advanced candidate = %+v, %v", candidate, err)
	}

	got, err := fixture.store.GetServiceRuntimeSelector(ctx, fixture.repository)
	if err != nil || got != selected {
		t.Fatalf("historical selector = %+v, %v; want %+v", got, err, selected)
	}
	listed, err := fixture.store.ListServiceRuntimeSelectors(ctx)
	if err != nil || len(listed) != 1 || listed[0] != selected {
		t.Fatalf("historical selector inventory = %+v, %v", listed, err)
	}
	if err := fixture.store.ConfirmServiceRuntimeSelector(ctx, selected); err != nil {
		t.Fatalf("confirm historical selector: %v", err)
	}
	if _, err := surrealdb.Query[any](ctx, fixture.store.db, `
DELETE service_state_v3_plan RETURN NONE`, nil); err != nil {
		t.Fatalf("clear restored restartable plans: %v", err)
	}
	if err := fixture.store.applySchema(ctx); err != nil {
		t.Fatalf("post-restore startup rejected historical selector: %v", err)
	}

	if _, err := fixture.store.SelectServiceRuntimeV2(
		ctx, ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selected.ControlRevision,
			ExpectedDigest:          selected.Digest,
			Target:                  fixture.v2,
		},
	); err != nil {
		t.Fatalf("reverse historical selector: %v", err)
	}
	if err := fixture.store.ConfirmServiceRuntimeSelector(
		ctx, selected,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("confirm superseded selector = %v", err)
	}
}

func TestServiceRuntimeSelectorV3SelectionDoesNotRequireRestartablePlan(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	if _, err := surrealdb.Query[any](ctx, fixture.store.db, `
DELETE service_state_v3_plan RETURN NONE`, nil); err != nil {
		t.Fatalf("clear restartable plans: %v", err)
	}
	selected, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository,
			Target:     fixture.v3,
		},
	)
	if err != nil || selected.Backend != ServiceRuntimeV3 {
		t.Fatalf("select v3 without restartable plan = %+v, %v", selected, err)
	}
}

func TestValidateServiceRuntimeDatabaseTargetChecksActiveSearch(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	for backend, target := range map[string]ServiceRuntimeTarget{
		ServiceRuntimeV2: fixture.v2,
		ServiceRuntimeV3: fixture.v3,
	} {
		if err := fixture.store.ValidateServiceRuntimeDatabaseTarget(
			t.Context(), fixture.repository, backend, target,
		); err != nil {
			t.Fatalf("valid %s target: %v", backend, err)
		}
		target.SearchGenerationDigest = selectorTestDigest("e")
		if err := fixture.store.ValidateServiceRuntimeDatabaseTarget(
			t.Context(), fixture.repository, backend, target,
		); !errors.Is(err, ErrConflict) {
			t.Fatalf("mismatched %s active search = %v", backend, err)
		}
	}
}

func TestServiceRuntimeSelectorTargetMismatchDoesNotLatch(t *testing.T) {
	for _, test := range []struct {
		name    string
		backend string
		mutate  func(*ServiceRuntimeTarget)
	}{
		{name: "v2 catalog", backend: ServiceRuntimeV2, mutate: func(target *ServiceRuntimeTarget) {
			target.CatalogGenerationDigest = selectorTestDigest("a")
		}},
		{name: "v2 state", backend: ServiceRuntimeV2, mutate: func(target *ServiceRuntimeTarget) {
			target.StateControlRevision++
		}},
		{name: "v2 search", backend: ServiceRuntimeV2, mutate: func(target *ServiceRuntimeTarget) {
			target.SearchGenerationDigest = selectorTestDigest("b")
		}},
		{name: "v3 catalog", backend: ServiceRuntimeV3, mutate: func(target *ServiceRuntimeTarget) {
			target.CatalogRootDigest = selectorTestDigest("c")
		}},
		{name: "v3 state", backend: ServiceRuntimeV3, mutate: func(target *ServiceRuntimeTarget) {
			target.StateSummaryDigest = selectorTestDigest("d")
		}},
		{name: "v3 search", backend: ServiceRuntimeV3, mutate: func(target *ServiceRuntimeTarget) {
			target.SearchGenerationDigest = selectorTestDigest("e")
		}},
		{name: "v3 relationship generation", backend: ServiceRuntimeV3, mutate: func(target *ServiceRuntimeTarget) {
			target.RelationshipGenerationDigest = selectorTestDigest("f")
		}},
		{name: "v3 relationship root", backend: ServiceRuntimeV3, mutate: func(target *ServiceRuntimeTarget) {
			target.RelationshipRootDigest = selectorTestDigest("0")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceRuntimeSelectorFixture(t)
			target := fixture.v3
			if test.backend == ServiceRuntimeV2 {
				target = fixture.v2
			}
			test.mutate(&target)
			request := ServiceRuntimeSelectionRequest{
				Repository: fixture.repository, Target: target,
			}
			var err error
			if test.backend == ServiceRuntimeV2 {
				_, err = fixture.store.SelectServiceRuntimeV2(t.Context(), request)
			} else {
				_, err = fixture.store.SelectServiceRuntimeV3(t.Context(), request)
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("mismatched target = %v", err)
			}
			if _, err := fixture.store.GetServiceRuntimeSelector(
				t.Context(), fixture.repository,
			); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed CAS wrote selector: %v", err)
			}
			if version := serviceRuntimeCompatibilityMarker(t, fixture.store); version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
				t.Fatalf("failed CAS latched compatibility marker %q", version)
			}
		})
	}
}

func TestServiceRuntimeSelectorCompatibilityLatchIsIrreversible(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	selected, err := fixture.store.SelectServiceRuntimeV3(
		t.Context(), ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	version := serviceRuntimeCompatibilityMarker(t, fixture.store)
	if version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
		t.Fatalf("activated compatibility marker = %q", version)
	}
	// This is the exact predecessor's acceptance predicate. Its v1-only
	// migrator therefore refuses before returning a usable store.
	if version == candidateControlRevisionMigrationVersion ||
		version == serviceRuntimeSelectorCompatibilityMigrationVersion ||
		version == serviceStateV3SnapshotCompatibilityMigrationVersion {
		t.Fatal("predecessor unexpectedly accepts activated selector marker")
	}
	if relationshipVersion := serviceRuntimeMigrationMarker(
		t, fixture.store, serviceCatalogV3RelationshipReferenceSchemaMigrationID(),
	); relationshipVersion != serviceCatalogV3RelationshipReferenceSchemaMigrationVersion {
		t.Fatalf("selector changed newer relationship marker to %q", relationshipVersion)
	}
	if err := fixture.store.migrateCandidateControlRevisions(
		t.Context(),
	); err != nil {
		t.Fatalf("current binary rejected compatibility latch: %v", err)
	}
	ready, err := fixture.store.derivedRetentionReadiness(
		t.Context(), derivedRetentionCandidate,
	)
	if err != nil || !ready {
		t.Fatalf("compatibility latch disabled candidate retention: ready=%t error=%v", ready, err)
	}
	if _, err := fixture.store.SelectServiceRuntimeV2(
		t.Context(), ServiceRuntimeSelectionRequest{
			Repository:              fixture.repository,
			ExpectedControlRevision: selected.ControlRevision,
			ExpectedDigest:          selected.Digest,
			Target:                  fixture.v2,
		},
	); err != nil {
		t.Fatal(err)
	}
	if version := serviceRuntimeCompatibilityMarker(t, fixture.store); version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
		t.Fatalf("reverse weakened compatibility latch to %q", version)
	}
}

func TestServiceRuntimeSelectorCASAdvancesSupportedCompatibilityMarkers(
	t *testing.T,
) {
	for _, version := range []string{
		candidateControlRevisionMigrationVersion,
		serviceRuntimeSelectorCompatibilityMigrationVersion,
		serviceStateV3SnapshotCompatibilityMigrationVersion,
		serviceCatalogV3SourceGenerationCompatibilityMigrationVersion,
	} {
		t.Run(version, func(t *testing.T) {
			fixture := newServiceRuntimeSelectorFixture(t)
			if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
UPDATE $rid SET version = $version RETURN NONE`, map[string]any{
				"rid":     candidateControlRevisionMigrationID(),
				"version": version,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.SelectServiceRuntimeV3(
				t.Context(),
				ServiceRuntimeSelectionRequest{
					Repository: fixture.repository,
					Target:     fixture.v3,
				},
			); err != nil {
				t.Fatalf("select from compatibility marker %q: %v", version, err)
			}
			if got := serviceRuntimeCompatibilityMarker(t, fixture.store); got != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
				t.Fatalf("compatibility marker after CAS = %q", got)
			}
		})
	}

	t.Run("unknown", func(t *testing.T) {
		fixture := newServiceRuntimeSelectorFixture(t)
		const unknown = "t41.10-service-catalog-v3-source-generation-compat-v999"
		if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
UPDATE $rid SET version = $version RETURN NONE`, map[string]any{
			"rid":     candidateControlRevisionMigrationID(),
			"version": unknown,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.SelectServiceRuntimeV3(
			t.Context(),
			ServiceRuntimeSelectionRequest{
				Repository: fixture.repository,
				Target:     fixture.v3,
			},
		); err == nil || !strings.Contains(err.Error(), "unsupported compatibility marker") {
			t.Fatalf("select from unknown compatibility marker = %v", err)
		}
		if got := serviceRuntimeCompatibilityMarker(t, fixture.store); got != unknown {
			t.Fatalf("unknown compatibility marker changed to %q", got)
		}
		if _, err := fixture.store.GetServiceRuntimeSelector(
			t.Context(), fixture.repository,
		); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown compatibility marker wrote selector: %v", err)
		}
	})
}

func TestServiceRuntimeSelectorRepositoryDeletionRetiresAuthority(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	ctx := t.Context()
	selected, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RetireServiceRuntimeSelectorForRepositoryDeletion(
		ctx, fixture.repository,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("retire live repository selector = %v", err)
	}
	if err := fixture.store.ConfirmServiceRuntimeSelector(ctx, selected); err != nil {
		t.Fatalf("refused retirement changed selector: %v", err)
	}

	if err := fixture.store.SetRepoDeleting(ctx, fixture.repository, true); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RetireServiceRuntimeSelectorForRepositoryDeletion(
		ctx, fixture.repository,
	); err != nil {
		t.Fatalf("retire deleting repository selector: %v", err)
	}
	if _, err := fixture.store.GetServiceRuntimeSelector(
		ctx, fixture.repository,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retired selector = %v", err)
	}
	references, err := surrealdb.Query[[]serviceCatalogV3StateReferenceRec](ctx, fixture.store.db, `
SELECT * FROM service_catalog_v3_state_reference
	WHERE repository = $repository AND kind = 'current' LIMIT 2`, map[string]any{
		"repository": fixture.repository,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows := firstDomainRows(references); len(rows) != 0 {
		t.Fatalf("retired selector retained current catalog reference: %+v", rows)
	}
	if version := serviceRuntimeCompatibilityMarker(t, fixture.store); version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
		t.Fatalf("retirement weakened compatibility latch to %q", version)
	}
	if err := fixture.store.validateServiceRuntimeSelectorStore(ctx); err != nil {
		t.Fatalf("retired selector store integrity: %v", err)
	}
	if _, err := fixture.store.SelectServiceRuntimeV3(
		ctx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleting repository selector resurrection = %v", err)
	}
	if err := fixture.store.ValidateServiceRuntimeDatabaseTarget(
		ctx, fixture.repository, ServiceRuntimeV3, fixture.v3,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleting repository target validation = %v", err)
	}
	if err := fixture.store.RetireServiceRuntimeSelectorForRepositoryDeletion(
		ctx, fixture.repository,
	); err != nil {
		t.Fatalf("idempotent selector retirement: %v", err)
	}
}

func TestServiceRuntimeSelectorStartupIntegrityRejectsLatchSplit(t *testing.T) {
	t.Run("selector without latch", func(t *testing.T) {
		fixture := newServiceRuntimeSelectorFixture(t)
		if _, err := fixture.store.SelectServiceRuntimeV3(
			t.Context(), ServiceRuntimeSelectionRequest{
				Repository: fixture.repository,
				Target:     fixture.v3,
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
UPDATE $rid SET version = $version RETURN NONE`, map[string]any{
			"rid":     candidateControlRevisionMigrationID(),
			"version": candidateControlRevisionMigrationVersion,
		}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.validateServiceRuntimeSelectorStore(
			t.Context(),
		); !errors.Is(err, ErrInvalidServiceRuntimeSelector) {
			t.Fatalf("selector without latch startup validation = %v", err)
		}
	})
	t.Run("schema latch without selector", func(t *testing.T) {
		fixture := newServiceRuntimeSelectorFixture(t)
		if version := serviceRuntimeCompatibilityMarker(t, fixture.store); version != serviceCatalogV3SourceGenerationCompatibilityMigrationVersion {
			t.Fatalf("schema compatibility latch = %q", version)
		}
		if err := fixture.store.validateServiceRuntimeSelectorStore(t.Context()); err != nil {
			t.Fatalf("schema latch without selector = %v", err)
		}
	})
	t.Run("snapshot predecessor without selector", func(t *testing.T) {
		fixture := newServiceRuntimeSelectorFixture(t)
		if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
UPDATE $rid SET version = $version RETURN NONE`, map[string]any{
			"rid":     candidateControlRevisionMigrationID(),
			"version": serviceStateV3SnapshotCompatibilityMigrationVersion,
		}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.validateServiceRuntimeSelectorStore(
			t.Context(),
		); !errors.Is(err, ErrInvalidServiceRuntimeSelector) {
			t.Fatalf("snapshot predecessor startup validation = %v", err)
		}
	})
}

func TestServiceRuntimeSelectorStartupIntegrityRejectsSplitRows(t *testing.T) {
	fixture := newServiceRuntimeSelectorFixture(t)
	selected, err := fixture.store.SelectServiceRuntimeV3(
		t.Context(), ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, Target: fixture.v3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
UPDATE $rid SET digest = $digest RETURN NONE`, map[string]any{
		"rid":    serviceRuntimeSelectorID(fixture.repository),
		"digest": selectorTestDigest("9"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.validateServiceRuntimeSelectorStore(
		t.Context(),
	); !errors.Is(err, ErrInvalidServiceRuntimeSelector) {
		t.Fatalf("tampered selector startup validation = %v", err)
	}
	if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
UPDATE $rid CONTENT $content RETURN NONE`, map[string]any{
		"rid":     serviceRuntimeSelectorID(fixture.repository),
		"content": serviceRuntimeSelectorContent(selected),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](t.Context(), fixture.store.db, `
DELETE $rid RETURN NONE`, map[string]any{
		"rid": serviceRuntimeCurrentCatalogReferenceID(fixture.repository),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.validateServiceRuntimeSelectorStore(
		t.Context(),
	); !errors.Is(err, ErrInvalidServiceRuntimeSelector) {
		t.Fatalf("missing selected catalog pin startup validation = %v", err)
	}
}

func serviceRuntimeCompatibilityMarker(t *testing.T, s *Surreal) string {
	t.Helper()
	return serviceRuntimeMigrationMarker(t, s, candidateControlRevisionMigrationID())
}

func serviceRuntimeMigrationMarker(
	t *testing.T,
	s *Surreal,
	rid models.RecordID,
) string {
	t.Helper()
	results, err := surrealdb.Query[[]serviceRuntimeMigrationRec](
		t.Context(), s.db, "SELECT version FROM $rid", map[string]any{
			"rid": rid,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		t.Fatalf("compatibility marker rows = %+v", rows)
	}
	return rows[0].Version
}
