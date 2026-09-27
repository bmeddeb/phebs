//go:build linux

package typedworkspace

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func ownerReplace(root *os.File, before, after string) error {
	return unix.Renameat(int(root.Fd()), before, int(root.Fd()), after)
}

// Acquire the existing publication lock through the pinned allocation root.
// The path-based shared helper may create ancestry, which is not acceptable
// between first-growth admission and the under-lock base identity check.
// The operator must provision the empty 0600 lock; this acquisition never grows.
func ownerBaseLease(ctx context.Context, root *os.File) (func(), error) {
	fd, err := unix.Openat(int(root.Fd()), publicationLock, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, ErrCustody
	}
	f := os.NewFile(uintptr(fd), "typed-owner-base-lock")
	fail := func(err error) (func(), error) { _ = f.Close(); return nil, err }
	var st, base unix.Stat_t
	if unix.Fstat(fd, &st) != nil || unix.Fstat(int(root.Fd()), &base) != nil || st.Mode != unix.S_IFREG|0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size != 0 || st.Dev != base.Dev {
		return fail(ErrCustody)
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return fail(ErrCustody)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-timer.C:
			return fail(ErrCustody)
		case <-tick.C:
		}
	}
	var named unix.Stat_t
	if unix.Fstatat(int(root.Fd()), publicationLock, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || named.Dev != st.Dev || named.Ino != st.Ino || named.Mode != st.Mode || named.Uid != st.Uid || named.Nlink != 1 || named.Size != 0 {
		return fail(ErrCustody)
	}
	return func() { _ = f.Close() }, nil
}
