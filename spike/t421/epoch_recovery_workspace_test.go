package t421

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

// All byte values and ownership flags in these unit fixtures are supplied;
// native process/SDK/session closure and whole-phase evidence are not asserted.
func recoveryParentModel() *ExecutionEpochOneRun {
	run := midphaseParentModel(2)
	run.epoch.Epoch = 3
	run.flow.epochs.released = 3
	run.checkpointAllowed, run.staleAllowed, run.terminalEntered, run.terminalRequested = true, true, true, true
	run.phaseDeadline = time.Now().Add(time.Minute)
	for i := 0; i < 5; i++ {
		run.result.RecoverySamples.Points[i] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
	}
	return run
}

func TestEpochRecoveryWorkspaceParentBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "canceled", "renewed", "active_author", "successor", "not_joined", "no_terminal", "no_retention", "prior_missing", "repeat", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			run := recoveryParentModel()
			ctx, cancel := context.WithDeadline(t.Context(), run.phaseDeadline)
			defer cancel()
			switch mode {
			case "canceled":
				cancel()
			case "renewed":
				run.phaseDeadline = run.phaseDeadline.Add(-time.Second)
			case "active_author":
				run.flow.epochs.author.active = true
			case "successor":
				run.flow.epochs.released++
			case "not_joined":
				run.result.SessionEmpty = false
			case "no_terminal":
				run.terminalRequested = false
			case "no_retention":
				run.returnStarting = false
			case "prior_missing":
				run.result.RecoverySamples.Points[4] = ExecutionWorkspaceBytePhase{}
			case "repeat":
				run.result.RecoverySamples.Points[5].Completed = 1
			case "unavailable":
				run.result.RecoverySamples.Unavailable = true
			}
			run.flow.mu.Lock()
			got := run.recoveryParentBoundaryLocked(ctx)
			run.flow.mu.Unlock()
			if got != (mode == "valid") {
				t.Fatal(mode, got)
			}
			copied := run.recoveryWorkspacePrefixSnapshot()
			copied.Points[0].Maximum.LogicalBytes++
			if copied == run.recoveryWorkspacePrefixSnapshot() {
				t.Fatal("aliased value copy")
			}
		})
	}
}

// Real bounded HTTP transport; the response and prerequisite evidence are
// modeled. Tests invoke the actual owning reader, not the byte converter alone.
func TestEpochRecoveryWorkspaceHTTP(t *testing.T) {
	for _, point := range []uint8{0, 2, 3, 6} {
		for _, mode := range []string{"valid", "wrong_phase", "incomplete", "conflict", "excess", "duplicate"} {
			t.Run(fmt.Sprint(point)+"/"+mode, func(t *testing.T) {
				calls := 0
				reader := epochTestHTTPReader(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					want := "start"
					if point == 2 || point == 6 {
						want = "finish"
					}
					if r.Method != http.MethodPost || r.Header.Get("X-Phebs-T422-Workspace-Point") != want {
						t.Error("wrong sample request")
					}
					if mode == "conflict" {
						w.WriteHeader(http.StatusConflict)
					}
					_, _ = fmt.Fprint(w, `{"logical_bytes":50,"allocated_bytes":100}`)
				}))
				reader.plan = accountingTestPlan(t)
				reader.run.flow = &ExecutionEpochOne{workspace: &productionRoot{}}
				reader.run.staleAllowed = true
				reader.run.epoch.Epoch = 3
				if point == 6 {
					reader.run.epoch.Epoch = 4
				}
				reader.projection.Phase = "stale_lease"
				if point >= 3 {
					reader.projection.Phase = "process_restart"
				}
				reader.finalUsed = point == 2 || point == 6
				reader.selectorCleanupPhase = reader.projection.Phase
				for i := uint8(0); i < point; i++ {
					reader.recoverySamples.Points[i] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
				}
				switch mode {
				case "wrong_phase":
					reader.projection.Phase = "return_a"
				case "incomplete":
					if point == 0 {
						reader.stalePrepared = true
					} else {
						reader.recoverySamples.Points[point-1] = ExecutionWorkspaceBytePhase{}
					}
				case "duplicate":
					reader.recoverySamples.Points[point].Attempts = 1
				case "excess":
					reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes = 99
				}
				err := reader.sampleRecoveryWorkspace(t.Context(), point)
				if (err == nil) != (mode == "valid") {
					t.Fatal(mode, err)
				}
				wantCalls := 0
				if mode == "valid" || mode == "conflict" || mode == "excess" {
					wantCalls = 1
				}
				if calls != wantCalls || reader.recoverySamples.LimitExceeded != (mode == "excess") {
					t.Fatal(calls, reader.recoverySamples)
				}
				if mode == "excess" && reader.recoverySamples.Points[point].Maximum.AllocatedBytes != 100 {
					t.Fatal("lost actual HTTP prefix")
				}
			})
		}
	}
}

func TestEpochRecoveryWorkspacePreparationPresence(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		for _, mode := range []string{"zero", "missing", "excess", "duplicate", "no_start", "unbound"} {
			t.Run(fmt.Sprint(checkpoint)+"/"+mode, func(t *testing.T) {
				reader := &executionEpochInspection{plan: accountingTestPlan(t), run: &ExecutionEpochOneRun{staleAllowed: true, epoch: ExecutionEpochConfig{Epoch: 3}, flow: &ExecutionEpochOne{workspace: &productionRoot{}}}}
				point := uint8(1)
				if checkpoint {
					point = 4
				}
				reader.recoverySamples.Points[point-1] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1}
				value := &epochRecoveryWorkspaceSample{}
				switch mode {
				case "missing":
					value = nil
				case "excess":
					value.AllocatedBytes = reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes + 1
				case "duplicate":
					reader.recoverySamples.Points[point].Attempts = 1
				case "no_start":
					reader.recoverySamples.Points[point-1] = ExecutionWorkspaceBytePhase{}
				case "unbound":
					reader.run.flow.workspace = nil
				}
				err := reader.recordRecoveryPreparationWorkspace(value, checkpoint)
				if (err == nil) != (mode == "zero") {
					t.Fatal(mode, err)
				}
				if mode == "zero" && reader.recoverySamples.Points[point].Completed != 1 {
					t.Fatal("bound zero became absence")
				}
				if mode == "excess" && (!reader.recoverySamples.LimitExceeded || reader.recoverySamples.Points[point].Maximum.AllocatedBytes != value.AllocatedBytes) {
					t.Fatal("excess prefix lost")
				}
			})
		}
	}
}

// Supplied source-bound stream values, not native traversals. Swapping an
// individual value while preserving the maximum must still fail reconciliation.
func TestEpochRecoveryWorkspaceJoinedPoints(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, producer := range []uint32{4, 5} {
		var stream ExecutionWorkspaceByteObservation
		input := "sha256:01" + strings.Repeat("00", 31)
		_, err := observeWorkspaceByteEvent([]byte(workspaceTestBinding(producer)), plan, producer, input, &stream)
		if err != nil {
			t.Fatal(err)
		}
		var samples ExecutionRecoveryWorkspaceSamples
		for i := range samples.Points {
			samples.Points[i] = ExecutionWorkspaceBytePhase{Attempts: 1, Completed: 1, Maximum: custodybytes.Sample{LogicalBytes: uint64(50 + i), AllocatedBytes: uint64(100 + i)}}
		}
		first, last := 0, 5
		if producer == 5 {
			first, last = 6, 7
		}
		var seq uint64
		for i := first; i < last; i++ {
			phase := uint32(7)
			if i >= 3 {
				phase = 8
			}
			seq++
			value := samples.Points[i].Maximum
			for _, line := range strings.SplitAfter(workspaceTestPair(producer, phase, seq, value.LogicalBytes, value.AllocatedBytes), "\n") {
				if line == "" {
					continue
				}
				if _, err = observeWorkspaceByteEvent([]byte(line), plan, producer, input, &stream); err != nil {
					t.Fatal(err)
				}
			}
		}
		if producer == 4 {
			samples.Points[5], samples.Points[6] = ExecutionWorkspaceBytePhase{}, ExecutionWorkspaceBytePhase{}
		}
		if !stream.finish() || !recoveryWorkspaceJoinedPrefix(producer, stream, samples, true) {
			t.Fatal("valid fixed prefix refused", stream, samples)
		}
		later := stream
		later.LimitExceeded, later.Complete = true, false
		if recoveryWorkspaceJoinedPrefix(producer, later, samples, true) || !recoveryWorkspacePointValuesMatch(producer, later, samples, true) {
			t.Fatal("later stream failure relabeled completed recovery point")
		}
		changed := samples
		changed.Points[first].Maximum.LogicalBytes--
		if recoveryWorkspaceJoinedPrefix(producer, stream, changed, true) {
			t.Fatal("masked individual sample mutation")
		}
		if producer == 4 {
			stream.Unavailable = true
			stream.Complete = false
			if recoveryWorkspaceJoinedPrefix(producer, stream, samples, true) || stream.Phases[7].Completed != 2 {
				t.Fatal("failed suffix lost positive prefix")
			}
		}
	}
}
