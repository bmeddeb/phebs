package lifecycle

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestObservedCapacityUsesPersistentLatch(t *testing.T) {
	gate := NewGateWithProbe("ancestor", func(context.Context, string) (Capacity, error) {
		t.Fatal("observed destination was replaced by an ancestor probe")
		return Capacity{}, nil
	})
	for _, step := range []struct {
		used   int64
		refuse bool
	}{{950, true}, {850, true}, {750, true}, {740, false}, {850, false}} {
		_, err := gate.CheckObserved(t.Context(), Capacity{TotalBytes: 1000, AvailableBytes: 1000 - step.used, UsedBytes: step.used}, 0)
		if errors.Is(err, ErrPressureRefusal) != step.refuse || !step.refuse && err != nil {
			t.Fatalf("used %d: %v", step.used, err)
		}
	}
}

func TestObservedCapacityRefusesInvalidObservations(t *testing.T) {
	gate := NewGate("unused")
	for _, observation := range []Capacity{
		{}, {TotalBytes: -1}, {TotalBytes: 100, AvailableBytes: -1},
		{TotalBytes: 100, AvailableBytes: 101}, {TotalBytes: 100, AvailableBytes: 50, UsedBytes: 49},
		{TotalBytes: math.MaxInt64, UsedBytes: math.MaxInt64},
	} {
		if _, err := gate.CheckObserved(t.Context(), observation, 1); err == nil {
			t.Fatalf("accepted %+v", observation)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := gate.CheckObserved(ctx, Capacity{TotalBytes: 100, AvailableBytes: 100}, 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
