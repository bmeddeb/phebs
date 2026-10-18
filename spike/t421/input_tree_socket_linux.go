//go:build linux

package t421

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const inputTreeWireLimit = 3 << 30

type inputTreeSocket struct {
	fd             int
	sent, received uint64
	written, read  uint64
	buffer         [treeWireMaximum]byte
	control        [256]byte
	peer           *unix.Ucred
}

func (wire *inputTreeSocket) wait(ctx context.Context, events int16) error {
	for {
		if ctx == nil || ctx.Err() != nil {
			return ErrLinuxInputTree
		}
		poll := []unix.PollFd{{Fd: int32(wire.fd), Events: events}}
		count, err := unix.Poll(poll, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || count != 0 && poll[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
			return ErrLinuxInputTree
		}
		if count != 0 && poll[0].Revents&(events|unix.POLLHUP) != 0 {
			return nil
		}
	}
}

func (wire *inputTreeSocket) send(ctx context.Context, kind byte, payload []byte, root *os.File) error {
	if wire.sent == ^uint64(0) || kind < treeWireInit || kind > treeWireAck ||
		len(payload) > treeWireMaximum-treeWireHeader || uint64(treeWireHeader+len(payload)) > inputTreeWireLimit-wire.written {
		return ErrLinuxInputTree
	}
	raw := wire.buffer[:treeWireHeader+len(payload)]
	clear(raw[:treeWireHeader])
	copy(raw, "ITN1")
	raw[4] = kind
	binary.BigEndian.PutUint64(raw[8:], wire.sent+1)
	binary.BigEndian.PutUint32(raw[16:], uint32(len(payload)))
	copy(raw[treeWireHeader:], payload)
	var rights []byte
	if root != nil {
		rights = unix.UnixRights(int(root.Fd()))
	}
	for {
		if wire.wait(ctx, unix.POLLOUT) != nil {
			return ErrLinuxInputTree
		}
		count, err := unix.SendmsgN(wire.fd, raw, rights, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || count != len(raw) {
			return ErrLinuxInputTree
		}
		wire.sent++
		wire.written += uint64(count)
		return nil
	}
}

// Every delivered right is owned immediately, even on a truncated/refused
// packet. MSG_CMSG_CLOEXEC prevents a concurrent exec from inheriting a right.
// Linux closes SCM_RIGHTS descriptors that do not fit the receive buffer.
func (wire *inputTreeSocket) receive(ctx context.Context, want byte, allowRoot bool) (payload []byte, root *os.File, retErr error) {
	defer func() {
		if retErr != nil && root != nil {
			_ = root.Close()
			root = nil
		}
	}()
	for {
		if wire.wait(ctx, unix.POLLIN) != nil {
			return nil, nil, ErrLinuxInputTree
		}
		count, controlBytes, flags, _, err := unix.Recvmsg(wire.fd, wire.buffer[:], wire.control[:], unix.MSG_DONTWAIT|unix.MSG_CMSG_CLOEXEC)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		descriptors, controlsValid := inputTreeControlRights(wire.control[:controlBytes], wire.peer)
		valid := err == nil && controlsValid && flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) == 0 &&
			(!allowRoot && len(descriptors) == 0 || allowRoot && len(descriptors) == 1)
		if !valid {
			for _, fd := range descriptors {
				_ = unix.Close(fd)
			}
			return nil, nil, ErrLinuxInputTree
		}
		if allowRoot {
			root = os.NewFile(uintptr(descriptors[0]), "readonly-input-tree")
		}
		if count == 0 || wire.received == ^uint64(0) || uint64(count) > inputTreeWireLimit-wire.read {
			return nil, root, ErrLinuxInputTree
		}
		kind, value, err := decodeInputTreeFrame(wire.buffer[:count], wire.received+1)
		if err != nil || kind != want {
			return nil, root, ErrLinuxInputTree
		}
		wire.received++
		wire.read += uint64(count)
		return value, root, nil
	}
}

// Parse incrementally: the whole-array parser discards previously parsed rights
// on a malformed later message. Every complete delivered right is returned to
// the receiver for closure even when subsequent control data refuses. Kernel
// control framing is trusted only to identify installed rights, never authority.
func inputTreeControlRights(raw []byte, expected *unix.Ucred) ([]int, bool) {
	var descriptors []int
	credentials, valid := 0, true
	for len(raw) != 0 {
		if len(raw) < unix.CmsgLen(0) {
			return descriptors, false
		}
		header, data, rest, err := unix.ParseOneSocketControlMessage(raw)
		if err != nil {
			return descriptors, false
		}
		message := unix.SocketControlMessage{Header: header, Data: data}
		switch {
		case header.Level == unix.SOL_SOCKET && header.Type == unix.SCM_RIGHTS:
			// ParseUnixRights assumes four-byte alignment; own any complete
			// rights even in an impossible malformed trailing byte control.
			aligned := len(data) &^ 3
			message.Data = data[:aligned]
			rights, parseErr := unix.ParseUnixRights(&message)
			descriptors = append(descriptors, rights...)
			valid = valid && parseErr == nil && aligned == len(data) && aligned != 0
		case header.Level == unix.SOL_SOCKET && header.Type == unix.SCM_CREDENTIALS && expected != nil:
			if len(data) != unix.SizeofUcred {
				valid = false
				break
			}
			peer, parseErr := unix.ParseUnixCredentials(&message)
			credentials++
			valid = valid && parseErr == nil && peer != nil && *peer == *expected
		default:
			valid = false
		}
		raw = rest
	}
	return descriptors, valid && (expected == nil && credentials == 0 || expected != nil && credentials == 1)
}

func inputTreeDeadline(raw []byte) (context.Context, context.CancelFunc, error) {
	if len(raw) != 48 {
		return nil, nil, ErrLinuxInputTree
	}
	deadline := time.Unix(0, int64(binary.BigEndian.Uint64(raw)))
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 30*time.Minute {
		return nil, nil, ErrLinuxInputTree
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	return ctx, cancel, nil
}
