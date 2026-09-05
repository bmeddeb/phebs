//go:build darwin || linux

package dispatchadmission

import (
	"os"
	"syscall"
)

// NewPipe creates an unnamed private bidirectional socket pair. Both ends are
// close-on-exec. The owner may pass exactly the child end through Cmd.ExtraFiles,
// then close its copy immediately after successful Start. No path or listener
// is created, and no ambient descriptor/environment is consulted.
func NewPipe() (parent, child *os.File, err error) {
	// Protect the non-atomic Darwin CLOEXEC setup against concurrent Go forks.
	syscall.ForkLock.RLock()
	fds, socketErr := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if socketErr == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}
	syscall.ForkLock.RUnlock()
	if socketErr != nil {
		return nil, nil, ErrTransport
	}
	return os.NewFile(uintptr(fds[0]), "dispatch-parent"), os.NewFile(uintptr(fds[1]), "dispatch-child"), nil
}

func protectInheritance(file *os.File) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return ErrTransport
	}
	syscall.ForkLock.RLock()
	err = raw.Control(func(fd uintptr) { syscall.CloseOnExec(int(fd)) })
	syscall.ForkLock.RUnlock()
	if err != nil {
		return ErrTransport
	}
	return nil
}
