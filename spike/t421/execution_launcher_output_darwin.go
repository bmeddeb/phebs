//go:build darwin

package t421

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

// The adopted inner owns this pipe's sole remaining writer: the held outer
// created it, started the child, and closed its own writer. Only this private
// channel may have its status flags changed; user-facing output never does.
// Darwin-only: the inner-adoption path and its outer launcher remain Darwin.
func prepareExecutionInnerOutput(ctx context.Context, parent *executionParentLiveness, inherited *os.File) (*executionAuthorizationOutput, error) {
	if parent == nil || parent.alive == nil || parent.alive.Err() != nil || inherited == nil {
		return nil, errExecutionAuthorization
	}
	raw, err := inherited.SyscallConn()
	if err != nil {
		return nil, errExecutionAuthorization
	}
	var changed error
	err = raw.Control(func(fd uintptr) {
		var stat unix.Stat_t
		flags, flagErr := unix.FcntlInt(fd, unix.F_GETFL, 0)
		if flagErr != nil || unix.Fstat(int(fd), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFIFO ||
			stat.Uid != uint32(os.Getuid()) || flags&unix.O_ACCMODE != unix.O_WRONLY {
			changed = errExecutionAuthorization
			return
		}
		_, changed = unix.FcntlInt(fd, unix.F_SETFL, flags|unix.O_NONBLOCK)
	})
	if err != nil || changed != nil {
		return nil, errExecutionAuthorization
	}
	return prepareExecutionAuthorizationOutput(ctx, inherited)
}
