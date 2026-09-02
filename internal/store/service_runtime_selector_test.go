package store

import (
	"context"
	"errors"
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
			if version := serviceRuntimeCompatibilityMarker(t, fixture.store); version != candidateControlRevisionMigrationVersion {
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
	if version != serviceRuntimeSelectorCompatibilityMigrationVersion ||
		version == candidateControlRevisionMigrationVersion {
		t.Fatalf("activated compatibility marker = %q", version)
	}
	// This is the exact predecessor's acceptance predicate. Its v1-only
	// migrator therefore refuses before returning a usable store.
	if version == candidateControlRevisionMigrationVersion {
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
	if version := serviceRuntimeCompatibilityMarker(t, fixture.store); version != serviceRuntimeSelectorCompatibilityMigrationVersion {
		t.Fatalf("reverse weakened compatibility latch to %q", version)
	}
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
	if version := serviceRuntimeCompatibilityMarker(t, fixture.store); version != serviceRuntimeSelectorCompatibilityMigrationVersion {
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
	t.Run("latch without selector", func(t *testing.T) {
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
DELETE $rid RETURN NONE`, map[string]any{
			"rid": serviceRuntimeSelectorID(fixture.repository),
		}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.validateServiceRuntimeSelectorStore(
			t.Context(),
		); !errors.Is(err, ErrInvalidServiceRuntimeSelector) {
			t.Fatalf("latch without selector startup validation = %v", err)
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
