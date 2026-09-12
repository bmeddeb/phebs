//go:build darwin || linux

package dispatchadmission

import "golang.org/x/sys/unix"

// Read-only capture precedes every socket adoption. A missing original FD7
// cannot later become an inherited socket just because the poller reused it.
type archiveMeasurementIdentity struct {
	device uint64
	inode  uint64
}

func captureInheritedArchiveMeasurement() *archiveMeasurementIdentity {
	if !inheritedProductionSocket(7) {
		return nil
	}
	var stat unix.Stat_t
	if unix.Fstat(7, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return nil
	}
	return &archiveMeasurementIdentity{device: uint64(stat.Dev), inode: stat.Ino}
}
