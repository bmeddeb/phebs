package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

type ownerMaintenanceStore struct {
	store.EvidenceStore
	calls   atomic.Int32
	entered [2]chan struct{}
	release [2]chan struct{}
}

func (state *ownerMaintenanceStore) step(ctx context.Context) (int, error) {
	index := state.calls.Add(1) - 1
	if index >= 2 {
		return 0, nil
	}
	close(state.entered[index])
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-state.release[index]:
		if index == 0 {
			return 1, nil
		}
		return 0, nil
	}
}

func (state *ownerMaintenanceStore) SweepProofBundles(ctx context.Context, _ time.Time) (int, error) {
	return state.step(ctx)
}

func (state *ownerMaintenanceStore) SweepEvidence(ctx context.Context, _ time.Time, _ time.Duration) (store.EvidenceSweepProgress, error) {
	count, err := state.step(ctx)
	return store.EvidenceSweepProgress{RunsDeleted: count}, err
}

func TestOwnerMaintenanceIncludesWholePassAndPreservesIdle(t *testing.T) {
	for _, name := range []string{"proof", "evidence"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
			if err != nil {
				t.Fatal(err)
			}
			state := &ownerMaintenanceStore{}
			for index := range 2 {
				state.entered[index] = make(chan struct{})
				state.release[index] = make(chan struct{})
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if name == "proof" {
					runProofBundleMaintenanceWithOwners(ctx, state, 24*time.Hour, time.Hour, time.Millisecond, owners)
				} else {
					runEvidenceMaintenanceWithOwners(ctx, state, time.Hour, time.Millisecond, 24*time.Hour, owners)
				}
			}()
			t.Cleanup(func() { cancel(); <-done })
			select {
			case <-state.entered[0]:
			case <-ctx.Done():
				t.Fatal("maintenance pass did not begin")
			}
			paused := make(chan error, 1)
			go func() { paused <- owners.Pause(ctx) }()
			// A spare slot proves Pause has closed new entries without needing
			// a production introspection or test-only barrier hook.
			for {
				probeCtx, stop := context.WithTimeout(ctx, time.Millisecond)
				probe, err := owners.Enter(probeCtx)
				stop()
				if err != nil {
					break
				}
				probe.End()
			}
			close(state.release[0])
			select {
			case <-state.entered[1]:
			case <-ctx.Done():
				t.Fatal("paused owner could not finish the same multi-step pass")
			}
			select {
			case err := <-paused:
				t.Fatalf("pause skipped the pass tail: %v", err)
			default:
			}
			close(state.release[1])
			select {
			case err := <-paused:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("maintenance pass did not drain")
			}
			if err := owners.Resume(); err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(20 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				t.Fatal("maintenance context expired")
			}
			if state.calls.Load() != 2 {
				t.Fatal("resume reset the maintenance loop's existing idle cadence")
			}
		})
	}
}
