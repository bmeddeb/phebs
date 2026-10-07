//go:build linux

package t421

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// This file supplies the Linux seam implementations for the existing custody
// machinery. Owned/Protected stay refusal-false: the Darwin immutable-flag
// custody model is not implemented here, so every flag-dependent caller
// refuses before creating or flagging anything. Same and systemToolReadOnlyVolume
// are real bounded observations used by the fixed-system tool path; neither
// proves immutability, a vendor signature or safety from a privileged host
// adversary.

func inputCustodyOwned(os.FileInfo) bool { return false }

func inputCustodyProtected(os.FileInfo) bool { return false }

// inputCustodySame compares every uniform Linux Stat_t identity field plus the
// FileInfo surface. Linux Stat_t has no Gen or Flags member, so those Darwin
// comparisons have no Linux counterpart; no weaker substitute is inferred.
func inputCustodySame(first, second os.FileInfo) bool {
	if first == nil || second == nil {
		return false
	}
	left, leftOK := first.Sys().(*syscall.Stat_t)
	right, rightOK := second.Sys().(*syscall.Stat_t)
	return leftOK && rightOK && left != nil && right != nil &&
		left.Dev == right.Dev && left.Ino == right.Ino &&
		left.Mode == right.Mode && left.Size == right.Size && left.Nlink == right.Nlink &&
		left.Uid == right.Uid && left.Gid == right.Gid &&
		left.Mtim == right.Mtim && left.Ctim == right.Ctim &&
		first.Mode() == second.Mode() && first.Size() == second.Size() && first.ModTime().Equal(second.ModTime())
}

func inputCustodyFlag(*os.File, bool) error {
	return errors.New("input custody flag protection is not implemented on Linux")
}

func inputCustodyVolume(*os.File) ([2]int32, error) {
	return [2]int32{}, errors.New("input custody volume identity is not implemented on Linux")
}

// systemToolReadOnlyVolume admits a root-owned fixed-system image: regular
// non-setuid file, gid 0, one link, mode without group/world write and with an
// execute bit. It does NOT assert a read-only mount, a vendor signature or
// protection from privileged root replacement; the caller separately pins the
// exact path and identity. The returned tuple synthesizes the filesystem type
// and device from held descriptors; Linux f_fsid semantics are not a stable
// contract, and the bits-level guard refuses values a lossless int32 encoding
// cannot carry (btrfs 0x9123683E sets the high bit and must stay admitted).
func systemToolReadOnlyVolume(file *os.File, info os.FileInfo) ([2]int32, error) {
	if file == nil || info == nil || !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return [2]int32{}, ErrExecutionToolCustody
	}
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !ok || metadata == nil || metadata.Uid != 0 || metadata.Gid != 0 || metadata.Nlink != 1 ||
		metadata.Mode&unix.S_IFMT != unix.S_IFREG || metadata.Mode&(unix.S_ISUID|unix.S_ISGID|0o022) != 0 || metadata.Mode&0o111 == 0 {
		return [2]int32{}, ErrExecutionToolCustody
	}
	var filesystem unix.Statfs_t
	var held unix.Stat_t
	if unix.Fstatfs(int(file.Fd()), &filesystem) != nil || unix.Fstat(int(file.Fd()), &held) != nil ||
		filesystem.Type < 0 || filesystem.Type > 0xFFFFFFFF || held.Dev > 0xFFFFFFFF {
		return [2]int32{}, ErrExecutionToolCustody
	}
	return [2]int32{int32(filesystem.Type), int32(held.Dev)}, nil
}
