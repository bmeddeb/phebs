package store

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

type ownerRunnerStore struct {
	Store
	claims         atomic.Int32
	heartbeats     atomic.Int32
	terminal       chan struct{}
	finishTerminal chan struct{}
}

func (*ownerRunnerStore) ReapStale(context.Context, JobKind, time.Duration, int) (int, error) {
	return 0, nil
}

func (state *ownerRunnerStore) ClaimJob(context.Context, JobKind, string) (*Job, error) {
	if state.claims.Add(1) != 1 {
		return nil, ErrNotFound
	}
	return &Job{ID: "owner-test", Kind: JobSync, Target: "local/owner-test", Status: StatusClaimed}, nil
}

func (state *ownerRunnerStore) HeartbeatJob(context.Context, Job) error {
	state.heartbeats.Add(1)
	return nil
}

func (state *ownerRunnerStore) SetJobStatus(ctx context.Context, _ Job, status JobStatus, _ string) error {
	if status != StatusDone {
		return nil
	}
	close(state.terminal)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-state.finishTerminal:
		return nil
	}
}

func TestRunnerOwnersIncludeSecondChildPersistenceAndReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 2, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	state := &ownerRunnerStore{terminal: make(chan struct{}), finishTerminal: make(chan struct{})}
	firstChild := make(chan struct{})
	secondAllowed := make(chan struct{})
	reporting := make(chan struct{})
	finishReport := make(chan struct{})
	runner := &Runner{Store: state, Kind: JobSync, Owners: owners,
		Interval: time.Millisecond, HeartbeatEvery: time.Millisecond,
		Handle: func(ctx context.Context, _ Job) error {
			for index := range 2 {
				command := exec.CommandContext(ctx, "/usr/bin/true")
				handle, err := (*dispatchadmission.Client)(nil).Start(ctx, 1, command)
				if err != nil {
					return err
				}
				if err := handle.Wait(); err != nil {
					return err
				}
				if index == 0 {
					close(firstChild)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-secondAllowed:
					}
				}
			}
			return nil
		},
		LifecycleReports: func(raw []byte) error {
			var report JobLifecycleReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return err
			}
			if report.Event == "done" {
				close(reporting)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-finishReport:
				}
			}
			return nil
		},
	}
	done := make(chan struct{})
	go func() { defer close(done); runner.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	runnerOwnerSignal(t, ctx, firstChild)
	paused := make(chan error, 1)
	go func() { paused <- owners.Pause(ctx) }()
	// One spare slot lets the test observe that new turns actually park,
	// without assuming scheduling order or adding a production test hook.
	for {
		probeCtx, stop := context.WithTimeout(ctx, time.Millisecond)
		probe, err := owners.Enter(probeCtx)
		stop()
		if err != nil {
			break
		}
		probe.End()
	}
	beats := state.heartbeats.Load()
	close(secondAllowed)
	runnerOwnerSignal(t, ctx, state.terminal)
	select {
	case err := <-paused:
		t.Fatalf("pause preceded terminal write: %v", err)
	default:
	}
	close(state.finishTerminal)
	runnerOwnerSignal(t, ctx, reporting)
	if state.heartbeats.Load() < beats {
		t.Fatal("heartbeat ownership was reset")
	}
	select {
	case err := <-paused:
		t.Fatalf("pause preceded exact report: %v", err)
	default:
	}
	close(finishReport)
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("whole runner turn did not drain")
	}
	if state.claims.Load() != 1 {
		t.Fatal("new durable claim crossed owner fence")
	}
	if err := owners.Resume(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return state.claims.Load() > 1 }, "runner did not resume")
}

func runnerOwnerSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal("runner owner boundary did not arrive")
	}
}
