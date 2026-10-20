//go:build linux && phebs_t42_native

package t421

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func TestLinuxGoBuildLifetimePidfdObservation(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "live-self-pidfd")
	if ready, err := nativeLeafExitReady(file, 0); err != nil || ready {
		_ = file.Close()
		t.Fatal("live process reported exited", ready, err)
	}
	if file.Close() != nil {
		t.Fatal("pidfd close")
	}
	for _, file := range []*os.File{nil, file} {
		if _, err := nativeLeafExitReady(file, 0); err == nil {
			t.Fatal("absent/closed pidfd accepted as an observation")
		}
	}
}

func TestLinuxGoBuildRequiredNativePIDNamespaceCancellation(t *testing.T) {
	helper := requireLinuxGoBuildHelper(t)
	var attempt *LinuxExecutionInputTree
	t.Cleanup(func() {
		if attempt != nil && !attempt.closed {
			t.Error("unjoined lifetime control retains image keeper")
			return
		}
		if helper.Close() != nil {
			t.Error("lifetime helper cleanup refused")
		}
	})
	baseline, err := goBuildImportHostRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := goBuildImportHostRoot()
		if err != nil || after != baseline {
			t.Error("host propagation changed")
		}
	})
	base, err := os.MkdirTemp("", "phebs-linux-go-lifetime-")
	if err != nil {
		t.Fatal(err)
	}
	mount, payload := filepath.Join(base, "mount"), filepath.Join(base, "mutable")
	for _, name := range []string{mount, payload} {
		if os.Mkdir(name, 0o700) != nil {
			t.Fatal("fixture directory; retain", base)
		}
	}
	if os.WriteFile(filepath.Join(payload, "sentinel"), []byte(linuxGoLifetimeSentinel), 0o400) != nil {
		t.Fatal("sentinel; retain", base)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	stage, err := os.Open(mount)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stage.Stat()
	if err != nil {
		_ = stage.Close()
		t.Fatal(err)
	}
	attempt = &LinuxExecutionInputTree{stagePath: mount, stage: stage, stageInfo: info, done: make(chan struct{}), cancel: cancel}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer closeCancel()
		_ = attempt.Close(closeCtx)
		if !attempt.closed {
			t.Error("unresolved detached leaf; retain", base)
			return
		}
		if os.RemoveAll(base) != nil {
			t.Error("fixture cleanup")
		}
	})
	go runNativeGoLifetimeAttempt(ctx, attempt, helper, payload)
	select {
	case <-attempt.done:
		if attempt.err != nil || !attempt.joined || !attempt.empty {
			t.Fatal("PID1 cancellation control refused", attempt.err)
		}
	case <-ctx.Done():
		t.Fatal("lifetime observation deadline; retained worker owns image, inputs and leaf pidfd")
	}
	t.Logf("lifetime PID1=%d actual_root_wait_sigkill=true detached_stopped_leaf_exit_ready=true root_session_empty=true", attempt.pid)
}

func runNativeGoLifetimeAttempt(ctx context.Context, tree *LinuxExecutionInputTree, helper *LinuxExecutionToolCustody, payload string) {
	defer completeInputTreeConstruction(tree.done, tree.cancel)
	roots, rows, err := gatherInputTreeSources(ctx, tree.stagePath, []LinuxInputTreeSource{{Name: "inputs", Path: payload}})
	if err != nil {
		tree.err = err
		return
	}
	defer func() {
		if closeInputTreeSources(roots) != nil {
			tree.err = ErrLinuxInputTree
		}
	}()
	err = helper.WithImage(ctx, linuxGoBuildRole, func(_ ExecutionToolIdentity, image *os.File) (retErr error) {
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
		if err != nil {
			return err
		}
		parent, child := os.NewFile(uintptr(pair[0]), "lifetime-controller"), os.NewFile(uintptr(pair[1]), "lifetime-helper")
		defer func() {
			_ = parent.Close()
			if child != nil {
				_ = child.Close()
			}
		}()
		if unix.SetsockoptInt(pair[0], unix.SOL_SOCKET, unix.SO_PASSCRED, 1) != nil {
			return ErrLinuxInputTree
		}
		command := exec.Command("/proc/self/fd/3", linuxGoLifetimeRoot)
		command.ExtraFiles, command.Dir, command.Env = []*os.File{image, child, tree.stage}, tree.stagePath, []string{"GOMAXPROCS=2"}
		command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS | unix.CLONE_NEWPID, Setsid: true, Pdeathsig: syscall.SIGKILL,
			UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}}, GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}}, GidMappingsEnableSetgroups: false}
		stderr := checkoutCommandOutput{remaining: 64 << 10, cancel: tree.cancel}
		command.Stderr, command.WaitDelay = &stderr, time.Second
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if command.Start() != nil {
			return ErrLinuxInputTree
		}
		tree.pid = command.Process.Pid
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		pidfd, pidfdErr := unix.PidfdOpen(tree.pid, 0)
		if pidfdErr == nil {
			defer func() { _ = unix.Close(pidfd) }()
		}
		var leaf *os.File
		leafAuthorized, leafValidated := false, false
		defer func() {
			if leaf != nil {
				_ = leaf.Close()
			}
		}()
		useErr := func() error {
			closeErr := child.Close()
			child = nil
			namespaces, nsErr := observeInputTreeNamespaces(tree.pid)
			var selfPIDNS, childPIDNS unix.Stat_t
			if closeErr != nil || pidfdErr != nil || nsErr != nil || t4013.MatchLinuxProcessExecutableImage(ctx, tree.pid, image) != nil ||
				unix.Stat("/proc/self/ns/pid", &selfPIDNS) != nil || unix.Stat(fmt.Sprintf("/proc/%d/ns/pid", tree.pid), &childPIDNS) != nil || selfPIDNS.Ino == childPIDNS.Ino && selfPIDNS.Dev == childPIDNS.Dev {
				return ErrLinuxInputTree
			}
			wire := &inputTreeSocket{fd: pair[0], peer: &unix.Ucred{Pid: int32(tree.pid), Uid: uint32(os.Geteuid()), Gid: uint32(os.Getegid())}}
			initial := make([]byte, 48)
			deadline, _ := ctx.Deadline()
			binary.BigEndian.PutUint64(initial, uint64(deadline.UnixNano()))
			binary.BigEndian.PutUint32(initial[8:], uint32(os.Geteuid()))
			binary.BigEndian.PutUint32(initial[12:], uint32(os.Getegid()))
			if _, err := rand.Read(initial[16:]); err != nil || wire.send(ctx, treeWireInit, initial, nil) != nil {
				return ErrLinuxInputTree
			}
			census, err := sendInputTreeSources(ctx, wire, roots, rows)
			if err != nil || wire.send(ctx, treeWireEnd, inputTreeManifest(census), nil) != nil {
				return ErrLinuxInputTree
			}
			ready, view, err := wire.receive(ctx, treeWireReady, true)
			tree.root = view
			expected := append(bytes.Clone(initial[16:]), inputTreeManifest(census)...)
			readyNS, nsErr := observeInputTreeNamespaces(tree.pid)
			if err != nil || !bytes.Equal(ready, expected) || nsErr != nil || namespaces != readyNS || tree.checkStage() != nil || observeInputTreeDroppedPrivileges(tree.pid) != nil ||
				t4013.MatchLinuxProcessExecutableImage(ctx, tree.pid, image) != nil {
				return ErrLinuxInputTree
			}
			tree.kernel, err = observeInputTreeRoot(tree.root)
			if err != nil || observeInputTreeHelperMount(tree.pid, tree.stagePath, tree.kernel.mount) != nil || closeInputTreeSources(roots) != nil {
				return ErrLinuxInputTree
			}
			roots = nil
			if os.RemoveAll(payload) != nil {
				return ErrLinuxInputTree
			}
			leafAuthorized = true // A failed send may still have reached PID1.
			if wire.send(ctx, treeWireAck, initial[16:], nil) != nil {
				return ErrLinuxInputTree
			}
			result, fd, err := wire.receive(ctx, treeWireEnd, true)
			leaf = fd // Own before every subsequent refusal.
			if err != nil || !bytes.Equal(result, expected) || checkNativeStoppedLeaf(ctx, leaf, tree.pid, image) != nil {
				return ErrLinuxInputTree
			}
			leafValidated = true
			tree.identity = LinuxInputTreeIdentity{Entries: census.inventory.entries, Files: census.inventory.files, Bytes: census.inventory.bytes, PathBytes: census.inventory.pathBytes}
			return nil
		}()
		// Cancellation is the measured action. Its success alone never releases
		// the image, mount or leaf pidfd: actual root Wait and leaf exit follow.
		signalErr := ErrLinuxInputTree
		if pidfdErr == nil {
			signalErr = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		}
		waitErr, joined := waitExecutionProcessRoot(waited, time.Now().Add(5*time.Second))
		if !joined {
			waitErr = <-waited
			joined = true
			useErr = ErrLinuxInputTree
		}
		var exited *exec.ExitError
		expectedWait := errors.As(waitErr, &exited) && exited.Sys().(syscall.WaitStatus).Signaled() && exited.Sys().(syscall.WaitStatus).Signal() == syscall.SIGKILL
		if leafAuthorized && !leafValidated {
			_, _ = fmt.Fprintln(os.Stderr, "authorized leaf identity unavailable; retain worker/image/inputs")
			select {} // Root Wait alone cannot qualify the unknown detached leaf.
		}
		if leafValidated {
			deadline := time.Now().Add(5 * time.Second)
			for {
				ready, err := nativeLeafExitReady(leaf, 50)
				if err != nil {
					_, _ = fmt.Fprintln(os.Stderr, "leaf pidfd observation unavailable; retain worker/image/inputs")
					select {} // No known leaf join: retain callback and all keepers.
				}
				if ready {
					break
				}
				if time.Now().After(deadline) {
					useErr = ErrLinuxInputTree
					_ = unix.PidfdSendSignal(int(leaf.Fd()), unix.SIGKILL, nil, 0) // Cleanup fallback invalidates the control.
				}
			}
		}
		tree.joined, tree.empty = joined, t4013.WaitPrivateProcessSession(tree.pid, time.Now().Add(5*time.Second)) == nil
		if useErr != nil || signalErr != nil || !expectedWait || !tree.empty || stderr.err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "lifetime fixture pid=%d joined=%t empty=%t protocol=%v wait=%v stderr=%q\n", tree.pid, tree.joined, tree.empty, useErr, waitErr, stderr.buffer.String())
			return ErrLinuxInputTree
		}
		return nil
	})
	if err != nil || tree.root == nil || tree.checkRoot() != nil {
		tree.err = ErrLinuxInputTree
	}
}

func nativeLeafExitReady(leaf *os.File, milliseconds int) (bool, error) {
	if leaf == nil || leaf.Fd() > 1<<31-1 {
		return false, ErrLinuxInputTree
	}
	poll := []unix.PollFd{{Fd: int32(leaf.Fd()), Events: unix.POLLIN}}
	_, err := unix.Poll(poll, milliseconds)
	if err == unix.EINTR {
		return false, nil
	}
	if err != nil || poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
		return false, ErrLinuxInputTree
	}
	return poll[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0, nil
}

func checkNativeStoppedLeaf(ctx context.Context, leaf *os.File, rootPID int, image *os.File) error {
	if leaf == nil {
		return ErrLinuxInputTree
	}
	flags, err := unix.FcntlInt(leaf.Fd(), unix.F_GETFD, 0)
	link, linkErr := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", leaf.Fd()))
	ready, pollErr := nativeLeafExitReady(leaf, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 || linkErr != nil || link != "anon_inode:[pidfd]" || pollErr != nil || ready || unix.PidfdSendSignal(int(leaf.Fd()), 0, nil, 0) != nil {
		return ErrLinuxInputTree
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", leaf.Fd()))
	if err != nil || len(raw) > 4096 {
		return ErrLinuxInputTree
	}
	pid := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if value, ok := strings.CutPrefix(line, "Pid:\t"); ok {
			if pid != 0 {
				return ErrLinuxInputTree
			}
			pid, _ = strconv.Atoi(value)
		}
	}
	if pid <= 1 || pid == rootPID || t4013.MatchLinuxProcessExecutableImage(ctx, pid, image) != nil || observeInputTreeDroppedPrivileges(pid) != nil {
		return ErrLinuxInputTree
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil || len(status) > 16<<10 {
		return ErrLinuxInputTree
	}
	stateStopped, parentMatches := false, false
	for _, line := range strings.Split(string(status), "\n") {
		if value, ok := strings.CutPrefix(line, "State:\t"); ok {
			stateStopped = strings.HasPrefix(value, "T (")
		}
		if value, ok := strings.CutPrefix(line, "PPid:\t"); ok {
			parentMatches = value == strconv.Itoa(rootPID)
		}
	}
	session, err := unix.Getsid(pid)
	if !stateStopped || !parentMatches || err != nil || session != pid || session == rootPID {
		return ErrLinuxInputTree
	}
	return nil
}
