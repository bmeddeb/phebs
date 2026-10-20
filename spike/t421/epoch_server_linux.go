//go:build linux

package t421

import (
	"context"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

type linuxEpochServer struct {
	trace            *linuxEpochTraceRoot // Non-nil only for the bound v4 flow. Actor owns pidfd/Wait.
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
	owner            *linuxEpochHeldFlow // Immutable after successful capture.
	run              *ExecutionEpochOneRun
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

// Actual Start must run on the locked creating tracer task, not merely after
// a beforeStart hook on the unrelated flow goroutine.
func (adapter *linuxEpochCoordinator) start(ctx, lifetime context.Context, run *ExecutionEpochOneRun, phase uint32) (bool, error) {
	if adapter.check(ctx) != nil || run == nil || run.command == nil || adapter.held.resources == nil || adapter.held.resources.trace == nil {
		return false, ErrExecutionEpochOne
	}
	server := &linuxEpochServer{pidfd: -1}
	adapter.held.servers[run] = server
	root, err := adapter.held.resources.trace.start(ctx, lifetime, run.command, adapter.core.parent, phase, dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}, adapter.held.images[1], run)
	if root == nil {
		delete(adapter.held.servers, run)
		return false, err
	}
	server.trace = root
	return true, err
}

func (adapter *linuxEpochCoordinator) startWait(ctx context.Context, run *ExecutionEpochOneRun, phase uint32) (<-chan error, error) {
	if existing := adapter.held.servers[run]; existing != nil && existing.trace != nil {
		root := existing.trace
		// Publish the meter (including refusal/nil) before releasing attachment.
		// The actor's separate observer performs exited() outside probeGate.
		defer close(root.observerReady)
		gauge, name, err := adapter.held.gauge(ctx, run.command.Process.Pid, root.original.birth)
		if err == nil {
			run.processObservation, err = adapter.held.resources.startServerObservation(ctx, run.command.Process.Pid, phase, name, gauge, func() { adapter.held.resources.Fail(); run.stopOnce.Do(func() { close(run.stop) }) })
		}
		if err != nil {
			root.requestStop()
		}
		return root.done, err
	}
	return nil, ErrExecutionEpochOne
}

func (adapter *linuxEpochCoordinator) finish(run *ExecutionEpochOneRun, waited <-chan error, joined bool, waitErr error, deadline time.Time) (bool, bool, error) {
	server := adapter.held.servers[run]
	if server == nil {
		return joined, false, ErrExecutionEpochOne
	}
	if server.trace != nil {
		root := server.trace
		if !joined {
			root.requestFinish()
			waitErr, joined = waitExecutionProcessRoot(waited, deadline)
		}
		if !joined {
			root.requestStop()
			waitErr = <-waited
			joined = true
		}
		// Receiving done proves actual Handle.Wait, original SID emptiness and
		// all callback/pidfd ownership retirement, never a timeout substitute.
		return joined, root.waitReturned, waitErr
	}
	return joined, false, ErrExecutionEpochOne
}
