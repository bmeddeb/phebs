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
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422CheckpointRecoveryHelperMode = "PHEBS_T422_CHECKPOINT_RECOVERY_TEST"

func t422CheckpointRecoveryRecord(t *testing.T) (dispatchadmission.ProductionBootstrap, []byte) {
	input := t422CheckpointRecoveryFixture()
	raw, _ := t422CheckpointRecoveryEnvelope(t, &input)
	record := t422ServeFlagsRecord()
	record.Producer.ID, record.Phase = 5, 8
	record.SemanticMode, record.InputSHA256 = dispatchadmission.ProductionSemanticV3, sha256.Sum256(raw)
	record.Control.Phases, record.Control.InitialPhase, record.Control.OwnerControl = []uint32{8}, 8, true
	return record, raw
}

// Real inherited DA/PC owner lifetimes and actual control callbacks. Prepared
// controls, reaper/completion events and native R values are supplied fixtures,
// not a native requeue, hard kill, all-success schedule or phase-eight proof.
func TestT422CheckpointRecoveryInheritedCallbacks(t *testing.T) {
	for _, mode := range []string{"complete", "reader_start", "recovered_before_requeue", "canceled_before_requeue", "same_lease", "report_failure", "wrong_final"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			record, _ := t422CheckpointRecoveryRecord(t)
			controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer},
				Phases: []dispatchadmission.Phase{{ID: 8, Roles: []dispatchadmission.RoleBudget{{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}}}}})
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
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422CheckpointRecoveryBootstrapHelper$")
			command.Env = []string{t422CheckpointRecoveryHelperMode + "=" + mode, dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
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
			go func() { served <- controller.Serve(ctx, 5, command.Process.Pid, parent) }()
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
			read("callback_joined")
			for _, op := range []func() error{func() error { return phase.DrainOwners(ctx) }, func() error { return phase.OpenRequests(ctx) }} {
				if err := op(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := input.Write([]byte{'f'}); err != nil {
				t.Fatal(err)
			}
			read("final_checked")
			for _, op := range []func() error{func() error { return phase.FenceRequests(ctx) }, func() error { return phase.Pause(ctx) }, controller.Fence, func() error { return phase.Checkpoint(ctx) }} {
				if err := op(); err != nil {
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
		})
	}
}

// The real selected binding is inherited, but time and scheduler entry are
// controlled here: no claim or five-second observer starts before the actual
// R reader arrives. Native requeue/result data remain the separate gates.
func testT422CheckpointReaderStart(t *testing.T, launch *t422SemanticLaunch) {
	t.Helper()
	for _, mode := range []string{"delayed_reader", "canceled_startup", "phase_expired", "invalid_reader", "canceled_reader", "duplicate_reader"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				selected := *launch
				var failures atomic.Int32
				selected.fail = func(error) { failures.Add(1) }
				reconciler := &extractionpublication.Reconciler{StoreAccounting: true, Runtime: &extractionpublication.Runtime{Fence: &extractionpublication.AuthorityFence{}}}
				control, err := newT422CheckpointRecoveryControl(ctx, &selected, reconciler)
				if err != nil {
					t.Fatal(err)
				}
				defer control.cancel()
				if mode == "phase_expired" {
					control.phaseEnd = time.Now().Add(7 * time.Second)
				}
				started := make(chan error, 1)
				if mode != "canceled_reader" && mode != "duplicate_reader" {
					go func() { started <- control.waitForReader(ctx) }()
				}
				synctest.Wait()
				time.Sleep(6 * time.Second) // Slow startup exceeds the callback budget.
				select {
				case err := <-started:
					t.Fatalf("scheduler entered before the recovered reader: %v", err)
				default:
				}
				if failures.Load() != 0 || control.recovered.observer != nil {
					t.Fatal("startup consumed a recovery observer deadline")
				}
				if mode == "canceled_startup" || mode == "phase_expired" {
					if mode == "canceled_startup" {
						cancel()
					} else {
						time.Sleep(time.Second)
					}
					if err := <-started; err == nil || failures.Load() != 1 {
						t.Fatal("unobserved scheduler did not refuse and join", err)
					}
					return
				}
				snapshot, err := dispatchadmission.ProductionSemanticState()
				if err != nil {
					t.Fatal(err)
				}
				if mode == "invalid_reader" {
					snapshot.ProducerID++
				}
				request, cancelRequest := context.WithCancel(context.WithValue(ctx, t422SemanticRequestKey{}, snapshot))
				defer cancelRequest()
				readDone := make(chan error, 1)
				go func() { _, _, err := control.read(request); readDone <- err }()
				synctest.Wait()
				switch mode {
				case "delayed_reader":
					if err := <-started; err != nil || failures.Load() != 0 {
						t.Fatal("valid recovered reader did not release scheduler", err)
					}
					// No native result is supplied here. Cancel its admitted wait;
					// this must still latch failure, not manufacture recovered R.
					cancelRequest()
				case "duplicate_reader":
					if _, _, err := control.read(request); err == nil {
						t.Fatal("duplicate recovered reader admitted")
					}
				default:
					cancelRequest()
				}
				if err := <-readDone; err == nil || failures.Load() != 1 || control.recovered.reported {
					t.Fatal("failed reader supplied usable recovery", err)
				}
				if mode == "canceled_reader" || mode == "duplicate_reader" {
					go func() { started <- control.waitForReader(ctx) }()
				}
				if mode != "delayed_reader" {
					if err := <-started; err == nil {
						t.Fatal("failed reader authorized scheduler entry")
					}
				}
			})
		})
	}
}

func TestT422CheckpointRecoveryBootstrapHelper(t *testing.T) {
	mode := os.Getenv(t422CheckpointRecoveryHelperMode)
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	_, raw := t422CheckpointRecoveryRecord(t)
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
		t.Fatal("owners", err)
	}
	if mode == "reader_start" {
		testT422CheckpointReaderStart(t, launch)
		mode = "complete" // Also retain the existing callback/report/close proof.
	}
	reconciler := &extractionpublication.Reconciler{StoreAccounting: true, Runtime: &extractionpublication.Runtime{Fence: &extractionpublication.AuthorityFence{}}}
	control, err := newT422CheckpointRecoveryControl(ctx, launch, reconciler)
	if err != nil {
		t.Fatal(err)
	}
	defer control.cancel()
	hit := control.input.Hit
	event := store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionHit, Repository: launch.request.Repository,
		Stage: extractionpublication.ScheduleStage, ResourceClass: store.GenerationResourceExtraction, Generation: hit.ScheduleGeneration, ScheduleDigest: hit.ScheduleDigest,
		ChunkIdentity: hit.ChunkIdentity, Offset: int64(control.input.Offset), Length: 1, ScheduleStatus: store.GenerationScheduleActive,
		Priority: 0, ChunkStatus: store.GenerationChunkRunning, Leased: true, StaleBefore: time.Now().Add(-20 * time.Second),
		PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest("killed-native-fixture-lease"), ChunkStateDigest: t422CheckpointTestDigest, CheckpointStateDigest: t422CheckpointTestDigest}
	reaper, reaperCancel := context.WithTimeout(ctx, 5*time.Second)
	defer reaperCancel()
	if err := control.transition(reaper, event); err != nil {
		t.Fatal(err)
	}
	requeued := event
	requeued.Point, requeued.Priority, requeued.ChunkStatus, requeued.Leased = store.GenerationStaleLeaseTransitionRequeued, 2, store.GenerationChunkPending, false
	requeued.StaleBefore, requeued.PrivateLeaseTokenDigest, requeued.ChunkStateDigest, requeued.CheckpointStateDigest = time.Time{}, "", "", ""
	event.Point, event.Priority, event.ChunkStatus, event.Leased = store.GenerationStaleLeaseTransitionRecovered, 2, store.GenerationChunkDone, false
	event.StaleBefore, event.ScheduleStatus, event.ChunkStateDigest, event.CheckpointStateDigest = time.Time{}, "", "", ""
	if mode != "same_lease" {
		event.PrivateLeaseTokenDigest = store.GenerationLeaseTokenDigest("new-native-fixture-lease")
	}
	observer, observerCancel := context.WithTimeout(ctx, 5*time.Second)
	defer observerCancel()
	owner, err := owners.Enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	if mode != "recovered_before_requeue" && mode != "canceled_before_requeue" {
		if err := control.transition(reaper, requeued); err != nil {
			t.Fatal(err)
		}
	}
	go func() { defer owner.End(); finished <- control.transition(observer, event) }()
	if mode == "recovered_before_requeue" || mode == "canceled_before_requeue" {
		// Test-only observation of the callback-entry latch, not production polling.
		ticker := time.NewTicker(time.Millisecond)
		for {
			control.mu.Lock()
			entered := control.recovered.observer != nil
			control.mu.Unlock()
			if entered {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		ticker.Stop()
		select {
		case <-control.recovered.ready:
			t.Fatal("inferred requeue before actual callback")
		default:
		}
		if mode == "canceled_before_requeue" {
			observerCancel()
		} else {
			if err := control.transition(reaper, requeued); err != nil {
				t.Fatal(err)
			}
		}
	}
	failed := mode == "same_lease" || mode == "canceled_before_requeue" || mode == "report_failure"
	if mode != "same_lease" && mode != "canceled_before_requeue" {
		select {
		case <-control.recovered.ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		control.mu.Lock()
		control.recovered.reading = true
		control.mu.Unlock()
		accounted, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
		if err != nil {
			t.Fatal(err)
		}
		_ = readaccounting.Charge(accounted, readaccounting.ControlFileRead, 7)
		_ = readaccounting.Charge(accounted, readaccounting.StoreReadAttempt, 4)
		state := t421NewExactReadAccountingState(func([]byte) error {
			if mode == "report_failure" {
				return errors.New("supplied sink failure")
			}
			return nil
		}, func(error) {})
		state.active = true
		state.finishRead(httptest.NewRecorder(), ledger, 1, true, "complete", nil, nil, func(cause error) {
			if cause != nil {
				_ = control.stop(cause)
				return
			}
			_ = control.finishReport(&control.recovered)
		})
	}
	if err := <-finished; (err != nil) != failed {
		t.Fatal("recovery callback", err)
	}
	if failed && (failures.Load() != 1 || control.recovered.reported) {
		t.Fatal("failed callback prefix advanced")
	}
	fmt.Println("callback_joined")
	var signal [1]byte
	read := func(want byte) {
		t.Helper()
		if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil || signal[0] != want {
			t.Fatal("parent", err)
		}
	}
	read('f')
	if !failed {
		state, response := t422StaleFixtureFinal()
		response.Authority = control.input.Prior
		if mode == "wrong_final" {
			response.Authority.PhysicalTree = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}
		snapshot, err := dispatchadmission.ProductionSemanticState()
		if err != nil {
			t.Fatal(err)
		}
		requestCtx := context.WithValue(ctx, t422SemanticRequestKey{}, snapshot)
		if err := control.captureFinal(requestCtx, state, response); (err != nil) != (mode == "wrong_final") {
			t.Fatal("final continuity", err)
		}
		if mode != "wrong_final" && !control.finalSeen {
			t.Fatal("final not compared")
		}
	}
	fmt.Println("final_checked")
	read('c')
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("closed")
}
