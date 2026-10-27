//go:build linux

package typedsandbox

import (
	"errors"
	"os/exec"
	"syscall"
)

// HostFormatterFailure describes existing formatter results, never descendant
// custody or cleanup authority. A missing wait status is explicitly unknown.
type HostFormatterFailure struct {
	Stage             string `json:"stage"`
	Started           bool   `json:"started"`
	WaitReturned      bool   `json:"wait_returned"`
	WaitStatusKnown   bool   `json:"wait_status_known"`
	ExitCode          int    `json:"exit_code"`
	Signal            int    `json:"signal"`
	ErrorNumber       uint64 `json:"error_number"`
	ProcessGroupProbe string `json:"process_group_probe"`
	GroupErrorNumber  uint64 `json:"group_error_number"`
	FormatterContext  string `json:"formatter_context"`
}

type hostFormatterFailure struct {
	class       error
	observation HostFormatterFailure
}

func (e *hostFormatterFailure) Error() string { return e.class.Error() }
func (e *hostFormatterFailure) Unwrap() error { return e.class }

// HostFormatterFailureOf returns a copy without exposing raw command errors,
// paths, arguments or formatter output. ErrRefused/ErrCustody remain unwrap-able.
func HostFormatterFailureOf(err error) (HostFormatterFailure, bool) {
	var failure *hostFormatterFailure
	if errors.As(err, &failure) {
		return failure.observation, true
	}
	return HostFormatterFailure{}, false
}

func formatterFailure(class error, stage string, cmd *exec.Cmd, cause, probe error) error {
	d := HostFormatterFailure{Stage: stage, ExitCode: -1, ProcessGroupProbe: "unobserved", FormatterContext: "unobserved"}
	var errno syscall.Errno
	if errors.As(cause, &errno) {
		d.ErrorNumber = uint64(errno)
	}
	if stage != "start" {
		d.Started, d.WaitReturned = true, true
		switch {
		case errors.Is(probe, syscall.ESRCH):
			d.ProcessGroupProbe = "absent"
		case probe == nil:
			d.ProcessGroupProbe = "present"
		default:
			d.ProcessGroupProbe = "unavailable"
		}
		if errors.As(probe, &errno) {
			d.GroupErrorNumber = uint64(errno)
		}
		if cmd.ProcessState != nil {
			if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
				d.WaitStatusKnown, d.ExitCode = true, status.ExitStatus()
				if status.Signaled() {
					d.Signal = int(status.Signal())
				}
			}
		}
	}
	return &hostFormatterFailure{class: class, observation: d}
}
