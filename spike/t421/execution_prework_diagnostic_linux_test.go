//go:build linux

package t421

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func runPreworkDiagnosticChild(t *testing.T, script string) linuxExecutorPreworkDiagnostic {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	stdout := checkoutCommandOutput{remaining: 128, cancel: cancel}
	stderr := checkoutCommandOutput{remaining: 1024, cancel: cancel}
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal("start diagnostic child", err)
	}
	waitErr := command.Wait()
	return linuxExecutorPreworkRefusal(command, waitErr, ctx.Err(), &stdout, &stderr, false)
}

func TestLinuxExecutorPreworkRefusalDiagnostic(t *testing.T) {
	exited := runPreworkDiagnosticChild(t, "printf receipt; printf 'reference digest mismatch' >&2; exit 3")
	if !exited.Started || !exited.Exited || exited.ExitCode != 3 || exited.Signal != "" || exited.WaitError != "" ||
		exited.Stdout != "receipt" || exited.Stderr != "reference digest mismatch" || exited.StdoutRefused != "" || exited.StderrRefused != "" || exited.ReceiptMatched {
		t.Fatalf("exit facts not retained: %#v", exited)
	}
	signaled := runPreworkDiagnosticChild(t, "kill -KILL $$")
	if !signaled.Started || signaled.Exited || signaled.ExitCode != -1 || signaled.Signal != "killed" || signaled.Stdout != "" || signaled.Stderr != "" {
		t.Fatalf("signal facts not retained: %#v", signaled)
	}
	// Over-bound stderr keeps only what was admitted before the refusal and the refusal itself.
	// The pause keeps the two writes in separate pipe reads, so the first is admitted.
	flooded := runPreworkDiagnosticChild(t, "head -c 600 /dev/zero | tr '\\0' a >&2; sleep 0.2; head -c 600 /dev/zero | tr '\\0' b >&2; exec sleep 5")
	if !flooded.Started || flooded.StderrRefused == "" || len(flooded.Stderr) > 1024 || !strings.HasPrefix(flooded.Stderr, strings.Repeat("a", 600)) {
		t.Fatalf("bounded stderr refusal not retained: started=%v refused=%q len=%d", flooded.Started, flooded.StderrRefused, len(flooded.Stderr))
	}
	if flooded.ContextError != context.Canceled.Error() {
		t.Fatalf("output refusal cancellation not retained: %q", flooded.ContextError)
	}
	unstarted := linuxExecutorPreworkRefusal(nil, errors.New("not started"), nil, nil, nil, false)
	if unstarted.Started || unstarted.ExitCode != -1 || unstarted.WaitError != "not started" {
		t.Fatalf("unstarted facts wrong: %#v", unstarted)
	}
}

func TestLinuxExecutorPreworkRefusalDiagnosticNeedsPublishedResult(t *testing.T) {
	owner := &linuxExecutorPrework{result: make(chan struct{})}
	if _, ok := owner.refusalDiagnostic(); ok {
		t.Fatal("diagnostic without refusal")
	}
	owner.diagnostic = &linuxExecutorPreworkDiagnostic{ExitCode: 3}
	if _, ok := owner.refusalDiagnostic(); ok {
		t.Fatal("diagnostic read before the result was published")
	}
	close(owner.result)
	if diagnostic, ok := owner.refusalDiagnostic(); !ok || diagnostic.ExitCode != 3 {
		t.Fatal("published diagnostic unavailable")
	}
	var missing *linuxExecutorPrework
	if _, ok := missing.refusalDiagnostic(); ok {
		t.Fatal("nil owner diagnostic")
	}
}
