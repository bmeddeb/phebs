//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

func (custody *LinuxExecutionGoBuildCustody) runGoReferenceAttempt(ctx context.Context, attempt *linuxGoReferenceAttempt, helper *LinuxGoReferenceHelper) {
	defer completeInputTreeConstruction(attempt.done, attempt.cancel)
	defer helper.mu.Unlock()
	err := custody.tree.WithRoot(ctx, func(root *os.File) error {
		return helper.tool.WithImage(ctx, linuxGoReferenceRole, func(_ ExecutionToolIdentity, image *os.File) error {
			return attempt.tool.input.WithInput(ctx, attempt.plan.Role, func(candidate *os.File) error {
				var err error
				attempt.plan.CandidateSHA256, err = digestExecutionReferenceFD(ctx, candidate)
				if err != nil || attempt.checkStage(custody) != nil {
					return ErrExecutionGoBuildCustody
				}
				return runGoReferenceNamespace(ctx, attempt, root, image, candidate)
			})
		})
	})
	if err != nil || attempt.tree == nil || !attempt.tree.joined || !attempt.tree.empty || attempt.tree.root == nil {
		attempt.err = ErrExecutionGoBuildCustody
	}
}

func runGoReferenceNamespace(ctx context.Context, attempt *linuxGoReferenceAttempt, root, image, candidate *os.File) (retErr error) {
	attempt.step = "original-snapshot"
	originals, err := snapshotGoReferenceOriginals(attempt.plan)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	parent, child := os.NewFile(uintptr(pair[0]), "go-reference-controller"), os.NewFile(uintptr(pair[1]), "go-reference-helper")
	work, err := attempt.stage.Open("work")
	if err != nil {
		_ = parent.Close()
		_ = child.Close()
		return ErrExecutionGoBuildCustody
	}
	defer func() {
		parentErr, workErr := parent.Close(), work.Close()
		if parentErr != nil || workErr != nil {
			retErr = ErrExecutionGoBuildCustody
		}
		if child != nil && child.Close() != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	if unix.SetsockoptInt(pair[0], unix.SOL_SOCKET, unix.SO_PASSCRED, 1) != nil {
		return ErrExecutionGoBuildCustody
	}
	tree := attempt.tree
	command := exec.Command("/proc/self/fd/3", linuxGoReferenceRecipe)
	command.ExtraFiles, command.Dir, command.Env = []*os.File{image, child, tree.stage, work, candidate}, tree.stagePath, []string{"GOMAXPROCS=2"}
	command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS | unix.CLONE_NEWPID,
		Setsid: true, Pdeathsig: syscall.SIGKILL, UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Geteuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getegid(), Size: 1}}, GidMappingsEnableSetgroups: false}
	stdout, stderr := checkoutCommandOutput{remaining: 64 << 10, cancel: attempt.cancel}, checkoutCommandOutput{remaining: 1 << 20, cancel: attempt.cancel}
	command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	attempt.step = "start"
	if command.Start() != nil {
		return ErrExecutionGoBuildCustody
	}
	tree.pid = command.Process.Pid
	pidfd, pidfdErr, waited := pinExecutionProcessRoot(command)
	if pidfdErr == nil {
		defer func() {
			if unix.Close(pidfd) != nil {
				retErr = ErrExecutionGoBuildCustody
			}
		}()
	}
	useErr := func() error {
		attempt.step = "child-control"
		closeErr := child.Close()
		child = nil
		if closeErr != nil || pidfdErr != nil {
			return ErrExecutionGoBuildCustody
		}
		attempt.step = "initial-namespaces"
		namespaces, err := observeGoReferenceStart(ctx, tree.pid, image)
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		wire := &inputTreeSocket{fd: pair[0], peer: &unix.Ucred{Pid: int32(tree.pid), Uid: uint32(os.Geteuid()), Gid: uint32(os.Getegid())}}
		initial := make([]byte, 48)
		deadline, _ := ctx.Deadline()
		binary.BigEndian.PutUint64(initial, uint64(deadline.UnixNano()))
		binary.BigEndian.PutUint32(initial[8:], uint32(os.Geteuid()))
		binary.BigEndian.PutUint32(initial[12:], uint32(os.Getegid()))
		attempt.step = "init-plan"
		if _, err := rand.Read(initial[16:]); err != nil || wire.send(ctx, treeWireInit, initial, nil) != nil || sendGoReferencePlan(ctx, wire, attempt.plan) != nil {
			return ErrExecutionGoBuildCustody
		}
		attempt.step = "input-transfer"
		census, err := sendGoReferenceHeldInputs(ctx, wire, root, attempt.input)
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		attempt.goDigest = goReferenceCensusDriver(census)
		attempt.step = "ready-receive"
		ready, view, err := wire.receive(ctx, treeWireReady, true)
		tree.root = view // Own every delivered descriptor before validation.
		expected := append(bytes.Clone(initial[16:]), inputTreeManifest(census)...)
		if err != nil || !bytes.Equal(ready, expected) || !validExecutionSHA256(attempt.goDigest) {
			return ErrExecutionGoBuildCustody
		}
		attempt.step = "ready-kernel-masks"
		if observeGoReferenceReady(ctx, attempt, image, namespaces, originals) != nil || wire.send(ctx, treeWireAck, initial[16:], nil) != nil {
			return ErrExecutionGoBuildCustody
		}
		attempt.step = "build-result"
		result, _, err := wire.receive(ctx, treeWireEnd, false)
		if err != nil || len(result) != 128 || !bytes.Equal(result[:96], expected) {
			return ErrExecutionGoBuildCustody
		}
		attempt.digest = "sha256:" + hex.EncodeToString(result[96:])
		if attempt.digest != attempt.plan.CandidateSHA256 || wire.send(ctx, treeWireAck, initial[16:], nil) != nil {
			return ErrExecutionGoBuildCustody
		}
		return nil
	}()
	deadline, _ := ctx.Deadline()
	if useErr != nil || ctx.Err() != nil {
		if pidfdErr == nil {
			_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		}
		deadline = time.Now().Add(5 * time.Second)
	}
	waitErr, joined := waitExecutionProcessRoot(waited, deadline)
	if !joined {
		if pidfdErr == nil {
			_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
		}
		waitErr = <-waited // Keep every loan and the launching thread retained.
		useErr = ErrExecutionGoBuildCustody
	}
	tree.joined = true
	for t4013.WaitPrivateProcessSession(tree.pid, time.Now().Add(5*time.Second)) != nil {
		time.Sleep(5 * time.Second) // No post-Wait bare-PID signal or assumed drain.
	}
	tree.empty = true
	attempt.stderr = bytes.Clone(stderr.buffer.Bytes())
	attempt.commandStderr = readGoReferenceCommandFailure(attempt)
	if waitErr != nil {
		attempt.waitError = waitErr.Error()
	}
	if useErr != nil || waitErr != nil || ctx.Err() != nil || stdout.err != nil || stderr.err != nil || stdout.buffer.Len() != 0 {
		return ErrExecutionGoBuildCustody
	}
	attempt.step = "joined"
	return nil
}

type goReferenceOriginal struct {
	path string
	stat unix.Stat_t
}

func snapshotGoReferenceOriginals(plan linuxGoReferencePlan) ([]goReferenceOriginal, error) {
	var originals []goReferenceOriginal
	for _, path := range []string{plan.OriginalSDK, plan.OriginalModules, plan.OriginalSource} {
		canonical, err := filepath.EvalSymlinks(path)
		var stat unix.Stat_t
		if !executionGitAbsolutePath(path) || err != nil || canonical != path || unix.Lstat(path, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return nil, ErrExecutionGoBuildCustody
		}
		originals = append(originals, goReferenceOriginal{path: path, stat: stat})
	}
	return originals, nil
}

func observeGoReferenceStart(ctx context.Context, pid int, image *os.File) (inputTreeNamespaces, error) {
	namespaces, err := observeInputTreeNamespaces(pid)
	if err != nil || t4013.MatchLinuxProcessExecutableImage(ctx, pid, image) != nil {
		return namespaces, ErrExecutionGoBuildCustody
	}
	var self, child unix.Stat_t
	if unix.Stat("/proc/self/ns/pid", &self) != nil || unix.Stat(fmt.Sprintf("/proc/%d/ns/pid", pid), &child) != nil || self.Ino == child.Ino && self.Dev == child.Dev {
		return namespaces, ErrExecutionGoBuildCustody
	}
	return namespaces, nil
}

func observeGoReferenceReady(ctx context.Context, attempt *linuxGoReferenceAttempt, image *os.File, before inputTreeNamespaces, originals []goReferenceOriginal) error {
	tree := attempt.tree
	attempt.step = "ready-namespaces"
	after, err := observeInputTreeNamespaces(tree.pid)
	if err != nil || after != before {
		return ErrExecutionGoBuildCustody
	}
	attempt.step = "ready-stage"
	if tree.checkStage() != nil {
		return ErrExecutionGoBuildCustody
	}
	attempt.step = "ready-privileges"
	if observeInputTreeDroppedPrivileges(tree.pid) != nil {
		return ErrExecutionGoBuildCustody
	}
	attempt.step = "ready-image"
	if t4013.MatchLinuxProcessExecutableImage(ctx, tree.pid, image) != nil {
		return ErrExecutionGoBuildCustody
	}
	attempt.step = "ready-root"
	tree.kernel, err = observeInputTreeRoot(tree.root)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	attempt.step = "ready-mount"
	if observeInputTreeHelperMount(tree.pid, tree.stagePath, tree.kernel.mount) != nil {
		return ErrExecutionGoBuildCustody
	}
	for index, original := range originals {
		attempt.step = fmt.Sprintf("ready-original-mask-%d", index)
		path := fmt.Sprintf("/proc/%d/root%s", tree.pid, original.path)
		var masked unix.Stat_t
		var filesystem unix.Statfs_t
		const flags = unix.ST_RDONLY | unix.ST_NOSUID | unix.ST_NODEV | unix.ST_NOEXEC
		if unix.Lstat(path, &masked) != nil || unix.Statfs(path, &filesystem) != nil || masked.Mode != unix.S_IFDIR || masked.Dev == original.stat.Dev ||
			filesystem.Type != unix.TMPFS_MAGIC || filesystem.Flags&flags != flags {
			return ErrExecutionGoBuildCustody
		}
	}
	return nil
}

// Private diagnostic bytes are read only after native Wait has joined every
// output pump. Public errors and returned identities never contain these bytes.
func readGoReferenceCommandFailure(attempt *linuxGoReferenceAttempt) []byte {
	file, err := attempt.stage.OpenFile("work/command.stderr", os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || !inputCustodyCopyOwned(info) || info.Size() < 0 || info.Size() > 64<<10 {
		_ = file.Close()
		return nil
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(raw)) != info.Size() {
		return nil
	}
	return raw
}
