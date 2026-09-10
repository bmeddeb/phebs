//go:build darwin

package custodybytes

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"math"
	"os"
	"path/filepath"
	"syscall"
)

type custodyByteDirectory struct {
	file      *os.File
	name      string
	pathBytes int
	before    unix.Stat_t
}

// Serial DFS retains at most (4096+1)/2 directory cursors. Pinned Darwin Go
// duplicates each cursor FD for libc fdopendir: at most 4096 traversal FDs plus
// one os.Root anchor, excluding the borrowed owner FD. Readdirnames(1) adds no
// whole-directory list; libc DIR storage/buffering is additional and unmeasured.
// Union mounts are refused because libc may inventory their whole directory.
// OS FD exhaustion refuses rather than raising limits. No rescans, content
// reads or child tools.
// Cancellation is checked between native calls; it is not a syscall deadline.
func walkCustodyBytes(ctx context.Context, owner borrowedRoot) (value Sample, retErr error) {
	if checkCustodyByteRoot(ctx, owner) != nil {
		return value, ErrUnavailable
	}
	root, err := os.OpenRoot(owner.path)
	if err != nil {
		return value, ErrUnavailable
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	file, err := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return value, ErrUnavailable
	}
	var initial unix.Stat_t
	if unix.Fstat(int(file.Fd()), &initial) != nil {
		_ = file.Close()
		return value, ErrUnavailable
	}
	info, err := file.Stat()
	volume, volumeErr := custodyByteVolume(file)
	if err != nil || volumeErr != nil || volume != owner.volume || !os.SameFile(owner.info, info) || checkCustodyByteRoot(ctx, owner) != nil {
		_ = file.Close()
		return value, ErrUnavailable
	}
	stack := []custodyByteDirectory{{file: file, pathBytes: len(owner.path), before: initial}}
	defer func() {
		for _, entry := range stack {
			retErr = errors.Join(retErr, entry.file.Close())
		}
	}()
	if addCustodyByteStat(&value, initial, initial.Dev) != nil {
		return Sample{}, ErrUnavailable
	}
	for len(stack) > 0 {
		if ctx.Err() != nil {
			return Sample{}, ErrUnavailable
		}
		current := &stack[len(stack)-1]
		entries, readErr := current.file.Readdirnames(1)
		if readErr != nil && readErr != io.EOF {
			return Sample{}, ErrUnavailable
		}
		if len(entries) == 0 {
			var after unix.Stat_t
			volume, e := custodyByteVolume(current.file)
			if readErr != io.EOF || e != nil || volume != owner.volume || unix.Fstat(int(current.file.Fd()), &after) != nil || !sameCustodyByteStat(current.before, after) {
				return Sample{}, ErrUnavailable
			}
			if len(stack) > 1 {
				var linked unix.Stat_t
				parent := stack[len(stack)-2].file
				if unix.Fstatat(int(parent.Fd()), current.name, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameCustodyByteStat(after, linked) {
					return Sample{}, ErrUnavailable
				}
			}
			if current.file.Close() != nil {
				return Sample{}, ErrUnavailable
			}
			stack = stack[:len(stack)-1]
			continue
		}
		name := entries[0]
		pathBytes := current.pathBytes + 1 + len(name)
		if name == "." || name == ".." || filepath.Base(name) != name || pathBytes > MaximumPathBytes {
			return Sample{}, ErrUnavailable
		}
		var before, after unix.Stat_t
		if unix.Fstatat(int(current.file.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil || before.Dev != initial.Dev {
			return Sample{}, ErrUnavailable
		}
		if before.Mode&unix.S_IFMT == unix.S_IFDIR {
			fd, e := unix.Openat(int(current.file.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if e != nil {
				return Sample{}, ErrUnavailable
			}
			child := os.NewFile(uintptr(fd), name)
			volume, volumeErr := custodyByteVolume(child)
			if volumeErr != nil || volume != owner.volume || unix.Fstat(fd, &after) != nil || !sameCustodyByteStat(before, after) {
				_ = child.Close()
				return Sample{}, ErrUnavailable
			}
			stack = append(stack, custodyByteDirectory{file: child, name: name, pathBytes: pathBytes, before: after})
		} else if unix.Fstatat(int(current.file.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameCustodyByteStat(before, after) {
			return Sample{}, ErrUnavailable
		}
		if addCustodyByteStat(&value, before, initial.Dev) != nil {
			return Sample{}, ErrUnavailable
		}
	}
	var final unix.Stat_t
	if unix.Fstat(int(owner.file.Fd()), &final) != nil || !sameCustodyByteStat(initial, final) || checkCustodyByteRoot(ctx, owner) != nil {
		return Sample{}, ErrUnavailable
	}
	return value, nil
}

func checkCustodyByteRoot(ctx context.Context, owner borrowedRoot) error {
	if ctx == nil || ctx.Err() != nil || owner.file == nil || owner.info == nil || owner.path == "/" || !filepath.IsAbs(owner.path) || len(owner.path) > MaximumPathBytes {
		return ErrUnavailable
	}
	held, e := owner.file.Stat()
	linked, pathErr := os.Lstat(owner.path)
	volume, volumeErr := custodyByteVolume(owner.file)
	canonical, canonicalErr := filepath.EvalSymlinks(owner.path)
	if e != nil || pathErr != nil || volumeErr != nil || canonicalErr != nil || canonical != owner.path || volume != owner.volume || !os.SameFile(owner.info, held) || !os.SameFile(held, linked) || !custodyOwned(linked) || !linked.IsDir() || linked.Mode().Perm() != 0o700 {
		return ErrUnavailable
	}
	return nil
}

func sameCustodyByteStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Gen == b.Gen && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Uid == b.Uid && a.Gid == b.Gid && a.Flags == b.Flags && a.Size == b.Size && a.Blocks == b.Blocks && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func custodyByteVolume(file *os.File) ([2]int32, error) {
	var stat unix.Statfs_t
	if file == nil || unix.Fstatfs(int(file.Fd()), &stat) != nil {
		return [2]int32{}, ErrUnavailable
	}
	return custodyByteFilesystem(stat)
}

func custodyByteFilesystem(stat unix.Statfs_t) ([2]int32, error) {
	if stat.Flags&unix.MNT_UNION != 0 {
		return [2]int32{}, ErrUnavailable
	}
	return stat.Fsid.Val, nil
}

func addCustodyByteStat(value *Sample, stat unix.Stat_t, device int32) error {
	if stat.Dev != device || stat.Size < 0 || stat.Blocks < 0 || uint64(stat.Blocks) > math.MaxUint64/512 {
		return ErrUnavailable
	}
	allocated := uint64(stat.Blocks) * 512
	if allocated > math.MaxUint64-value.AllocatedBytes {
		return ErrUnavailable
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFREG {
		if uint64(stat.Size) > math.MaxUint64-value.LogicalBytes {
			return ErrUnavailable
		}
		value.LogicalBytes += uint64(stat.Size)
	}
	value.AllocatedBytes += allocated
	return nil
}

func custodyOwned(info os.FileInfo) bool {
	if info == nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat != nil && int(stat.Uid) == os.Geteuid() && stat.Mode&(unix.S_ISUID|unix.S_ISGID) == 0 && stat.Mode&unix.S_IFMT == unix.S_IFDIR && info.IsDir()
}
