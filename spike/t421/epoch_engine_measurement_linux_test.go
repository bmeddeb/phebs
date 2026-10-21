//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

func pinEpochEngineTestLeader(t *testing.T, ctx context.Context, root *linuxEpochTraceRoot, pid int) int {
	t.Helper()
	select {
	case root.owner.admission.probeGate <- struct{}{}:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer func() { <-root.owner.admission.probeGate }()
	leader := root.owner.leaders[pid]
	if leader == nil || leader.root != root || root.liveLeader(ctx, leader) != nil {
		t.Fatal("original live actor leader unavailable", pid)
	}
	fd, err := unix.FcntlInt(uintptr(leader.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Close(fd); err != nil {
			t.Error(err)
		}
	})
	return fd
}

func TestLinuxEpochEngineMeasurementRealGroup(t *testing.T) {
	engineImage := linuxEpochEngineNativeFixture(t)
	for _, mode := range []string{"success", "callback-error", "callback-panic", "callback-cancel", "clone-burst"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := "engine-parent"
			if mode == "clone-burst" {
				fixtureMode = "engine-burst-parent"
			}
			fixture := linuxEpochTraceMechanicalStartEngine(t, fixtureMode, false, engineImage)
			root := fixture.root
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			var report linuxEpochTraceFixtureReport
			for {
				var ready bool
				report, ready = fixture.report(t, "TRACE_ENGINE_READY ")
				if ready {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("real engine not ready", ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			for {
				child, ready := fixture.report(t, "TRACE_ENGINE_CHILD_READY ")
				if ready {
					if child.PID != report.ChildPID || child.Tasks < 2 {
						t.Fatal("child readiness binding", child, report)
					}
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("child signal readiness", ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			engineFD := pinEpochEngineTestLeader(t, ctx, root, report.ChildPID)
			rootFD := pinEpochEngineTestLeader(t, ctx, root, report.PID)
			// Wait for the child's ordinary runtime to publish its several owned CLONEs.
			// The measurement itself must establish its exact stopped union independently.
			tasks, err := linuxEpochTraceGroupEntries(report.ChildPID, linuxEpochTraceMaxTIDs)
			if err != nil || len(tasks) < 2 {
				t.Fatal("real engine group", tasks, err)
			}
			calls, samples := 0, 0
			sentinel := errors.New("measurement callback refused")
			measurementCtx, cancelMeasurement := context.WithCancel(ctx)
			defer cancelMeasurement()
			var result error
			var pausedTIDs []int
			panicObserved := false
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						panicObserved = recovered == sentinel
					}
				}()
				result = root.withEngineMeasurement(measurementCtx, func(operation context.Context, identity dispatchadmission.EngineMeasurementIdentity) error {
					calls++
					actual, e := linuxEpochTraceIdentityOf(report.ChildPID)
					if e != nil || identity.PID != uint32(report.ChildPID) || identity.Tracer != uint32(root.tid) || actual.birth != strconv.FormatUint(identity.Birth, 10) {
						t.Fatal("derived identity", identity, actual, e)
					}
					before, e := linuxEpochTraceGroupEntries(report.ChildPID, linuxEpochTraceMaxTIDs)
					if e != nil {
						return e
					}
					pausedTIDs = slices.Clone(before)
					for _, tid := range before {
						fresh, e := linuxEpochTraceIdentityOf(tid)
						if e != nil || fresh.state != "t" {
							t.Fatal("not truly group-stopped", tid, fresh, e)
						}
					}
					// Several real complete censuses acquire the same gate during the walk.
					for range 3 {
						select {
						case root.owner.admission.probeGate <- struct{}{}:
						case <-operation.Done():
							return operation.Err()
						}
						gauge, _, sampleErr := root.owner.held.gauge(operation, root.owner.held.controllerPID, root.owner.held.controllerStart)
						if sampleErr == nil {
							_, sampleErr = gauge.Sample(operation)
						}
						<-root.owner.admission.probeGate
						if sampleErr != nil {
							t.Fatal("whole meter blocked/refused during stopped measurement", sampleErr, root.owner.failure())
						}
						samples++
					}
					switch mode {
					case "callback-cancel":
						cancelMeasurement()
						return operation.Err()
					case "callback-error":
						return sentinel
					case "callback-panic":
						panic(sentinel)
					}
					return nil
				})
			}()
			if calls != 1 || samples != 3 || (mode == "success" || mode == "clone-burst") && result != nil || mode == "callback-error" && !errors.Is(result, sentinel) || mode == "callback-panic" && !panicObserved || mode == "callback-cancel" && !errors.Is(result, context.Canceled) {
				t.Fatal("measurement result", calls, samples, result, panicObserved, root.owner.failure())
			}
			if mode == "clone-burst" {
				for {
					burst, ready := fixture.report(t, "TRACE_ENGINE_BURST_READY ")
					if ready {
						if burst.PID != report.ChildPID || burst.Tasks != 8 {
							t.Fatal("actual resumed clone burst binding", burst, report)
						}
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("resumed clone burst unavailable", ctx.Err(), root.owner.failure())
					case <-time.After(5 * time.Millisecond):
					}
				}
				// READY proves simultaneous occupants; original actor registration
				// of a TID absent at HOLD additionally proves a real post-CONT CLONE.
				newThreads := 0
				func() {
					select {
					case root.owner.admission.probeGate <- struct{}{}:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					defer func() { <-root.owner.admission.probeGate }()
					for tid, task := range root.tasks {
						if task.original.tgid == report.ChildPID && !task.initial && !task.reconciled && !slices.Contains(pausedTIDs, tid) {
							newThreads++
						}
					}
				}()
				if newThreads != 8 {
					t.Fatal("actual post-CONT registered pthread count", newThreads)
				}
				t.Log("actual post-CONT registered pthreads:", newThreads)
			}
			// Real resumed execution is necessary before the child can handle this signal.
			if err = unix.PidfdSendSignal(engineFD, unix.SIGUSR2, nil, 0); err != nil {
				t.Fatal(err)
			}
			for {
				var ready bool
				report, ready = fixture.report(t, linuxEpochTraceFixturePreACK)
				if ready {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("original parent did not reap resumed engine", ctx.Err(), root.owner.failure())
				case <-time.After(5 * time.Millisecond):
				}
			}
			if report.WaitCode != 17 || report.Active != 0 || report.Ordinal != 1 {
				t.Fatal("genuine engine Wait", report)
			}
			if err = unix.PidfdSendSignal(rootFD, unix.SIGUSR2, nil, 0); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-root.done:
				fixture.joined = true
				if err != nil {
					t.Fatal("genuine root Wait", err, root.owner.failure())
				}
			case <-ctx.Done():
				t.Fatal("root not joined", ctx.Err(), root.owner.failure())
			}
			if !root.waitReturned || root.command.ProcessState == nil || root.command.ProcessState.ExitCode() != 0 {
				t.Fatal("actual original Wait unavailable")
			}
			raw, _ := json.Marshal(report)
			t.Log("real all-thread stop, concurrent sampled whole tree, resume, engine Wait17, root Wait0:", strings.TrimSpace(string(raw)))
		})
	}
}

// Kill the pinned engine inside its acknowledged, actually stopped lease.
// Failed RELEASE must return without claiming resume or inner DA settlement;
// the original actor still owns the real root Wait and native closure.
func TestLinuxEpochEngineMeasurementTerminalLeaseReplies(t *testing.T) {
	fixture := linuxEpochTraceMechanicalStartEngine(t, "engine-exit-parent", false, linuxEpochEngineNativeFixture(t))
	root := fixture.root
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	var report linuxEpochTraceFixtureReport
	for {
		parent, parentReady := fixture.report(t, "TRACE_ENGINE_READY ")
		child, childReady := fixture.report(t, "TRACE_ENGINE_CHILD_READY ")
		if parentReady && childReady {
			if parent.ChildPID != child.PID {
				t.Fatal("original child binding")
			}
			report = parent
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	fd := pinEpochEngineTestLeader(t, ctx, root, report.ChildPID)
	invoked := 0
	var killErr error
	killed := false
	err := root.withEngineMeasurement(ctx, func(_ context.Context, identity dispatchadmission.EngineMeasurementIdentity) error {
		invoked++
		if identity.PID != uint32(report.ChildPID) {
			return errors.New("owned lease identity mismatch")
		}
		killErr = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
		killed = killErr == nil
		return killErr
	})
	if !killed || killErr != nil {
		t.Fatal("actual original engine kill missing or failed", killed, killErr)
	}
	if invoked != 1 || err == nil {
		t.Fatal("acknowledged dead-engine RELEASE did not refuse", invoked, err)
	}
	root.requestStop()
	select {
	case <-root.done:
		fixture.joined = true
	case <-ctx.Done():
		t.Fatal("actual failed-root Wait not joined", ctx.Err(), root.owner.failure())
	}
	if !root.waitReturned || root.command.ProcessState == nil {
		t.Fatal("no original root Wait")
	}
	count, countErr := fixture.parent.Count()
	if countErr != nil || count.Ordinal != 1 || count.Active != 0 {
		t.Fatal("original outer root DA settlement", count, countErr)
	}
	t.Logf("acknowledged actual held engine pidfd kill; failed RELEASE returned; original outer root Wait=%d DAordinal=1 active=0; inner child settlement not claimed", root.command.ProcessState.ExitCode())
}
