//go:build linux

package typedsandbox

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
)

// ReadWorkerInvocation is for the fixed helper's WorkerCommand dispatch only.
// The root supervisor receives the expected seal hash from the host controller
// in container argv, checks it after arming the absolute watchdog, then forwards
// these exact arguments while dropping to the worker UID. The immutable helper
// image and that process boundary are trusted; arbitrary code in this process
// is not a supported adversary. No authority is taken from control files here.
func ReadWorkerInvocation(ctx context.Context) (WorkerInvocation, error) {
	if ctx == nil {
		return WorkerInvocation{}, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return WorkerInvocation{}, err
	}
	a, phase, request, seal, err := parseWorkerArgs(os.Args[1:])
	if err != nil || os.Getppid() != 1 || authenticateWorkerSocket(3, invocationDigest(a, phase, request, seal), unix.Ucred{Pid: 1, Uid: 0, Gid: 0}) != nil || ValidateWorker() != nil {
		return WorkerInvocation{}, ErrRefused
	}
	if err = a.CheckLive(ctx); err != nil {
		return WorkerInvocation{}, err
	}
	return WorkerInvocation{a, phase, request, seal, os.Getpid()}, nil
}

// The existing progress socket is full duplex. The supervisor sends exactly
// one authorization frame in the opposite direction before starting the child.
// Kernel peer credentials distinguish PID1's socket from an orphan's imitation;
// CLOEXEC removes it from every later external child. Reads never wait.
func authenticateWorkerSocket(fd int, expected string, want unix.Ucred) error {
	unix.CloseOnExec(fd)
	kind, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || kind != unix.SOCK_DGRAM || !hostDigest(expected) {
		return ErrRefused
	}
	peer, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil || *peer != want {
		return ErrRefused
	}
	var raw [72]byte
	n, _, flags, _, err := unix.Recvmsg(fd, raw[:], nil, unix.MSG_DONTWAIT)
	if err != nil || flags&unix.MSG_TRUNC != 0 || n != 71 || string(raw[:n]) != expected {
		return ErrRefused
	}
	return nil
}
func sendWorkerInvocation(root *os.File, args []string) error {
	a, phase, request, seal, err := parseWorkerArgs(args)
	if err != nil {
		return err
	}
	raw := []byte(invocationDigest(a, phase, request, seal))
	n, err := unix.SendmsgN(int(root.Fd()), raw, nil, nil, unix.MSG_DONTWAIT)
	if err != nil || n != len(raw) {
		return ErrRefused
	}
	return nil
}
