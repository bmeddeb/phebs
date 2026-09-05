//go:build darwin

package dispatchadmission

import (
	"os"

	"golang.org/x/sys/unix"
)

// An environment string does not own an arbitrary descriptor. Inspect it using
// raw read-only calls BEFORE wrapping/adopting/closing it: FD3 may already be the
// Go runtime's kqueue when the claimed inheritance is absent.
func inheritedProductionSocket(fd int) bool {
	kind, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || kind != unix.SOCK_STREAM {
		return false
	}
	peer, err := unix.Getpeername(fd)
	if _, ok := peer.(*unix.SockaddrUnix); err != nil || !ok {
		return false
	}
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil || pid <= 0 || pid != os.Getppid() {
		return false
	}
	credentials, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	// Darwin sys/ucred.h defines XUCRED_VERSION=0 (not exported by x/sys).
	return err == nil && credentials.Version == 0 && credentials.Uid == uint32(os.Geteuid())
}
