//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/bmeddeb/phebs/internal/store"
)

const t422StaleBootstrapMode = "PHEBS_T422_STALE_BOOTSTRAP_TEST"

func t422StaleBootstrapRecord(t *testing.T) (dispatchadmission.ProductionBootstrap, []byte) {
	t.Helper()
	raw, _ := t422SemanticTestRequest(t)
	raw = bytes.Replace(raw, []byte(`"server_epoch":1`), []byte(`"server_epoch":3`), 1)
	record := t422ServeFlagsRecord()
	record.SemanticMode, record.InputSHA256 = dispatchadmission.ProductionSemanticV3, sha256.Sum256(raw)
	record.Producer.ID, record.Phase, record.Limits.Phases = 4, 6, 2
	record.Control.OwnerControl = true
	record.Control.Phases, record.Control.InitialPhase, record.Control.MaximumPhases = []uint32{6, 7}, 6, 2
	return record, raw
}

// Actual inherited DA/PC and owner lifetimes; the prepared target and native
// transition events are deliberately supplied protocol fixtures. This test
// neither executes PrepareCurrentRecovery/native R nor proves actual reaping.
func TestT422StaleInheritedWorkerOrdering(t *testing.T) {
	for _, mode := range []string{"complete", "report-failure", "observer-cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			record, _ := t422StaleBootstrapRecord(t)
			configuration := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}}
			for _, phase := range record.Control.Phases {
				configuration.Phases = append(configuration.Phases, dispatchadmission.Phase{ID: phase, Roles: []dispatchadmission.RoleBudget{
					{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility},
				}})
			}
			controller, err := dispatchadmission.New(ctx, configuration)
			if err != nil {
				t.Fatal(err)
			}
			parent, child, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(); _ = child.Close() }()
			controlParent, controlChild, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = controlParent.Close(); _ = controlChild.Close() }()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422StaleBootstrapHelper$")
			command.Env = []string{t422StaleBootstrapMode + "=" + mode, dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles, command.WaitDelay = []*os.File{child, controlChild}, time.Second
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
			if err := dispatchadmission.SendProductionBootstrap(ctx, parent, controlParent, record); err != nil {
				t.Fatal(err)
			}
			served := make(chan error, 1)
			go func() { served <- controller.Serve(ctx, record.Producer.ID, command.Process.Pid, parent) }()
			defer func() {
				cancel()
				select {
				case <-served:
				case <-time.After(3 * time.Second):
					t.Error("receiver not joined")
				}
			}()
			phase, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = phase.Close() }()
			scanner := bufio.NewScanner(output)
			read := func(want string) {
				t.Helper()
				if !scanner.Scan() || scanner.Text() != want {
					t.Fatal("helper response", scanner.Text(), diagnostic.String())
				}
			}
			read("ready")
			for _, operation := range []func() error{func() error { return phase.DrainOwners(ctx) }, func() error { return phase.Pause(ctx) },
				controller.Fence, func() error { return phase.Checkpoint(ctx) }, controller.Advance, func() error { return phase.Resume(ctx) }, func() error { return phase.ReopenOwners(ctx) }} {
				if err := operation(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := input.Write([]byte{'g'}); err != nil {
				t.Fatal(err)
			}
			read("workers_joined")
			for _, operation := range []func() error{func() error { return phase.DrainOwners(ctx) }, func() error { return phase.Pause(ctx) },
				controller.Fence, func() error { return phase.Checkpoint(ctx) }} {
				if err := operation(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := input.Write([]byte{'c'}); err != nil {
				t.Fatal(err)
			}
			read("closed")
			if err := command.Wait(); err != nil {
				t.Fatal(err, diagnostic.String())
			}
			if snapshot, err := controller.Snapshot(); err != nil || !snapshot.Complete || snapshot.Attempts != 0 {
				t.Fatal("protocol fixture did not close exact empty dispatch", snapshot, err)
			}
		})
	}
}

func TestT422StaleBootstrapHelper(t *testing.T) {
	mode := os.Getenv(t422StaleBootstrapMode)
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	_, raw := t422StaleBootstrapRecord(t)
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
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 3, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("owner bootstrap", err)
	}
	// Constructor wiring only. No method on this incomplete Runtime is used.
	reconciler := &extractionpublication.Reconciler{StoreAccounting: true, Runtime: &extractionpublication.Runtime{Fence: &extractionpublication.AuthorityFence{}}}
	control, err := newT422StaleControl(ctx, launch, reconciler)
	if err != nil || !reconciler.RecoveryPreparationEnabled {
		t.Fatal("selected constructor", err)
	}
	defer control.cancel()
	fmt.Println("ready")
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil || signal[0] != 'g' {
		t.Fatal(err)
	}
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	control.armed, control.phaseEnd = true, time.Now().Add(time.Second*10)
	control.target = extractionpublication.RecoveryPreparationTarget{Domain: "grpc-caller", Ordinal: 6, Offset: 6,
		Schedule: store.GenerationSchedule{Repository: launch.request.Repository, Generation: digest, Digest: digest}}
	chunk := store.GenerationChunk{Repository: launch.request.Repository, Stage: extractionpublication.ScheduleStage,
		ResourceClass: store.GenerationResourceExtraction, Generation: digest, ScheduleDigest: digest, Identity: digest,
		Offset: 6, Length: 1, Priority: store.GenerationPriorityNeverRun, Status: store.GenerationChunkRunning, LeaseToken: "old-lease"}
	oldDone := make(chan error, 1)
	go func() {
		owner, err := owners.Enter(ctx)
		if err != nil {
			oldDone <- err
			return
		}
		defer owner.End()
		oldDone <- control.beforeHeartbeat(ctx, chunk)
	}()
	// Test-only rendezvous for the internal fixture claim; production uses the
	// native reaper callback, never this loop or an eager reaper invocation.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		control.mu.Lock()
		captured := control.old.Identity != ""
		control.mu.Unlock()
		if captured {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("old claim absent")
		}
	}
	observer, stopObserver := context.WithTimeout(ctx, 5*time.Second)
	defer stopObserver()
	event := store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionHit, Repository: chunk.Repository,
		Stage: chunk.Stage, ResourceClass: chunk.ResourceClass, Generation: chunk.Generation, ScheduleDigest: chunk.ScheduleDigest,
		ChunkIdentity: chunk.Identity, Offset: chunk.Offset, Length: 1, Priority: chunk.Priority, ChunkStatus: chunk.Status,
		Leased: true, ScheduleStatus: store.GenerationScheduleActive, ChunkStateDigest: digest, StaleBefore: time.Now().Add(-20 * time.Second),
		PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest(chunk.LeaseToken)}
	observed := make(chan error, 1)
	go func() {
		owner, err := owners.Enter(ctx)
		if err != nil {
			observed <- err
			return
		}
		defer owner.End()
		observed <- control.transition(observer, event)
	}()
	select {
	case <-control.hit.ready:
	case <-ctx.Done():
		t.Fatal("hit absent")
	}
	select {
	case err := <-oldDone:
		t.Fatal("old claim released before requeue", err)
	default:
	}
	control.mu.Lock()
	control.hit.reading = true
	control.mu.Unlock()
	if mode == "report-failure" {
		_ = control.stop(errors.New("supplied report failure"))
	} else if mode == "observer-cancel" {
		stopObserver()
	} else if err := control.finishReport(&control.hit); err != nil {
		t.Fatal(err)
	}
	if err := <-observed; (err != nil) != (mode != "complete") {
		t.Fatal("observer result", err)
	}
	if mode == "complete" {
		select {
		case err := <-oldDone:
			t.Fatal("hit report alone released old claim", err)
		default:
		}
		event.Point, event.Priority, event.ChunkStatus, event.Leased = store.GenerationStaleLeaseTransitionRequeued, store.GenerationPriorityStale, store.GenerationChunkPending, false
		if err := control.transition(observer, event); err != nil {
			t.Fatal(err)
		}
		if err := <-oldDone; err != store.ErrGenerationLeaseLost {
			t.Fatal("old worker did not return exact stale fence", err)
		}
		chunk.Priority, chunk.LeaseToken = store.GenerationPriorityStale, "new-lease"
		recoveredOwner, err := owners.Enter(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := control.beforeHeartbeat(ctx, chunk); err != nil {
			t.Fatal("reclaimed worker refused", err)
		}
		recoveryCtx, stopRecovery := context.WithTimeout(ctx, 5*time.Second)
		defer stopRecovery()
		event.Point, event.ChunkStatus = store.GenerationStaleLeaseTransitionRecovered, store.GenerationChunkDone
		event.PrivateLeaseTokenDigest = store.GenerationLeaseTokenDigest(chunk.LeaseToken)
		go func() { observed <- control.transition(recoveryCtx, event) }()
		select {
		case <-control.recovered.ready:
		case <-ctx.Done():
			t.Fatal("recovered absent")
		}
		control.mu.Lock()
		control.recovered.reading = true
		control.mu.Unlock()
		if err := control.finishReport(&control.recovered); err != nil {
			t.Fatal(err)
		}
		if err := <-observed; err != nil {
			t.Fatal(err)
		}
		recoveredOwner.End()
		if failures.Load() != 0 {
			t.Fatal("legitimate ordering latched failure")
		}
	} else {
		if err := <-oldDone; err == nil || err == store.ErrGenerationLeaseLost {
			t.Fatal("failure fabricated old lease requeue", err)
		}
		if failures.Load() != 1 || control.hit.reported || control.requeueSeen || control.reclaimed {
			t.Fatal("failed prefix cleared or advanced")
		}
	}
	fmt.Println("workers_joined")
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil || signal[0] != 'c' {
		t.Fatal(err)
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("closed")
}
