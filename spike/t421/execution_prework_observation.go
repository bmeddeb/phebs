package t421

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// Private finite-prework facts, never caller/wire authority. Public constructors
// pass nil. Opaque process-local readers remain covered by actual process Wait.
type executionPreworkObservation struct {
	nativeCloseFailed atomic.Bool
	mu                sync.Mutex
	unknownCommand    *exec.Cmd
	unknownSettled    bool
	unknownJoined     bool
	unknownEmpty      bool
	retainedWorkspace string
	workspaceRetained bool
}

func (value *executionPreworkObservation) retainWorkspace(path string) {
	if value == nil {
		return
	}
	value.mu.Lock()
	defer value.mu.Unlock()
	if !value.workspaceRetained {
		value.retainedWorkspace, value.workspaceRetained = path, true
	}
}

func (value *executionPreworkObservation) close(err error) error {
	if value != nil && err != nil {
		value.nativeCloseFailed.Store(true)
	}
	return err
}

func (value *executionPreworkObservation) canStart() bool {
	if value == nil {
		return true
	}
	value.mu.Lock()
	defer value.mu.Unlock()
	return !value.nativeCloseFailed.Load() && value.unknownCommand == nil
}

func (value *executionPreworkObservation) process(command *exec.Cmd, joined, empty bool) {
	if value == nil || command == nil || command.Process == nil || joined && empty && command.ProcessState != nil {
		return
	}
	value.mu.Lock()
	defer value.mu.Unlock()
	// Construction is serial and stops on the first unknown; canStart prevents
	// a later launch from replacing this original owned command.
	if value.unknownCommand == nil {
		value.unknownCommand = command
		value.unknownJoined, value.unknownEmpty = joined, empty
	}
}

func (value *executionPreworkObservation) unknown() bool {
	if value == nil {
		return false
	}
	value.mu.Lock()
	defer value.mu.Unlock()
	return value.unknownCommand != nil
}

func (value *executionPreworkObservation) settle(ctx context.Context) bool {
	if value == nil {
		return true
	}
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.nativeCloseFailed.Load() {
		return false
	}
	if value.unknownCommand == nil || value.unknownSettled {
		return true
	}
	command := value.unknownCommand
	if command.ProcessState == nil || ctx == nil || ctx.Err() != nil {
		return false
	}
	deadline := time.Now().Add(5 * time.Second)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if t4013.WaitPrivateProcessSession(command.Process.Pid, deadline) != nil {
		return false
	}
	// Preserve the original negative fact and command; this is a later actual
	// settlement observation, never a reset or additional preparation attempt.
	value.unknownSettled = true
	return true
}
