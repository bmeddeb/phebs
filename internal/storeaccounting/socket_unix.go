//go:build darwin || linux

package storeaccounting

import (
	"os"
	"syscall"
)

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
