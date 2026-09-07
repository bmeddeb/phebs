//go:build darwin

package t421

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// These tiny process fixtures reuse the actual private-session descendant and
// DA Handle.Wait. They prove no admitted Phebs image, SDK/PC/SA terminal fence,
// prepared checkpoint, same-phase successor or whole-phase measurement.
func TestExecutionTerminalProcessOwnedKillAndRefusals(t *testing.T) {
	for _, mode := range []string{"owned", "settlementFailure", "alreadyJoined", "preKilled", "canceled", "cancelAfterWait"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{
				Limits: dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1,
					ActivePerProducer: 1, Attempts: 1, WireBytes: 4096, AckTimeout: time.Second},
				Producers: []dispatchadmission.Producer{{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{{ID: 1, Role: 1, Persistent: true}}}},
				Phases:    []dispatchadmission.Phase{{ID: 8, Roles: []dispatchadmission.RoleBudget{{Role: 1, Attempts: 1}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := controller.NewLocalProducer(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close(ctx) }()
			command := exec.Command(os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
			helper := "held-root"
			if mode == "alreadyJoined" {
				helper = "exit-root"
			}
			command.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=" + helper, "GORACE=atexit_sleep_ms=0"}
			command.Stderr = os.Stderr
			command.WaitDelay = 5 * time.Second
			prepareProductionSession(command)
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close() }()
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = output.Close() }()
			handle, err := parent.Start(ctx, 1, command)
			if err != nil {
				t.Fatal(err)
			}
			joined := false
			var waited chan error
			defer func() {
				_ = t4013.KillPrivateProcessSession(command.Process.Pid)
				if !joined {
					if waited == nil {
						_ = handle.Wait()
					} else {
						<-waited
					}
				}
				if err := t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(6*time.Second)); err != nil {
					t.Error(err)
				}
			}()
			var descendant [8]byte
			if _, err := io.ReadFull(output, descendant[:]); err != nil {
				t.Fatal(err)
			}
			childPID := int(binary.BigEndian.Uint64(descendant[:]))
			session, sessionErr := unix.Getsid(childPID)
			group, groupErr := syscall.Getpgid(childPID)
			if sessionErr != nil || groupErr != nil || session != command.Process.Pid || group == command.Process.Pid {
				t.Fatal("descendant not in its owned distinct-group session", session, group, sessionErr, groupErr)
			}
			waited = make(chan error, 1)
			killCtx, stopKill := context.WithCancel(ctx)
			defer stopKill()
			if mode == "alreadyJoined" || mode == "preKilled" {
				if mode == "alreadyJoined" {
					if _, err := input.Write([]byte{1}); err != nil {
						t.Fatal(err)
					}
				} else if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				// This is the actual sole native Handle.Wait, buffered before the
				// helper enters. The still-live orphan must still be cleaned up.
				waited <- handle.Wait()
			} else {
				go func() {
					err := handle.Wait()
					if mode == "cancelAfterWait" {
						stopKill()
					}
					waited <- err
				}()
			}
			if mode == "settlementFailure" {
				if err := controller.Advance(); err == nil { // Real invalid DA transition latches before settlement.
					t.Fatal("unfenced advance did not fail accounting")
				}
			} else if mode != "canceled" {
				if parent.Pause(ctx) != nil || controller.Fence() != nil || parent.Checkpoint(ctx) != nil {
					t.Fatal("parent checkpoint failed")
				}
			}
			if mode == "canceled" {
				stopKill()
			}
			result, err := killExecutionProcessSession(killCtx, command, waited)
			joined = result.RootJoined
			if mode == "canceled" {
				if err == nil || result.RootJoined || result.SessionEmpty || result.ProcessState != nil {
					t.Fatal("pre-canceled caller gained death proof", result, err)
				}
				return
			}
			if !result.RootJoined || !result.SessionEmpty || result.ProcessState != command.ProcessState || (err == nil) != (mode == "owned") {
				t.Fatal("owned kill/join classification differs", result, err)
			}
			prefix, _ := controller.Snapshot()
			if prefix.Attempts != 1 {
				t.Fatal("native kill erased attempted root", prefix)
			}
			if mode == "owned" {
				testExecutionTerminalWaitShape(t, result, command.Process.Pid)
			}
		})
	}
}

func testExecutionTerminalWaitShape(t *testing.T, result executionProcessDeath, pid int) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(result.WaitErr, &exit) || exit.ProcessState != result.ProcessState {
		t.Fatal("real native ExitError missing")
	}
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"native", exit, true}, {"handle", result.WaitErr, true},
		{"nil", nil, false}, {"canceled", errors.Join(exit, context.Canceled), false},
		{"transport", errors.Join(exit, dispatchadmission.ErrTransport), false},
		{"duplicate", errors.Join(exit, exit), false}, {"nested", errors.Join(errors.Join(exit)), false},
		{"wrongNativeState", &exec.ExitError{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if executionProcessSIGKILL(test.err, result.ProcessState, pid) != test.want {
				t.Fatal("non-native wait leaf was normalized", test.err)
			}
		})
	}
	if executionProcessSIGKILL(exit, nil, pid) || executionProcessSIGKILL(exit, result.ProcessState, pid+1) {
		t.Fatal("foreign native identity accepted")
	}
}

func TestExecutionTerminalProcessUnavailableInputs(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for _, command := range []*exec.Cmd{nil, exec.Command("unstarted")} {
		result, err := killExecutionProcessSession(ctx, command, nil)
		if err == nil || result.RootJoined || result.ProcessState != nil || result.SessionEmpty {
			t.Fatal("invalid input acquired native evidence", result, err)
		}
	}
	closed := make(chan error)
	close(closed)
	if _, joined := waitExecutionTerminalRoot(closed, time.Now().Add(time.Second)); joined {
		t.Fatal("closed channel fabricated Wait")
	}
}
