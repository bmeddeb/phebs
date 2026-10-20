//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

// Two fixed direct children on the same creating tracer task. Both terminal
// statuses remain owned by their genuine DA Handles; no raw root status is
// consumed. The omitted second Z is a mechanical negative census control only.
func TestLinuxEpochTraceFinalCensusRefusesUnknownZombie(t *testing.T) {
	runtime.LockOSThread()
	outstanding := 0
	defer func() {
		if outstanding != 0 {
			t.Error("unknown genuine handles retained on original locked tracer task")
			for {
				time.Sleep(linuxEpochTraceFallback)
			}
		}
		runtime.UnlockOSThread()
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	settlement, settleCancel := context.WithCancel(context.Background())
	defer settleCancel()
	controller, err := dispatchadmission.New(settlement, dispatchadmission.Config{
		Limits:    dispatchadmission.Limits{Producers: 1, Sites: 1, Roles: 1, Phases: 1, ActivePerProducer: 2, Attempts: 2, WireBytes: 16 << 10, AckTimeout: 2 * time.Second},
		Producers: []dispatchadmission.Producer{{ID: 1, Binding: [32]byte{1}, Sites: []dispatchadmission.Site{{ID: 1, Role: 1}}}},
		Phases:    []dispatchadmission.Phase{{ID: 1, Roles: []dispatchadmission.RoleBudget{{Role: 1, Attempts: 2}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(settlement, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cc := context.WithTimeout(context.Background(), 3*time.Second)
		defer cc()
		if e := parent.Close(c); e != nil {
			t.Error("genuine DA close", e)
		}
	}()
	type child struct {
		command *exec.Cmd
		handle  dispatchadmission.Handle
		fd      int
		joined  bool
	}
	children := make([]*child, 0, 2)
	defer func() {
		for _, entry := range children {
			if !entry.joined {
				if entry.fd >= 0 {
					_ = unix.PidfdSendSignal(entry.fd, unix.SIGKILL, nil, 0)
				}
				cleanup, cc := context.WithTimeout(context.Background(), 3*time.Second)
				if e := linuxEpochTraceTestTerminal(cleanup, entry.command.Process.Pid); e == nil {
					_ = entry.handle.Wait()
					entry.joined = true
					outstanding--
				} else {
					t.Error("unknown direct-root cleanup retained on locked tracer task", e)
					cc()
					// The outer test deadline bounds this refused attempt.
					// Never unwind into parent Close/settlement cancellation
					// or recycle this creating task with an unknown child.
					for {
						time.Sleep(linuxEpochTraceFallback)
					}
				}
				cc()
			}
			if entry.joined && entry.fd >= 0 {
				if e := unix.Close(entry.fd); e != nil {
					t.Error(e)
				}
			}
		}
	}()
	for range 2 {
		command := exec.Command("/usr/bin/git", "--version")
		command.Env = []string{"LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		command.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setsid: true}
		handle, e := parent.StartInPhase(ctx, 1, dispatchadmission.Site{ID: 1, Role: 1}, command)
		if e != nil {
			t.Fatal("fixed own root Start", e)
		}
		entry := &child{command: command, handle: handle, fd: -1}
		children = append(children, entry)
		outstanding++
		entry.fd, e = unix.PidfdOpen(command.Process.Pid, 0)
		if e != nil {
			t.Fatal(e)
		}
		// The fixed exec-stop is before application instructions. EPERM/refusal
		// fails; neither settings nor host privileges are changed.
		for {
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			stop, e := linuxEpochTraceWait(command.Process.Pid, unix.WSTOPPED)
			if errors.Is(e, unix.EINTR) {
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			if stop.signo == 0 {
				time.Sleep(time.Millisecond)
				continue
			}
			if stop.code != 4 || stop.status != int32(unix.SIGTRAP) || stop.pid != int32(command.Process.Pid) || stop.uid != uint32(os.Getuid()) {
				t.Fatal("fixed initial stop refused", stop)
			}
			break
		}
		if e = unix.PtraceCont(command.Process.Pid, 0); e != nil {
			t.Fatal(e)
		}
		if e = linuxEpochTraceTestTerminal(ctx, command.Process.Pid); e != nil {
			t.Fatal(e)
		}
	}
	first, second := children[0], children[1]
	identity, err := linuxEpochTraceIdentityOf(first.command.Process.Pid)
	if err != nil || identity.state != "Z" || identity.tracer != unix.Gettid() {
		t.Fatal("original terminal tracer identity", identity, err)
	}
	secondIdentity, err := linuxEpochTraceIdentityOf(second.command.Process.Pid)
	if err != nil || secondIdentity.state != "Z" || secondIdentity.tracer != unix.Gettid() {
		t.Fatal("omitted real Z control", secondIdentity, err)
	}
	// finalTracerCensus itself grants no Wait/signal authority. This deliberately
	// incomplete mechanical view must refuse even though live SID census omits Z.
	root := &linuxEpochTraceRoot{original: identity, tid: unix.Gettid(), gateOwned: true, exitSeen: true, tasks: map[int]*linuxEpochTraceTask{identity.pid: {original: identity}}, pending: map[int]*linuxEpochTraceLeader{}}
	if err = root.finalTracerCensus(ctx); err == nil {
		t.Fatal("unknown unreaped tracer Z was ignored")
	}
	if err = second.handle.Wait(); err != nil {
		second.joined = true
		outstanding--
		t.Fatal("second genuine DA Wait", err)
	}
	second.joined = true
	outstanding--
	mask, err := linuxEpochTracePoll(ctx, second.fd)
	if err != nil || mask&unix.POLLHUP == 0 {
		t.Fatal("second original HUP", mask, err)
	}
	if err = root.finalTracerCensus(ctx); err != nil {
		t.Fatal("only known original root remained", err)
	}
	if err = first.handle.Wait(); err != nil {
		first.joined = true
		outstanding--
		t.Fatal("first genuine DA Wait", err)
	}
	first.joined = true
	outstanding--
	mask, err = linuxEpochTracePoll(ctx, first.fd)
	if err != nil || mask&unix.POLLHUP == 0 {
		t.Fatal("first original HUP", mask, err)
	}
	count, err := parent.Count()
	if err != nil || count.Ordinal != 2 || count.Active != 0 {
		t.Fatal("two actual DA settlements", count, err)
	}
	t.Logf("unknownZ=%d refused; knownRoot=%d; genuineWaits=2; active=%d", secondIdentity.pid, identity.pid, count.Active)
}

func linuxEpochTraceTestTerminal(ctx context.Context, pid int) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stop, err := linuxEpochTraceWait(pid, unix.WSTOPPED)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil && !errors.Is(err, unix.ECHILD) {
			return err
		}
		if stop.signo != 0 {
			return ErrExecutionEpochOne
		} // No runtime event/signal waiver.
		terminal, err := linuxEpochTraceWait(pid, unix.WEXITED|unix.WNOWAIT)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if terminal.terminal(pid) {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
}
