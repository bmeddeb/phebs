//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// These are bounds on concurrently retained tracing records, not observations
// that every Go process has a fixed thread count or a fixed SDK-helper count.
const (
	linuxEpochTraceMaxLeaders   = t4013.MaxNativeProcessRecords - 1
	linuxEpochTraceMaxTIDs      = 1024
	linuxEpochTraceRecentEvents = 32
	linuxEpochTraceFallback     = 50 * time.Millisecond
	linuxEpochTraceOptions      = unix.PTRACE_O_TRACEFORK | unix.PTRACE_O_TRACEVFORK | unix.PTRACE_O_TRACEVFORKDONE | unix.PTRACE_O_TRACECLONE | unix.PTRACE_O_TRACEEXEC | unix.PTRACE_O_TRACEEXIT | unix.PTRACE_O_EXITKILL
)

// The fixed Linux 64-bit SIGCHLD union prefix. Only these child fields are read.
type linuxEpochTraceSiginfo struct {
	signo  int32
	_      int32
	code   int32
	_      int32
	pid    int32
	uid    uint32
	status int32
}

func linuxEpochTraceChildInfo(info *unix.Siginfo) linuxEpochTraceSiginfo {
	return *(*linuxEpochTraceSiginfo)(unsafe.Pointer(info))
}
func (info linuxEpochTraceSiginfo) terminal(pid int) bool {
	return info.signo == int32(unix.SIGCHLD) && info.pid == int32(pid) && info.uid == uint32(os.Getuid()) && info.code >= 1 && info.code <= 3
}
func linuxEpochTraceWait(pid, flags int) (linuxEpochTraceSiginfo, error) {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PID, pid, &info, flags|unix.WNOHANG|unix.WALL, nil)
	return linuxEpochTraceChildInfo(&info), err
}

type linuxEpochTraceIdentity struct {
	pid, tgid, parent, tracer, sid int
	birth, name, state             string
	namespace                      os.FileInfo
}

func linuxEpochTraceRead(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(raw)) > limit {
		return nil, errors.Join(ErrExecutionEpochOne, readErr, closeErr)
	}
	return raw, nil
}
func linuxEpochTraceIdentityOf(pid int) (linuxEpochTraceIdentity, error) {
	var value linuxEpochTraceIdentity
	raw, err := linuxEpochTraceRead("/proc/"+strconv.Itoa(pid)+"/stat", 4096)
	if err != nil {
		return value, err
	}
	start, end := strings.IndexByte(string(raw), '('), strings.LastIndex(string(raw), ") ")
	if start < 1 || end <= start+1 {
		return value, ErrExecutionEpochOne
	}
	fields := strings.Fields(string(raw[end+2:]))
	if len(fields) < 22 {
		return value, ErrExecutionEpochOne
	}
	value.pid, err = strconv.Atoi(strings.TrimSpace(string(raw[:start])))
	if err != nil || value.pid != pid {
		return value, ErrExecutionEpochOne
	}
	value.parent, err = strconv.Atoi(fields[1])
	if err != nil {
		return value, err
	}
	value.sid, err = strconv.Atoi(fields[3])
	if err != nil {
		return value, err
	}
	birth, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || birth == 0 {
		return value, ErrExecutionEpochOne
	}
	value.birth, value.name, value.state = strconv.FormatUint(birth, 10), string(raw[start+1:end]), fields[0]
	raw, err = linuxEpochTraceRead("/proc/"+strconv.Itoa(pid)+"/status", 64<<10)
	if err != nil {
		return value, err
	}
	found := 0
	for _, line := range strings.Split(string(raw), "\n") {
		row := strings.Fields(line)
		if len(row) != 2 {
			continue
		}
		switch row[0] {
		case "Tgid:":
			value.tgid, err = strconv.Atoi(row[1])
			found++
		case "TracerPid:":
			value.tracer, err = strconv.Atoi(row[1])
			found++
		}
		if err != nil {
			return value, err
		}
	}
	value.namespace, err = os.Stat("/proc/" + strconv.Itoa(pid) + "/ns/pid")
	if err != nil || found != 2 || value.tgid <= 0 || !validObservedProcessName(value.name) {
		return value, errors.Join(ErrExecutionEpochOne, err)
	}
	return value, nil
}
func (value linuxEpochTraceIdentity) sameLifetime(other linuxEpochTraceIdentity) bool {
	return value.pid == other.pid && value.tgid == other.tgid && value.parent == other.parent && value.tracer == other.tracer && value.sid == other.sid && value.birth == other.birth && value.namespace != nil && other.namespace != nil && os.SameFile(value.namespace, other.namespace)
}

// Ordinary actor probes retry only actual EINTR on the same fd and the same
// existing turn context. No syscall attempt introduces another timer/budget.
func linuxEpochTracePoll(ctx context.Context, fd int) (int16, error) {
	return linuxEpochTracePollResult(linuxEpochClassifierPoll(ctx, fd))
}

// Cleanup has no operational probe context. Make exactly one actual attempt;
// its caller may discard only EINTR at its existing fifty-millisecond wait.
func linuxEpochTracePollOnce(fd int) (int16, error) {
	return linuxEpochTracePollResult(linuxEpochClassifierPollOnce(context.Background(), fd))
}

func linuxEpochTracePollResult(result linuxEpochClassifierPollResult) (int16, error) {
	validCount := result.n == 0 && result.revents == 0 || result.n == 1 && result.revents != 0
	if result.syscallErr != nil || result.contextErr != nil || !validCount || result.revents&^int16(unix.POLLIN|unix.POLLHUP) != 0 {
		return 0, fmt.Errorf("%s: %w", result.refusal("trace/pidfd"), errors.Join(ErrExecutionEpochOne, result.syscallErr, result.contextErr))
	}
	return result.revents, nil
}

// Only genuine held resource admission can issue this owner. It neither owns
// caller-supplied observations nor creates image authority from a PID/name.
type linuxEpochTraceOwner struct {
	admission     *linuxEpochResourceAdmission
	held          *linuxEpochHeldFlow
	mu            sync.Mutex
	roots         map[*linuxEpochTraceRoot]bool
	closed        bool
	firstFailure  linuxEpochTraceFailure
	tids          int
	leaders       map[int]*linuxEpochTraceLeader // probeGate protects publication/retirement.
	hardRoot      *linuxEpochTraceRoot           // Same gate; at most one selected root-first cohort.
	notify        chan os.Signal
	stop, stopped chan struct{}
	stopOnce      sync.Once
}
type linuxEpochTraceLeader struct {
	root               *linuxEpochTraceRoot
	original           linuxEpochTraceIdentity
	fd                 int
	parentBirth        string
	verified           bool // Initial inherited/exec image checked before continuation.
	termSent, killSent bool
	exiting            bool
}
type linuxEpochTraceTask struct {
	original   linuxEpochTraceIdentity
	initial    bool
	reconciled bool // Current dying-group stop; no recovered creation history.
	leader     *linuxEpochTraceLeader
	engineHeld bool // An actually consumed group-stop in the one measurement lease.
}
type linuxEpochTraceEvent struct {
	tid, group, event int
	birth             string
}
type linuxEpochTraceRoot struct {
	owner                                  *linuxEpochTraceOwner
	command                                *exec.Cmd
	parent                                 *dispatchadmission.LocalProducer
	phase                                  uint32
	site                                   dispatchadmission.Site
	image                                  *os.File
	handle                                 dispatchadmission.Handle // Only the locked creating actor may Wait.
	pidfd, tid                             int
	currentTID                             int
	original                               linuxEpochTraceIdentity
	tasks                                  map[int]*linuxEpochTraceTask // Actor-private kernel wait ownership.
	wake, stop                             chan struct{}
	started                                chan error
	done                                   chan error
	events                                 uint64
	recent                                 [linuxEpochTraceRecentEvents]linuxEpochTraceEvent
	err                                    error
	completionErr                          error
	death                                  executionProcessDeath // Published only through done after the creating actor's sole Wait.
	startedNative, waitReturned, gateOwned bool
	continued                              bool
	serverRun                              *ExecutionEpochOneRun
	observerReady, observerDone, exited    chan struct{}
	exitSeen, stopping, graceful, unknown  bool
	observerJoined                         bool
	detached                               map[int]linuxEpochTraceIdentity // Witnessed EXIT, bounded current records.
	pending                                map[int]*linuxEpochTraceLeader
	stopCallback                           func() bool
	callbackDone                           chan struct{}
	grace                                  chan struct{}
	engineRequests                         chan *linuxEpochEngineRequest
	engineDone                             chan struct{}
	engineLease                            *linuxEpochEngineLease // Creating actor only.
	hardRequests                           chan *linuxEpochHardDeathRequest
	hardDeathIssued                        bool // Only after this actor's original-root pidfd SIGKILL succeeds.
	hardAnchorProven                       bool // Actual terminal CLD; original pidfd must remain IN without HUP.
}

func newLinuxEpochTraceOwner(admission *linuxEpochResourceAdmission) (*linuxEpochTraceOwner, error) {
	if admission == nil {
		return nil, ErrExecutionEpochOne
	}
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if !admission.issued || admission.closed || admission.err != nil || admission.closeErr != nil || admission.trace != nil || admission.policySHA != linuxEpochPolicySHA256() || admission.held == nil || admission.held.resources != admission || admission.held.flow == nil || admission.held.flow.held != admission.held || admission.held.flow.core == nil || admission.held.flow.core.parent == nil || !admission.held.flow.core.parent.OnController(admission.held.flow.core.controller) {
		return nil, ErrExecutionEpochOne
	}
	return allocateLinuxEpochTraceOwner(admission), nil
}

// Allocation supplies notification mechanics, not admission. The sole runtime
// caller above checks genuine issuance; mechanical own-image tests call this
// lower layer without invoking the bound production Start entry.
func allocateLinuxEpochTraceOwner(admission *linuxEpochResourceAdmission) *linuxEpochTraceOwner {
	owner := &linuxEpochTraceOwner{admission: admission, held: admission.held, roots: make(map[*linuxEpochTraceRoot]bool), leaders: make(map[int]*linuxEpochTraceLeader), notify: make(chan os.Signal, 1), stop: make(chan struct{}), stopped: make(chan struct{})}
	// Notify is a scoped Go subscription, not a signal mask/ptrace entitlement.
	// Unrelated SIGCHLD only wakes targeted reads; it grants no PID authority.
	signal.Notify(owner.notify, syscall.SIGCHLD)
	go func() {
		defer close(owner.stopped)
		for {
			select {
			case <-owner.notify:
				owner.mu.Lock()
				for root := range owner.roots {
					select {
					case root.wake <- struct{}{}:
					default:
					}
				}
				owner.mu.Unlock()
			case <-owner.stop:
				return
			}
		}
	}()
	return owner
}
func (owner *linuxEpochTraceOwner) close() error {
	if owner == nil {
		return nil
	}
	select {
	case owner.admission.probeGate <- struct{}{}:
	default:
		return ErrExecutionEpochOne
	}
	owner.mu.Lock()
	active := len(owner.roots) != 0 || owner.tids != 0 || len(owner.leaders) != 0
	if !active {
		owner.closed = true
	}
	owner.mu.Unlock()
	<-owner.admission.probeGate
	if active {
		return ErrExecutionEpochOne
	}
	owner.stopOnce.Do(func() { signal.Stop(owner.notify); close(owner.stop); <-owner.stopped })
	return nil
}
func (root *linuxEpochTraceRoot) requestStop() {
	if root == nil {
		return
	}
	select {
	case root.stop <- struct{}{}:
	default:
	}
	select {
	case root.wake <- struct{}{}:
	default:
	}
}
func (root *linuxEpochTraceRoot) record(tid, group, event int, birth string) error {
	if root.events == math.MaxUint64 {
		return ErrExecutionEpochOne
	}
	root.recent[root.events%linuxEpochTraceRecentEvents] = linuxEpochTraceEvent{tid: tid, group: group, event: event, birth: birth}
	root.events++
	return nil
}

// Private bounded first-cause evidence only. Snapshot publication uses mu;
// callers never read an unjoined actor's maps/counters directly.
type linuxEpochTraceFailure struct {
	Stage, Cause                                               string
	RootPID, TracerTID, CurrentTID, Tasks, Pending, ActiveTIDs int
	Events                                                     uint64
	Recent                                                     [linuxEpochTraceRecentEvents]linuxEpochTraceEvent
}

func (owner *linuxEpochTraceOwner) failure() linuxEpochTraceFailure {
	if owner == nil {
		return linuxEpochTraceFailure{}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.firstFailure
}
func (root *linuxEpochTraceRoot) refuse(stage string, causes ...error) {
	if root.err == nil {
		root.err = fmt.Errorf("linux epoch trace %s: %w", stage, ErrExecutionEpochOne)
	}
	cause := errors.Join(causes...)
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	root.owner.mu.Lock()
	if root.owner.firstFailure.Stage == "" {
		pid := 0
		if root.command != nil && root.command.Process != nil {
			pid = root.command.Process.Pid
		}
		root.owner.firstFailure = linuxEpochTraceFailure{Stage: stage, Cause: strings.Clone(detail[:min(len(detail), maxPrivateProcessRefusalBytes)]), RootPID: pid, TracerTID: root.tid, CurrentTID: root.currentTID, Tasks: len(root.tasks), Pending: len(root.pending), ActiveTIDs: root.owner.tids, Events: root.events, Recent: root.recent}
	}
	root.owner.mu.Unlock()
	root.owner.admission.failDetail("trace/" + stage)
}
