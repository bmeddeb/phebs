package t421

import "os"

// linuxInputCopyOwnerMatches is the Linux metadata-only copy predicate. Its
// fixed Linux mode bits keep off-host controls independent of host Stat_t.
// Neither this observation nor a successful mutable copy protects bytes,
// directory entries, mounts or aliases, or admits a command or tool identity.
func linuxInputCopyOwnerMatches(mode os.FileMode, nativeMode, uid uint32, euid int, links uint64) bool {
	const (
		linuxTypeMask  = 0o170000
		linuxRegular   = 0o100000
		linuxDirectory = 0o040000
		linuxSetID     = 0o6000
	)
	if euid < 0 || uint64(euid) > 1<<32-1 || uid != uint32(euid) ||
		mode&(os.ModeSetuid|os.ModeSetgid) != 0 || nativeMode&linuxSetID != 0 ||
		mode.Perm() != os.FileMode(nativeMode&0o777) {
		return false
	}
	switch nativeMode & linuxTypeMask {
	case linuxRegular:
		return mode.IsRegular() && links == 1
	case linuxDirectory:
		return mode.Type() == os.ModeDir && links > 0
	default:
		return false
	}
}
