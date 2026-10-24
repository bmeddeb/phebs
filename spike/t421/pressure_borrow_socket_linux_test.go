//go:build linux

package t421

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// These are mechanical packet/descriptor oracles. The fixture birth and nonce
// test wire binding, not source issuance, pidfd custody, or privileged UID drop.
func pressureBorrowPacketPair(t *testing.T) (*linuxPressureBorrowSocket, *linuxPressureBorrowSocket, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	fds, err := newLinuxPressureBorrowSockets()
	for _, fd := range fds {
		if fd >= 0 {
			t.Cleanup(func() {
				if closeErr := unix.Close(fd); closeErr != nil {
					t.Errorf("close owned packet endpoint: %v", closeErr)
				}
			})
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	peer := unix.Ucred{Pid: int32(os.Getpid()), Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}
	newWire := func(fd int) *linuxPressureBorrowSocket {
		return &linuxPressureBorrowSocket{fd: fd, peer: peer, attempt: [16]byte{1, 3, 7, 11}, birth: 41, observation: new(executionPreworkObservation)}
	}
	return newWire(fds[0]), newWire(fds[1]), ctx
}

func pressureBorrowRawPacket(wire *linuxPressureBorrowSocket, phase byte) []byte {
	raw := make([]byte, 32)
	copy(raw, "PB01")
	raw[4] = phase
	copy(raw[8:24], wire.attempt[:])
	binary.BigEndian.PutUint64(raw[24:], wire.birth)
	return raw
}

func pressureBorrowSendRaw(t *testing.T, fd int, packet, control []byte) {
	t.Helper()
	n, err := unix.SendmsgN(fd, packet, control, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
	if err != nil || n != len(packet) {
		t.Fatalf("send owned packet: bytes=%d err=%v", n, err)
	}
}

func TestLinuxPressureBorrowPacketRoundTrip(t *testing.T) {
	for _, terminal := range []byte{linuxPressureBorrowSuccess, linuxPressureBorrowFailure} {
		t.Run(map[byte]string{linuxPressureBorrowSuccess: "success", linuxPressureBorrowFailure: "failure"}[terminal], func(t *testing.T) {
			sender, receiver, ctx := pressureBorrowPacketPair(t)
			if err := sender.send(ctx, linuxPressureBorrowHello); err != nil {
				t.Fatal(err)
			}
			if err := receiver.receive(ctx, linuxPressureBorrowHello); err != nil {
				t.Fatal(err)
			}
			if err := receiver.send(ctx, linuxPressureBorrowProceed); err != nil {
				t.Fatal(err)
			}
			if err := sender.receive(ctx, linuxPressureBorrowProceed); err != nil {
				t.Fatal(err)
			}
			if err := sender.send(ctx, terminal); err != nil {
				t.Fatal(err)
			}
			got, err := receiver.receiveChoice(ctx, linuxPressureBorrowSuccess, linuxPressureBorrowFailure)
			if err != nil || got != terminal {
				t.Fatalf("terminal: phase=%d err=%v", got, err)
			}
			if sender.send(ctx, terminal) != ErrLinuxPressure || receiver.receive(ctx, terminal) != ErrLinuxPressure {
				t.Fatal("duplicate terminal did not refuse before another packet")
			}
			if sender.send(ctx, linuxPressureBorrowHello) != ErrLinuxPressure || receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure {
				t.Fatal("earlier phase did not refuse")
			}
		})
	}
}

func TestLinuxPressureBorrowPacketRefusals(t *testing.T) {
	tests := []struct {
		name   string
		change func([]byte) []byte
	}{
		{"magic", func(raw []byte) []byte { raw[0] = 'X'; return raw }},
		{"phase", func(raw []byte) []byte { raw[4] = linuxPressureBorrowProceed; return raw }},
		{"reserved", func(raw []byte) []byte { raw[7] = 1; return raw }},
		{"nonce", func(raw []byte) []byte { raw[8] ^= 1; return raw }},
		{"birth", func(raw []byte) []byte { raw[31] ^= 1; return raw }},
		{"short", func(raw []byte) []byte { return raw[:31] }},
		{"truncated", func(raw []byte) []byte { return append(raw, 1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sender, receiver, ctx := pressureBorrowPacketPair(t)
			pressureBorrowSendRaw(t, sender.fd, test.change(pressureBorrowRawPacket(sender, linuxPressureBorrowHello)), nil)
			if receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure || receiver.read != 0 {
				t.Fatal("malformed packet advanced receiver")
			}
		})
	}
	for _, field := range []string{"pid", "uid", "gid"} {
		t.Run("kernel-"+field, func(t *testing.T) {
			sender, receiver, ctx := pressureBorrowPacketPair(t)
			switch field {
			case "pid":
				receiver.peer.Pid++
			case "uid":
				receiver.peer.Uid ^= 1
			case "gid":
				receiver.peer.Gid ^= 1
			}
			if err := sender.send(ctx, linuxPressureBorrowHello); err != nil {
				t.Fatal(err)
			}
			if receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure {
				t.Fatal("different kernel sender credentials accepted")
			}
		})
	}
	t.Run("missing-kernel-credentials", func(t *testing.T) {
		sender, receiver, ctx := pressureBorrowPacketPair(t)
		if err := unix.SetsockoptInt(receiver.fd, unix.SOL_SOCKET, unix.SO_PASSCRED, 0); err != nil {
			t.Fatal(err)
		}
		if err := sender.send(ctx, linuxPressureBorrowHello); err != nil {
			t.Fatal(err)
		}
		if receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure {
			t.Fatal("missing credentials accepted")
		}
	})
}

func TestLinuxPressureBorrowEarlyTerminalChoice(t *testing.T) {
	for _, test := range []struct {
		name  string
		phase byte
		valid bool
	}{
		{"hello", linuxPressureBorrowHello, true},
		{"early-failure", linuxPressureBorrowFailure, true},
		{"unselected-middle-phase", linuxPressureBorrowProceed, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sender, receiver, ctx := pressureBorrowPacketPair(t)
			if err := sender.send(ctx, test.phase); err != nil {
				t.Fatal(err)
			}
			got, err := receiver.receiveChoice(ctx, linuxPressureBorrowHello, linuxPressureBorrowFailure)
			if test.valid {
				if err != nil || got != test.phase {
					t.Fatalf("selected phase: got=%d err=%v", got, err)
				}
			} else if err != ErrLinuxPressure || receiver.read != 0 {
				t.Fatal("phase between the two choices was accepted")
			}
		})
	}
}

func TestLinuxPressureBorrowUnexpectedRightsClosed(t *testing.T) {
	for _, count := range []int{1, 96} {
		name := "one-right"
		if count > 1 {
			name = "truncated-controls"
		}
		t.Run(name, func(t *testing.T) {
			sender, receiver, ctx := pressureBorrowPacketPair(t)
			pipe := []int{-1, -1}
			if err := unix.Pipe2(pipe, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, fd := range pipe {
					if fd >= 0 {
						if err := unix.Close(fd); err != nil {
							t.Errorf("close owned pipe: %v", err)
						}
					}
				}
			})
			rights := make([]int, count)
			for i := range rights {
				rights[i] = pipe[1]
			}
			pressureBorrowSendRaw(t, sender.fd, pressureBorrowRawPacket(sender, linuxPressureBorrowHello), unix.UnixRights(rights...))
			if err := unix.Close(pipe[1]); err != nil {
				t.Fatal(err)
			}
			pipe[1] = -1
			if receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure || receiver.read != 0 {
				t.Fatal("rights-bearing packet accepted")
			}
			// Every copy of the only writer must be closed. A leaked installed
			// right returns EAGAIN here instead of EOF; no /proc FD scan needed.
			var raw [1]byte
			if n, err := unix.Read(pipe[0], raw[:]); n != 0 || err != nil {
				t.Fatalf("delivered writer remains open: bytes=%d err=%v", n, err)
			}
			if receiver.observation.nativeCloseFailed.Load() {
				t.Fatal("genuine successful closes recorded as failed")
			}
		})
	}
}

func TestLinuxPressureBorrowPacketCancellationAndEOF(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		sender, receiver, _ := pressureBorrowPacketPair(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if sender.send(ctx, linuxPressureBorrowHello) != ErrLinuxPressure || receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure {
			t.Fatal("canceled operation accepted")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		_, receiver, _ := pressureBorrowPacketPair(t)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		if receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure || ctx.Err() == nil {
			t.Fatal("empty live endpoint did not stop on caller deadline")
		}
	})
	t.Run("eof", func(t *testing.T) {
		sender, receiver, ctx := pressureBorrowPacketPair(t)
		if err := unix.Shutdown(sender.fd, unix.SHUT_WR); err != nil {
			t.Fatal(err)
		}
		if receiver.receive(ctx, linuxPressureBorrowHello) != ErrLinuxPressure {
			t.Fatal("peer EOF accepted")
		}
	})
}

func TestLinuxPressureBorrowPacketOwnedChildCredentials(t *testing.T) {
	sender, receiver, ctx := pressureBorrowPacketPair(t)
	fd, err := unix.Dup(sender.fd)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "owned-pressure-packet")
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxPressureBorrowPacketChild$", "--", "pressure-packet-child")
	command.ExtraFiles = []*os.File{file}
	if err := command.Start(); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close unstarted endpoint: %v", closeErr)
		}
		t.Fatal(err)
	}
	// Registered immediately after Start, including all assertion failures.
	defer func() {
		if err := command.Wait(); err != nil {
			t.Errorf("owned packet child Wait: %v", err)
		}
	}()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	receiver.peer.Pid = int32(command.Process.Pid)
	if err := receiver.receive(ctx, linuxPressureBorrowHello); err != nil {
		t.Fatalf("actual child PID/real UID/GID were not authenticated: %v", err)
	}
}

func TestLinuxPressureBorrowPacketChild(t *testing.T) {
	if os.Args[len(os.Args)-1] != "pressure-packet-child" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	t.Cleanup(func() {
		if err := unix.Close(3); err != nil {
			t.Errorf("close inherited endpoint: %v", err)
		}
	})
	wire := linuxPressureBorrowSocket{fd: 3, attempt: [16]byte{1, 3, 7, 11}, birth: 41}
	if err := wire.send(ctx, linuxPressureBorrowHello); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxPressureBorrowModeledAncillaryParser(t *testing.T) {
	// Crafted control bytes are not kernel credential provenance. The real
	// packet tests above establish that separately. No modeled FD is installed.
	peer := unix.Ucred{Pid: 17, Uid: 23, Gid: 29}
	credentials := unix.UnixCredentials(&peer)
	shortCredentials := append([]byte{}, credentials...)
	// Both supported Linux targets have the 64-bit cmsghdr length field.
	binary.NativeEndian.PutUint64(shortCredentials[:8], uint64(unix.CmsgLen(unix.SizeofUcred-1)))
	wrong := peer
	wrong.Pid++
	tests := []struct {
		name  string
		raw   []byte
		valid bool
		fds   []int
	}{
		{"one", credentials, true, nil},
		{"missing", nil, false, nil},
		{"duplicate", append(append([]byte{}, credentials...), credentials...), false, nil},
		{"wrong", unix.UnixCredentials(&wrong), false, nil},
		{"wrong-credential-size", shortCredentials, false, nil},
		{"short-control", credentials[:len(credentials)-9], false, nil},
		{"rights-before-malformed", append(unix.UnixRights(71, 73), 1), false, []int{71, 73}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fds, valid := inputTreeControlRights(test.raw, &peer)
			if valid != test.valid || !reflect.DeepEqual(fds, test.fds) {
				t.Fatalf("valid=%v rights=%v", valid, fds)
			}
		})
	}
}
