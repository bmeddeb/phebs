//go:build linux

package t421

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"time"
)

func TestLinuxEpochCheckpointSelectedPolicy(t *testing.T) {
	prepared, _ := linuxEpochCheckpointV12.policy()
	selected, ok := linuxEpochCheckpointV13.policy()
	raw, err := json.Marshal(selected)
	if !ok || err != nil || SHA256(raw) != linuxEpochCheckpointPolicySHA256 {
		t.Fatal("selected canonical policy differs", SHA256(raw), err)
	}
	selected.Schema, selected.IndexRuntime, selected.EngineMeasurement, selected.Tracing, selected.RootFirst = prepared.Schema, prepared.IndexRuntime, prepared.EngineMeasurement, prepared.Tracing, prepared.RootFirst
	if selected != prepared {
		t.Fatal("selection changed a numerical allowance or unrelated policy")
	}
	adapter := linuxEpochRecipeTestCoordinatorForRecipe(t, linuxEpochCheckpointV13)
	if !adapter.issued() || adapter.recipe.policySHA != linuxEpochCheckpointPolicySHA256 {
		t.Fatal("source-selected identity not shared")
	}
	for _, epoch := range []uint64{1, 2, 3, 4, 5} {
		recipe, maximum := adapter.engineMeasurements(epoch)
		wantRecipe, wantMaximum := linuxEpochCheckpointV12.engine(epoch)
		if recipe != wantRecipe || maximum != wantMaximum {
			t.Fatal("selected engine/runtime relationships changed", epoch)
		}
	}
	// Placeholder workspaces cannot turn protocol selection into native entry.
	prior := &ExecutionEpochOneRun{flow: adapter.core, epoch: ExecutionEpochConfig{Epoch: 3}}
	if adapter.checkpointEntry(t.Context(), prior, false, false) == nil || adapter.checkpointEpoch(4) {
		t.Fatal("descriptor substituted for actual predecessor authority")
	}
}

func TestLinuxEpochCheckpointSelectorsRefuseBeforeMutation(t *testing.T) {
	for _, selected := range []bool{false, true} {
		recipe := linuxEpochLegacyV11
		if selected {
			recipe = linuxEpochCheckpointV13
		}
		adapter := linuxEpochRecipeTestCoordinatorForRecipe(t, recipe)
		for _, mode := range []struct {
			name              string
			stale, checkpoint bool
		}{{"plain", false, false}, {"stale", true, false}, {"checkpoint", true, true}} {
			t.Run(recipeName(selected)+"/"+mode.name, func(t *testing.T) {
				run := &ExecutionEpochOneRun{flow: adapter.core, epoch: ExecutionEpochConfig{Epoch: 2}}
				allowed := selected && mode.stale && mode.checkpoint || !selected && !mode.stale && !mode.checkpoint
				if (adapter.checkpointReturn(t.Context(), run, mode.stale, mode.checkpoint) == nil) != allowed {
					t.Fatal("source selection lost closed return mode")
				}
				if !allowed {
					if _, err := run.startReturnA(t.Context(), mode.stale, mode.checkpoint); !errors.Is(err, ErrExecutionEpochOne) {
						t.Fatal("closed selector accepted", err)
					}
					if adapter.core.returnUsed || adapter.core.retained != nil || adapter.core.epochs.author.next != 0 {
						t.Fatal("refusal mutated source/flow before admission")
					}
				}
			})
		}
		for _, mode := range []struct{ pressure, backup bool }{{true, false}, {true, true}, {false, true}} {
			run := &ExecutionEpochOneRun{flow: adapter.core, epoch: ExecutionEpochConfig{Epoch: 3}}
			if adapter.checkpointEntry(t.Context(), run, mode.pressure, mode.backup) == nil {
				t.Fatal("out-of-scope native alias admitted")
			}
		}
	}
	for _, request := range []LinuxExecutionEpochRequest{{}, {Epochs: &LinuxExecutionEpochConfigCustody{}}} {
		if flow, err := prepareLinuxCheckpointExecutionEpochFlow(t.Context(), request); flow != nil || err == nil {
			t.Fatal("source-only constructor bypassed genuine owners")
		}
	}
}

func recipeName(selected bool) string {
	if selected {
		return "selected"
	}
	return "legacy"
}

// Logical capability shape only: real DA/SA constructors, no operational
// resource/image authority. Actual issuance below still refuses these owners.
func linuxEpochCheckpointShape(t *testing.T) (*linuxEpochCoordinator, *ExecutionEpochOneRun, *ExecutionEpochOneRun) {
	t.Helper()
	adapter := linuxEpochRecipeTestCoordinatorForRecipe(t, linuxEpochCheckpointV13)
	prior := &ExecutionEpochOneRun{flow: adapter.core, epoch: ExecutionEpochConfig{Epoch: 3}, done: make(chan struct{})}
	next := &ExecutionEpochOneRun{flow: adapter.core, checkpointRecovery: &epochCheckpointRecoveryInput{}, checkpointPrior: &AuthorityPhaseResult{}, processPrior: &ProcessObservation{}, lifetimeDeadline: time.Now().Add(time.Minute)}
	lifetime, cancel := context.WithDeadline(t.Context(), next.lifetimeDeadline)
	t.Cleanup(cancel)
	adapter.checkpoint = &linuxEpochCheckpointSuccessor{coordinator: adapter, recipe: adapter.recipe, prior: prior, next: next, config: adapter.held.epochs.epochs[3], recovery: next.checkpointRecovery, priorAuthority: next.checkpointPrior, processPrior: next.processPrior, lifetime: lifetime, deadline: next.lifetimeDeadline, runDeadline: next.lifetimeDeadline}
	return adapter, prior, next
}

func TestLinuxEpochCheckpointBorrowTransferKeepsExactProof(t *testing.T) {
	adapter, prior, next := linuxEpochCheckpointShape(t)
	adapter.core.retained = prior
	if adapter.checkpointLaunch(t.Context(), next, 4) != nil {
		t.Fatal("shape reservation")
	}
	adapter.core.retained = nil // Actual shared launch clears this during borrow transfer.
	adapter.core.epochs.author.borrowedBy = next
	for range 3 {
		if adapter.checkpointRun(t.Context(), next, 4) != nil {
			t.Fatal("repeatable proof depended on completed predecessor context")
		}
	}
	if adapter.checkpointLaunch(t.Context(), next, 4) == nil {
		t.Fatal("second launch reused reservation")
	}
	if adapter.checkpointRun(t.Context(), &ExecutionEpochOneRun{flow: adapter.core}, 4) == nil {
		t.Fatal("foreign next acquired source borrow")
	}
	// The structural token itself never replaces protected config/image/loan checks.
	if err := adapter.release(t.Context(), 4); err == nil {
		t.Fatal("modeled proof released real listener authority")
	}
	adapter.core.epochs.author.borrowedBy = nil
}

func TestLinuxEpochCheckpointProofRefusesForeignAndExpired(t *testing.T) {
	for _, name := range []string{"missing", "foreign-recipe", "foreign-coordinator", "foreign-config", "foreign-next", "foreign-prior", "recovery", "authority", "process", "lifetime", "expired", "pressure", "backup", "archive", "epoch5", "canceled", "canceled-lifetime", "missing-lifetime", "later-context", "unbounded-lifetime", "drifted-actual-deadline", "missing-planned-deadline"} {
		t.Run(name, func(t *testing.T) {
			adapter, _, next := linuxEpochCheckpointShape(t)
			proof := adapter.checkpoint
			if adapter.checkpointLaunch(t.Context(), next, 4) != nil {
				t.Fatal("shape reservation")
			}
			ctx := t.Context()
			epoch := uint64(4)
			switch name {
			case "missing":
				adapter.checkpoint = nil
			case "foreign-recipe":
				proof.recipe = &linuxEpochRecipeIdentity{}
			case "foreign-coordinator":
				proof.coordinator = &linuxEpochCoordinator{}
			case "foreign-config":
				proof.config.ConfigSHA256 = "foreign"
			case "foreign-next":
				proof.next = &ExecutionEpochOneRun{}
			case "foreign-prior":
				proof.prior = next
			case "recovery":
				next.checkpointRecovery = &epochCheckpointRecoveryInput{}
			case "authority":
				next.checkpointPrior = &AuthorityPhaseResult{}
			case "process":
				next.processPrior = &ProcessObservation{}
			case "lifetime":
				next.lifetimeDeadline = next.lifetimeDeadline.Add(time.Second)
			case "expired":
				proof.deadline = time.Now().Add(-time.Second)
				next.lifetimeDeadline = proof.deadline
			case "pressure":
				next.pressureAllowed = true
			case "backup":
				next.backupAllowed = true
			case "archive":
				next.archiveInput = &epochArchiveInput{}
			case "epoch5":
				epoch = 5
			case "later-context":
				lifetime, cancel := context.WithDeadline(t.Context(), proof.deadline.Add(time.Second))
				t.Cleanup(cancel)
				proof.lifetime = lifetime
			case "unbounded-lifetime":
				proof.lifetime = context.Background()
			case "drifted-actual-deadline":
				proof.deadline = proof.deadline.Add(time.Second)
			case "missing-planned-deadline":
				proof.runDeadline = time.Time{}
			case "missing-lifetime":
				proof.lifetime = nil
			case "canceled-lifetime":
				lifetime, cancel := context.WithCancel(t.Context())
				cancel()
				proof.lifetime = lifetime
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if adapter.checkpointRun(ctx, next, epoch) == nil {
				t.Fatal("foreign/missing/expired shape accepted")
			}
		})
	}
}

func TestLinuxEpochCheckpointReservationIsOneShot(t *testing.T) {
	adapter, _, next := linuxEpochCheckpointShape(t)
	var group sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		group.Go(func() { results <- adapter.checkpointLaunch(t.Context(), next, 4) })
	}
	group.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatal("launch reservation cardinality", accepted)
	}
}

func TestLinuxEpochCheckpointPartialOwnersNeverIssue(t *testing.T) {
	for _, joined := range []bool{false, true} {
		adapter := linuxEpochRecipeTestCoordinatorForRecipe(t, linuxEpochCheckpointV13)
		prior := &ExecutionEpochOneRun{flow: adapter.core, epoch: ExecutionEpochConfig{Epoch: 3}, done: make(chan struct{}), result: ExecutionEpochOneResult{RootStarted: true, RootJoined: true, SessionEmpty: true}}
		if joined {
			close(prior.done)
		}
		adapter.core.retained = prior
		next := &ExecutionEpochOneRun{flow: adapter.core, checkpointRecovery: &epochCheckpointRecoveryInput{}, checkpointPrior: &AuthorityPhaseResult{}, processPrior: &ProcessObservation{}, phaseDeadline: time.Now().Add(time.Minute), lifetimeDeadline: time.Now().Add(time.Minute), cancelRun: func() {}}
		if adapter.checkpointSuccessor(t.Context(), t.Context(), prior, next) == nil || adapter.checkpoint != nil {
			t.Fatal("joined flags replaced native Wait/terminal/cutover authority")
		}
		adapter.core.retained = nil
	}
}

func TestLinuxEpochCheckpointEndpointRequiresCompletedSequence(t *testing.T) {
	for _, epoch := range []uint64{3, 4} {
		want := []uint32{6, 6, 7, 7, 7, 8, 8}
		if epoch == 4 {
			want = []uint32{8}
		}
		for _, name := range []string{"exact", "early-eof", "wrong-order", "endpoint-error", "not-joined"} {
			t.Run(recipeName(epoch == 4)+"/"+name, func(t *testing.T) {
				run := &ExecutionEpochOneRun{engineMeasurementFinished: make(chan struct{})}
				completion := linuxEpochEngineCompletion{}
				phases := append([]uint32(nil), want...)
				if name == "early-eof" {
					phases = phases[:len(phases)-1]
				}
				if name == "wrong-order" {
					phases[0] = 7
				}
				for _, phase := range phases {
					if completion.record(phase) != nil {
						t.Fatal("fixed record cap")
					}
				}
				if name == "endpoint-error" {
					completion.err = ErrExecutionEpochOne
				}
				if name != "not-joined" {
					close(run.engineMeasurementFinished)
				}
				if completion.completed(run, epoch) != (name == "exact") {
					t.Fatal("EOF/count/order/error or join substituted for complete protocol")
				}
			})
		}
	}
	completion := linuxEpochEngineCompletion{}
	for range 7 {
		if completion.record(8) != nil {
			t.Fatal("fixed cap")
		}
	}
	if completion.record(8) == nil || completion.count != 7 {
		t.Fatal("completion backing overflow")
	}
}

// Real Unix EM02 HOLD/RELEASE and EOF, with a modeled guard identity only.
// This proves endpoint ordering, not native engine or measurement authority.
func linuxEpochCheckpointProtocolEOF(t *testing.T) linuxEpochEngineCompletion {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	parent, child := epochEngineLaunchTestPipe(t)
	conn, err := adoptAuthorCustodySocket(parent)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	joined := make(chan struct{})
	var served error
	var completion linuxEpochEngineCompletion
	binding := dispatchadmission.EngineMeasurementBinding{Recipe: dispatchadmission.EngineMeasurementCheckpointV2, ProducerID: 5, ProducerBinding: [32]byte{1}, InputSHA256: [32]byte{2}}
	go func() {
		defer close(joined)
		served = dispatchadmission.ServeEngineMeasurement(ctx, conn, binding, 1, func(operation context.Context, phase uint32, measure func(context.Context, dispatchadmission.EngineMeasurementIdentity) error) error {
			if err := measure(operation, dispatchadmission.EngineMeasurementIdentity{PID: 42, Birth: 91, Tracer: 17}); err != nil {
				return err
			}
			return completion.record(phase)
		})
		completion.err = served
	}()
	t.Cleanup(func() { cancel(); _ = conn.Close(); _ = child.Close(); <-joined })
	deadline, _ := ctx.Deadline()
	if child.SetDeadline(deadline) != nil {
		t.Fatal("protocol deadline")
	}
	var header [76]byte
	if _, err := io.ReadFull(child, header[:]); err != nil || string(header[:4]) != "EM02" {
		t.Fatal("protocol header", err)
	}
	if _, err := child.Write(header[:]); err != nil {
		t.Fatal("protocol hello", err)
	}
	var hold [16]byte
	copy(hold[:4], "EM02")
	hold[4] = 1
	binary.BigEndian.PutUint32(hold[8:12], 8)
	binary.BigEndian.PutUint32(hold[12:16], 1)
	if _, err := child.Write(hold[:]); err != nil {
		t.Fatal("protocol hold", err)
	}
	var identity [32]byte
	if _, err := io.ReadFull(child, identity[:]); err != nil {
		t.Fatal("protocol derived identity", err)
	}
	release := hold
	release[4] = 2
	if _, err := child.Write(release[:]); err != nil {
		t.Fatal("protocol release", err)
	}
	var ack [16]byte
	if _, err := io.ReadFull(child, ack[:]); err != nil || ack != release {
		t.Fatal("protocol resume acknowledgement", err)
	}
	if child.CloseWrite() != nil {
		t.Fatal("protocol EOF request")
	}
	var extra [1]byte
	if n, err := child.Read(extra[:]); n != 0 || err != io.EOF {
		t.Fatal("actual endpoint EOF acknowledgement", n, err)
	}
	<-joined
	if served != nil || !completion.completeSequence(4) {
		t.Fatal("actual protocol result", served)
	}
	return completion
}

func TestLinuxEpochCheckpointEndpointPublicationJoin(t *testing.T) {
	completion := linuxEpochCheckpointProtocolEOF(t)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		deadline, _ := ctx.Deadline()
		run := &ExecutionEpochOneRun{phaseDeadline: deadline, engineMeasurementFinished: make(chan struct{})}
		sole := make(chan error, 1)
		sole <- ErrExecutionEpochOne
		run.engineMeasurementDone = sole
		server := &linuxEpochServer{measurements: completion}
		result := make(chan error, 1)
		go func() { result <- server.waitEngineCompletion(ctx, run, 4, deadline) }()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatal("child EOF substituted for parent publication", err)
		default:
		}
		if !run.mu.TryLock() {
			t.Fatal("endpoint join retained run.mu")
		}
		run.mu.Unlock()
		close(run.engineMeasurementFinished)
		synctest.Wait()
		if err := <-result; err != nil {
			t.Fatal("joined successful endpoint refused", err)
		}
		select {
		case err := <-sole:
			if err != ErrExecutionEpochOne {
				t.Fatal("sole shared result changed")
			}
		default:
			t.Fatal("helper consumed shared finish result")
		}
	})
}

func TestLinuxEpochCheckpointEndpointPublicationRefusals(t *testing.T) {
	for _, name := range []string{"timeout", "endpoint-error", "partial", "missing", "unbounded", "expired-phase"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				deadline, _ := ctx.Deadline()
				run := &ExecutionEpochOneRun{phaseDeadline: deadline, engineMeasurementFinished: make(chan struct{})}
				server := &linuxEpochServer{measurements: linuxEpochEngineCompletion{phases: [7]uint32{8}, count: 1}}
				switch name {
				case "endpoint-error":
					server.measurements.err = ErrExecutionEpochOne
					close(run.engineMeasurementFinished)
				case "partial":
					server.measurements.count = 0
					close(run.engineMeasurementFinished)
				case "missing":
					run.engineMeasurementFinished = nil
				case "unbounded":
					ctx = context.Background()
				case "expired-phase":
					run.phaseDeadline = time.Now().Add(-time.Second)
				}
				if server.waitEngineCompletion(ctx, run, 4, deadline) == nil {
					t.Fatal("missing/error/timeout endpoint publication admitted")
				}
			})
		})
	}
}

func TestLinuxEpochCheckpointShorterCallerLifetime(t *testing.T) {
	adapter, _, next := linuxEpochCheckpointShape(t)
	planned := next.lifetimeDeadline
	lifetime, cancel := context.WithDeadline(t.Context(), planned.Add(-30*time.Second))
	defer cancel()
	actual, _ := lifetime.Deadline()
	proof := adapter.checkpoint
	proof.lifetime, proof.deadline = lifetime, actual
	if !actual.Before(planned) || proof.runDeadline != planned || adapter.checkpointLaunch(t.Context(), next, 4) != nil || adapter.checkpointRun(t.Context(), next, 4) != nil {
		t.Fatal("valid earlier caller clip refused or planned bound changed")
	}
	cancel()
	if adapter.checkpointRun(t.Context(), next, 4) == nil {
		t.Fatal("clipped successor lifetime outlived cancellation")
	}
}
