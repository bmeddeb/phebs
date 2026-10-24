//go:build linux

package t421

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

type linuxEpochServer struct {
	trace            *linuxEpochTraceRoot // Non-nil only for the admitted flow. Actor owns pidfd/Wait.
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
	measurements     linuxEpochEngineCompletion
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
	if adapter.check(ctx) != nil || run == nil || adapter.checkpointRun(ctx, run, run.epoch.Epoch) != nil || run.epoch.Epoch == 4 && !adapter.checkpoint.released.Load() || run.command == nil || adapter.held.resources == nil || adapter.held.resources.trace == nil {
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
			run.processObservation, err = adapter.held.resources.startServerObservation(ctx, run.command.Process.Pid, phase, name, gauge, func() { run.stopOnce.Do(func() { close(run.stop) }) })
		}
		if err != nil {
			root.requestStop()
		}
		return root.done, err
	}
	return nil, ErrExecutionEpochOne
}

func (adapter *linuxEpochCoordinator) finish(run *ExecutionEpochOneRun, waited <-chan error, joined bool, waitErr error, deadline time.Time, terminalOK bool) (executionProcessDeath, error) {
	if adapter == nil || adapter.held == nil || run == nil {
		return executionProcessDeath{}, ErrExecutionEpochOne
	}
	server := adapter.held.servers[run]
	if server == nil || server.trace == nil || server.trace.owner == nil || server.trace.owner.held != adapter.held || waited == nil || waited != server.trace.done || run.command != server.trace.command {
		return executionProcessDeath{}, ErrExecutionEpochOne
	}
	root := server.trace
	run.mu.Lock()
	terminal, requested, operation := run.terminalEntered, run.terminalRequested, run.terminalContext
	selected := run.epoch.Epoch == 3 && run.checkpointAllowed && run.checkpointUsed && run.retainParent && run.err == nil
	run.mu.Unlock()
	// The caller's settled terminal intent is necessary, never sufficient:
	// require the exact issued adapter, retained checkpoint and actual actor run.
	var hardErr error
	if terminal && terminalOK && requested && selected && !joined && operation != nil && operation.Err() == nil && adapter.issued() && run.flow == adapter.core && root.serverRun == run && adapter.held.resources != nil && root.owner == adapter.held.resources.trace {
		if adapter.recipe.recipe == linuxEpochCheckpointV13 {
			hardErr = server.waitEngineCompletion(operation, run, 3, deadline)
		}
		adapter.core.mu.Lock()
		retained := !adapter.core.closed && adapter.core.retained == run
		adapter.core.mu.Unlock()
		if hardErr == nil && retained && adapter.check(operation) == nil {
			death, err := root.finishHardDeath(operation, deadline)
			if death.RootJoined {
				return death, err
			}
			hardErr = err
		}
	}
	// Partial/unissued terminal entry still owns leaf-first cleanup and refuses.
	if terminal {
		root.requestStop()
	}
	if !joined {
		if !terminal {
			root.requestFinish()
		}
		waitErr, joined = waitExecutionProcessRoot(waited, deadline)
	}
	if !joined {
		root.requestStop()
		waitErr = <-waited
	}
	// done joins publication of the exact native result separately from the
	// actor's composite error; a failed operation must not replace WaitErr.
	death := root.death
	if !root.waitReturned || !death.RootJoined || death.ProcessState == nil || run.command == nil || run.command.Process == nil || death.ProcessState != run.command.ProcessState || death.ProcessState.Pid() != run.command.Process.Pid {
		return executionProcessDeath{}, errors.Join(ErrExecutionEpochOne, waitErr)
	}
	if terminal || !death.SessionEmpty {
		return death, errors.Join(ErrExecutionEpochOne, hardErr, waitErr)
	}
	return death, waitErr
}
