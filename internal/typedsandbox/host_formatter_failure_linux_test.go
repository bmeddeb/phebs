//go:build linux

package typedsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHostFormatterDiagnosticHelper(t *testing.T) {
	switch os.Getenv("PHEBS_FORMATTER_DIAGNOSTIC") {
	case "exit":
		os.Exit(23)
	case "cancel":
		_, _ = io.WriteString(os.Stdout, "ready")
		for {
			time.Sleep(time.Hour)
		}
	}
}

type formatterReady struct{ ready chan struct{} }

func (w formatterReady) Write(p []byte) (int, error) {
	select {
	case w.ready <- struct{}{}:
	default:
	}
	return len(p), nil
}

func TestHostFormatterFailureEvidence(t *testing.T) {
	for _, mode := range []string{"start", "exit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHostFormatterDiagnosticHelper$")
			cmd.Env = append(os.Environ(), "PHEBS_FORMATTER_DIAGNOSTIC="+mode)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
			if mode == "start" {
				cmd.Path = filepath.Join(t.TempDir(), "private-missing-formatter")
			}
			ready := make(chan struct{}, 1)
			cmd.Stdout = formatterReady{ready}
			done := make(chan error, 1)
			go func() { done <- runHostFormatter(cmd) }()
			if mode == "cancel" {
				select {
				case <-ready:
					cancel()
				case <-time.After(10 * time.Second):
					cancel()
					<-done
					t.Fatal("helper not ready")
				}
			}
			err := <-done
			if !errors.Is(err, ErrRefused) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || err.Error() != ErrRefused.Error() {
				t.Fatal("original public error classification changed", err)
			}
			d, ok := HostFormatterFailureOf(err)
			if !ok || d.FormatterContext != "unobserved" {
				t.Fatal("missing formatter observation", d)
			}
			if mode == "start" {
				if d.Stage != "start" || d.Started || d.WaitReturned || d.WaitStatusKnown || d.ExitCode != -1 || d.ErrorNumber != uint64(syscall.ENOENT) || d.ProcessGroupProbe != "unobserved" {
					t.Fatal("unstarted child has wait evidence", d)
				}
			} else {
				if d.Stage != "wait" || !d.Started || !d.WaitReturned || !d.WaitStatusKnown || d.ProcessGroupProbe != "absent" || d.GroupErrorNumber != uint64(syscall.ESRCH) {
					t.Fatal("actual wait or group probe lost", d)
				}
				if mode == "exit" && (d.ExitCode != 23 || d.Signal != 0) || mode == "cancel" && (d.ExitCode != -1 || d.Signal != int(syscall.SIGKILL)) {
					t.Fatal("actual exit/signal lost", d)
				}
			}
			raw, encodeErr := json.Marshal(d)
			if encodeErr != nil || len(raw) > 2048 || strings.Contains(string(raw), "private") || strings.Contains(string(raw), cmd.Path) {
				t.Fatal("diagnostic leaked a path or exceeded its bound")
			}
			d.Stage = "changed copy"
			if again, _ := HostFormatterFailureOf(err); again.Stage == d.Stage {
				t.Fatal("diagnostic accessor exposed shared mutable state")
			}
		})
	}
	for _, err := range []error{nil, ErrRefused, errors.New("private error")} {
		if _, ok := HostFormatterFailureOf(err); ok {
			t.Fatal("unobserved formatter evidence invented")
		}
	}
}
