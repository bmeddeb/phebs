//go:build linux || darwin

package typedsandbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWatchdogProcessHelper(t *testing.T) {
	mode := os.Getenv("PHEBS_WATCHDOG_TEST")
	if mode == "" {
		return
	}
	if mode == "child" {
		time.Sleep(time.Hour)
		os.Exit(9)
	}
	s := newWatchdogSnapshots(100 * time.Millisecond)
	s.resources(Resources{LimitsVerified: true, Samples: 1, SampledPeakRSSBytes: 42})
	s.progress(2, 0)
	fdOK := nonblockingReportFD(2)
	if !fdOK {
		os.Exit(90)
	}
	go func() { time.Sleep(100 * time.Millisecond); watchdogExit(s, 2, fdOK) }()
	switch mode {
	case "hung-child":
		child := exec.Command(os.Args[0], "-test.run=^TestWatchdogProcessHelper$")
		child.Env = append(os.Environ(), "PHEBS_WATCHDOG_TEST=child")
		if child.Start() != nil {
			os.Exit(91)
		}
		_ = child.Wait()
	case "blocked-stdout":
		for {
			_, _ = os.Stdout.Write(make([]byte, 65536))
		}
	case "full-stderr":
		for {
			if _, err := unix.Write(2, make([]byte, 4096)); err != nil {
				break
			}
		}
		time.Sleep(time.Hour)
	case "closed-stderr":
		time.Sleep(time.Hour)
	case "stalled-filesystem":
		_, _ = os.Open(os.Getenv("PHEBS_WATCHDOG_FIFO"))
	case "stalled-sample":
		// A resource sampler/publisher stalls in a pipe/filesystem read while
		// holding the publication lock; watchdog must use the previous frame.
		s.mu.Lock()
		r, w, err := os.Pipe()
		if err != nil {
			os.Exit(92)
		}
		defer func() { _ = r.Close(); _ = w.Close() }()
		_, _ = io.ReadAll(r)
	default:
		os.Exit(93)
	}
	os.Exit(94)
}

func TestWatchdogDeadlineSubprocess(t *testing.T) {
	for _, mode := range []string{"hung-child", "blocked-stdout", "full-stderr", "closed-stderr", "stalled-sample", "stalled-filesystem"} {
		t.Run(mode, func(t *testing.T) {
			outR, outW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			errR, errW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = outR.Close(); _ = outW.Close(); _ = errR.Close(); _ = errW.Close() }()
			c := exec.Command(os.Args[0], "-test.run=^TestWatchdogProcessHelper$")
			c.Env = append(os.Environ(), "PHEBS_WATCHDOG_TEST="+mode)
			if mode == "stalled-filesystem" {
				fifo := filepath.Join(t.TempDir(), "stalled-fifo")
				if err := unix.Mkfifo(fifo, 0600); err != nil {
					t.Fatal(err)
				}
				c.Env = append(c.Env, "PHEBS_WATCHDOG_FIFO="+fifo)
			}
			if mode == "closed-stderr" {
				_ = errR.Close()
			}
			c.Stdout, c.Stderr = outW, errW
			c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			start := time.Now()
			if err = c.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }()
			_ = outW.Close()
			_ = errW.Close()
			done := make(chan error, 1)
			go func() { done <- c.Wait() }()
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
				<-done
				t.Fatal("deadline blocked")
			}
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 124 || time.Since(start) > 2*time.Second {
				t.Fatal(err, time.Since(start))
			}
			if mode == "closed-stderr" {
				return
			}
			raw, err := io.ReadAll(errR)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "full-stderr" {
				if _, err = decodeWatchdog(raw, 100*time.Millisecond); err == nil {
					t.Fatal("full transport invented evidence")
				}
			} else {
				r, err := decodeWatchdog(raw, 100*time.Millisecond)
				if err != nil || r.WorkerStage != "planning" || r.Resources.SampledPeakRSSBytes != 42 || !r.ResourceSampleAvailable {
					t.Fatal(string(raw), err)
				}
			}
		})
	}
}

func TestWatchdogTransportRefusesFilesystem(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "report")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if nonblockingReportFD(int(f.Fd())) {
		t.Fatal("regular file transport can block")
	}
}

func TestWorkerProgressSocket(t *testing.T) {
	r, w, err := workerProgressPair()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	s := newWatchdogSnapshots(WallLimit)
	done := make(chan struct{})
	go func() { readWorkerProgress(r, s); close(done) }()
	if _, err = w.Write([]byte{2, 1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte{255, 2, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("progress reader stalled")
	}
	got, err := decodeWatchdog(s.frame.Load().data, WallLimit)
	if err != nil || got.WorkerProgressAvailable || got.WorkerStage != "planning" {
		t.Fatal(got, err)
	}
	// The Linux descriptor-isolation test covers /proc reopening and child exec.

}

func TestMalformedProgressInvalidatesLastSnapshot(t *testing.T) {
	for _, size := range []int{2, 10, 100} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			r, w, err := workerProgressPair()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close(); _ = w.Close() }()
			s := newWatchdogSnapshots(WallLimit)
			if _, err = w.Write([]byte{2, 1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(make([]byte, size)); err != nil {
				t.Fatal(err)
			}
			readWorkerProgress(r, s)
			got, err := decodeWatchdog(s.frame.Load().data, WallLimit)
			if err != nil || got.WorkerProgressAvailable || got.WorkerStage != "planning" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestProgressEOFAndTransportFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close(); _ = w.Close() }()
			s := newWatchdogSnapshots(WallLimit)
			s.progress(2, 0)
			if failure {
				_ = r.Close()
			} else {
				_ = w.Close()
			}
			readWorkerProgress(r, s)
			got, err := decodeWatchdog(s.frame.Load().data, WallLimit)
			if err != nil || got.WorkerProgressAvailable == failure || got.WorkerStage != "planning" {
				t.Fatal(got, err)
			}
		})
	}
}
