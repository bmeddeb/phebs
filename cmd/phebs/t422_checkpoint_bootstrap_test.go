//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

const t422CheckpointBootstrapMode = "PHEBS_T422_CHECKPOINT_BOOTSTRAP_TEST"

func t422CheckpointBootstrapRecord(t *testing.T) (dispatchadmission.ProductionBootstrap, []byte) {
	t.Helper()
	record, raw := t422StaleBootstrapRecord(t)
	record.Limits.Phases = 3
	record.Control.Phases, record.Control.MaximumPhases, record.Control.TerminalPhase = []uint32{6, 7, 8}, 3, 8
	record.Control.MaximumWireBytes = 4096
	return record, raw
}

// Inherited DA/PC/SA with a real scheduler-owned capability. Native schedule,
// prepared target and R values are supplied fixtures, not a durable checkpoint,
// exact HTTP read, SDK-quiescence, owned-kill or recovery proof.
func TestT422CheckpointInheritedHeldClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	record, _ := t422CheckpointBootstrapRecord(t)
	configuration := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}}
	for _, id := range record.Control.Phases {
		configuration.Phases = append(configuration.Phases, dispatchadmission.Phase{ID: id, Roles: []dispatchadmission.RoleBudget{
			{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}}})
	}
	controller, err := dispatchadmission.New(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	sa, err := storeaccounting.New(ctx, storeaccounting.Config{Producers: []storeaccounting.Producer{{ID: 4, Calls: 40, Transactions: 2}},
		Phases: []storeaccounting.Phase{{ID: 6, Transactions: 1, Rows: 1}, {ID: 7, Transactions: 1, Rows: 1}, {ID: 8, Transactions: 1, Rows: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, sa, storeaccounting.WireConfig{Producers: []storeaccounting.WireProducer{{ID: 4, Binding: [32]byte{9}, Phases: 224}}, AckTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	storeChild, storeConfig, err := transport.Open(4)
	if err != nil {
		t.Fatal(err)
	}
	record.Store = &storeConfig
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(); _ = child.Close(); _ = storeChild.Close() }()
	controlParent, controlChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controlParent.Close(); _ = controlChild.Close() }()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422CheckpointBootstrapHelper$")
	command.Env = []string{t422CheckpointBootstrapMode + "=1", dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionStoreSelector, "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles, command.WaitDelay = []*os.File{child, controlChild, storeChild}, time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	_ = child.Close()
	_ = controlChild.Close()
	_ = storeChild.Close()
	if err := dispatchadmission.SendProductionBootstrap(ctx, parent, controlParent, record); err != nil {
		t.Fatal(err, diagnostic.String())
	}
	served := make(chan error, 1)
	go func() { served <- controller.Serve(ctx, record.Producer.ID, command.Process.Pid, parent) }()
	defer func() { cancel(); <-served }()
	phase, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = phase.Close() }()
	scanner := bufio.NewScanner(output)
	read := func(want string) {
		t.Helper()
		if !scanner.Scan() || scanner.Text() != want {
			t.Fatal("helper", scanner.Text(), diagnostic.String())
		}
	}
	read("ready")
	for range 2 {
		for _, operation := range []func() error{
			func() error { return phase.DrainOwners(ctx) }, func() error { return phase.Pause(ctx) }, controller.Fence, transport.Fence,
			func() error { return phase.Checkpoint(ctx) }, controller.Advance, transport.Advance,
			func() error { return phase.Resume(ctx) }, func() error { return phase.ReopenOwners(ctx) },
		} {
			if err := operation(); err != nil {
				t.Fatal(err, diagnostic.String())
			}
		}
	}
	if _, err := input.Write([]byte{'g'}); err != nil {
		t.Fatal(err)
	}
	read("reported_and_parked")
	if err := phase.TerminalQuiesce(ctx); err != nil {
		t.Fatal(err, diagnostic.String())
	}
	if _, err := input.Write([]byte{'q'}); err != nil {
		t.Fatal(err)
	}
	read("quiescent_and_parked")
	if _, err := input.Write([]byte{'c'}); err != nil {
		t.Fatal(err)
	}
	read("canceled_prefix_retained")
	if err := command.Wait(); err != nil {
		t.Fatal(err, diagnostic.String())
	}
	// Cancellation is intentionally not an ordinary Close/EOF or phase pass.
}

type t422CheckpointSchedulerStore struct {
	store.GenerationSchedulerStore // Unexpected mutations panic this fixture.
	chunk                          store.GenerationChunk
	claimed                        atomic.Bool
	beats                          atomic.Int32
}

func (state *t422CheckpointSchedulerStore) ExpandNextGenerationSchedule(context.Context, store.GenerationResourceClass) (*store.GenerationSchedule, error) {
	return nil, nil
}
func (state *t422CheckpointSchedulerStore) ReapStaleGenerationChunks(context.Context, store.GenerationResourceClass, time.Duration) (int, error) {
	return 0, nil
}
func (state *t422CheckpointSchedulerStore) ClaimGenerationChunk(_ context.Context, _ store.GenerationResourceClass, worker string) (*store.GenerationChunk, error) {
	if !state.claimed.CompareAndSwap(false, true) {
		return nil, nil
	}
	chunk := state.chunk
	chunk.ClaimedBy = worker
	return &chunk, nil
}
func (state *t422CheckpointSchedulerStore) HeartbeatGenerationChunk(context.Context, store.GenerationChunk) error {
	state.beats.Add(1)
	return nil
}

func TestT422CheckpointBootstrapHelper(t *testing.T) {
	if os.Getenv(t422CheckpointBootstrapMode) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	_, raw := t422CheckpointBootstrapRecord(t)
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		t.Fatal(err)
	}
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var failures atomic.Int32
	launch.fail = func(error) { failures.Add(1) }
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 4, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("owners", err)
	}
	if owner, err := lifetime.TakeStoreOwner(); owner == nil || err != nil {
		t.Fatal("store owner", err)
	}
	reconciler := &extractionpublication.Reconciler{StoreAccounting: true, Runtime: &extractionpublication.Runtime{Fence: &extractionpublication.AuthorityFence{}}}
	stale, err := newT422StaleControl(ctx, launch, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.cancel()
	control, err := newT422CheckpointControl(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	defer control.cancel()
	if err := dispatchadmission.BindProductionTerminalQuiescence(control.quiesce); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	var signal [1]byte
	read := func(want byte) {
		t.Helper()
		if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil || signal[0] != want {
			t.Fatal("parent signal", err)
		}
	}
	read('g')
	_, event, target := t422CheckpointTestIdentities()
	event.Repository, target.Schedule.Repository = launch.request.Repository, launch.request.Repository
	control.armed, control.target, control.phaseEnd = true, target, time.Now().Add(10*time.Second)
	state := &t422CheckpointSchedulerStore{chunk: store.GenerationChunk{Identity: event.ChunkIdentity, Repository: event.Repository, Stage: event.Stage,
		Generation: event.Generation, ScheduleDigest: event.ScheduleDigest, ResourceClass: event.ResourceClass, Offset: event.Offset,
		Length: 1, Status: store.GenerationChunkRunning, LeaseToken: "actual-fixture-claim"}}
	var started, settled atomic.Int32
	scheduler := &generationscheduler.Scheduler{Store: state, Owners: owners, PollEvery: time.Hour, HeartbeatEvery: time.Hour,
		ChunkReports: func(raw []byte) error {
			var report generationscheduler.ChunkLifecycleReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return err
			}
			if report.Event == "started" {
				started.Add(1)
			} else {
				settled.Add(1)
			}
			return nil
		},
		ChunkReportFailure: func(error) { failures.Add(1) },
		Classes: map[store.GenerationResourceClass]generationscheduler.Class{store.GenerationResourceExtraction: {
			Concurrency: 1, TerminalHeartbeat: true, Budget: generationscheduler.Budget{MaxMemoryBytes: 1024, MaxDescriptors: 1},
			Handle: func(ctx context.Context, _ store.GenerationChunk, _ generationscheduler.Budget) error {
				return control.checkpoint(ctx, event)
			},
		}}}
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case <-control.hit.ready:
	case err := <-done:
		t.Fatal("scheduler exited", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	control.mu.Lock()
	control.hit.reading = true
	control.mu.Unlock()
	if err := control.finishHit(extractionpublication.CheckpointRestartTransition{Point: event.Point, PrivateLeaseTokenDigest: event.PrivateLeaseTokenDigest, CheckpointStateDigest: t422CheckpointTestDigest}); err != nil {
		t.Fatal(err)
	}
	fmt.Println("reported_and_parked")
	read('q')
	control.mu.Lock()
	valid := control.parked && control.terminal && control.hit.reported && control.err == nil && control.hit.observer.Err() == nil
	control.mu.Unlock()
	if !valid || started.Load() != 1 || settled.Load() != 0 || failures.Load() != 0 {
		t.Fatal("terminal checkpoint released or settled")
	}
	if _, err := owners.EnterRequest(ctx); !errors.Is(err, dispatchadmission.ErrFenced) {
		t.Fatal("request reopened", err)
	}
	fmt.Println("quiescent_and_parked")
	read('c')
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if settled.Load() != 0 || failures.Load() == 0 {
		t.Fatal("canceled held prefix manufactured settlement")
	}
	fmt.Println("canceled_prefix_retained")
}
