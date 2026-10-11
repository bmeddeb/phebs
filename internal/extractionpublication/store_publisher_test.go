package extractionpublication

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

type observedPublicationStore struct {
	store.PartitionedEvidenceStore
	calls  int
	err    error
	before func()
	value  store.PartitionedExtractionDomain
}

func (state *observedPublicationStore) PublishPartitionedExtractionDomain(_ context.Context, value store.PartitionedExtractionDomain) error {
	if state.before != nil {
		state.before()
	}
	state.calls++
	state.value = value
	return state.err
}

func (state *observedPublicationStore) PublishPartitionedExtractionDomainRefusal(_ context.Context, value store.PartitionedExtractionDomain) error {
	if state.before != nil {
		state.before()
	}
	state.calls++
	state.value = value
	return state.err
}

func TestStorePublisherObservesActualCalls(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("1", 64), false)
	root, err := candidate.BuildDomainResultRoot(plan, []candidate.PartitionResult{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ordinary", "success", "native_error", "invalid", "sink", "panic", "canceled", "during"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			events := 0
			nativeErr := errors.New("native publication failure")
			state := &observedPublicationStore{}
			if mode == "native_error" {
				state.err = nativeErr
			}
			if mode != "ordinary" {
				ctx, err = readaccounting.WithPublicationObserver(ctx, func() error {
					events++
					switch mode {
					case "sink":
						return readaccounting.ErrEvent
					case "panic":
						panic("sink")
					case "during":
						cancel()
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				state.before = func() {
					if events != state.calls+1 {
						t.Fatal("native publication preceded its actual attempt event")
					}
				}
			}
			if mode == "canceled" {
				cancel()
			}
			publisher := StorePublisher{Store: state}
			run := "run-observed"
			if mode == "invalid" {
				run = ""
			}
			wantEvents, wantCalls := 1, 0
			switch mode {
			case "ordinary":
				wantEvents, wantCalls = 0, 1
			case "success", "native_error":
				wantCalls = 1
			}
			err := publisher.PublishDomain(ctx, plan, root, run)
			if (err == nil) != (mode == "ordinary" || mode == "success") || events != wantEvents || state.calls != wantCalls {
				t.Fatalf("events=%d native=%d error=%v", events, state.calls, err)
			}
			if mode == "native_error" && err != nativeErr {
				t.Fatal("observation changed native error identity")
			}
			if mode == "success" {
				first := state.value
				if err := publisher.PublishDomain(ctx, plan, root, run); err != nil || events != 2 || state.calls != 2 || state.value != first {
					t.Fatal("exact-current recount was skipped or authority changed", err)
				}
			}
		})
	}
}

func TestRuntimeStorePublisherBothRoutesObserveOnce(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("2", 64), false)
	runtime, _, _, _, _, fence, domain := newRuntimeFixture(t, plan)
	events, settled := 0, 0
	ctx, err := readaccounting.WithPublicationObserver(t.Context(), func() error {
		if !fence.active() {
			t.Fatal("publication observation lost the existing authority fence")
		}
		events++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state := &observedPublicationStore{}
	runtime.Publisher = StorePublisher{Store: state}
	runtime.OnSettled = func(context.Context, string) error { settled++; return nil }
	digest, err := runtime.Reconcile(ctx, plan.Repository, []DomainPlan{domain})
	if err != nil || events != 1 || state.calls != 1 || settled != 1 {
		t.Fatal("initial publishRoot route", events, state.calls, settled, err)
	}
	directory := runtime.generationDirectory(plan.Repository, digest)
	generation, err := runtime.openGeneration(directory, plan.Repository, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.reactivateComplete(ctx, generation, directory); err != nil || events != 2 || state.calls != 2 || settled != 2 {
		t.Fatal("reactivateComplete route", events, state.calls, settled, err)
	}
	if fence.active() {
		t.Fatal("publication route retained its authority fence")
	}
}

func TestStorePublisherRefusalRouteValidityAndObservation(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("3", 64), true)
	result, err := candidate.BuildPartitionResult(plan, 0, candidate.PartitionResultSpec{
		Disposition: candidate.PartitionResultTerminalRefusal,
		Reason:      "deterministic_refusal",
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := candidate.BuildDomainResultRoot(plan, []candidate.PartitionResult{result})
	if err != nil {
		t.Fatal(err)
	}
	if root.Disposition != candidate.PartitionResultTerminalRefusal {
		t.Fatalf("refusal root disposition = %s", root.Disposition)
	}

	state := &observedPublicationStore{}
	publisher := StorePublisher{Store: state}
	if err := publisher.PublishRefusal(t.Context(), plan, root, "run-refused"); err != nil {
		t.Fatalf("refusal publication = %v", err)
	}
	if state.calls != 1 || state.value.RunID != "run-refused" ||
		state.value.PlanDigest != plan.Digest || state.value.RootDigest != root.Digest ||
		state.value.Repository != plan.Repository || state.value.Domain != plan.Domain ||
		state.value.Facts != root.Totals.Facts || state.value.Rows != root.Totals.Rows ||
		state.value.References != root.Totals.References {
		t.Fatalf("refusal publication = %+v", state.value)
	}

	// The observation route counts the refused attempt exactly once before
	// native store work, like the success route.
	events := 0
	ctx, err := readaccounting.WithPublicationObserver(t.Context(), func() error {
		events++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedPublicationStore{}
	observed.before = func() {
		if events != observed.calls+1 {
			t.Fatal("refusal preceded its actual attempt event")
		}
	}
	publisher = StorePublisher{Store: observed}
	if err := publisher.PublishRefusal(ctx, plan, root, "run-refused"); err != nil ||
		events != 1 || observed.calls != 1 {
		t.Fatalf("observed refusal = %d events, %d calls, %v", events, observed.calls, err)
	}

	// The exact disposition and run binding are checked before native work.
	reservation := plan.Expected[0].Reservation
	successResult, err := candidate.BuildPartitionResult(plan, 0, candidate.PartitionResultSpec{
		Disposition: candidate.PartitionResultSuccess,
		Totals: candidate.DomainResultTotals{
			Facts: min(1, reservation.Facts), Rows: min(2, reservation.Rows),
			References: min(1, reservation.References),
		},
		ContentDigest: "sha256:" + strings.Repeat("f", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	successRoot, err := candidate.BuildDomainResultRoot(plan, []candidate.PartitionResult{successResult})
	if err != nil {
		t.Fatal(err)
	}
	invalid := &observedPublicationStore{}
	for _, test := range []struct {
		name  string
		root  candidate.DomainResultRoot
		run   string
		store store.PartitionedEvidenceStore
	}{
		{name: "non-refusal disposition", root: successRoot, run: "run-refused", store: invalid},
		{name: "empty run", root: root, run: "", store: invalid},
		{name: "nil store", root: root, run: "run-refused", store: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			publisher := StorePublisher{Store: test.store}
			if err := publisher.PublishRefusal(t.Context(), plan, test.root, test.run); err == nil {
				t.Fatal("invalid refusal authority was accepted")
			}
		})
	}
	if invalid.calls != 0 {
		t.Fatalf("invalid refusals reached the store %d times", invalid.calls)
	}

	nativeErr := errors.New("native refusal store failure")
	failing := &observedPublicationStore{err: nativeErr}
	if err := (StorePublisher{Store: failing}).PublishRefusal(
		t.Context(), plan, root, "run-refused",
	); err != nativeErr {
		t.Fatalf("refusal error identity = %v, want %v", err, nativeErr)
	}
}

// TestRuntimeStorePublisherRefusalRoutesObserveOnce proves the runtime's
// refusal publication and its restart/reuse re-settlement both reach the real
// StorePublisher route under the existing authority fence exactly once each.
func TestRuntimeStorePublisherRefusalRoutesObserveOnce(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("4", 64), true)
	runtime, state, _, _, _, fence, domain := newRuntimeFixture(t, plan)
	events, settled := 0, 0
	ctx, err := readaccounting.WithPublicationObserver(t.Context(), func() error {
		if !fence.active() {
			t.Fatal("refusal observation lost the existing authority fence")
		}
		events++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	storeState := &observedPublicationStore{}
	runtime.Publisher = StorePublisher{Store: storeState}
	runtime.OnSettled = func(context.Context, string) error { settled++; return nil }
	runtime.Executor = terminalExecutor{}
	if _, err := runtime.Reconcile(ctx, plan.Repository, []DomainPlan{domain}); err != nil {
		t.Fatal(err)
	}
	chunk := currentChunk(t, state, plan.Repository, 0)
	chunk.Identity = "sha256:" + strings.Repeat("c", 64)
	if err := runtime.Handle(ctx, chunk); !store.IsTerminal(err) {
		t.Fatalf("refusing partition = %v", err)
	}
	if events != 1 || storeState.calls != 1 || settled != 1 {
		t.Fatal("refusal publishRoot route", events, storeState.calls, settled, err)
	}
	if _, err := runtime.Reconcile(ctx, plan.Repository, []DomainPlan{domain}); err != nil {
		t.Fatal(err)
	}
	if events != 2 || storeState.calls != 2 || settled != 2 {
		t.Fatal("refusal reuse route", events, storeState.calls, settled, err)
	}
	if fence.active() {
		t.Fatal("refusal route retained its authority fence")
	}
}
