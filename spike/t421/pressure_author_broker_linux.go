//go:build linux

package t421

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func (issuer *linuxPressureBorrowIssuer) authorChild(ctx context.Context, workspace *os.File, failureAttempted *bool) (retErr error) {
	if issuer == nil || !issuer.used.CompareAndSwap(false, true) || issuer.self != issuer || issuer.author == nil || issuer.volume == nil || issuer.inner == nil ||
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
	authorValue := *issuer.author
	authorValue.Borrow = value
	encoded, err := encodeLinuxPressureAuthor(authorValue)
	if err != nil || image.check(ctx) != nil || leaf.checkLink(ctx, workspace) != nil {
		return ErrLinuxPressure
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", executionLinuxPressureAuthorBootstrapMode, "--selection-base64url", encoded)
	command.Env, command.Dir = []string{"LANG=C", "LC_ALL=C", "TZ=UTC", "GOMAXPROCS=2"}, "/"
	command.ExtraFiles = []*os.File{issuer.image, read, leaf.file, image.file, childSocket, issuer.namespace.file}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Cloneflags: unix.CLONE_NEWNS}
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	output := checkoutCommandOutput{remaining: 128, cancel: issuer.parent.cancel}
	errout := checkoutCommandOutput{remaining: 1024, cancel: issuer.parent.cancel}
	command.Stdout, command.Stderr = &output, &errout
	if command.Start() != nil {
		return ErrLinuxPressure
	}
	pidfd, pidfdErr := unix.PidfdOpen(command.Process.Pid, 0)
	if pidfdErr == nil {
		defer func() { retErr = errors.Join(retErr, observation.close(unix.Close(pidfd))) }()
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
	wire := linuxPressureBorrowSocket{fd: int(parentSocket.Fd()), author: true, peer: unix.Ucred{Pid: int32(command.Process.Pid), Uid: 0, Gid: 0}, attempt: attempt, birth: birth, observation: observation}
	protocolErr := ErrLinuxPressure
	earlyFailure := false
	var targetNamespace, targetView *os.File
	defer func() {
		if targetView != nil {
			retErr = errors.Join(retErr, observation.close(targetView.Close()))
		}
		if targetNamespace != nil {
			retErr = errors.Join(retErr, observation.close(targetNamespace.Close()))
		}
	}()
	if pidfdErr == nil && readErr == nil && childSocketErr == nil && rowErr == nil && birthErr == nil && birth != 0 && row.ParentPID == os.Getpid() && decodeErr == nil && len(decoded) == len(attempt) {
		first, firstErr := wire.receiveChoice(ctx, linuxPressureAuthorRootHello, linuxPressureAuthorFailure)
		earlyFailure = firstErr == nil && first == linuxPressureAuthorFailure
		if firstErr == nil && first == linuxPressureAuthorRootHello && issuer.parent.check(ctx) == nil && linuxPressureAuthorChildState(ctx, command.Process.Pid, birth, pidfd) == nil && t4013.MatchLinuxProcessExecutableImage(ctx, command.Process.Pid, issuer.image) == nil && wire.send(ctx, linuxPressureAuthorRootProceed) == nil {
			ready, readyErr := wire.receiveChoice(ctx, linuxPressureAuthorViewReady, linuxPressureAuthorFailure)
			earlyFailure = readyErr == nil && ready == linuxPressureAuthorFailure
			if readyErr == nil && ready == linuxPressureAuthorViewReady && linuxPressureAuthorChildState(ctx, command.Process.Pid, birth, pidfd) == nil {
				targetNamespace, err = os.Open(fmt.Sprintf("/proc/%d/ns/mnt", command.Process.Pid))
				if err == nil {
					targetView, err = os.Open(fmt.Sprintf("/proc/%d/root%s", command.Process.Pid, linuxPressureAuthorWork))
				}
				var ns unix.Stat_t
				if err == nil && unix.Fstat(int(targetNamespace.Fd()), &ns) == nil && (uint64(ns.Dev) != value.NamespaceDev || ns.Ino != value.NamespaceIno) && observeLinuxPressureAuthorHelperLayout(command.Process.Pid, leaf.file) == nil && wire.send(ctx, linuxPressureAuthorExecProceed) == nil {
					wire.peer.Uid, wire.peer.Gid = policy.uid, policy.gid
					hello, helloErr := wire.receiveChoice(ctx, linuxPressureAuthorTargetHello, linuxPressureAuthorFailure)
					earlyFailure = helloErr == nil && hello == linuxPressureAuthorFailure
					if helloErr == nil && hello == linuxPressureAuthorTargetHello && linuxPressureAuthorChildState(ctx, command.Process.Pid, birth, pidfd) == nil && t4013.MatchLinuxProcessExecutableImage(ctx, command.Process.Pid, image.file) == nil && linuxPressureAuthorLiveView(command.Process.Pid, targetNamespace, targetView, leaf.file) == nil && wire.send(ctx, linuxPressureAuthorTargetProceed) == nil {
						protocolErr = nil
					}
				}
			}
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
	terminal := linuxPressureAuthorSuccess
	if !command.ProcessState.Success() {
		terminal = linuxPressureAuthorFailure
	}
	// No final ACK is needed: one bounded terminal datagram fits before exit.
	// Read the selected outcome only after actual Wait, never discard Failure.
	settlement, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var terminalErr error
	if earlyFailure {
		if terminal != linuxPressureAuthorFailure {
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
	if targetView == nil || targetNamespace == nil {
		return ErrLinuxPressure
	}
	viewErr, namespaceErr := observation.close(targetView.Close()), observation.close(targetNamespace.Close())
	targetView, targetNamespace = nil, nil
	if viewErr != nil || namespaceErr != nil {
		return ErrLinuxPressure
	}
	if leaf.Close() != nil || unix.Unlinkat(int(workspace.Fd()), linuxPressureBorrowLeafName, unix.AT_REMOVEDIR) != nil || workspace.Sync() != nil {
		return ErrLinuxPressure
	}
	return nil
}

func linuxPressureAuthorChildState(ctx context.Context, pid int, birth uint64, pidfd int) error {
	poll := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	count, err := unix.Poll(poll, 0)
	if err != nil || count != 0 || poll[0].Revents != 0 {
		return ErrLinuxPressure
	}
	row, err := linuxPressureBorrowRecord(ctx, pid)
	sid, sidErr := unix.Getsid(pid)
	if err != nil || sidErr != nil || sid != pid || row.ParentPID != os.Getpid() || row.StartIdentity != strconv.FormatUint(birth, 10) {
		return ErrLinuxPressure
	}
	return nil
}

func linuxPressureAuthorLiveView(pid int, namespace, view, original *os.File) error {
	if namespace == nil || view == nil || original == nil {
		return ErrLinuxPressure
	}
	var held, current, left, right unix.Stat_t
	if unix.Fstat(int(namespace.Fd()), &held) != nil || unix.Stat(fmt.Sprintf("/proc/%d/ns/mnt", pid), &current) != nil || held.Dev != current.Dev || held.Ino != current.Ino ||
		unix.Fstat(int(view.Fd()), &left) != nil || unix.Fstat(int(original.Fd()), &right) != nil || left.Dev != right.Dev || left.Ino != right.Ino || observeLinuxPressureAuthorHelperLayout(pid, original) != nil {
		return ErrLinuxPressure
	}
	return nil
}
