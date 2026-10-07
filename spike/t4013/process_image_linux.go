//go:build linux

package t4013

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLinuxProcessImageMatch contains no PID, path or native diagnostic.
var ErrLinuxProcessImageMatch = errors.New("linux process executable object unavailable or mismatched")

// MatchLinuxProcessExecutableImage compares a live task's kernel executable
// object with one held image FD, including anonymous/deleted objects. This is
// separate from pathname observations, which still reject deleted images.
// A pidfd exit fence and two bounded stat records bracket one executable open.
// This is a sampled object match, not provenance, immutability, ownership,
// continuous exec history, descendant custody or permission to dispatch. The
// caller must hold and independently protect/admit the expected image.
func MatchLinuxProcessExecutableImage(ctx context.Context, pid int, image *os.File) (retErr error) {
	if ctx == nil || ctx.Err() != nil || pid <= 0 || pid > math.MaxInt32 || image == nil {
		return ErrLinuxProcessImageMatch
	}
	expected, err := image.Stat()
	if err != nil || !expected.Mode().IsRegular() || expected.Mode().Perm()&0o111 == 0 {
		return ErrLinuxProcessImageMatch
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return ErrLinuxProcessImageMatch
	}
	defer func() {
		if unix.Close(pidfd) != nil {
			retErr = ErrLinuxProcessImageMatch
		}
	}()
	if !linuxImageTaskLive(pidfd) || ctx.Err() != nil {
		return ErrLinuxProcessImageMatch
	}
	before, err := linuxProcessStatAt("/proc", pid)
	if err != nil || linuxProcessDefunct(before.state) || ctx.Err() != nil {
		return ErrLinuxProcessImageMatch
	}
	// Following this kernel proc link is intentional; no user image path is
	// resolved, and inode equality must match the held object, never its label.
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/exe", pid), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrLinuxProcessImageMatch
	}
	observed := os.NewFile(uintptr(fd), "process-executable-object")
	defer func() {
		if observed.Close() != nil {
			retErr = ErrLinuxProcessImageMatch
		}
	}()
	info, err := observed.Stat()
	if err != nil || !os.SameFile(expected, info) || ctx.Err() != nil {
		return ErrLinuxProcessImageMatch
	}
	after, err := linuxProcessStatAt("/proc", pid)
	current, imageErr := image.Stat()
	if err != nil || imageErr != nil || before.snapshot != after.snapshot || linuxProcessDefunct(after.state) ||
		!os.SameFile(expected, current) || !linuxImageTaskLive(pidfd) || ctx.Err() != nil {
		return ErrLinuxProcessImageMatch
	}
	return nil
}

func linuxImageTaskLive(pidfd int) bool {
	if pidfd < 0 {
		return false
	}
	poll := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	n, err := unix.Poll(poll, 0)
	return err == nil && n == 0 && poll[0].Revents == 0
}
