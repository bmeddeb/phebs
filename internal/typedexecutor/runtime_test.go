package typedexecutor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedRuntimeClosedConstruction(t *testing.T) {
	lookup := func(context.Context, typedindex.Admission) (string, []byte, error) {
		t.Fatal("unready lookup")
		return "", nil, nil
	}
	if _, err := NewRuntime(nil, lookup); err == nil {
		t.Fatal("nil controller")
	}
	c := &Controller{config: Config{Socket: "fixed", Image: "fixed", Acquire: func(context.Context) (func(), error) { return func() {}, nil }}, serial: make(chan struct{}, 1)}
	if _, err := NewRuntime(c, nil); err == nil {
		t.Fatal("nil lookup")
	}
	r, err := NewRuntime(c, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Scheduler(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unready scheduler", err)
	}
	if err = r.Class().Handle(t.Context(), store.GenerationChunk{}, generationscheduler.TypedIndexBudget()); !store.IsDeferral(err) {
		t.Fatal("unready work lost", err)
	}
	if err = r.Coordinator(t.Context(), store.Job{Kind: store.JobSync}); !store.IsTerminal(err) {
		t.Fatal("wrong coordinator kind", err)
	}
	c.ready = true
	scheduler, err := r.Scheduler(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if scheduler.HeartbeatEvery != 5*time.Second || scheduler.StaleAfter != 20*time.Second || scheduler.Classes[store.GenerationResourceTypedIndex].Concurrency != 1 {
		t.Fatal("scheduler bounds")
	}
	if scheduler.Classes[store.GenerationResourceTypedIndex].Budget != generationscheduler.TypedIndexBudget() {
		t.Fatal("native budget changed")
	}
	r.stale = r.heartbeat
	if err = r.Reconcile(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("invalid stale threshold", err)
	}
}
