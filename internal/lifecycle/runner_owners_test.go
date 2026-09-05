package lifecycle

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestLifecycleOwnersDrainCapacityTailAndPreserveCycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	cursors := newMemoryCursorStore()
	controller, err := NewController(cursors,
		recordingOwner{name: "a", calls: &calls}, recordingOwner{name: "b", calls: &calls})
	if err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	gate := NewGateWithProbe(t.TempDir(), func(context.Context, string) (Capacity, error) {
		probes.Add(1)
		return Capacity{TotalBytes: 1_000, AvailableBytes: 500, UsedBytes: 500}, nil
	})
	firstCapacity := make(chan struct{})
	finishCapacity := make(chan struct{})
	secondCapacity := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunWithOwners(ctx, controller, gate, time.Hour, time.Millisecond,
			func(OwnerResult) {}, func(Capacity, error) {
				switch probes.Load() {
				case 1:
					close(firstCapacity)
					select {
					case <-ctx.Done():
					case <-finishCapacity:
					}
				case 2:
					close(secondCapacity)
				}
			}, owners)
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-firstCapacity:
	case <-ctx.Done():
		t.Fatal("first lifecycle capacity did not arrive")
	}
	paused := make(chan error, 1)
	go func() { paused <- owners.Pause(ctx) }()
	for {
		probeCtx, stop := context.WithTimeout(ctx, time.Millisecond)
		probe, err := owners.Enter(probeCtx)
		stop()
		if err != nil {
			break
		}
		probe.End()
	}
	select {
	case err := <-paused:
		t.Fatalf("pause skipped post-owner capacity report: %v", err)
	default:
	}
	close(finishCapacity)
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("lifecycle turn did not drain")
	}
	if rotation, revision, err := cursors.GetLifecycleCursor(ctx, rotationCursorKey); err != nil || rotation != "a" || revision != 1 {
		t.Fatalf("durable rotation = %q/%d, %v", rotation, revision, err)
	}
	if probes.Load() != 1 {
		t.Fatal("capacity probe crossed parked lifecycle")
	}
	if err := owners.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondCapacity:
	case <-ctx.Done():
		t.Fatal("same lifecycle loop did not resume")
	}
	// Preserved cycleStarted makes b finish the observed cycle and select the
	// original one-hour idle, rather than restarting a fresh a/b cycle.
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("lifecycle test context ended")
	case <-timer.C:
	}
	cancel()
	<-done
	if !slices.Equal(calls, []string{"a:", "b:"}) || probes.Load() != 2 {
		t.Fatalf("cycle after resume = %v, probes %d", calls, probes.Load())
	}
}
