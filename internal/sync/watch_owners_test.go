package sync_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type ownerWatchStore struct {
	store.Store
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (state *ownerWatchStore) EnqueuePending(ctx context.Context, kind store.JobKind, target string, force bool) (*store.Job, error) {
	if state.calls.Add(1) == 1 {
		close(state.entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-state.release:
		}
	}
	return &store.Job{Kind: kind, Target: target, Force: force, Status: store.StatusPending}, nil
}

func TestWatcherOwnersDrainEnqueueAndPreserveBaseline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	gitc(t, origin, "commit", "--allow-empty", "-m", "one")
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	state := &ownerWatchStore{entered: make(chan struct{}), release: make(chan struct{})}
	watcher := &phebssync.Watcher{Store: state, Owners: owners, Interval: 5 * time.Millisecond,
		Conns: []config.Connection{{Name: "owner", Type: "git", URL: origin, Watch: true}}}
	done := make(chan struct{})
	go func() { defer close(done); watcher.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-state.entered:
	case <-ctx.Done():
		t.Fatal("watcher enqueue did not arrive")
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
		t.Fatalf("pause skipped enqueue/baseline tail: %v", err)
	default:
	}
	close(state.release)
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("watcher turn did not drain")
	}
	if err := owners.Resume(); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(30 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("watcher context expired")
	}
	if err := owners.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if state.calls.Load() != 1 {
		t.Fatal("resume discarded the same watcher's unchanged baseline")
	}
	gitc(t, origin, "commit", "--allow-empty", "-m", "two")
	if err := owners.Resume(); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for state.calls.Load() != 2 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("resumed watcher did not enqueue moved HEAD")
		}
	}
}

func TestResyncOwnersDrainAllConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	state := &ownerWatchStore{entered: make(chan struct{}), release: make(chan struct{})}
	cfg := &config.Config{Connections: []config.Connection{
		{Name: "first", Type: "github"}, {Name: "second", Type: "gitlab"},
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		phebssync.ResyncWithOwners(ctx, state, cfg, 5*time.Millisecond, owners)
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-state.entered:
	case <-ctx.Done():
		t.Fatal("resync enqueue did not arrive")
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
		t.Fatalf("pause skipped a resync enqueue: %v", err)
	default:
	}
	close(state.release)
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("resync turn did not drain")
	}
	if state.calls.Load() != 2 {
		t.Fatal("pause did not cover the whole connection-enqueue turn")
	}
	if err := owners.Resume(); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for state.calls.Load() < 4 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("resumed resync did not enqueue its next complete turn")
		}
	}
}
