//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestLinuxEpochNativeTerminalActor(t *testing.T) {
	for _, mode := range []string{"capture-live", "engine-parent"} {
		t.Run(mode, func(t *testing.T) {
			fixture := linuxEpochTraceMechanicalStart(t, mode, false)
			root := fixture.root
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			prefix := linuxEpochTraceFixturePreACK
			if mode == "engine-parent" {
				prefix = "TRACE_ENGINE_CHILD_READY "
			}
			for {
				if report, ready := fixture.report(t, prefix); ready {
					if report.Tasks < 2 {
						t.Fatal("actual native threads missing", report)
					}
					break
				}
				if ctx.Err() != nil {
					t.Fatal("genuine actor ready boundary", ctx.Err())
				}
				time.Sleep(time.Millisecond)
			}
			death, err := root.finishHardDeath(ctx, time.Now().Add(5*time.Second))
			fixture.joined = death.RootJoined
			if err != nil || !death.RootJoined || !death.SessionEmpty || root.cleanupErr != nil || root.unknown || root.owner.hardRoot != nil || len(root.owner.leaders) != 0 || len(root.tasks) != 0 || len(root.pending) != 0 {
				t.Fatalf("actual terminal actor join: death=%+v err=%v trace=%+v", death, err, root.owner.failure())
			}
			testLinuxExecutionSIGKILLShapes(t, death, root.command.Process.Pid)
			t.Log("source-owned actual root-first kill, native Wait, survivor retirement and strict terminal finish joined")
		})
	}
}

func testLinuxExecutionSIGKILLShapes(t *testing.T, death executionProcessDeath, pid int) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(death.WaitErr, &exit) || exit.ProcessState != death.ProcessState {
		t.Fatal("genuine native ExitError missing", death.WaitErr)
	}
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"native", exit, true}, {"actual-handle", death.WaitErr, true},
		{"nil", nil, false}, {"cancellation-sibling", errors.Join(exit, context.Canceled), false},
		{"accounting-sibling", errors.Join(exit, dispatchadmission.ErrTransport), false},
		{"duplicate", errors.Join(exit, exit), false}, {"nested", errors.Join(errors.Join(exit)), false},
		{"wrapped", fmt.Errorf("wrapped: %w", exit), false}, {"missing-state", &exec.ExitError{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if executionProcessSIGKILL(test.err, death.ProcessState, pid) != test.want {
				t.Fatal("strict native shape changed", test.err)
			}
		})
	}
	if executionProcessSIGKILL(exit, nil, pid) || executionProcessSIGKILL(exit, death.ProcessState, pid+1) {
		t.Fatal("foreign native state accepted")
	}
}

func TestLinuxEpochNativeTerminalRefusesUnissuedInputs(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for _, operation := range []context.Context{nil, context.Background(), ctx} {
		for _, root := range []*linuxEpochTraceRoot{nil, {}} {
			death, err := root.finishHardDeath(operation, time.Now().Add(time.Second))
			if err == nil || death != (executionProcessDeath{}) {
				t.Fatal("unissued input gained terminal authority", death, err)
			}
		}
	}
}
