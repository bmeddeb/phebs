//go:build linux

package t421

import (
	"context"
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

const (
	linuxPressureBorrowHello byte = iota + 1
	linuxPressureBorrowProceed
	linuxPressureBorrowSuccess
	linuxPressureBorrowFailure
)

// One reader per endpoint. Liveness uses a different descriptor; no watcher
// consumes protocol messages. No descriptor transfer is part of this protocol.
type linuxPressureBorrowSocket struct {
	fd          int
	peer        unix.Ucred
	attempt     [16]byte
	birth       uint64
	sent, read  byte
	observation *executionPreworkObservation
}

func (wire *linuxPressureBorrowSocket) wait(ctx context.Context, events int16) error {
	for {
		if ctx == nil || ctx.Err() != nil {
			return ErrLinuxPressure
		}
		poll := []unix.PollFd{{Fd: int32(wire.fd), Events: events}}
		n, err := unix.Poll(poll, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n != 0 && poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return ErrLinuxPressure
		}
		if n != 0 && poll[0].Revents&(events|unix.POLLHUP) != 0 {
			return nil
		}
	}
}

func newLinuxPressureBorrowSockets() ([2]int, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return [2]int{-1, -1}, ErrLinuxPressure
	}
	for _, fd := range fds {
		if unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PASSCRED, 1) != nil {
			return fds, ErrLinuxPressure // Caller owns both descriptors even on error.
		}
	}
	return fds, nil
}

func (wire *linuxPressureBorrowSocket) send(ctx context.Context, phase byte) error {
	if wire == nil || phase < linuxPressureBorrowHello || phase > linuxPressureBorrowFailure || phase <= wire.sent {
		return ErrLinuxPressure
	}
	var raw [32]byte
	copy(raw[:4], "PB01")
	raw[4] = phase
	copy(raw[8:24], wire.attempt[:])
	binary.BigEndian.PutUint64(raw[24:], wire.birth)
	for {
		if wire.wait(ctx, unix.POLLOUT) != nil {
			return ErrLinuxPressure
		}
		// No supplied credentials: the kernel supplies the real sender identity.
		n, err := unix.SendmsgN(wire.fd, raw[:], nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil || n != len(raw) {
			return ErrLinuxPressure
		}
		wire.sent = phase
		return nil
	}
}

func (wire *linuxPressureBorrowSocket) receive(ctx context.Context, phase byte) error {
	_, err := wire.receiveChoice(ctx, phase, phase)
	return err
}

func (wire *linuxPressureBorrowSocket) receiveChoice(ctx context.Context, first, second byte) (byte, error) {
	if wire == nil || wire.peer.Pid <= 0 || first < linuxPressureBorrowHello || first > linuxPressureBorrowFailure || second < first || second > linuxPressureBorrowFailure || first <= wire.read {
		return 0, ErrLinuxPressure
	}
	var raw [32]byte
	var control [256]byte
	for {
		if wire.wait(ctx, unix.POLLIN) != nil {
			return 0, ErrLinuxPressure
		}
		n, size, flags, _, err := unix.Recvmsg(wire.fd, raw[:], control[:], unix.MSG_DONTWAIT|unix.MSG_CMSG_CLOEXEC)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		rights, valid := inputTreeControlRights(control[:size], &wire.peer)
		for _, fd := range rights {
			_ = wire.observation.close(unix.Close(fd))
		}
		if err != nil || !valid || len(rights) != 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || n != len(raw) ||
			string(raw[:4]) != "PB01" || raw[4] != first && raw[4] != second || raw[5] != 0 || raw[6] != 0 || raw[7] != 0 ||
			[16]byte(raw[8:24]) != wire.attempt || binary.BigEndian.Uint64(raw[24:]) != wire.birth {
			return 0, ErrLinuxPressure
		}
		wire.read = raw[4]
		return raw[4], nil
	}
}
