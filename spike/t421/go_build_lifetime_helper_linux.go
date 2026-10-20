//go:build linux

package t421

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	linuxGoLifetimeRoot         = "linux-go-lifetime-root-v1"
	linuxGoLifetimeIntermediate = "linux-go-lifetime-intermediate-v1"
	linuxGoLifetimeLeaf         = "linux-go-lifetime-leaf-v1"
	linuxGoLifetimeSentinel     = "neutral protected lifetime\n"
)

// These closed modes exercise cancellation of one stopped, adopted leaf in a
// different session. They accept no command or source-program selection.
func linuxGoLifetimeHelperMain(mode string) (status int) {
	status = 125
	stage := "entry"
	defer func() {
		if status != 0 {
			_, _ = fmt.Fprintf(os.Stderr, "go-lifetime-helper refused stage=%s\n", stage)
		}
	}()
	if runtime.GOARCH != "amd64" || os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 {
		return status
	}
	for _, fd := range []int{3, 4, 5} {
		unix.CloseOnExec(fd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stage = "fixed-descendant"
	if mode == linuxGoLifetimeIntermediate {
		if os.Getpid() == 1 || os.Getppid() != 1 {
			return status
		}
		command := exec.Command("/proc/self/fd/3", linuxGoLifetimeLeaf)
		command.ExtraFiles = []*os.File{os.NewFile(3, "self-image"), os.NewFile(4, "private-leaf-wire")}
		command.Env = []string{"GOMAXPROCS=2"}
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // No parent-death signal: adoption must leave it live.
		if command.Start() != nil || command.Process.Release() != nil {
			return status
		}
		return 0 // Intentionally orphan the fixed leaf; PID1 owns its later reap.
	}
	if mode == linuxGoLifetimeLeaf {
		session, err := unix.Getsid(0)
		sentinel, readErr := os.ReadFile("inputs/sentinel")
		if os.Getpid() == 1 || err != nil || session != os.Getpid() || readErr != nil || string(sentinel) != linuxGoLifetimeSentinel || observeInputTreeDroppedPrivileges(os.Getpid()) != nil {
			return status
		}
		for os.Getppid() != 1 {
			select {
			case <-ctx.Done():
				return status
			case <-time.After(10 * time.Millisecond):
			}
		}
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(os.Getpid()))
		if (&inputTreeSocket{fd: 4}).send(ctx, treeWireReady, raw[:], nil) != nil || unix.Kill(os.Getpid(), unix.SIGSTOP) != nil {
			return status
		}
		return status // A resumed leaf can never report success.
	}
	if mode != linuxGoLifetimeRoot || os.Getpid() != 1 || os.Getppid() != 0 {
		return status
	}
	stage = "exclusive-control"
	kind, kindErr := unix.GetsockoptInt(4, unix.SOL_SOCKET, unix.SO_TYPE)
	peer, peerErr := unix.GetsockoptUcred(4, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if kindErr != nil || peerErr != nil || kind != unix.SOCK_SEQPACKET || peer.Pid != 0 || peer.Uid != 0 || peer.Gid != 0 {
		return status
	}
	wire := &inputTreeSocket{fd: 4}
	initial, err := receiveGoLifetimeInitial(ctx, wire)
	if err != nil || !inputTreeHelperMappings(initial) {
		return status
	}
	stage = "fresh-protected-tree"
	if setupInputTreeMount() != nil || unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "") != nil {
		return status
	}
	self, err := os.Readlink("/proc/self")
	if err != nil || self != "1" {
		return status
	}
	protected, err := receiveProtectedInputTree(ctx, wire)
	if err != nil {
		return status
	}
	defer func() {
		if protected.close() != nil {
			status = 126
		}
	}()
	if !goLifetimeInputShape(protected.census) {
		return status
	}
	sentinel, err := protected.root.ReadFile("inputs/sentinel")
	if err != nil || string(sentinel) != linuxGoLifetimeSentinel {
		return status
	}
	ready := append(bytes.Clone(initial[16:]), protected.manifest...)
	stage = "ready-ack"
	if wire.send(ctx, treeWireReady, ready, protected.view) != nil {
		return status
	}
	ack, _, err := wire.receive(ctx, treeWireAck, false)
	if err != nil || !bytes.Equal(ack, initial[16:]) {
		return status
	}
	stage = "adopt-stopped-leaf"
	leaf, err := goLifetimeAdoptStoppedLeaf(ctx)
	if err != nil {
		return status
	}
	defer func() { _ = leaf.Close() }()
	if wire.send(ctx, treeWireEnd, ready, leaf) != nil {
		return status
	}
	// The controller must kill PID1 while the adopted leaf is stopped. Socket
	// closure and timeout refuse; neither is accepted as descendant drain.
	_, _, _ = wire.receive(ctx, treeWireAck, false)
	return status
}

func receiveGoLifetimeInitial(ctx context.Context, wire *inputTreeSocket) ([]byte, error) {
	raw, _, err := wire.receive(ctx, treeWireInit, false)
	if err != nil || len(raw) != 48 {
		return nil, ErrLinuxInputTree
	}
	// The wire owns a reusable receive buffer; later tree frames overwrite it.
	return bytes.Clone(raw), nil
}

func goLifetimeInputShape(census inputTreeCensus) bool {
	return len(census.rows) == 2 && census.rows[0].name == "inputs" && census.rows[0].mode == 0o700 &&
		census.rows[1].name == "inputs/sentinel" && census.rows[1].mode == 0o400 && census.rows[1].size == uint64(len(linuxGoLifetimeSentinel))
}

func goLifetimeAdoptStoppedLeaf(ctx context.Context) (*os.File, error) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	parent, child := os.NewFile(uintptr(pair[0]), "private-leaf-controller"), os.NewFile(uintptr(pair[1]), "private-leaf-wire")
	defer func() { _ = parent.Close(); _ = child.Close() }()
	command := exec.CommandContext(ctx, "/proc/self/fd/3", linuxGoLifetimeIntermediate)
	command.ExtraFiles = []*os.File{os.NewFile(3, "self-image"), child}
	command.Env = []string{"GOMAXPROCS=2"}
	if command.Start() != nil {
		return nil, ErrLinuxInputTree
	}
	// The intermediate is the only direct exec.Cmd. Its Wait finishes before
	// Wait4 can observe any adopted child, avoiding competing wait owners.
	if command.Wait() != nil {
		return nil, ErrLinuxInputTree
	}
	raw, _, err := (&inputTreeSocket{fd: pair[0]}).receive(ctx, treeWireReady, false)
	if err != nil || len(raw) != 4 {
		return nil, ErrLinuxInputTree
	}
	pid := int(binary.BigEndian.Uint32(raw))
	if pid <= 1 {
		return nil, ErrLinuxInputTree
	}
	for ctx.Err() == nil {
		var status unix.WaitStatus
		observed, err := unix.Wait4(pid, &status, unix.WNOHANG|unix.WUNTRACED, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil || observed != 0 && (!status.Stopped() || status.StopSignal() != unix.SIGSTOP) {
			return nil, ErrLinuxInputTree
		}
		if observed == pid {
			session, err := unix.Getsid(pid)
			if err != nil || session != pid {
				return nil, ErrLinuxInputTree
			}
			fd, err := unix.PidfdOpen(pid, 0)
			if err != nil {
				return nil, err
			}
			return os.NewFile(uintptr(fd), "stopped-adopted-leaf-pidfd"), nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil, ErrLinuxInputTree
}
