//go:build linux

package t421

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func validLinuxExecutorPrework(value linuxExecutorPreworkSelection, entered time.Time) bool {
	if value.Schema != executionLinuxPreworkSchema || entered.IsZero() || value.DeadlineUnixNano <= entered.UnixNano() ||
		value.DeadlineUnixNano > entered.Add(executionLinuxPreworkMaximum).UnixNano() || value.ParentPID <= 0 ||
		!validLinuxExecutorStart(value.ParentStart) || value.ReadInode == 0 {
		return false
	}
	if !validLinuxExecutorAttempt(value.Attempt) {
		return false
	}
	if !validExecutionSHA256(value.ExpectedInput.ManifestSHA256) || !validExecutionSHA256(value.ExpectedInput.HelperSHA256) ||
		!validExecutionSHA256(value.ExpectedGoSHA256) || value.ExpectedInput.Entries == 0 || value.ExpectedInput.Files == 0 ||
		value.ExpectedInput.Files > value.ExpectedInput.Entries || value.ExpectedInput.Bytes == 0 {
		return false
	}
	for _, commit := range []string{value.PlanSourceCommit, value.IntegratedMainCommit, value.SourceCommit} {
		if !validCommit(commit) {
			return false
		}
	}
	for _, path := range []string{value.PreparationRoot, value.RepositoryRoot, value.GoRoot, value.ModuleCache, value.GitBinary, value.InputHelperBinary, value.ReferenceHelperBinary} {
		canonical, err := filepath.EvalSymlinks(path)
		if !executionGitAbsolutePath(path) || err != nil || canonical != path {
			return false
		}
	}
	for _, path := range []string{value.RepositoryRoot, value.GoRoot, value.ModuleCache} {
		if inputTreePathsOverlap(value.PreparationRoot, path) {
			return false
		}
	}
	return linuxGitPrivateDirectory(value.PreparationRoot)
}

func bindLinuxExecutorPrework(ctx context.Context, value linuxExecutorPreworkSelection, read *os.File) (linuxExecutorPreworkSelection, error) {
	parent, err := linuxExecutorRecord(ctx, os.Getpid(), os.Getpid())
	var stat unix.Stat_t
	if err != nil || read == nil || unix.Fstat(int(read.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFIFO {
		return value, ErrExecutionLauncher
	}
	value.ParentPID, value.ParentStart = parent.PID, parent.StartIdentity
	value.ReadDevice, value.ReadInode = uint64(stat.Dev), stat.Ino
	return value, nil
}

func linuxExecutorPreworkCommand(ctx context.Context, image, read *os.File, mode string, value linuxExecutorPreworkSelection) (*exec.Cmd, error) {
	encoded, err := encodeLinuxExecutorPrework(value)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", mode, "--selection-base64url", encoded)
	command.ExtraFiles = []*os.File{image, read}
	command.Env = []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	command.Dir = value.PreparationRoot
	// The actual image handles SIGINT by canceling its own source-owned work.
	// Wait remains the sole retirement authority, including after cancellation.
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	return command, nil
}

// The caller's source/root/image loans remain held after Run returns an unknown
// prefix. Only Close, after actual Wait/control/session retirement, releases them.
type linuxExecutorPrework struct {
	mu                      sync.Mutex
	used                    atomic.Bool
	builds                  *LinuxExecutionGoBuildCustody
	tool                    *LinuxExecutionToolCustody
	selection               linuxExecutorPreworkSelection
	command                 *exec.Cmd
	writer                  *os.File
	result, joined, release chan struct{}
	resultErr, loanErr      error
	closeOnce               sync.Once
	released                bool
	releaseRequested        bool
	retired                 bool
	caller                  context.Context
	settled                 bool
	finalErr                error
	self                    *linuxExecutorPrework
	claim                   *linuxExecutorPreworkClaim
	observation             *executionPreworkObservation
	pressure                *linuxPressureExecutorPrework
}

func newLinuxExecutorPrework(builds *LinuxExecutionGoBuildCustody, tool *LinuxExecutionToolCustody, value linuxExecutorPreworkSelection) *linuxExecutorPrework {
	owner := &linuxExecutorPrework{builds: builds, tool: tool, selection: value, result: make(chan struct{}), joined: make(chan struct{}), release: make(chan struct{}), observation: &executionPreworkObservation{}}
	owner.self = owner
	if tool != nil {
		owner.claim = tool.executorClaim
		owner.claim.bindOuter(owner)
	}
	return owner
}

func (owner *linuxExecutorPrework) Run(ctx context.Context) error {
	if owner == nil || !owner.used.CompareAndSwap(false, true) {
		return ErrExecutionLauncher
	}
	claimed := owner.claim.consumeOuter(owner)
	if owner.self != owner {
		return ErrExecutionLauncher
	}
	if !claimed {
		owner.resultErr = ErrExecutionLauncher
		close(owner.result)
		close(owner.joined)
		return ErrExecutionLauncher
	}
	if ctx == nil || ctx.Err() != nil || runtime.GOARCH != "amd64" || owner.builds == nil || owner.tool == nil {
		owner.resultErr = ErrExecutionLauncher
		close(owner.result)
		close(owner.joined)
		return ErrExecutionLauncher
	}
	if owner.pressure != nil && (os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 || !linuxPressureBorrowBounding()) {
		owner.resultErr = ErrExecutionLauncher
		close(owner.result)
		close(owner.joined)
		return ErrExecutionLauncher
	}
	deadline := time.Now().Add(executionLinuxPreworkMaximum)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	owner.selection.Schema, owner.selection.DeadlineUnixNano = executionLinuxPreworkSchema, deadline.UnixNano()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		owner.resultErr = ErrExecutionLauncher
		close(owner.result)
		close(owner.joined)
		return ErrExecutionLauncher
	}
	owner.selection.Attempt = hex.EncodeToString(nonce[:])
	owner.caller = ctx
	work, cancel := context.WithDeadline(ctx, deadline)
	go owner.run(work, cancel)
	select {
	case <-owner.result:
		return owner.resultErr
	case <-ctx.Done():
		return ErrExecutionLauncher
	}
}

func (owner *linuxExecutorPrework) run(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()
	defer close(owner.joined)
	resultPublished := false
	publish := func(err error) {
		owner.resultErr = err
		resultPublished = true
		close(owner.result)
	}
	owner.builds.mu.Lock()
	defer owner.builds.mu.Unlock()
	builds := owner.builds
	if builds.closing || builds.released || builds.err != nil || builds.active != nil || builds.tree == nil ||
		owner.tool.referenceInputs != builds || owner.tool.identity.Role != "t422-execute" || owner.tool.identity.BuildVCSRevision != builds.request.SourceCommit ||
		owner.selection.SourceCommit != builds.request.SourceCommit || owner.selection.PlanSourceCommit != builds.request.PlanSourceCommit ||
		owner.selection.IntegratedMainCommit != builds.request.IntegratedMainCommit || owner.selection.RepositoryRoot != builds.request.RepositoryRoot ||
		owner.selection.GoRoot != builds.request.GoRoot || owner.selection.ModuleCache != builds.request.ModuleCache {
		publish(ErrExecutionLauncher)
		return
	}
	input, err := builds.tree.Check(ctx)
	if err != nil || input != builds.last.input || !builds.last.joined || !builds.last.empty || builds.last.pid <= 0 ||
		builds.last.toolDigest != owner.tool.identity.SHA256 || builds.last.goDigest != builds.goIdentity.SHA256 || !validExecutionSHA256(builds.last.goDigest) {
		publish(ErrExecutionLauncher)
		return
	}
	owner.selection.ExpectedInput, owner.selection.ExpectedGoSHA256 = linuxExecutorPreworkInputFacts(input), builds.last.goDigest
	owner.loanErr = builds.tree.WithRoot(context.Background(), func(*os.File) error {
		return owner.tool.WithImage(context.Background(), "t422-execute", func(_ ExecutionToolIdentity, image *os.File) error {
			read, writer, err := os.Pipe()
			if err != nil {
				return ErrExecutionLauncher
			}
			readOpen := true
			defer func() {
				if readOpen {
					_ = owner.observation.close(read.Close())
				}
			}()
			owner.writer = writer
			value, err := bindLinuxExecutorPrework(ctx, owner.selection, read)
			if err != nil || !validLinuxExecutorPrework(value, time.Now()) {
				return ErrExecutionLauncher
			}
			command, err := linuxExecutorPreworkCommand(ctx, image, read, executionLinuxPreworkOuterMode, value)
			if owner.pressure != nil {
				command, err = owner.pressure.command(ctx, image, read, value, owner.observation)
			}
			if err != nil {
				return err
			}
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			stdout := checkoutCommandOutput{remaining: 128, cancel: cancel}
			stderr := checkoutCommandOutput{remaining: 1024, cancel: cancel}
			command.Stdout, command.Stderr = &stdout, &stderr
			owner.command = command
			if command.Start() != nil {
				return ErrExecutionLauncher
			}
			readErr := owner.observation.close(read.Close()) // No extra receiver in the caller after actual Start.
			readOpen = false
			waitErr := command.Wait()
			owner.retired = readErr == nil && stdout.err == nil && linuxExecutorPreworkRetired(stdout.buffer.String(), value.Attempt)
			success := stdout.buffer.String() == linuxExecutorPreworkReceipt(value.Attempt, false)
			if owner.pressure != nil {
				owner.retired = readErr == nil && stdout.err == nil && owner.pressure.accept(stdout.buffer.String(), value.Attempt)
				success = stdout.buffer.String() == linuxPressureBorrowReceipt(value.Attempt, false)
			}
			err = nil
			if waitErr != nil || command.ProcessState == nil || !command.ProcessState.Success() || ctx.Err() != nil ||
				stdout.err != nil || stderr.err != nil || stderr.buffer.Len() != 0 || !success {
				err = ErrExecutionLauncher
			}
			publish(err)
			<-owner.release
			return nil // Operational failure is separate from protection validation.
		})
	})
	if !resultPublished {
		publish(ErrExecutionLauncher)
	}
}

func (owner *linuxExecutorPrework) Close(ctx context.Context) error {
	if owner == nil || ctx == nil || !owner.used.Load() || owner.self != owner {
		return ErrExecutionLauncher
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.settled {
		return owner.finalErr
	}
	select {
	case <-owner.result:
	case <-ctx.Done():
		return ErrExecutionLauncher
	}
	if !owner.releaseRequested {
		if owner.command != nil && owner.command.Process != nil {
			if owner.command.ProcessState == nil || !owner.retired {
				return ErrExecutionLauncher
			}
			members, err := t4013.PrivateProcessSessionMembership(owner.command.Process.Pid)
			if err != nil || len(members) != 0 {
				return ErrExecutionLauncher
			}
		}
		if owner.pressure != nil && owner.pressure.closeNamespace() != nil {
			return ErrExecutionLauncher
		}
		if owner.writer != nil && owner.observation.close(owner.writer.Close()) != nil {
			return ErrExecutionLauncher
		}
		owner.closeOnce.Do(func() { close(owner.release) })
		owner.releaseRequested = true
	}
	select {
	case <-owner.joined:
	case <-ctx.Done():
		return ErrExecutionLauncher
	}
	if !owner.observation.settle(ctx) {
		owner.finalErr, owner.settled = ErrExecutionLauncher, true
		return owner.finalErr
	}
	if owner.tool != nil && owner.tool.input != nil {
		owner.tool.input.mu.Lock()
		failed := owner.tool.input.closeErr != nil
		owner.tool.input.mu.Unlock()
		if failed {
			owner.finalErr, owner.settled = ErrExecutionLauncher, true
			return owner.finalErr
		}
	}
	if owner.builds != nil && owner.builds.tree != nil {
		owner.builds.tree.mu.Lock()
		failed := owner.builds.tree.closeErr != nil
		owner.builds.tree.mu.Unlock()
		if failed {
			owner.finalErr, owner.settled = ErrExecutionLauncher, true
			return owner.finalErr
		}
	}
	owner.released = true
	if owner.caller != nil && (owner.caller.Err() != nil || !time.Now().Before(time.Unix(0, owner.selection.DeadlineUnixNano))) {
		owner.finalErr = ErrExecutionLauncher
	} else {
		owner.finalErr = errors.Join(owner.resultErr, owner.loanErr)
	}
	owner.settled = true
	return owner.finalErr
}

func linuxExecutorPreworkInputFacts(input LinuxInputTreeIdentity) linuxExecutorPreworkInput {
	return linuxExecutorPreworkInput(input)
}

func runLinuxExecutionPreworkCommand(ctx context.Context, entered time.Time, args, environment []string) error {
	if runtime.GOARCH != "amd64" || ctx == nil || ctx.Err() != nil || len(args) != 4 || args[2] != "--selection-base64url" {
		return ErrExecutionLauncher
	}
	for _, value := range environment {
		if strings.HasPrefix(value, executionLivenessEnvironment+"=") {
			return ErrExecutionLauncher
		}
	}
	value, err := decodeLinuxExecutorPrework(args[3])
	if err != nil || !validLinuxExecutorPrework(value, entered) {
		return ErrExecutionLauncher
	}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(0, value.DeadlineUnixNano))
	defer cancel()
	observation := &executionPreworkObservation{}
	failureAttempted := false
	unix.CloseOnExec(3)
	unix.CloseOnExec(4)
	// NewFile registers the pipe with Go's poller only when it is already
	// nonblocking; setting the flag afterward cannot enable read deadlines.
	if unix.SetNonblock(4, true) != nil {
		readErr, imageErr := observation.close(unix.Close(4)), observation.close(unix.Close(3))
		_ = writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value, observation, nil)
		if readErr == nil && imageErr == nil {
			_, _ = os.Stdout.WriteString(linuxExecutorPreworkReceipt(value.Attempt, true))
		}
		return ErrExecutionLauncher
	}
	image, read := os.NewFile(3, "executor-image"), os.NewFile(4, "executor-parent")
	parent, err := adoptLinuxExecutorParentObserved(ctx, value, image, read, args[1] == executionLinuxPreworkInnerMode, observation)
	if err != nil {
		readErr, imageErr := observation.close(read.Close()), observation.close(image.Close())
		_ = writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value, observation, nil)
		if readErr == nil && imageErr == nil && observation.settle(context.Background()) {
			_, _ = os.Stdout.WriteString(linuxExecutorPreworkReceipt(value.Attempt, true))
		}
		return ErrExecutionLauncher
	}
	var retired bool
	var workErr error
	if args[1] == executionLinuxPreworkOuterMode {
		retired, workErr = runLinuxExecutorPreworkOuter(parent.alive, value, parent, image)
	} else {
		retired, workErr = runLinuxExecutorPreworkInner(parent.alive, value, parent, image, &failureAttempted)
	}
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	watchErr := parent.Close()
	imageErr := observation.close(image.Close())
	if recordErr := writeLinuxExecutorPreworkFailureOnce(&failureAttempted, value, observation, nil); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	if !retired || !parent.retired || imageErr != nil || !observation.settle(context.Background()) {
		return ErrExecutionLauncher
	}
	// This original context is distinct from parent.alive, which Close cancels.
	// Cleanup cannot turn expiry, cancellation or a sticky failure into success.
	if workErr != nil || watchErr != nil || ctx.Err() != nil || !time.Now().Before(time.Unix(0, value.DeadlineUnixNano)) {
		_, _ = os.Stdout.WriteString(linuxExecutorPreworkReceipt(value.Attempt, true))
		return ErrExecutionLauncher
	}
	_, err = os.Stdout.WriteString(linuxExecutorPreworkReceipt(value.Attempt, false))
	return err
}

func runLinuxExecutorPreworkOuter(ctx context.Context, value linuxExecutorPreworkSelection, parent *linuxExecutorParent, image *os.File) (retired bool, retErr error) {
	read, writer, err := os.Pipe()
	if err != nil {
		return true, ErrExecutionLauncher
	}
	defer func() {
		if read != nil {
			if err := parent.observation.close(read.Close()); err != nil {
				retired, retErr = false, ErrExecutionLauncher
			}
		}
		if err := parent.observation.close(writer.Close()); err != nil {
			retired, retErr = false, ErrExecutionLauncher
		}
	}()
	value, err = bindLinuxExecutorPrework(ctx, value, read)
	if err != nil || parent.check(ctx) != nil {
		return true, ErrExecutionLauncher
	}
	command, err := linuxExecutorPreworkCommand(ctx, image, read, executionLinuxPreworkInnerMode, value)
	if err != nil {
		return true, err
	}
	stdout := checkoutCommandOutput{remaining: 128, cancel: parent.cancel}
	stderr := checkoutCommandOutput{remaining: 1024, cancel: parent.cancel}
	command.Stdout, command.Stderr = &stdout, &stderr
	if command.Start() != nil {
		return true, ErrExecutionLauncher
	}
	readErr := parent.observation.close(read.Close())
	read = nil
	waitErr := command.Wait()
	members, membershipErr := t4013.PrivateProcessSessionMembership(os.Getpid())
	retired = stdout.err == nil && linuxExecutorPreworkRetired(stdout.buffer.String(), value.Attempt)
	if command.ProcessState == nil || !retired || membershipErr != nil || len(members) != 1 || members[0].PID != os.Getpid() {
		return false, ErrExecutionLauncher
	}
	if readErr != nil {
		return false, ErrExecutionLauncher
	}
	if waitErr != nil || !command.ProcessState.Success() || stdout.buffer.String() != linuxExecutorPreworkReceipt(value.Attempt, false) || stderr.err != nil || stderr.buffer.Len() != 0 || parent.check(ctx) != nil {
		return true, ErrExecutionLauncher
	}
	return true, nil
}
