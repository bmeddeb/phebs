//go:build linux || darwin

package typedsandbox

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRefuseSiteWritesTheFrameToDescriptorTwo proves the frame really reaches
// fd 2 rather than only being constructed correctly. Descriptor 2 is redirected
// to a pipe for the duration of one call and then restored, so a failure here
// cannot be explained by the frame builder alone.
func TestRefuseSiteWritesTheFrameToDescriptorTwo(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	readFD, writeFD := fds[0], fds[1]
	defer unix.Close(readFD)
	defer unix.Close(writeFD)

	saved, err := unix.Dup(2)
	if err != nil {
		t.Fatalf("dup stderr: %v", err)
	}
	defer unix.Close(saved)
	if err := unix.Dup2(writeFD, 2); err != nil {
		t.Fatalf("redirect stderr: %v", err)
	}
	restored := false
	defer func() {
		if !restored {
			t.Errorf("descriptor 2 was left redirected to the pipe")
		}
	}()

	RefuseSite(SiteScratch)

	if err := unix.Dup2(saved, 2); err != nil {
		t.Fatalf("restore stderr: %v", err)
	}
	restored = true
	unix.Close(writeFD)

	var got [refusalSiteFrameBytes]byte
	n, err := unix.Read(readFD, got[:])
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if want := string(refusalSiteFrame(SiteScratch)); string(got[:n]) != want {
		t.Fatalf("descriptor 2 received %q, want %q", got[:n], want)
	}
	if left, err := unix.Read(readFD, got[:]); err != unix.EAGAIN && left != 0 {
		t.Fatalf("descriptor 2 received %d unexpected extra bytes (err=%v)", left, err)
	}
}

// TestRefuseSiteDoesNotBlockOnAFullDescriptorTwo is the regression that the first
// draft of this change failed. A terminal refusal whose fd-2 write blocks turns a
// fast exit into a hang whenever the reader of that pipe is itself waiting for the
// process to exit, which is what a composition test in cmd/phebs does. The pipe is
// filled while non-blocking and then returned to blocking mode, so the descriptor
// really is a full blocking pipe when RefuseSite is called and the test cannot pass
// by accident.
func TestRefuseSiteDoesNotBlockOnAFullDescriptorTwo(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	readFD, writeFD := fds[0], fds[1]
	defer unix.Close(readFD)
	defer unix.Close(writeFD)

	if err := unix.SetNonblock(writeFD, true); err != nil {
		t.Fatalf("set nonblocking for the fill: %v", err)
	}
	chunk := make([]byte, 4096)
	filled := 0
	for {
		n, err := unix.Write(writeFD, chunk)
		if err == unix.EAGAIN {
			break
		}
		if err != nil {
			t.Fatalf("fill write: %v", err)
		}
		filled += n
	}
	if filled == 0 {
		t.Fatal("the pipe accepted nothing, so it was never full and this test proves nothing")
	}
	if err := unix.SetNonblock(writeFD, false); err != nil {
		t.Fatalf("restore blocking mode: %v", err)
	}

	saved, err := unix.Dup(2)
	if err != nil {
		t.Fatalf("dup stderr: %v", err)
	}
	defer unix.Close(saved)
	if err := unix.Dup2(writeFD, 2); err != nil {
		t.Fatalf("redirect stderr: %v", err)
	}
	defer func() {
		if err := unix.Dup2(saved, 2); err != nil {
			t.Errorf("restore stderr: %v", err)
		}
	}()

	done := make(chan struct{})
	go func() {
		RefuseSite(SiteScratch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RefuseSite blocked writing to a full descriptor 2 instead of dropping the frame")
	}
}
