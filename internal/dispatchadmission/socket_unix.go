//go:build darwin || linux

package dispatchadmission

import (
	"os"
	"syscall"

	"github.com/bmeddeb/phebs/internal/ownedpipe"
)

// NewPipe creates an unnamed private bidirectional socket pair. Both ends are
// close-on-exec. The owner may pass exactly the child end through Cmd.ExtraFiles,
// then close its copy immediately after successful Start. No path or listener
// is created, and no ambient descriptor/environment is consulted.
func NewPipe() (parent, child *os.File, err error) {
	parent, child, err = ownedpipe.New()
	if err != nil {
		return nil, nil, ErrTransport
	}
	return parent, child, nil
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
