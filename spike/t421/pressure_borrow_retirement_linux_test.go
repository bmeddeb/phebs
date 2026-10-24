//go:build linux

package t421

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxPressureBorrowOriginalHelperLaterRetirement(t *testing.T) {
	read, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = writer.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxExecutorPreworkKernelHelper$")
	command.Env = []string{"PHEBS_EXECUTOR_KERNEL_HELPER=retained-leaf", "GORACE=atexit_sleep_ms=0"}
	command.Stdin = read
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if command.Start() != nil {
		t.Fatal("own finite helper start")
	}
	waited := false
	defer func() {
		if !waited {
			_, _ = writer.Write([]byte{1})
			_ = writer.Close()
			_ = command.Wait()
		}
	}()
	file, err := os.CreateTemp(t.TempDir(), "owned-descriptor-")
	if err != nil {
		t.Fatal(err)
	}
	observation := &executionPreworkObservation{}
	observation.process(command, false, false)
	// Volume/negative-prefix shape is modeled. The command, original native
	// Wait, live private SID and later empty-session observation are genuine.
	volume := &LinuxPressureVolume{image: file, observation: observation, closeResults: make(map[*os.File]error, 9), formatterCommand: command, formatterSession: command.Process.Pid}
	if volume.retireOriginalPressureFormatter(t.Context()) || volume.Close() == nil || volume.closed {
		t.Fatal("live original helper released protection")
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("held original FD closed before helper retirement")
	}
	if _, err := writer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		waited = true
		t.Fatal(err)
	}
	waited = true
	if !volume.retireOriginalPressureFormatter(t.Context()) || !volume.formatterRetiredLater || volume.formatterJoined || volume.formatterEmpty {
		t.Fatal("actual later retirement lost or rewrote original negative prefix")
	}
	if volume.Close() != nil || !linuxPressureVolumeDescriptorsRetired(volume) {
		t.Fatal("known-retired failure could not safely close original descriptors")
	}
	if observation.unknownCommand != command || !observation.unknown() {
		t.Fatal("original failure witness discarded")
	}
}

func TestLinuxPressureBorrowHandshakeEOFCancelsBeforeWorkDeadline(t *testing.T) {
	sockets, err := newLinuxPressureBorrowSockets()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(sockets[0]) }()
	childSocket := os.NewFile(uintptr(sockets[1]), "own-child-control")
	defer func() { _ = childSocket.Close() }()
	read, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = writer.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxPressureBorrowHandshakeHelper$", "--", "pressure-handshake-eof")
	command.Env = []string{"GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = []*os.File{read, childSocket}
	if command.Start() != nil {
		t.Fatal("own child start")
	}
	_ = read.Close()
	_ = childSocket.Close()
	wire := linuxPressureBorrowSocket{fd: sockets[0], peer: unix.Ucred{Pid: int32(command.Process.Pid), Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}, birth: 1}
	helloErr := wire.receive(ctx, linuxPressureBorrowHello)
	start := time.Now()
	closeErr := writer.Close() // Source-owned refusal: no Proceed, sole writer EOF.
	waitErr := command.Wait()  // Actual sole Wait even when Hello failed.
	if helloErr != nil || closeErr != nil || waitErr != nil || command.ProcessState == nil || !command.ProcessState.Success() || time.Since(start) > 2*time.Second {
		t.Fatal("liveness EOF did not stop the actual child handshake before its 3s work deadline", helloErr, closeErr, waitErr)
	}
}

func TestLinuxPressureBorrowHandshakeHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "pressure-handshake-eof" {
		return
	}
	unix.CloseOnExec(3)
	unix.CloseOnExec(4)
	if unix.SetNonblock(3, true) != nil {
		t.Fatal("nonblock own inherited pipe")
	}
	read := os.NewFile(3, "own-parent-liveness")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	alive, stop := context.WithCancel(ctx)
	pidfd, err := unix.PidfdOpen(os.Getppid(), 0)
	if err != nil {
		t.Fatal("own parent pidfd", err)
	}
	parent := &linuxExecutorParent{file: read, pidfd: pidfd, alive: alive, cancel: stop, done: make(chan struct{})}
	go parent.watch()
	wire := linuxPressureBorrowSocket{fd: 4, peer: unix.Ucred{Pid: int32(os.Getppid()), Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid())}, birth: 1}
	// Actual pipe watcher/protocol operations; this does not model provenance,
	// a pidfd or root UID. It discriminates raw ctx from authenticated alive.
	if linuxPressureBorrowHandshake(ctx, parent, &wire) == nil || ctx.Err() != nil || alive.Err() == nil {
		t.Fatal("handshake ignored genuine liveness EOF")
	}
	_ = parent.Close()
	if unix.Close(4) != nil {
		t.Fatal("own control close")
	}
}
