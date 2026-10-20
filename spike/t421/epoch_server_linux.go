//go:build linux

package t421

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

type linuxEpochServer struct {
	pidfd            int
	pinErr           error
	sessionErr       error
	stopCallback     func() bool
	callbackDone     chan struct{}
	flowStopCallback func() bool
	flowCallbackDone chan struct{}
	callbackOnce     sync.Once
	closeErr         error
	session          *linuxEpochSessionAuthority
	sessionEmpty     bool
}

// Root cancellation depends only on the successfully pinned owned Start.
// A refused session anchor must never suppress these safe root signals.
func (server *linuxEpochServer) armCancellation(ctx, lifetime context.Context) {
	if server.pinErr != nil || server.pidfd < 0 {
		return
	}
	server.callbackDone, server.flowCallbackDone = make(chan struct{}), make(chan struct{})
	server.flowStopCallback = context.AfterFunc(lifetime, func() {
		defer close(server.flowCallbackDone)
		_ = unix.PidfdSendSignal(server.pidfd, unix.SIGKILL, nil, 0)
	})
	server.stopCallback = context.AfterFunc(ctx, func() {
		defer close(server.callbackDone)
		_ = unix.PidfdSendSignal(server.pidfd, unix.SIGKILL, nil, 0)
	})
}

func (server *linuxEpochServer) closeRoot() error {
	server.callbackOnce.Do(func() {
		if server.stopCallback != nil && !server.stopCallback() {
			<-server.callbackDone
		}
		if server.flowStopCallback != nil && !server.flowStopCallback() {
			<-server.flowCallbackDone
		}
		if server.pidfd >= 0 && unix.Close(server.pidfd) != nil {
			server.closeErr = ErrExecutionEpochOne
		}
	})
	return server.closeErr
}

func (adapter *linuxEpochCoordinator) beforeStart(ctx context.Context, command *exec.Cmd) error {
	if adapter.check(ctx) != nil || adapter.held.resources == nil || adapter.held.resources.Check(ctx) != nil || command == nil || command.SysProcAttr != nil {
		return ErrExecutionEpochOne
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}
func (adapter *linuxEpochCoordinator) startWait(ctx context.Context, run *ExecutionEpochOneRun, phase uint32, handle dispatchadmission.Handle) (<-chan error, error) {
	server := &linuxEpochServer{pidfd: -1}
	server.pidfd, server.pinErr = pinExecutionRootDescriptor(run.command)
	adapter.held.servers[run] = server
	if server.pinErr == nil {
		_, server.sessionErr = captureLinuxEpochSession(adapter.held, run, server)
	}
	server.armCancellation(ctx, adapter.held.flow.lifetime)
	var meterErr error
	if server.pinErr == nil && server.sessionErr == nil {
		gauge, name, err := adapter.held.gauge(ctx, run.command.Process.Pid, "")
		if err == nil {
			run.processObservation, err = adapter.held.resources.startServerObservation(ctx, run.command.Process.Pid, phase, name, gauge, func() { adapter.held.resources.Fail(); run.stopOnce.Do(func() { close(run.stop) }) })
		}
		meterErr = err
	}
	waited := make(chan error, 1)
	go func() {
		// No Wait may reap this root until its original SID is drained under the
		// still-waitable root anchor. Unknown capture/exit retains every loan.
		for server.pinErr != nil || server.sessionErr != nil {
			time.Sleep(time.Second)
		}
		var information unix.Siginfo
		for {
			err := unix.Waitid(unix.P_PIDFD, server.pidfd, &information, unix.WEXITED|unix.WNOWAIT, nil)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err == nil && information.Signo == int32(unix.SIGCHLD) {
				break
			}
			// A refused exit observation cannot later become successful evidence.
			// Keep the unreaped root and complete loan until external resolution.
			for {
				time.Sleep(time.Second)
			}
		}
		server.session.exited = true
		run.processObservation.exited()
		var sessionErr error
		for {
			probe, cancel := context.WithTimeout(context.Background(), epochProcessProbeTimeout)
			empty, err := adapter.held.stopSession(probe, server.session)
			cancel()
			if empty && err == nil {
				break
			}
			// Later actual drain permits cleanup, never a clean qualification.
			sessionErr = ErrExecutionEpochOne
			time.Sleep(time.Second)
		}
		server.sessionEmpty = true
		waited <- errors.Join(sessionErr, handle.Wait())
	}()
	return waited, errors.Join(server.pinErr, server.sessionErr, meterErr)
}
func (adapter *linuxEpochCoordinator) finish(run *ExecutionEpochOneRun, waited <-chan error, joined bool, waitErr error, deadline time.Time) (bool, bool, error) {
	server := adapter.held.servers[run]
	if server == nil {
		return joined, false, ErrExecutionEpochOne
	}
	var result error
	if !joined && server.pinErr == nil {
		if err := unix.PidfdSendSignal(server.pidfd, unix.SIGTERM, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			result = ErrExecutionEpochOne
		}
	}
	if !joined {
		waitErr, joined = waitExecutionProcessRoot(waited, deadline)
	}
	if !joined && server.pinErr == nil {
		_ = unix.PidfdSendSignal(server.pidfd, unix.SIGKILL, nil, 0)
		result = ErrExecutionEpochOne
	}
	// Keep every loan when a root is still unjoined; a timeout is never a Wait.
	if !joined {
		waitErr = <-waited
		joined = true
	}
	return joined, server.sessionEmpty, errors.Join(result, waitErr, server.pinErr, server.sessionErr, server.closeRoot())
}
