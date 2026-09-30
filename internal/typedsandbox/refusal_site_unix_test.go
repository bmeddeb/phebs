//go:build unix

package typedsandbox

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// closeOnce registers a descriptor close that runs at most once and returns the
// closer so a test can also close it early. At most once matters because a second
// unix.Close on the same number can close an unrelated descriptor the runtime
// assigned that number in between, which would fail some later test rather than this
// one. Registering it as a cleanup rather than a bare defer matters too, because a
// t.Fatalf between the redirect and the restore would otherwise leak it.
func closeOnce(t *testing.T, fd int) func() {
	t.Helper()
	done := false
	closer := func() {
		if done {
			return
		}
		done = true
		_ = unix.Close(fd)
	}
	t.Cleanup(closer)
	return closer
}

// redirectStderr points descriptor 2 at fd for the duration of the test and restores
// it during cleanup. The restore is a cleanup rather than a step on the happy path:
// without it a t.Fatalf between the redirect and the restore would leave every later
// diagnostic in the package writing into a pipe whose write end is closed, and Go
// re-raises SIGPIPE for descriptors 1 and 2, which can kill the whole test binary and
// mask the real failure.
func redirectStderr(t *testing.T, fd int) {
	t.Helper()
	saved, err := unix.Dup(2)
	if err != nil {
		t.Fatalf("dup stderr: %v", err)
	}
	if err := unix.Dup2(fd, 2); err != nil {
		_ = unix.Close(saved)
		t.Fatalf("redirect stderr: %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Dup2(saved, 2); err != nil {
			t.Errorf("restore stderr: %v", err)
		}
		_ = unix.Close(saved)
	})
}

// TestRefuseSiteWritesTheFrameToDescriptorTwo proves the frame really reaches
// fd 2 rather than only being constructed correctly. Descriptor 2 is redirected
// to a pipe for the duration of one call, so a failure here cannot be explained
// by the frame builder alone.
func TestRefuseSiteWritesTheFrameToDescriptorTwo(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		t.Fatalf("pipe: %v", err)
	}
	readFD, writeFD := fds[0], fds[1]
	closeOnce(t, readFD)
	closeWrite := closeOnce(t, writeFD)

	redirectStderr(t, writeFD)
	RefuseSite(SiteScratch)

	// Closing the write end before reading is what lets the second read observe EOF
	// instead of blocking. Descriptor 2 holds its own reference to the same open file
	// description, so this does not disturb the redirect.
	closeWrite()

	want := string(refusalSiteFrame(SiteScratch))
	var got [refusalSiteFrameBytes]byte
	n, err := unix.Read(readFD, got[:])
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if string(got[:n]) != want {
		t.Fatalf("descriptor 2 received %q, want %q", got[:n], want)
	}
	if left, err := unix.Read(readFD, got[:]); err != unix.EAGAIN && left != 0 {
		t.Fatalf("descriptor 2 received %d unexpected extra bytes (err=%v)", left, err)
	}
}

// TestRefuseSiteWritesARegularFile covers the S_IFREG branch. A regular-file write
// cannot block, so it must still happen rather than be dropped along with the
// terminal kind. That the same branch never sets O_NONBLOCK is a structural assertion
// in the scope oracle instead of a test here, because x/sys/unix exposes no descriptor
// flag getter on every platform this file builds for — and the reason it must not is
// that O_NONBLOCK lives on the open file description, so setting it would leak out to
// whoever else holds that description, a parent shell above all.
func TestRefuseSiteWritesARegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "refusal-site")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer func() { _ = f.Close() }()

	redirectStderr(t, int(f.Fd()))
	RefuseSite(SiteEncode)

	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if want := string(refusalSiteFrame(SiteEncode)); string(got) != want {
		t.Fatalf("regular file received %q, want %q", got, want)
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
	closeOnce(t, readFD)
	closeOnce(t, writeFD)

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

	redirectStderr(t, writeFD)

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
