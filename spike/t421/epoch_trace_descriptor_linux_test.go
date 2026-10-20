//go:build linux

package t421

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxEpochTraceDescriptorCloseDisarmsBeforeError(t *testing.T) {
	for _, alreadyClosed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "uncertain-close"}[alreadyClosed], func(t *testing.T) {
			fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			leader := &linuxEpochTraceLeader{fd: fd}
			if alreadyClosed && unix.Close(fd) != nil {
				t.Fatal("fixture close")
			}
			err = leader.closeFD()
			if leader.fd != -1 || alreadyClosed && !errors.Is(err, unix.EBADF) || !alreadyClosed && err != nil {
				t.Fatal("close did not consume the original descriptor capability", leader.fd, err)
			}
			// Deliberately reuse our retired slot, then require the retained
			// leader to refuse without closing or polling this unrelated object.
			replacement, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(replacement) }()
			if replacement != fd {
				duplicate, err := unix.FcntlInt(uintptr(replacement), unix.F_DUPFD_CLOEXEC, fd)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = unix.Close(duplicate) }()
				if duplicate != fd {
					t.Fatal("retired fixture slot concurrently occupied; no replacement touched")
				}
			}
			if leader.closeFD() == nil {
				t.Fatal("retired integer regained close authority")
			}
			var stat unix.Stat_t
			if err := unix.Fstat(fd, &stat); err != nil {
				t.Fatal("replacement descriptor was touched", err)
			}
			root := &linuxEpochTraceRoot{pending: map[int]*linuxEpochTraceLeader{42: leader}}
			if root.retireReaped(t.Context()) == nil || len(root.pending) != 1 || root.pending[42] != leader {
				t.Fatal("uncertain descriptor retired its pending owner")
			}
			if err := unix.Fstat(fd, &stat); err != nil {
				t.Fatal("later retirement touched the reused slot", err)
			}
		})
	}
}
