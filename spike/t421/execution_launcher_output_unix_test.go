//go:build darwin || linux

package t421

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func executionLauncherOutputPipe(t *testing.T, nonblocking bool) (*os.File, *os.File) {
	t.Helper()
	var descriptors [2]int
	if err := unix.Pipe(descriptors[:]); err != nil {
		t.Fatal(err)
	}
	for index, fd := range descriptors {
		unix.CloseOnExec(fd)
		if err := unix.SetNonblock(fd, index == 0 || nonblocking); err != nil {
			_ = unix.Close(descriptors[0])
			_ = unix.Close(descriptors[1])
			t.Fatal(err)
		}
	}
	reader := os.NewFile(uintptr(descriptors[0]), "test-launcher-output-read")
	writer := os.NewFile(uintptr(descriptors[1]), "test-launcher-output-write")
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	return reader, writer
}

func executionLauncherOutputTestRow(t *testing.T, file *os.File) (executionPipeIdentity, int) {
	t.Helper()
	raw, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var identity executionPipeIdentity
	var access int
	var observeErr error
	if err := raw.Control(func(fd uintptr) {
		identity, access, observeErr = executionAuthorizationOutputRow(fd)
	}); err != nil || observeErr != nil {
		t.Fatal("stream identity/access/nonblocking check", err, observeErr)
	}
	return identity, access
}

func TestExecutionAuthorizationOutputPreservesBorrowedFlagsAndIdentity(t *testing.T) {
	reader, writer := executionLauncherOutputPipe(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	before, access := executionLauncherOutputTestRow(t, writer)
	output, err := prepareExecutionAuthorizationOutput(ctx, writer)
	if err != nil || output.identity != before || output.access != access {
		t.Fatal("owned output did not preserve inherited identity", err)
	}
	if err := output.file.Close(); err != nil {
		t.Fatal(err)
	}
	after, afterAccess := executionLauncherOutputTestRow(t, writer)
	if before != after || access != afterAccess {
		t.Fatal("owned output changed inherited identity or access")
	}
	if n, err := writer.Write([]byte{'x'}); err != nil || n != 1 {
		t.Fatal("closing output closed borrowed writer", err)
	}
	var one [1]byte
	if n, err := reader.Read(one[:]); err != nil || n != 1 || one[0] != 'x' {
		t.Fatal("borrowed stream no longer works", err)
	}
}

func TestExecutionAuthorizationOutputFullPipeCancellationAndDeadlines(t *testing.T) {
	for _, mode := range []string{"cancel", "final_admission", "caller_deadline"} {
		t.Run(mode, func(t *testing.T) {
			_, writer := executionLauncherOutputPipe(t, true)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			output, err := prepareExecutionAuthorizationOutput(ctx, writer)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = output.file.Close() }()
			executionLauncherFillOutputPipe(t, writer)
			outerDeadline := time.Now().Add(5 * time.Second).UnixNano()
			finalDeadline := outerDeadline - 1
			if mode == "final_admission" {
				finalDeadline = time.Now().Add(200 * time.Millisecond).UnixNano()
			}
			if mode == "caller_deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer deadlineCancel()
			}
			image := "sha256:" + strings.Repeat("c", 64)
			projection, err := projectExecutionAuthorizationHandoff("/tmp/t422-execute", "/tmp/t422/auth.sock", image, outerDeadline)
			if err != nil {
				t.Fatal(err)
			}
			handoff, err := buildExecutionAuthorizationHandoff("/tmp/t422-execute", "/tmp/t422/auth.sock", image, outerDeadline, finalDeadline,
				strings.Repeat("a", 64), strings.Repeat("b", 64), projection)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- forwardExecutionAuthorizationHandoff(ctx, output, handoff.frame) }()
			select {
			case err := <-done:
				t.Fatal("full pipe did not block the real output operation", err)
			case <-time.After(20 * time.Millisecond):
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, errExecutionAuthorization) {
					t.Fatal("interrupted output did not refuse", err)
				}
			case <-time.After(time.Second):
				_ = output.file.Close()
				<-done
				t.Fatal("full output pipe escaped cancellation/deadline")
			}
			executionLauncherOutputTestRow(t, writer)
		})
	}
}

func executionLauncherFillOutputPipe(t *testing.T, writer *os.File) {
	t.Helper()
	raw, err := writer.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var writeErr error
	filled := 0
	if err := raw.Control(func(fd uintptr) {
		var block [1024]byte
		for filled < 1<<20 {
			n, err := unix.Write(int(fd), block[:])
			if err != nil {
				writeErr = err
				return
			}
			if n <= 0 {
				return
			}
			filled += n
		}
	}); err != nil || !errors.Is(writeErr, unix.EAGAIN) || filled <= 0 || filled >= 1<<20 {
		t.Fatal("owned nonblocking output pipe was not filled within its test bound", filled, err, writeErr)
	}
}
