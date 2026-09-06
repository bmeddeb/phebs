//go:build darwin || linux

// Package ownedpipe creates unnamed, close-on-exec Unix socket pairs.
package ownedpipe

import (
	"os"
	"syscall"
)

// New transfers both endpoints to the caller. Only an explicit ExtraFiles
// entry can pass one to a child; no path, listener or ambient FD is used.
func New() (parent, child *os.File, err error) {
	// Darwin needs non-atomic CLOEXEC setup protected against concurrent forks.
	syscall.ForkLock.RLock()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(fds[0]), "dispatch-parent"), os.NewFile(uintptr(fds[1]), "dispatch-child"), nil
}
