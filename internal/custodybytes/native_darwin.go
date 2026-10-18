//go:build darwin

package custodybytes

import "golang.org/x/sys/unix"

func sameCustodyByteStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Gen == b.Gen && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Uid == b.Uid && a.Gid == b.Gid && a.Flags == b.Flags && a.Size == b.Size && a.Blocks == b.Blocks && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func custodyByteFilesystem(stat unix.Statfs_t) ([2]int32, error) {
	if stat.Flags&unix.MNT_UNION != 0 {
		return [2]int32{}, ErrUnavailable
	}
	return stat.Fsid.Val, nil
}
