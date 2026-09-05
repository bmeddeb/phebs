//go:build darwin

package t421

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func inputCustodyOwned(info os.FileInfo) bool {
	if info == nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil || int(stat.Uid) != os.Geteuid() || stat.Mode&(unix.S_ISUID|unix.S_ISGID) != 0 {
		return false
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && info.Mode().IsRegular() && stat.Nlink == 1 ||
		stat.Mode&unix.S_IFMT == unix.S_IFDIR && info.IsDir()
}

func inputCustodyProtected(info os.FileInfo) bool {
	return inputCustodyOwned(info) && info.Sys().(*syscall.Stat_t).Flags&unix.UF_IMMUTABLE != 0
}

func inputCustodySame(first, second os.FileInfo) bool {
	if first == nil || second == nil {
		return false
	}
	left, leftOK := first.Sys().(*syscall.Stat_t)
	right, rightOK := second.Sys().(*syscall.Stat_t)
	return leftOK && rightOK && left != nil && right != nil &&
		left.Dev == right.Dev && left.Ino == right.Ino && left.Gen == right.Gen &&
		left.Mode == right.Mode && left.Size == right.Size && left.Nlink == right.Nlink &&
		left.Uid == right.Uid && left.Gid == right.Gid && left.Flags == right.Flags &&
		left.Mtimespec == right.Mtimespec && left.Ctimespec == right.Ctimespec &&
		first.Mode() == second.Mode() && first.Size() == second.Size() && first.ModTime().Equal(second.ModTime())
}

// inputCustodyFlag changes only the owner-set immutable bit on an owned copy.
// The constructor must close its writing descriptor before protection: this
// flag does not revoke access through a pre-existing writable descriptor.
func inputCustodyFlag(file *os.File, protected bool) error {
	if file == nil {
		return errors.New("input custody descriptor is unavailable")
	}
	info, err := file.Stat()
	if err != nil || !inputCustodyOwned(info) {
		return errors.New("input custody descriptor is not an owned regular file or directory")
	}
	flags := info.Sys().(*syscall.Stat_t).Flags &^ unix.UF_IMMUTABLE
	if protected {
		flags |= unix.UF_IMMUTABLE
	}
	if err := unix.Fchflags(int(file.Fd()), int(flags)); err != nil {
		return errors.New("input custody flags cannot be changed")
	}
	updated, err := file.Stat()
	if err != nil || !inputCustodyOwned(updated) || updated.Sys().(*syscall.Stat_t).Flags != flags {
		return errors.New("input custody flags were not established")
	}
	return nil
}

func inputCustodyVolume(file *os.File) ([2]int32, error) {
	if file == nil {
		return [2]int32{}, errors.New("input custody volume descriptor is unavailable")
	}
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return [2]int32{}, errors.New("input custody volume is unavailable")
	}
	return stat.Fsid.Val, nil
}
