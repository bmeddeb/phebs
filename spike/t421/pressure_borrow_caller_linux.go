//go:build linux

package t421

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

type linuxPressureExecutorPrework struct {
	value     linuxPressureBorrowSelection
	author    *linuxPressureAuthorSelection
	namespace *linuxPressureBorrowNamespace
	retained  bool
	hold      chan struct{}
}

func newLinuxPressureExecutorPrework(builds *LinuxExecutionGoBuildCustody, tool *LinuxExecutionToolCustody, value linuxPressureBorrowSelection) *linuxExecutorPrework {
	owner := &linuxExecutorPrework{builds: builds, tool: tool, selection: value.Broker, result: make(chan struct{}), joined: make(chan struct{}), release: make(chan struct{}), observation: &executionPreworkObservation{},
		pressure: &linuxPressureExecutorPrework{value: value, hold: make(chan struct{})}}
	owner.self = owner
	if tool != nil {
		owner.claim = tool.executorClaim
		owner.claim.bindPressureOuter(owner)
	}
	return owner
}

func (owner *linuxPressureExecutorPrework) command(ctx context.Context, image, read *os.File, broker linuxExecutorPreworkSelection, observation *executionPreworkObservation) (*exec.Cmd, error) {
	owner.value.Broker = broker
	owner.value.Schema = executionLinuxPressureSchema
	owner.value.Target.Schema = executionLinuxPreworkSchema
	owner.value.Target.ExpectedInput, owner.value.Target.ExpectedGoSHA256 = broker.ExpectedInput, broker.ExpectedGoSHA256
	owner.value.Target.DeadlineUnixNano, owner.value.Target.Attempt = broker.DeadlineUnixNano, broker.Attempt
	if !validLinuxPressureBrokerSelection(owner.value, time.Now()) {
		return nil, ErrLinuxPressure
	}
	var err error
	owner.namespace, err = pinLinuxPressureBorrowNamespace(observation)
	if err != nil {
		return nil, err
	}
	owner.value.NamespaceDev, owner.value.NamespaceIno = owner.namespace.device, owner.namespace.ino
	encoded, err := encodeLinuxPressureBorrow(owner.value)
	mode := executionLinuxPressureBrokerMode
	if owner.author != nil {
		owner.author.Schema = executionLinuxPressureAuthorSchema
		owner.author.Borrow = owner.value
		if !validLinuxPressureAuthorSelection(*owner.author, time.Now()) {
			return nil, ErrLinuxPressure
		}
		encoded, err = encodeLinuxPressureAuthor(*owner.author)
		mode = executionLinuxPressureAuthorBrokerMode
	}
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", mode, "--selection-base64url", encoded)
	command.ExtraFiles = []*os.File{image, read, owner.namespace.file}
	command.Env = []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	command.Dir = broker.PreparationRoot
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	return command, nil
}

func (owner *linuxPressureExecutorPrework) receipt(attempt string, retained bool) string {
	if owner.author != nil {
		return linuxPressureAuthorReceipt(attempt, retained)
	}
	return linuxPressureBorrowReceipt(attempt, retained)
}

func (owner *linuxPressureExecutorPrework) accept(raw, attempt string) bool {
	if !validLinuxExecutorAttempt(attempt) || owner.namespace == nil || owner.namespace.check() != nil {
		return false
	}
	owner.retained = raw == owner.receipt(attempt, true)
	return owner.retained || raw == owner.receipt(attempt, false)
}

func (owner *linuxPressureExecutorPrework) closeNamespace() error {
	if owner.namespace == nil {
		return nil // No namespace loan was created before this unstarted refusal.
	}
	if owner.namespace.check() != nil {
		return ErrLinuxPressure
	}
	if owner.retained {
		// Failed derived fixture remains in this actual pinned namespace. This
		// original keeper is not an adoption/teardown capability and stays live.
		return nil
	}
	return owner.namespace.Close()
}

// The original admitted caller must not exit while its failed fixture depends
// on this namespace. This never waits for an invented recovery/adoption token.
// The native harness calls it after actual executor/source retirement and uses
// timeout=0. Strong reachability keeps the original pin, not merely its inode.
func (owner *linuxExecutorPrework) holdRetainedPressure() {
	if owner != nil && owner.pressure != nil && owner.pressure.retained && owner.released {
		<-owner.pressure.hold
		runtime.KeepAlive(owner)
	}
}

func validLinuxPressureBrokerSelection(value linuxPressureBorrowSelection, entered time.Time) bool {
	policy := linuxPressureBorrowPolicy{value.UID, value.GID}
	if !policy.valid() || !validLinuxPressureRequest(value.Pressure) || value.Schema != executionLinuxPressureSchema ||
		!validLinuxExecutorPrework(value.Broker, entered) || value.Target.Schema != executionLinuxPreworkSchema ||
		value.Target.DeadlineUnixNano != value.Broker.DeadlineUnixNano || value.Target.Attempt != value.Broker.Attempt ||
		value.Target.ExpectedInput != value.Broker.ExpectedInput || value.Target.ExpectedGoSHA256 != value.Broker.ExpectedGoSHA256 ||
		value.Target.PlanSourceCommit != value.Broker.PlanSourceCommit || value.Target.IntegratedMainCommit != value.Broker.IntegratedMainCommit || value.Target.SourceCommit != value.Broker.SourceCommit {
		return false
	}
	for _, path := range []string{value.Target.PreparationRoot, value.Target.RepositoryRoot, value.Target.GoRoot, value.Target.ModuleCache, value.Target.GitBinary, value.Target.InputHelperBinary, value.Target.ReferenceHelperBinary} {
		canonical, err := filepath.EvalSymlinks(path)
		if !executionGitAbsolutePath(path) || err != nil || canonical != path {
			return false
		}
	}
	for _, path := range []string{value.Target.RepositoryRoot, value.Target.GoRoot, value.Target.ModuleCache, value.Broker.PreparationRoot, value.Broker.RepositoryRoot, value.Broker.GoRoot, value.Broker.ModuleCache, value.Pressure.Parent} {
		if inputTreePathsOverlap(value.Target.PreparationRoot, path) {
			return false
		}
	}
	var stat unix.Stat_t
	return unix.Lstat(value.Target.PreparationRoot, &stat) == nil && stat.Mode == unix.S_IFDIR|0o700 && stat.Uid == value.UID && stat.Gid == value.GID
}

// Only created while the original source/tool/volume loans are held. No
// readiness flag or decoded value can construct an issuer outside that scope.
type linuxPressureBorrowIssuer struct {
	self      *linuxPressureBorrowIssuer
	used      atomic.Bool
	inner     *linuxExecutorInnerPrework
	volume    *LinuxPressureVolume
	image     *os.File
	namespace *linuxPressureBorrowNamespace
	parent    *linuxExecutorParent
	value     linuxPressureBorrowSelection
	author    *linuxPressureAuthorSelection
}
