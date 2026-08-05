package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestServiceStateIndependentTransitionsAndABA(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/service-state"
	commit := "5555555555555555555555555555555555555555"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/service-state.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	first := statePublication(t, repository, commit, "v1", []servicecatalog.Service{
		acceptedStateService("orders", "Orders"),
		acceptedStateService("payments", "Payments"),
	})
	publishStateCatalog(t, ctx, s, first)
	summary := requireStateSummary(t, ctx, s, repository)
	if summary.LiveServiceCount != 2 || summary.UnavailableCount != 2 ||
		summary.ControlRevision != 1 {
		t.Fatalf("initial summary = %+v", summary)
	}
	page, err := s.ListServiceStates(ctx, repository, store.ServiceStateFilter{
		Status: servicecatalog.StatusUnavailable,
	}, store.ServiceStatePosition{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].State.ServiceKey != "orders" ||
		page.Entries[1].State.ServiceKey != "payments" ||
		page.Entries[0].Projection == nil ||
		len(page.Entries[0].Projection.Memberships) != 1 ||
		page.Summary.SummaryDigest != summary.SummaryDigest {
		t.Fatalf("initial service page = %+v", page)
	}
	if err := s.ConfirmServiceStateSnapshot(ctx, repository, page.Summary); err != nil {
		t.Fatalf("confirm initial service page: %v", err)
	}
	changedSummary := page.Summary
	changedSummary.ControlRevision++
	if err := s.ConfirmServiceStateSnapshot(
		ctx, repository, changedSummary,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("confirm changed service page = %v, want ErrConflict", err)
	}
	read, err := s.GetServiceStateRead(ctx, repository, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if read.Entry.Projection == nil || read.Entry.State.ServiceKey != "orders" ||
		read.Publication.GenerationDigest != page.Publication.GenerationDigest ||
		read.Summary.SummaryDigest != page.Summary.SummaryDigest {
		t.Fatalf("initial service detail = %+v", read)
	}
	currentCatalog, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileServiceStates(ctx, *currentCatalog); err != nil {
		t.Fatalf("exact state retry: %v", err)
	}
	exactSummary := requireStateSummary(t, ctx, s, repository)
	if exactSummary.ControlRevision != summary.ControlRevision ||
		!exactSummary.UpdatedAt.Equal(summary.UpdatedAt) ||
		exactSummary.SummaryDigest != summary.SummaryDigest {
		t.Fatalf("exact retry changed summary: before=%+v after=%+v", summary, exactSummary)
	}
	activateStateService(t, ctx, s, repository, "orders")
	activateStateService(t, ctx, s, repository, "payments")
	ordersA := requireServiceState(t, ctx, s, repository, "orders")
	paymentsA := requireServiceState(t, ctx, s, repository, "payments")
	if ordersA.Status != servicecatalog.StatusCurrent ||
		paymentsA.Status != servicecatalog.StatusCurrent {
		t.Fatalf("activated states = %+v, %+v", ordersA, paymentsA)
	}

	second := statePublication(t, repository, commit, "v2", []servicecatalog.Service{
		acceptedStateService("orders", "Orders API"),
		acceptedStateService("payments", "Payments"),
	})
	publishStateCatalog(t, ctx, s, second)
	ordersB := requireServiceState(t, ctx, s, repository, "orders")
	paymentsB := requireServiceState(t, ctx, s, repository, "payments")
	if ordersB.Status != servicecatalog.StatusStale ||
		ordersB.ControlRevision != ordersA.ControlRevision+1 {
		t.Fatalf("changed service = %+v; prior %+v", ordersB, ordersA)
	}
	if paymentsB.StateDigest != paymentsA.StateDigest ||
		paymentsB.ControlRevision != paymentsA.ControlRevision ||
		paymentsB.Status != servicecatalog.StatusCurrent {
		t.Fatalf("sibling was relabeled: before=%+v after=%+v", paymentsA, paymentsB)
	}

	// The semantic endpoint returns to A under a third authority version. The
	// active A identity makes the service current again, while row and summary
	// revisions preserve the intervening B transition.
	third := statePublication(t, repository, commit, "v3", []servicecatalog.Service{
		acceptedStateService("orders", "Orders"),
		acceptedStateService("payments", "Payments"),
	})
	publishStateCatalog(t, ctx, s, third)
	ordersABA := requireServiceState(t, ctx, s, repository, "orders")
	if ordersABA.Status != servicecatalog.StatusCurrent ||
		ordersABA.DesiredGeneration != ordersA.DesiredGeneration ||
		ordersABA.ControlRevision != ordersB.ControlRevision+1 {
		t.Fatalf("A-B-A state = %+v; A=%+v B=%+v", ordersABA, ordersA, ordersB)
	}

	// A one-to-one successor transition renames the key without changing the
	// old incarnation or guessing identity continuity for the replacement.
	renamed := statePublication(t, repository, commit, "v4", []servicecatalog.Service{
		{
			Key: "orders", DisplayName: "Orders", Disposition: servicecatalog.DispositionRejected,
			Origin: servicecatalog.OriginBase, Reason: "renamed",
			Successors: []string{"order-service"},
		},
		acceptedStateService("order-service", "Order Service"),
		acceptedStateService("payments", "Payments"),
	})
	publishStateCatalog(t, ctx, s, renamed)
	renamedOrders := requireServiceState(t, ctx, s, repository, "orders")
	newOrderService := requireServiceState(t, ctx, s, repository, "order-service")
	if !renamedOrders.Removed || len(renamedOrders.Successors) != 1 ||
		renamedOrders.Successors[0] != "order-service" ||
		newOrderService.Incarnation != 1 {
		t.Fatalf("rename states = old %+v, new %+v", renamedOrders, newOrderService)
	}

	// A second explicit rejected record splits into two successors. The old key
	// remains a tombstone and both new keys start at incarnation one.
	split := statePublication(t, repository, commit, "v5", []servicecatalog.Service{
		{
			Key: "order-service", DisplayName: "Order Service",
			Disposition: servicecatalog.DispositionRejected,
			Origin:      servicecatalog.OriginBase, Reason: "split",
			Successors: []string{"orders-admin", "orders-v2"},
		},
		acceptedStateService("orders-admin", "Orders Admin"),
		acceptedStateService("orders-v2", "Orders v2"),
		acceptedStateService("payments", "Payments"),
	})
	publishStateCatalog(t, ctx, s, split)
	removedOrderService := requireServiceState(t, ctx, s, repository, "order-service")
	if !removedOrderService.Removed || removedOrderService.Status != servicecatalog.StatusRemoved ||
		removedOrderService.Incarnation != 1 || len(removedOrderService.Successors) != 2 {
		t.Fatalf("split tombstone = %+v", removedOrderService)
	}
	if got := requireServiceState(t, ctx, s, repository, "orders-v2"); got.Incarnation != 1 || got.Status != servicecatalog.StatusUnavailable {
		t.Fatalf("split successor = %+v", got)
	}

	// Merging the successors back re-adds the original key under incarnation
	// two and clears the prior incarnation's active identity.
	merged := statePublication(t, repository, commit, "v6", []servicecatalog.Service{
		acceptedStateService("orders", "Orders merged"),
		{
			Key: "orders-admin", DisplayName: "Orders Admin",
			Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase,
			Reason: "merged", Successors: []string{"orders"},
		},
		{
			Key: "orders-v2", DisplayName: "Orders v2",
			Disposition: servicecatalog.DispositionRejected, Origin: servicecatalog.OriginBase,
			Reason: "merged", Successors: []string{"orders"},
		},
		acceptedStateService("payments", "Payments"),
	})
	publishStateCatalog(t, ctx, s, merged)
	readded := requireServiceState(t, ctx, s, repository, "orders")
	if readded.Incarnation != 2 || readded.Status != servicecatalog.StatusUnavailable ||
		readded.ActiveDesiredGeneration != "" {
		t.Fatalf("re-added service = %+v", readded)
	}
	if _, err := s.ListServiceStates(
		ctx, repository, store.ServiceStateFilter{},
		store.ServiceStatePosition{ServiceKey: "orders", Incarnation: 1}, 2,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale incarnation seek = %v, want ErrConflict", err)
	}
	finalSummary := requireStateSummary(t, ctx, s, repository)
	if finalSummary.LiveServiceCount != 2 || finalSummary.UnavailableCount != 1 ||
		finalSummary.CurrentCount != 1 || finalSummary.TombstoneCount != 3 {
		t.Fatalf("merged summary = %+v", finalSummary)
	}

	// Omitting payments creates an implicit tombstone, while an explicit
	// conflict for orders remains a live but non-servable service state.
	conflicted := statePublication(t, repository, commit, "v7", []servicecatalog.Service{
		{
			Key: "orders", DisplayName: "Orders disputed",
			Disposition: servicecatalog.DispositionConflict,
			Origin:      servicecatalog.OriginBase, Reason: "two accepted owners",
		},
	})
	publishStateCatalog(t, ctx, s, conflicted)
	ordersConflict := requireServiceState(t, ctx, s, repository, "orders")
	removedPayments := requireServiceState(t, ctx, s, repository, "payments")
	if ordersConflict.Status != servicecatalog.StatusConflict || ordersConflict.Removed {
		t.Fatalf("conflict state = %+v", ordersConflict)
	}
	if !removedPayments.Removed || removedPayments.Reason != servicecatalog.ImplicitRemovalReason {
		t.Fatalf("implicit removal = %+v", removedPayments)
	}
	conflictSummary := requireStateSummary(t, ctx, s, repository)
	if conflictSummary.LiveServiceCount != 1 || conflictSummary.ConflictCount != 1 ||
		conflictSummary.TombstoneCount != 4 {
		t.Fatalf("conflict/removal summary = %+v", conflictSummary)
	}
}

func TestServiceStateConcurrentReconcileAndStaleActivationFailClosed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/service-state-race"
	commit := "6666666666666666666666666666666666666666"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/service-state-race.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	first := statePublication(t, repository, commit, "race-v1", []servicecatalog.Service{
		acceptedStateService("orders", "Orders"),
		acceptedStateService("payments", "Payments"),
	})
	publishStateCatalog(t, ctx, s, first)
	orders := requireServiceState(t, ctx, s, repository, "orders")
	summary := requireStateSummary(t, ctx, s, repository)
	staleActivation := servicecatalog.ServiceActivation{
		Repository: repository, ServiceKey: orders.ServiceKey,
		Incarnation: orders.Incarnation, DesiredGeneration: orders.DesiredGeneration,
		StateControlRevision:      orders.ControlRevision,
		RepositoryControlRevision: summary.ControlRevision,
	}

	second := statePublication(t, repository, commit, "race-v2", []servicecatalog.Service{
		acceptedStateService("orders", "Orders API"),
		acceptedStateService("payments", "Payments"),
	})
	if err := s.PublishServiceCatalog(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetServiceStateSummary(ctx, repository); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unreconciled catalog/state gap = %v, want ErrConflict", err)
	}
	current, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- s.ReconcileServiceStates(ctx, *current)
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil && !errors.Is(err, store.ErrConflict) {
			t.Fatalf("concurrent reconcile = %v", err)
		}
	}
	if err := s.ActivateService(ctx, staleActivation); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale activation = %v, want ErrConflict", err)
	}
	ordersAfter := requireServiceState(t, ctx, s, repository, "orders")
	paymentsAfter := requireServiceState(t, ctx, s, repository, "payments")
	if ordersAfter.ControlRevision != orders.ControlRevision+1 ||
		paymentsAfter.ControlRevision != 1 {
		t.Fatalf("concurrent/stale transition changed rows unexpectedly: orders=%+v payments=%+v", ordersAfter, paymentsAfter)
	}
}

func TestServiceStateGenerationActivationIsAtomicAndIgnoresProposals(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/service-generation-activation"
	commit := "7777777777777777777777777777777777777777"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/service-generation-activation.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	publication := statePublication(t, repository, commit, "generation-v1", []servicecatalog.Service{
		acceptedStateService("orders", "Orders"),
		acceptedStateService("payments", "Payments"),
		{
			Key: "proposal", DisplayName: "Proposal",
			Disposition: servicecatalog.DispositionProposal,
			Origin:      servicecatalog.OriginBase, Reason: "not accepted",
		},
	})
	publishStateCatalog(t, ctx, s, publication)
	current, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	sourceGeneration, err := servicecatalog.SourceGenerationDigest(*current)
	if err != nil {
		t.Fatal(err)
	}
	needed, err := s.ServiceGenerationActivationNeeded(
		ctx, repository, current.GenerationDigest, sourceGeneration,
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	)
	if err != nil || !needed {
		t.Fatalf("activation needed = %t, %v", needed, err)
	}
	before := requireStateSummary(t, ctx, s, repository)
	activated, err := s.ActivateServiceGeneration(
		ctx, repository, current.GenerationDigest, sourceGeneration,
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	)
	if err != nil || activated != 2 {
		t.Fatalf("generation activation = %d, %v", activated, err)
	}
	after := requireStateSummary(t, ctx, s, repository)
	if after.ControlRevision != before.ControlRevision+1 ||
		after.CurrentCount != 2 || after.UnavailableCount != 1 {
		t.Fatalf("generation summary = %+v; before %+v", after, before)
	}
	for _, key := range []string{"orders", "payments"} {
		if state := requireServiceState(t, ctx, s, repository, key); state.Status != servicecatalog.StatusCurrent {
			t.Fatalf("activated state %q = %+v", key, state)
		}
	}
	if proposal := requireServiceState(t, ctx, s, repository, "proposal"); proposal.Status != servicecatalog.StatusUnavailable ||
		proposal.ActiveDesiredGeneration != "" {
		t.Fatalf("proposal was activated = %+v", proposal)
	}
	needed, err = s.ServiceGenerationActivationNeeded(
		ctx, repository, current.GenerationDigest, sourceGeneration,
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	)
	if err != nil || needed {
		t.Fatalf("post-activation needed = %t, %v", needed, err)
	}
	const replacementSearch = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	needed, err = s.ServiceGenerationActivationNeeded(
		ctx, repository, current.GenerationDigest, sourceGeneration, replacementSearch,
	)
	if err != nil || !needed {
		t.Fatalf("same-source replacement activation needed = %t, %v", needed, err)
	}
	activated, err = s.ActivateServiceGeneration(
		ctx, repository, current.GenerationDigest, sourceGeneration, replacementSearch,
	)
	if err != nil || activated != 2 {
		t.Fatalf("same-source replacement activation = %d, %v", activated, err)
	}
	for _, key := range []string{"orders", "payments"} {
		state := requireServiceState(t, ctx, s, repository, key)
		if state.ActiveSearchGeneration != replacementSearch {
			t.Fatalf("replacement search identity for %q = %+v", key, state)
		}
	}
	if _, err := s.ActivateServiceGeneration(
		ctx, repository, current.GenerationDigest,
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mismatched source activation = %v, want ErrConflict", err)
	}
}

func acceptedStateService(key, displayName string) servicecatalog.Service {
	return servicecatalog.Service{
		Key: key, DisplayName: displayName,
		Disposition: servicecatalog.DispositionAccepted,
		Origin:      servicecatalog.OriginBase,
	}
}

func statePublication(
	t *testing.T,
	repository, commit, version string,
	services []servicecatalog.Service,
) servicecatalog.Publication {
	t.Helper()
	memberships := make([]servicecatalog.Membership, 0, len(services))
	for _, service := range services {
		if service.Disposition != servicecatalog.DispositionAccepted {
			continue
		}
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: service.Key, Path: "services/" + service.Key,
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "state-catalog", Version: version,
		},
		Services: services, Memberships: memberships,
		Unowned: []servicecatalog.UnownedPlacement{},
	}
	return finishTestCatalogPublication(
		t, repository, commit, servicecatalog.SourceOperator,
		"/state-catalog.json", "", catalog,
	)
}

func publishStateCatalog(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	publication servicecatalog.Publication,
) {
	t.Helper()
	if err := s.PublishServiceCatalog(ctx, publication); err != nil {
		t.Fatalf("publish catalog: %v", err)
	}
	current, err := s.GetServiceCatalog(ctx, publication.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileServiceStates(ctx, *current); err != nil {
		t.Fatalf("reconcile service states: %v", err)
	}
}

func activateStateService(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository, serviceKey string,
) {
	t.Helper()
	state := requireServiceState(t, ctx, s, repository, serviceKey)
	summary := requireStateSummary(t, ctx, s, repository)
	if err := s.ActivateService(ctx, servicecatalog.ServiceActivation{
		Repository: repository, ServiceKey: serviceKey,
		Incarnation: state.Incarnation, DesiredGeneration: state.DesiredGeneration,
		StateControlRevision:      state.ControlRevision,
		RepositoryControlRevision: summary.ControlRevision,
	}); err != nil {
		t.Fatalf("activate %q: %v", serviceKey, err)
	}
}

func requireServiceState(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository, serviceKey string,
) servicecatalog.ServiceState {
	t.Helper()
	state, err := s.GetServiceState(ctx, repository, serviceKey)
	if err != nil {
		t.Fatalf("get service state %q: %v", serviceKey, err)
	}
	return *state
}

func requireStateSummary(
	t *testing.T,
	ctx context.Context,
	s *store.Surreal,
	repository string,
) servicecatalog.RepositoryState {
	t.Helper()
	summary, err := s.GetServiceStateSummary(ctx, repository)
	if err != nil {
		t.Fatalf("get service state summary: %v", err)
	}
	return *summary
}
