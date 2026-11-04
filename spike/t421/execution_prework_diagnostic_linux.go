//go:build linux

package t421

import (
	"errors"
	"os/exec"
	"syscall"
)

// linuxExecutorPreworkDiagnostic is private refusal evidence for the native
// harness. It copies only what the outer prework already captured within its
// existing bounds (128 B stdout, 1 KiB stderr) and the actual Wait facts. It
// never changes a refusal, grants authority or substitutes for retirement.
type linuxExecutorPreworkDiagnostic struct {
	Started        bool
	Exited         bool
	ExitCode       int
	Signal         string
	WaitError      string
	ContextError   string
	ReceiptMatched bool
	Stdout         string
	StdoutRefused  string
	Stderr         string
	StderrRefused  string
}

func linuxExecutorPreworkRefusal(command *exec.Cmd, waitErr, contextErr error, stdout, stderr *checkoutCommandOutput, receiptMatched bool) linuxExecutorPreworkDiagnostic {
	diagnostic := linuxExecutorPreworkDiagnostic{ExitCode: -1, ReceiptMatched: receiptMatched}
	if command != nil && command.Process != nil {
		diagnostic.Started = true
	}
	if command != nil && command.ProcessState != nil {
		diagnostic.ExitCode = command.ProcessState.ExitCode()
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok {
			diagnostic.Exited = status.Exited()
			if status.Signaled() {
				diagnostic.Signal = status.Signal().String()
			}
		}
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		diagnostic.WaitError = waitErr.Error()
	}
	if contextErr != nil {
		diagnostic.ContextError = contextErr.Error()
	}
	if stdout != nil {
		diagnostic.Stdout = stdout.buffer.String()
		if stdout.err != nil {
			diagnostic.StdoutRefused = stdout.err.Error()
		}
	}
	if stderr != nil {
		diagnostic.Stderr = stderr.buffer.String()
		if stderr.err != nil {
			diagnostic.StderrRefused = stderr.err.Error()
		}
	}
	return diagnostic
}

// refusalDiagnostic is valid only after Run returned; the result channel close
// orders it after the run goroutine's write.
func (owner *linuxExecutorPrework) refusalDiagnostic() (linuxExecutorPreworkDiagnostic, bool) {
	if owner == nil || owner.diagnostic == nil {
		return linuxExecutorPreworkDiagnostic{}, false
	}
	select {
	case <-owner.result:
		return *owner.diagnostic, true
	default:
		return linuxExecutorPreworkDiagnostic{}, false
	}
}
