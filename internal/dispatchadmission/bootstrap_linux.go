//go:build linux

package dispatchadmission

import (
	"os"

	"golang.org/x/sys/unix"
)

func inheritedProductionSocket(fd int) bool {
	kind, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || kind != unix.SOCK_STREAM {
		return false
	}
	peer, err := unix.Getpeername(fd)
	if _, ok := peer.(*unix.SockaddrUnix); err != nil || !ok {
		return false
	}
	credentials, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	return err == nil && credentials.Pid > 0 && int(credentials.Pid) == os.Getppid() && credentials.Uid == uint32(os.Geteuid())
}
