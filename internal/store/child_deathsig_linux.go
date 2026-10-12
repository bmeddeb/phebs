//go:build linux

package store

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// startBoundToParent starts the store child so the kernel SIGKILLs it if this
// process dies without stopping it (kill -9, an OOM kill of phebs alone). An
// orphaned child would otherwise keep the data directory's runtime descriptor
// live and refuse every later start. Linux delivers Pdeathsig when the
// creating thread exits, not the process, so a dedicated locked thread performs
// the start and stays parked until release is called after the child is joined.
func startBoundToParent(command *exec.Cmd, start func() (dispatchadmission.Handle, error)) (dispatchadmission.Handle, func(), error) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Pdeathsig = syscall.SIGKILL
	type started struct {
		handle dispatchadmission.Handle
		err    error
	}
	result := make(chan started, 1)
	parked := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		handle, err := start()
		result <- started{handle, err}
		if err == nil {
			<-parked
		}
	}()
	outcome := <-result
	var once sync.Once
	return outcome.handle, func() { once.Do(func() { close(parked) }) }, outcome.err
}
