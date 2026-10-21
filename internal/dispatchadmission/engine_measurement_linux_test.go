//go:build linux

package dispatchadmission

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Only this fixed child examines its inherited numeric FD7. Ordinary unit
// tests never replace a descriptor owned by their Go runtime.
func TestEngineMeasurementInheritedCaptureChild(t *testing.T) {
	mode := os.Getenv("PHEBS_PRIVATE_EM_CAPTURE")
	if mode == "" {
		return
	}
	captured := captureInheritedArchiveMeasurement()
	if mode != "socket" && mode != "replaced" {
		if captured != nil {
			t.Fatal("non-socket inherited as measurement")
		}
		// Allocate unrelated sockets after capture. Their existence cannot turn
		// an absent original handle into a selected measurement endpoint.
		a, b, err := NewPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = a.Close(); _ = b.Close() }()
		if conn, err := adoptProductionMeasurement(nil, captured); err == nil || conn != nil {
			t.Fatal("absent original endpoint retried")
		}
		if _, err := a.Stat(); err != nil {
			t.Fatal("refusal closed unrelated allocation", err)
		}
		if mode == "regular" {
			var info unix.Stat_t
			if unix.Fstat(7, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFREG {
				t.Fatal("omitted non-socket was adopted/closed")
			}
		}
		return
	}
	if captured == nil {
		t.Fatal("socket not captured")
	}
	if mode == "replaced" {
		file, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		if err := unix.Dup3(int(file.Fd()), 7, unix.O_CLOEXEC); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = unix.Close(7) }()
		if conn, err := adoptProductionMeasurement(nil, captured); err == nil || conn != nil {
			t.Fatal("replacement socket identity adopted")
		}
		if _, err := unix.FcntlInt(7, unix.F_GETFD, 0); err != nil {
			t.Fatal("replacement descriptor closed by refusal", err)
		}
		return
	}
	conn, err := adoptProductionMeasurement(nil, captured)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var controlErr error
	if err := raw.Control(func(fd uintptr) { flags, controlErr = unix.FcntlInt(fd, unix.F_GETFD, 0) }); err != nil || controlErr != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("adopted EM endpoint not protected", err, controlErr)
	}
}

func TestEngineMeasurementActualInheritedFD7(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	regular, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = regular.Close() }()
	for _, mode := range []string{"socket", "regular", "absent", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEngineMeasurementInheritedCaptureChild$")
			command.Env = []string{"PHEBS_PRIVATE_EM_CAPTURE=" + mode, "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
			if mode == "regular" {
				command.ExtraFiles = []*os.File{null, null, null, null, regular}
			}
			if mode == "socket" || mode == "replaced" {
				a, b, err := NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = a.Close(); _ = b.Close() }()
				command.ExtraFiles = []*os.File{null, null, null, null, b}
			}
			if output, err := command.CombinedOutput(); err != nil || ctx.Err() != nil {
				t.Fatalf("fixed FD7 child actual Wait: %v %s", err, output)
			}
		})
	}
}
