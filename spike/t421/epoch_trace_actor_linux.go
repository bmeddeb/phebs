//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"golang.org/x/sys/unix"
)

// The only callers are the bound Linux server start and shared-author start.
// A post-Start refusal returns this retained root, never a fictitious no-start.
func (owner *linuxEpochTraceOwner) start(ctx, lifetime context.Context, command *exec.Cmd, parent *dispatchadmission.LocalProducer, phase uint32, site dispatchadmission.Site, image *os.File, server *ExecutionEpochOneRun) (*linuxEpochTraceRoot, error) {
	if owner == nil || ctx == nil || lifetime == nil || lifetime.Err() != nil || ctx.Err() != nil || command == nil || command.Process != nil || parent == nil || owner.held == nil || owner.held.flow == nil || owner.held.flow.core == nil || owner.held.flow.core.parent != parent || owner.admission != owner.held.resources || owner.admission.Check(ctx) != nil {
		return nil, ErrExecutionEpochOne
	}
	author := site == ExecutionParentAuthorSite() && image == owner.held.images[0] && server == nil
	serve := site == (dispatchadmission.Site{ID: executionSiteServe, Role: executionRolePhebs, Persistent: true}) && image == owner.held.images[1] && server != nil && server.command == command
	if (!author && !serve) || !linuxEpochTraceAttributes(command, author) {
		return nil, ErrExecutionEpochOne
	}
	root := &linuxEpochTraceRoot{owner: owner, command: command, parent: parent, phase: phase, site: site, image: image, pidfd: -1, tasks: make(map[int]*linuxEpochTraceTask), pending: make(map[int]*linuxEpochTraceLeader), wake: make(chan struct{}, 1), stop: make(chan struct{}, 1), grace: make(chan struct{}, 1), started: make(chan error, 1), done: make(chan error, 1), exited: make(chan struct{}), observerReady: make(chan struct{}), observerDone: make(chan struct{}), serverRun: server, callbackDone: make(chan struct{})}
	if server == nil {
		close(root.observerReady)
	}
	owner.mu.Lock()
	if owner.closed || len(owner.roots) >= 2 {
		owner.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	owner.roots[root] = true
	owner.mu.Unlock()
	root.stopCallback = context.AfterFunc(lifetime, func() { defer close(root.callbackDone); root.requestStop() })
	go root.run(ctx)
	// Abandoning a still-pending native Start would lose its future real child.
	// The enclosing fixed flow retains the loan worker until actual Start returns.
	err := <-root.started
	if !root.startedNative {
		<-root.done // The callback and registry must retire before caller cleanup.
		return nil, err
	}
	return root, err
}
func (root *linuxEpochTraceRoot) requestFinish() {
	if root == nil {
		return
	}
	select {
	case root.grace <- struct{}{}:
	default:
	}
	select {
	case root.wake <- struct{}{}:
	default:
	}
}
func (root *linuxEpochTraceRoot) tryGate() bool {
	if root.gateOwned {
		return true
	}
	select {
	case root.owner.admission.probeGate <- struct{}{}:
		root.gateOwned = true
		return true
	default:
		return false
	}
}

// Queue behind an existing complete census instead of repeatedly missing its
// free gaps. Only this actor reads/writes gateOwned; no owner/gauge mutex may be
// held while waiting. The same caller context also bounds the subsequent turn.
func (root *linuxEpochTraceRoot) acquireGate(ctx context.Context) error {
	if ctx == nil || root == nil || root.owner == nil || root.owner.admission == nil || root.owner.admission.probeGate == nil {
		return ErrExecutionEpochOne
	}
	if err := ctx.Err(); err != nil {
		return err // In particular, never release an older owned token here.
	}
	if root.gateOwned {
		return nil
	}
	select {
	case root.owner.admission.probeGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-root.owner.admission.probeGate
			return err
		}
		root.gateOwned = true
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (root *linuxEpochTraceRoot) releaseGate() {
	if root.gateOwned {
		root.gateOwned = false
		<-root.owner.admission.probeGate
	}
}
func (root *linuxEpochTraceRoot) allowedImage(ctx context.Context, pid int, expected *os.File) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	before, err := linuxEpochTraceIdentityOf(pid)
	if err != nil || before.tracer != root.tid || before.namespace == nil || root.owner.held.pidNamespace == nil || !os.SameFile(before.namespace, root.owner.held.pidNamespace) {
		return errors.Join(ErrExecutionEpochOne, err)
	}
	actual, err := os.Open("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return err
	}
	info, statErr := actual.Stat()
	closeErr := actual.Close()
	if statErr != nil || closeErr != nil {
		return errors.Join(ErrExecutionEpochOne, statErr, closeErr)
	}
	matched := false
	candidates := []*os.File{root.owner.held.controllerImage, root.owner.held.shellImage}
	candidates = append(candidates, root.owner.held.images[:]...)
	candidates = append(candidates, root.owner.held.gitImages[:]...)
	if expected != nil {
		candidates = []*os.File{expected}
	}
	for _, image := range candidates {
		if image == nil {
			return ErrExecutionEpochOne
		}
		candidate, e := image.Stat()
		if e != nil {
			return e
		}
		if os.SameFile(info, candidate) {
			matched = true
		}
	}
	after, err := linuxEpochTraceIdentityOf(pid)
	if err != nil || !before.sameLifetime(after) || !matched || ctx.Err() != nil {
		return errors.Join(ErrExecutionEpochOne, err)
	}
	return nil
}
func (root *linuxEpochTraceRoot) initial(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stop, err := linuxEpochTraceWait(root.command.Process.Pid, unix.WSTOPPED)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if stop.signo == 0 {
			select {
			case <-root.wake:
			case <-time.After(linuxEpochTraceFallback):
			}
			continue
		}
		if stop.code != 4 || stop.pid != int32(root.command.Process.Pid) || stop.uid != uint32(os.Getuid()) || stop.status != int32(unix.SIGTRAP) {
			return ErrExecutionEpochOne
		}
		return nil
	}
}
func (root *linuxEpochTraceRoot) run(operation context.Context) {
	runtime.LockOSThread()
	// Every returning path below has completed actual native ownership. Unknown
	// ptrace/capture state stays in this actor; thread death is never a join.
	root.tid = unix.Gettid()
	gateCtx, gateCancel := context.WithTimeout(operation, epochProcessProbeTimeout)
	gateErr := root.acquireGate(gateCtx)
	gateCancel()
	if gateErr != nil {
		root.refuse("initial-gate", gateErr)
		root.started <- gateErr
		root.completeNoStart()
		runtime.UnlockOSThread()
		root.publishDone()
		return
	}
	if root.owner.admission.Check(operation) != nil {
		root.releaseGate()
		root.started <- ErrExecutionEpochOne
		root.completeNoStart()
		runtime.UnlockOSThread()
		root.publishDone()
		return
	}
	root.startOnThread(operation)
	runtime.UnlockOSThread()
	root.publishDone()
}

// The resource-bound production run is the only runtime caller. Mechanical
// own-image tests exercise this kernel layer without manufacturing admission.
// Entry requires the creating task locked and the complete census gate owned.
func (root *linuxEpochTraceRoot) startOnThread(operation context.Context) {
	root.engineRequests = make(chan *linuxEpochEngineRequest)
	root.engineDone = make(chan struct{})
	if !linuxEpochTraceAttributes(root.command, root.serverRun == nil) || len(root.owner.leaders) >= linuxEpochTraceMaxLeaders {
		root.releaseGate()
		root.started <- ErrExecutionEpochOne
		root.completeNoStart()
		return
	}
	root.command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Ptrace: true, Pdeathsig: syscall.SIGKILL}
	var startErr error
	root.handle, startErr = root.parent.StartInPhase(operation, root.phase, root.site, root.command)
	if startErr != nil {
		root.releaseGate()
		root.started <- startErr
		root.completeNoStart()
		return
	}
	root.startedNative = true
	root.pidfd, startErr = unix.PidfdOpen(root.command.Process.Pid, 0)
	cleanup, cancel := context.WithTimeout(context.Background(), epochProcessProbeTimeout)
	if startErr == nil {
		startErr = root.initial(cleanup)
	}
	if startErr == nil {
		root.original, startErr = linuxEpochTraceIdentityOf(root.command.Process.Pid)
	}
	if startErr == nil && (root.original.pid != root.original.tgid || root.original.parent != root.owner.held.controllerPID || root.original.sid != root.original.pid || root.original.tracer != root.tid || root.original.namespace == nil || !os.SameFile(root.original.namespace, root.owner.held.pidNamespace)) {
		startErr = ErrExecutionEpochOne
	}
	if startErr == nil {
		startErr = root.allowedImage(cleanup, root.original.pid, root.image)
	}
	if startErr == nil {
		startErr = unix.PtraceSetOptions(root.original.pid, linuxEpochTraceOptions)
	}
	if startErr == nil {
		startErr = root.publishRoot()
	}
	cancel()
	if startErr != nil {
		root.refuse("initial-capture", startErr)
	}
	root.started <- startErr
	// Observer attachment does not own Start, kernel waits, or pidfd closure.
	go func() {
		<-root.observerReady
		<-root.exited
		if root.serverRun != nil {
			root.serverRun.processObservation.exited()
		}
		close(root.observerDone)
	}()
	if startErr != nil {
		// Before our first CONT this original root has executed no application
		// instruction and cannot have created descendants. Keep the sample
		// gate through original-fd kill and actual terminal/genuine Wait.
		root.cleanupInitial()
		return
	}
	root.continued = true
	if err := unix.PtraceCont(root.original.pid, 0); err != nil {
		root.unknown = true
		root.refuse("initial-continue", err)
	}
	root.releaseGate()
	root.serve()
	// serve returns only after the sole genuine root Wait, all trace tasks and
	// pidfds, cancellation callbacks, observer and gate-release watcher have joined.
}
func (root *linuxEpochTraceRoot) completeNoStart() {
	if root.stopCallback != nil && !root.stopCallback() {
		<-root.callbackDone
	}
	root.completionErr = ErrExecutionEpochOne
}

// Last actor action, after actual native/pump/DA/callback/pidfd joins and after
// unlocking the creating kernel task. Caller cleanup cannot outrun retirement.
func (root *linuxEpochTraceRoot) publishDone() {
	if root.engineDone != nil {
		close(root.engineDone)
	}
	root.owner.mu.Lock()
	delete(root.owner.roots, root)
	root.owner.mu.Unlock()
	root.done <- root.completionErr
}

func (root *linuxEpochTraceRoot) publishRoot() error {
	if !root.gateOwned || root.pidfd < 0 || len(root.owner.leaders) >= linuxEpochTraceMaxLeaders || root.owner.leaders[root.original.pid] != nil {
		return ErrExecutionEpochOne
	}
	root.owner.mu.Lock()
	if root.owner.tids >= linuxEpochTraceMaxTIDs {
		root.owner.mu.Unlock()
		return ErrExecutionEpochOne
	}
	root.owner.tids++
	root.owner.mu.Unlock()
	leader := &linuxEpochTraceLeader{root: root, original: root.original, fd: root.pidfd, parentBirth: root.owner.held.controllerStart, verified: true}
	root.owner.leaders[root.original.pid] = leader
	root.tasks[root.original.pid] = &linuxEpochTraceTask{original: root.original, leader: leader}
	return nil
}

// Never silently discard an unexpected namespace/security/credential control.
// These are exactly the two existing closed caller shapes plus no attributes.
func linuxEpochTraceAttributes(command *exec.Cmd, author bool) bool {
	if command == nil || command.Cancel != nil {
		return false
	}
	if command.SysProcAttr == nil {
		return true
	}
	expected := syscall.SysProcAttr{Setsid: true}
	if author {
		expected.Pdeathsig = syscall.SIGKILL
	}
	return reflect.DeepEqual(*command.SysProcAttr, expected)
}
