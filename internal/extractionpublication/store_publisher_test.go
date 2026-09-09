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
