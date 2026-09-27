//go:build linux

package typedsandbox

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWorkerProgressDescriptorHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_PROGRESS_TEST")
	if mode == "" {
		return
	}
	if mode == "worker" {
		if InitializeWorkerProgress() != nil {
			os.Exit(90)
		}
		if f, err := os.OpenFile("/proc/self/fd/3", os.O_WRONLY, 0); err == nil {
			_ = f.Close()
			os.Exit(91)
		}
		flags, err := unix.FcntlInt(3, unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			os.Exit(92)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestWorkerProgressDescriptorHelper$")
		child.Env = append(os.Environ(), "PHEBS_PROGRESS_TEST=child")
		if child.Run() != nil {
			os.Exit(93)
		}
		os.Exit(0)
	}
	if mode == "child" {
		// The runtime can reuse fd3, but it must not be the inherited socket.
		var st unix.Stat_t
		if unix.Fstat(3, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFSOCK {
			os.Exit(94)
		}
		os.Exit(0)
	}
	os.Exit(95)
}

func TestWorkerProgressDescriptorIsolation(t *testing.T) {
	r, w, err := workerProgressPair()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	child := exec.Command(os.Args[0], "-test.run=^TestWorkerProgressDescriptorHelper$")
	child.Env = append(os.Environ(), "PHEBS_PROGRESS_TEST=worker")
	child.ExtraFiles = []*os.File{w}
	if raw, err := child.CombinedOutput(); err != nil {
		t.Fatal(fmt.Errorf("descriptor helper: %w: %s", err, raw))
	}
}
