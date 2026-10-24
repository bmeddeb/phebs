//go:build linux

package t421

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func prepareLinuxPressureReference(ctx context.Context, value linuxExecutorPreworkSelection, image *os.File, inner *linuxExecutorInnerPrework) error {
	if inner == nil || !inner.attempted.CompareAndSwap(false, true) || ctx == nil || ctx.Err() != nil || image == nil {
		return ErrLinuxPressure
	}
	if inner.prepareReference(ctx, value, image) != nil || !inner.tool.executorClaim.bindPressureInner(inner) || !inner.tool.executorClaim.consumePressureInner(inner) {
		return ErrLinuxPressure
	}
	identity, err := inner.tool.Check(ctx, "t422-execute")
	if err != nil || inner.tool.referenceInputs != inner.builds || identity.BuildVCSRevision != value.SourceCommit || identity.Provenance != "go-build-info-vcs-v1" ||
		inner.measuredInputs(ctx, value, identity) != nil || linuxExecutorImage(ctx, image) != nil {
		return ErrLinuxPressure
	}
	return nil
}

func runLinuxPressureBroker(ctx context.Context, value linuxPressureBorrowSelection, parent *linuxExecutorParent, image *os.File, namespace *linuxPressureBorrowNamespace, failureAttempted *bool) (retired, retained bool, retErr error) {
	inner := &linuxExecutorInnerPrework{observation: parent.observation}
	var volume *LinuxPressureVolume
	workErr := prepareLinuxPressureReference(ctx, value.Broker, image, inner)
	if workErr == nil {
		builds := inner.builds
		// Source -> protected tree -> tool -> pressure volume. No volume method
		// or reverse-IPC callback reenters volume.mu while WithRoot is active.
		builds.mu.Lock()
		workErr = builds.tree.WithRoot(context.Background(), func(*os.File) error {
			return inner.tool.WithImage(context.Background(), "t422-execute", func(_ ExecutionToolIdentity, candidate *os.File) error {
				left, leftErr := image.Stat()
				right, rightErr := candidate.Stat()
				if leftErr != nil || rightErr != nil || !os.SameFile(left, right) || parent.check(ctx) != nil || namespace.check() != nil ||
					t4013.MatchLinuxProcessExecutableImage(ctx, os.Getpid(), candidate) != nil || !linuxPressureBorrowBounding() {
					return ErrLinuxPressure
				}
				var err error
				volume, err = prepareLinuxPressureVolumeObserved(ctx, value.Pressure, parent.observation)
				if err != nil {
					return err
				}
				if _, err = volume.Geometry(ctx); err != nil {
					return err
				}
				return volume.WithRoot(ctx, func(workspace *os.File) error {
					issuer := &linuxPressureBorrowIssuer{inner: inner, volume: volume, image: candidate, namespace: namespace, parent: parent, value: value}
					issuer.self = issuer
					return issuer.child(ctx, workspace, failureAttempted)
				})
			})
		})
		builds.mu.Unlock()
	}
	if workErr == nil && volume != nil {
		workErr = volume.Remove(ctx)
	}
	var closeErr error
	if recordErr := writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, inner.observation, volume); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	if volume != nil {
		closeErr = volume.Close() // The original first Close, never a later nil as proof.
		_, _, removed := volume.RetainedOwner()
		retained = volume.rootCreated && !removed
	}
	if volume != nil && !linuxPressureVolumeDescriptorsRetired(volume) {
		if recordErr := writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, inner.observation, volume); recordErr != nil {
			workErr = errors.Join(workErr, recordErr)
		}
		// Keep the original graph while observing only the original formatter.
		// Initial flags/work failure stay exact; no reWait or new helper exists.
		for !linuxPressureVolumeDescriptorsRetired(volume) {
			if volume.retireOriginalPressureFormatter(context.Background()) {
				if err := volume.Close(); err != nil && closeErr == nil {
					closeErr = err
				}
				if recordErr := writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, inner.observation, volume); recordErr != nil {
					workErr = errors.Join(workErr, recordErr)
				}
			}
			time.Sleep(time.Second)
		}
	}
	if recordErr := writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, inner.observation, volume); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	closeErr = errors.Join(closeErr, inner.Close(context.Background()))
	if recordErr := writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, inner.observation, volume); recordErr != nil {
		workErr = errors.Join(workErr, recordErr)
	}
	for !inner.retired() || !inner.observation.settle(context.Background()) || volume != nil && !linuxPressureVolumeDescriptorsRetired(volume) {
		// Same original owners only; no new preparation, forced detach, retry of
		// work, or additional success time. Failed fixtures remain in the pin.
		time.Sleep(time.Second)
		if err := inner.Close(context.Background()); err != nil && closeErr == nil {
			closeErr = err
		}
		if volume != nil && !volume.closed {
			if err := volume.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
		if recordErr := writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, inner.observation, volume); recordErr != nil {
			workErr = errors.Join(workErr, recordErr)
		}
	}
	if namespace.check() != nil {
		return false, retained, ErrLinuxPressure
	}
	return true, retained, errors.Join(workErr, closeErr, parent.check(ctx))
}

func linuxPressureVolumeDescriptorsRetired(volume *LinuxPressureVolume) bool {
	volume.mu.Lock()
	defer volume.mu.Unlock()
	return volume.closed && !volume.observation.nativeCloseFailed.Load() &&
		(volume.formatterSession == 0 || volume.formatterJoined && volume.formatterEmpty || volume.formatterRetiredLater)
}

func (volume *LinuxPressureVolume) retireOriginalPressureFormatter(ctx context.Context) bool {
	volume.mu.Lock()
	defer volume.mu.Unlock()
	if volume.observation == nil || volume.observation.nativeCloseFailed.Load() {
		return false
	}
	if volume.formatterSession == 0 || volume.formatterJoined && volume.formatterEmpty || volume.formatterRetiredLater {
		return true
	}
	if !volume.observation.settle(ctx) {
		return false
	}
	volume.observation.mu.Lock()
	defer volume.observation.mu.Unlock()
	command := volume.formatterCommand
	if command == nil || command != volume.observation.unknownCommand || command.Process == nil || command.Process.Pid != volume.formatterSession ||
		command.ProcessState == nil || !volume.observation.unknownSettled {
		return false
	}
	volume.formatterRetiredLater = true
	return true
}

func (issuer *linuxPressureBorrowIssuer) child(ctx context.Context, workspace *os.File, failureAttempted *bool) (retErr error) {
	if issuer == nil || !issuer.used.CompareAndSwap(false, true) || issuer.self != issuer || issuer.volume == nil || issuer.inner == nil ||
		issuer.inner.tool == nil || issuer.inner.tool.referenceInputs != issuer.inner.builds || issuer.namespace.check() != nil || issuer.parent.check(ctx) != nil ||
		issuer.volume.observation != issuer.parent.observation || issuer.volume.workspace.file == nil || workspace == nil || os.Getuid() != 0 || os.Geteuid() != 0 || !linuxPressureBorrowBounding() {
		return ErrLinuxPressure
	}
	observation := issuer.parent.observation
	policy := linuxPressureBorrowPolicy{issuer.value.UID, issuer.value.GID}
	if !policy.valid() {
		return ErrLinuxPressure
	}
	image, err := copyLinuxPressureBorrowImage(ctx, issuer.image, policy.uid, policy.gid, observation)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, image.Close()) }()
	if image.digest != issuer.inner.tool.identity.SHA256 {
		return ErrLinuxPressure
	}
	leaf, err := createLinuxPressureBorrowLeaf(ctx, workspace, policy.uid, policy.gid, observation)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, leaf.Close()) }()
	sockets, err := newLinuxPressureBorrowSockets()
	if err != nil {
		for _, fd := range sockets {
			if fd >= 0 {
				_ = observation.close(unix.Close(fd))
			}
		}
		return err
	}
	parentSocket := os.NewFile(uintptr(sockets[0]), "pressure-broker-control")
	childSocket := os.NewFile(uintptr(sockets[1]), "pressure-child-control")
	childOpen := true
	defer func() { retErr = errors.Join(retErr, observation.close(parentSocket.Close())) }()
	defer func() {
		if childOpen {
			retErr = errors.Join(retErr, observation.close(childSocket.Close()))
		}
	}()
	read, writer, err := os.Pipe()
	if err != nil {
		return ErrLinuxPressure
	}
	readOpen := true
	writerOpen := true
	defer func() {
		if readOpen {
			retErr = errors.Join(retErr, observation.close(read.Close()))
		}
		if writerOpen {
			retErr = errors.Join(retErr, observation.close(writer.Close()))
		}
	}()
	value := issuer.value
	value.Target, err = bindLinuxExecutorPrework(ctx, value.Target, read)
	if err != nil {
		return err
	}
	value.LeafDevice, value.LeafInode, value.LeafMount, value.LeafFSID = uint64(leaf.stat.Dev), leaf.stat.Ino, leaf.mount, leaf.fsid
	encoded, err := encodeLinuxPressureBorrow(value)
	if err != nil || image.check(ctx) != nil || leaf.checkLink(ctx, workspace) != nil {
		return ErrLinuxPressure
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", executionLinuxPressureChildMode, "--selection-base64url", encoded)
	command.Env, command.Dir = []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}, "/"
	command.ExtraFiles = []*os.File{image.file, read, leaf.file, childSocket}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Credential: &syscall.Credential{Uid: policy.uid, Gid: policy.gid, Groups: []uint32{}}}
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	output := checkoutCommandOutput{remaining: 128, cancel: issuer.parent.cancel}
	errout := checkoutCommandOutput{remaining: 1024, cancel: issuer.parent.cancel}
	command.Stdout, command.Stderr = &output, &errout
	if command.Start() != nil {
		return ErrLinuxPressure
	}
	readErr := observation.close(read.Close())
	readOpen = false
	// Parent no longer holds a child protocol endpoint alias. The single
	// source-owned receiver below is independent of the liveness writer.
	childSocketErr := observation.close(childSocket.Close())
	childOpen = false
	row, rowErr := linuxPressureBorrowRecord(ctx, command.Process.Pid)
	birth, birthErr := strconv.ParseUint(row.StartIdentity, 10, 64)
	var attempt [16]byte
	decoded, decodeErr := hex.DecodeString(value.Target.Attempt)
	copy(attempt[:], decoded)
	wire := linuxPressureBorrowSocket{fd: int(parentSocket.Fd()), peer: unix.Ucred{Pid: int32(command.Process.Pid), Uid: policy.uid, Gid: policy.gid}, attempt: attempt, birth: birth, observation: observation}
	protocolErr := ErrLinuxPressure
	earlyFailure := false
	if readErr == nil && childSocketErr == nil && rowErr == nil && birthErr == nil && birth != 0 && row.ParentPID == os.Getpid() && decodeErr == nil && len(decoded) == len(attempt) {
		first, firstErr := wire.receiveChoice(ctx, linuxPressureBorrowHello, linuxPressureBorrowFailure)
		earlyFailure = firstErr == nil && first == linuxPressureBorrowFailure
		if firstErr == nil && first == linuxPressureBorrowHello && issuer.parent.check(ctx) == nil && wire.send(ctx, linuxPressureBorrowProceed) == nil {
			protocolErr = nil
		}
	}
	if protocolErr != nil && !earlyFailure {
		// EOF cancels the child's existing authenticated lifetime even if it
		// is awaiting Proceed. Keep the original deadline and sole actual Wait.
		_ = observation.close(writer.Close())
		writerOpen = false
	}
	waitErr := command.Wait() // Sole actual Wait, including every post-Start refusal.
	deadline := time.Now().Add(5 * time.Second)
	joined := command.ProcessState != nil
	finishErr := t4013.WaitPrivateProcessSession(command.Process.Pid, deadline)
	empty := finishErr == nil
	observation.process(command, joined, empty)
	if command.ProcessState == nil || !joined || !empty {
		_ = writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, observation, issuer.volume)
		for !observation.settle(context.Background()) {
			time.Sleep(time.Second)
		}
		if command.ProcessState == nil {
			for {
				time.Sleep(time.Second)
			}
		}
	}
	if finishErr != nil && waitErr == nil {
		protocolErr = ErrLinuxPressure
	}
	terminal := linuxPressureBorrowSuccess
	if !command.ProcessState.Success() {
		terminal = linuxPressureBorrowFailure
	}
	// No final ACK is needed: one bounded terminal datagram fits before exit.
	// Read the selected outcome only after actual Wait, never discard Failure.
	settlement, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var terminalErr error
	if earlyFailure {
		if terminal != linuxPressureBorrowFailure {
			terminalErr = ErrLinuxPressure
		}
	} else {
		terminalErr = wire.receive(settlement, terminal)
	}
	if terminalErr != nil {
		// A missing retirement receipt cannot release the volume/source loan.
		_ = writeLinuxExecutorPreworkFailureOnce(failureAttempted, value.Broker, observation, issuer.volume)
		for {
			time.Sleep(time.Second)
		}
	}
	if protocolErr != nil || waitErr != nil || output.err != nil || output.buffer.Len() != 0 || errout.err != nil || errout.buffer.Len() != 0 ||
		leaf.checkLink(ctx, workspace) != nil || image.check(ctx) != nil || issuer.namespace.check() != nil || issuer.parent.check(ctx) != nil {
		return ErrLinuxPressure
	}
	if leaf.Close() != nil || unix.Unlinkat(int(workspace.Fd()), linuxPressureBorrowLeafName, unix.AT_REMOVEDIR) != nil || workspace.Sync() != nil {
		return ErrLinuxPressure
	}
	return nil
}
