//go:build darwin || linux

package t421

import (
	"context"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// executionPipeIdentity is the uniform device/inode/mode identity row shared by
// launcher pipe and authorization-output observations.
type executionPipeIdentity struct {
	device int64
	inode  uint64
	mode   uint32
}

// executionAuthorizationOutput owns a duplicate of an already nonblocking
// stream. It never changes the inherited open-file description's status flags.
// Blocking streams, terminals and regular files are not yet launcher outputs.
type executionAuthorizationOutput struct {
	file        *os.File
	identity    executionPipeIdentity
	access      int
	deadline    time.Time
	used        bool
	packageUsed bool
}

func prepareExecutionAuthorizationOutput(ctx context.Context, inherited *os.File) (*executionAuthorizationOutput, error) {
	if ctx == nil || ctx.Err() != nil || inherited == nil {
		return nil, errExecutionAuthorization
	}
	deadline, ok := ctx.Deadline()
	if !ok || !time.Now().Before(deadline) {
		return nil, errExecutionAuthorization
	}
	raw, err := inherited.SyscallConn()
	if err != nil {
		return nil, errExecutionAuthorization
	}
	output := &executionAuthorizationOutput{deadline: deadline}
	duplicate := -1
	var observeErr error
	err = raw.Control(func(fd uintptr) {
		output.identity, output.access, observeErr = executionAuthorizationOutputRow(fd)
		if observeErr == nil {
			duplicate, observeErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 3)
		}
	})
	if err != nil || observeErr != nil || duplicate < 0 {
		if duplicate >= 0 {
			_ = unix.Close(duplicate)
		}
		return nil, errExecutionAuthorization
	}
	output.file = os.NewFile(uintptr(duplicate), "t422-authorization-output")
	if output.file == nil {
		_ = unix.Close(duplicate)
		return nil, errExecutionAuthorization
	}
	if output.check(ctx) != nil || output.file.SetWriteDeadline(deadline) != nil || ctx.Err() != nil {
		_ = output.file.Close()
		return nil, errExecutionAuthorization
	}
	return output, nil
}

func (output *executionAuthorizationOutput) check(ctx context.Context) error {
	if output == nil || output.file == nil || ctx == nil || ctx.Err() != nil || !time.Now().Before(output.deadline) {
		return errExecutionAuthorization
	}
	raw, err := output.file.SyscallConn()
	if err != nil {
		return errExecutionAuthorization
	}
	var checkErr error
	err = raw.Control(func(fd uintptr) {
		identity, access, observeErr := executionAuthorizationOutputRow(fd)
		flags, flagErr := unix.FcntlInt(fd, unix.F_GETFD, 0)
		if observeErr != nil || flagErr != nil || flags&unix.FD_CLOEXEC == 0 || identity != output.identity || access != output.access {
			checkErr = errExecutionAuthorization
		}
	})
	if err != nil || checkErr != nil || ctx.Err() != nil {
		return errExecutionAuthorization
	}
	return nil
}

func executionAuthorizationOutputRow(fd uintptr) (executionPipeIdentity, int, error) {
	flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	var stat unix.Stat_t
	statErr := unix.Fstat(int(fd), &stat)
	access := flags & unix.O_ACCMODE
	kind := stat.Mode & unix.S_IFMT
	if err != nil || statErr != nil || flags&unix.O_NONBLOCK == 0 ||
		(access != unix.O_WRONLY && access != unix.O_RDWR) || (kind != unix.S_IFIFO && kind != unix.S_IFSOCK) ||
		stat.Uid != uint32(os.Getuid()) {
		return executionPipeIdentity{}, 0, errExecutionAuthorization
	}
	return executionPipeIdentity{device: int64(stat.Dev), inode: stat.Ino, mode: uint32(stat.Mode)}, access, nil
}
