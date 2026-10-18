//go:build linux

package custodybytes

import "golang.org/x/sys/unix"

// Linux has no Darwin inode generation or immutable-flag fields. Compare the
// actual Linux metadata instead; this is an observation, not protected custody.
func sameCustodyByteStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Nlink == b.Nlink &&
		a.Uid == b.Uid && a.Gid == b.Gid && a.Rdev == b.Rdev && a.Size == b.Size &&
		a.Blksize == b.Blksize && a.Blocks == b.Blocks && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

// The actual held filesystem ID is compared again throughout the walk. It
// proves neither an isolated mount, immutable inputs nor unique physical bytes.
// Those remain the caller's independently admitted ownership boundaries.
func custodyByteFilesystem(stat unix.Statfs_t) ([2]int32, error) {
	return stat.Fsid.Val, nil
}
