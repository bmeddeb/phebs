//go:build linux

package t421

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Called after the existing real tracer fixture consumes its sole done result.
// This verifies terminal fact transport, not server/resource admission.
func assertLinuxEpochNativeWaitFacts(t *testing.T, fixture *linuxEpochTraceMechanical, completionErr error) {
	t.Helper()
	root := fixture.root
	run := &ExecutionEpochOneRun{command: root.command}
	held := root.owner.held
	held.servers = map[*ExecutionEpochOneRun]*linuxEpochServer{run: {trace: root}}
	adapter := &linuxEpochCoordinator{held: held}
	death, err := adapter.finish(run, root.done, true, completionErr, time.Now(), false)
	if !death.RootJoined || !death.SessionEmpty || death.ProcessState == nil || death.ProcessState != root.command.ProcessState || death.ProcessState.Pid() != root.command.Process.Pid || err != completionErr {
		t.Fatalf("native terminal facts differ from the creating actor: %+v, %v", death, err)
	}
	if death.ProcessState.Success() {
		if death.WaitErr != nil {
			t.Fatal("successful genuine Wait gained an error", death.WaitErr)
		}
	} else {
		var exit *exec.ExitError
		if !errors.As(death.WaitErr, &exit) || exit.ProcessState != death.ProcessState || errors.Is(death.WaitErr, ErrExecutionEpochOne) {
			t.Fatal("native Wait error replaced by composite operation failure", death.WaitErr)
		}
	}
	// Even a completed original Wait cannot turn the ordinary leaf-first route
	// into an accepted root-first checkpoint. No second Wait is consumed here.
	run.terminalEntered = true
	terminal, err := adapter.finish(run, root.done, true, completionErr, time.Now(), false)
	if !errors.Is(err, ErrExecutionEpochOne) || terminal != death {
		t.Fatalf("unqualified hard death accepted or native facts lost: %+v, %v", terminal, err)
	}
}

func TestLinuxEpochFinishRefusesUnownedWaitResults(t *testing.T) {
	for _, mutation := range []string{"nil-adapter", "nil-held", "nil-run", "missing-server", "nil-trace", "nil-owner", "foreign-owner", "nil-wait", "closed-wait", "foreign-wait", "foreign-command", "no-native-wait"} {
		t.Run(mutation, func(t *testing.T) {
			done := make(chan error, 1)
			root := &linuxEpochTraceRoot{command: &exec.Cmd{}, done: done}
			run := &ExecutionEpochOneRun{command: root.command}
			adapter := &linuxEpochCoordinator{held: &linuxEpochHeldFlow{servers: map[*ExecutionEpochOneRun]*linuxEpochServer{run: {trace: root}}}}
			root.owner = &linuxEpochTraceOwner{held: adapter.held}
			var waited <-chan error = done
			switch mutation {
			case "nil-adapter":
				adapter = nil
			case "nil-held":
				adapter.held = nil
			case "nil-run":
				run = nil
			case "missing-server":
				delete(adapter.held.servers, run)
			case "nil-trace":
				adapter.held.servers[run].trace = nil
			case "nil-owner":
				root.owner = nil
			case "foreign-owner":
				root.owner.held = &linuxEpochHeldFlow{}
			case "nil-wait":
				waited = nil
			case "closed-wait":
				close(done)
			case "foreign-wait":
				waited = make(chan error)
			case "foreign-command":
				run.command = &exec.Cmd{}
			}
			death, err := adapter.finish(run, waited, true, nil, time.Now(), true)
			if !errors.Is(err, ErrExecutionEpochOne) || death != (executionProcessDeath{}) {
				t.Fatalf("unowned channel/boolean manufactured native facts: %+v, %v", death, err)
			}
		})
	}
}

func TestLinuxEpochFinishJoinsOriginalActor(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "ordinary-stop"
		if terminal {
			name = "refused-terminal"
		}
		t.Run(name, func(t *testing.T) {
			fixture := linuxEpochTraceMechanicalStart(t, "capture-live", false)
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, ready := fixture.report(t, linuxEpochTraceFixturePreACK); ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("live fixture barrier unavailable")
				}
				time.Sleep(10 * time.Millisecond)
			}
			root := fixture.root
			run := &ExecutionEpochOneRun{command: root.command, terminalEntered: terminal}
			held := root.owner.held
			held.servers = map[*ExecutionEpochOneRun]*linuxEpochServer{run: {trace: root}}
			death, err := (&linuxEpochCoordinator{held: held}).finish(run, root.done, false, nil, deadline, terminal)
			fixture.joined = death.RootJoined
			if !death.RootJoined || !death.SessionEmpty || death.ProcessState == nil || death.ProcessState != root.command.ProcessState || err == nil || death.WaitErr == nil {
				t.Fatalf("actual signaled native Wait lost: %+v, %v", death, err)
			}
			if terminal && !errors.Is(err, ErrExecutionEpochOne) {
				t.Fatal("leaf-first cleanup accepted as checkpoint death", err)
			}
		})
	}
}

func TestLinuxCheckpointRefusesBeforeTerminalMutation(t *testing.T) {
	for _, kind := range []string{"checkpoint", "pressure", "backup"} {
		t.Run(kind, func(t *testing.T) {
			run := &ExecutionEpochOneRun{
				flow:    &ExecutionEpochOne{native: &linuxEpochCoordinator{}},
				control: &dispatchadmission.PhaseControl{}, epoch: ExecutionEpochConfig{Epoch: 3},
			}
			var next *ExecutionEpochOneRun
			var err error
			switch kind {
			case "checkpoint":
				next, err = run.CheckpointRestart(t.Context())
			case "pressure":
				next, err = run.CheckpointRestartPressure(t.Context())
			case "backup":
				next, err = run.CheckpointRestartBackup(t.Context())
			}
			if next != nil || !errors.Is(err, ErrExecutionEpochOne) || !strings.Contains(err.Error(), "native root-first ownership unavailable") || run.checkpointUsed || run.terminalEntered || run.terminalRequested || run.flow.retained != nil {
				t.Fatalf("unqualified successor reached terminal mutation: %v, %v", next, err)
			}
		})
	}
}
