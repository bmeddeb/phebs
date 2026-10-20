//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Gate mechanics only: these controls issue no process/image/resource authority.
func linuxEpochTraceGateControl(gate chan struct{}) *linuxEpochTraceRoot {
	return &linuxEpochTraceRoot{owner: &linuxEpochTraceOwner{admission: &linuxEpochResourceAdmission{probeGate: gate}}}
}

func TestLinuxEpochTraceQueuedGateOccupiedDeadline(t *testing.T) {
	gate := make(chan struct{}, 1)
	gate <- struct{}{} // A real earlier census owns the sole token.
	root := linuxEpochTraceGateControl(gate)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	err := root.acquireGate(ctx)
	if err != context.DeadlineExceeded || ctx.Err() != context.DeadlineExceeded {
		t.Fatal("busy acquisition did not queue until the actual context deadline", err, ctx.Err())
	}
	if root.gateOwned || len(gate) != 1 {
		t.Fatal("refused waiter took ownership or lost the earlier token", root.gateOwned, len(gate))
	}
	<-gate
	if err = root.acquireGate(t.Context()); err != nil {
		t.Fatal("later bounded cleanup could not acquire the released gate", err)
	}
	root.releaseGate()
	if len(gate) != 0 {
		t.Fatal("cleanup acquisition leaked a token")
	}
}

func TestLinuxEpochTraceQueuedGateContextAndOwnership(t *testing.T) {
	for _, test := range []struct {
		name string
		root *linuxEpochTraceRoot
	}{
		{"nil-root", nil},
		{"nil-owner", &linuxEpochTraceRoot{}},
		{"nil-admission", &linuxEpochTraceRoot{owner: &linuxEpochTraceOwner{}}},
		{"nil-gate", linuxEpochTraceGateControl(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.root.acquireGate(t.Context()); err != ErrExecutionEpochOne {
				t.Fatal("invalid owner accepted", err)
			}
		})
	}
	gate := make(chan struct{}, 1)
	root := linuxEpochTraceGateControl(gate)
	//nolint:staticcheck // Deliberately verify that a nil context fails closed.
	if err := root.acquireGate(nil); err != ErrExecutionEpochOne || len(gate) != 0 {
		t.Fatal("nil context acquired a token", err, len(gate))
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := root.acquireGate(canceled); err != context.Canceled || root.gateOwned || len(gate) != 0 {
		t.Fatal("canceled free-gate acquisition succeeded", err, root.gateOwned, len(gate))
	}
	if err := root.acquireGate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := root.acquireGate(t.Context()); err != nil || !root.gateOwned || len(gate) != 1 {
		t.Fatal("already owned token was acquired twice", err, root.gateOwned, len(gate))
	}
	if err := root.acquireGate(canceled); err != context.Canceled || !root.gateOwned || len(gate) != 1 {
		t.Fatal("cancellation released an older owned pending/exit token", err, root.gateOwned, len(gate))
	}
	root.releaseGate()
	if len(gate) != 0 || root.gateOwned {
		t.Fatal("actual owner release did not retire exactly one token")
	}
}

// The real context is canceled at the post-send Err check, exercising the exact
// window without global hooks or fabricated syscall/liveness results.
type linuxEpochTraceGateCancelAfterSend struct {
	context.Context
	cancel context.CancelFunc
	reads  int
}

func (ctx *linuxEpochTraceGateCancelAfterSend) Err() error {
	ctx.reads++
	if ctx.reads == 2 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestLinuxEpochTraceQueuedGateReturnsNewCanceledToken(t *testing.T) {
	gate := make(chan struct{}, 1)
	root := linuxEpochTraceGateControl(gate)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &linuxEpochTraceGateCancelAfterSend{Context: base, cancel: cancel}
	if err := root.acquireGate(ctx); err != context.Canceled || ctx.reads != 2 || base.Err() != context.Canceled || root.gateOwned || len(gate) != 0 {
		t.Fatal("post-acquisition cancellation did not return the new token", err, ctx.reads, base.Err(), root.gateOwned, len(gate))
	}
}

func TestLinuxEpochTraceQueuedGateActorAndSamplerHandoffs(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), epochProcessProbeTimeout)
	defer cancel()
	gate := make(chan struct{}, 1)
	gate <- struct{}{} // Start with an earlier real gate holder.
	entered := make(chan int, 3)
	results := make(chan error, 3)
	ready := make(chan struct{}, 3)
	releases := [3]chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var workers sync.WaitGroup
	var active atomic.Int32
	for id := range 3 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			root := linuxEpochTraceGateControl(gate)
			ready <- struct{}{}
			var err error
			if id < 2 {
				err = root.acquireGate(ctx) // The two allowed actor waiters.
			} else {
				// The existing sampler's real channel acquisition shape.
				select {
				case gate <- struct{}{}:
					root.gateOwned = true
				case <-ctx.Done():
					err = ctx.Err()
				}
			}
			if err != nil {
				results <- err
				return
			}
			if current := active.Add(1); current != 1 {
				err = fmt.Errorf("overlapping gate owners: %d", current)
			}
			entered <- id
			select {
			case <-releases[id]:
			case <-ctx.Done():
				err = errors.Join(err, ctx.Err())
			}
			active.Add(-1)
			root.releaseGate()
			results <- err
		}()
	}
	for range 3 {
		<-ready
	}
	<-gate
	var seen [3]bool
	for range 3 {
		select {
		case id := <-entered:
			if id < 0 || id >= 3 || seen[id] {
				t.Error("invalid handoff", id)
				cancel()
				continue
			}
			seen[id] = true
			close(releases[id])
		case <-ctx.Done():
			t.Error("bounded handoff progress unavailable", ctx.Err())
		}
	}
	// Join every test worker even after a failed progress assertion.
	for id := range releases {
		if !seen[id] {
			close(releases[id])
		}
	}
	workers.Wait()
	cancel()
	for range 3 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if active.Load() != 0 || len(gate) != 0 || !seen[0] || !seen[1] || !seen[2] {
		t.Fatal("handoff or owner retirement incomplete", active.Load(), len(gate), seen)
	}
}
