package extractionpublication

import (
	"context"
	"errors"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"os"
	"strings"
	"testing"
)

func TestRuntimePublicationFailureStages(t *testing.T) {
	for _, mode := range []string{"publisher", "pointer", "settlement", "fence", "success", "panicking_observer"} {
		t.Run(mode, func(t *testing.T) {
			plan := buildTestPlan(t, "sha256:"+strings.Repeat("1", 64), true)
			runtime, state, source, executor, publisher, fence, domain := newRuntimeFixture(t, plan)
			if _, err := runtime.Reconcile(t.Context(), plan.Repository, []DomainPlan{domain}); err != nil {
				t.Fatal(err)
			}
			sentinel := errors.New("private settlement payload")
			settled := 0
			runtime.OnSettled = func(context.Context, string) error {
				settled++
				if mode == "settlement" {
					return sentinel
				}
				return nil
			}
			want := readaccounting.PublicationFailurePublisher
			switch mode {
			case "publisher", "panicking_observer":
				publisher.failures = 1
			case "pointer":
				want = readaccounting.PublicationFailurePointer
				if err := os.Mkdir(runtime.currentPath(plan.Repository, plan.Domain), 0700); err != nil {
					t.Fatal(err)
				}
			case "settlement":
				want = readaccounting.PublicationFailureSettlement
			case "fence":
				want = readaccounting.PublicationFailureFence
				fence.stale = true
			}
			var stages []readaccounting.PublicationFailureStage
			var firstErr error
			ctx := readaccounting.WithPublicationFailureObserver(t.Context(), func(stage readaccounting.PublicationFailureStage, err error, _ readaccounting.PublicationFailureFlags) {
				if firstErr == nil {
					firstErr = err
				}
				stages = append(stages, stage)
				if mode == "panicking_observer" {
					panic("diagnostic sink")
				}
			})
			err := runtime.Handle(ctx, currentChunk(t, state, plan.Repository, 0))
			if mode == "success" {
				if err != nil || len(stages) != 0 || settled != 1 {
					t.Fatal("healthy behavior changed", err, stages)
				}
				return
			}
			if err == nil {
				t.Fatal("diagnostic changed operational refusal")
			}
			if len(stages) == 0 {
				t.Fatal("missing publication failure diagnostic")
			}
			if stages[0] != want {
				t.Fatal("wrong first publication stage", stages, want)
			}
			if mode == "settlement" && err != sentinel {
				t.Fatal("settlement error identity changed")
			}
			if mode != "fence" && err != firstErr {
				t.Fatal("original error identity changed")
			}
			if mode != "settlement" && settled != 0 {
				t.Fatal("failure ran downstream settlement")
			}
			if acquired, released := source.counts(); acquired != 1 || released != 1 || executor.callCount() != 1 {
				t.Fatal("diagnostic repeated content")
			}
			if fence.active() {
				t.Fatal("publication fence remained held after return")
			}
		})
	}
}
func TestStorePublisherFailureStages(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("2", 64), false)
	root, err := candidate.BuildDomainResultRoot(plan, []candidate.PartitionResult{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"authority", "observer", "publisher", "success"} {
		t.Run(mode, func(t *testing.T) {
			state := &observedPublicationStore{}
			sentinel := errors.New("private native result")
			want := readaccounting.PublicationFailureAuthority
			run := "run"
			ctx := t.Context()
			switch mode {
			case "authority":
				run = ""
			case "observer":
				want = readaccounting.PublicationFailureObservation
				var err error
				ctx, err = readaccounting.WithPublicationObserver(ctx, func() error { return sentinel })
				if err != nil {
					t.Fatal(err)
				}
			case "publisher":
				want = readaccounting.PublicationFailurePublisher
				state.err = sentinel
			}
			var stages []readaccounting.PublicationFailureStage
			ctx = readaccounting.WithPublicationFailureObserver(ctx, func(stage readaccounting.PublicationFailureStage, _ error, _ readaccounting.PublicationFailureFlags) {
				stages = append(stages, stage)
			})
			err := (StorePublisher{Store: state}).PublishDomain(ctx, plan, root, run)
			if mode == "success" {
				if err != nil || len(stages) != 0 || state.calls != 1 {
					t.Fatal("healthy publisher changed")
				}
				return
			}
			if err == nil || len(stages) != 1 || stages[0] != want {
				t.Fatal("missing publisher failure diagnostic", stages, err)
			}
			if (mode == "publisher" || mode == "observer") && err != sentinel {
				t.Fatal("publisher error identity changed")
			}
			if (mode == "authority" || mode == "observer") && state.calls != 0 {
				t.Fatal("refusal reached store")
			}
		})
	}
}
