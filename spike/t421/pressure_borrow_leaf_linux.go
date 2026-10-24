//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const linuxPressureBorrowLeafName = "executor-leaf"

type linuxPressureBorrowLeaf struct {
	file        *os.File
	stat        unix.Stat_t
	fsid        [2]int32
	mount       uint64
	uid, gid    uint32
	observation *executionPreworkObservation
	closed      bool
	closeErr    error
}

// The only modified ownership is a fresh leaf. This function borrows the actual
// workspace descriptor; it never changes the root-owned H2m ancestors.
func createLinuxPressureBorrowLeaf(ctx context.Context, workspace *os.File, uid, gid uint32, observation *executionPreworkObservation) (_ *linuxPressureBorrowLeaf, retErr error) {
	if ctx == nil || ctx.Err() != nil || workspace == nil || uid == ^uint32(0) || gid == ^uint32(0) {
		return nil, ErrLinuxPressure
	}
	fd := int(workspace.Fd())
	if unix.Mkdirat(fd, linuxPressureBorrowLeafName, 0o700) != nil {
		return nil, ErrLinuxPressure
	}
	leafFD, err := unix.Openat(fd, linuxPressureBorrowLeafName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	leaf := &linuxPressureBorrowLeaf{file: os.NewFile(uintptr(leafFD), "pressure-child-leaf"), uid: uint32(os.Geteuid()), gid: uint32(os.Getegid()), observation: observation}
	defer func() {
		if retErr != nil {
			_ = leaf.Close()
		}
	}()
	// Authenticate the freshly opened root-owned link before changing metadata.
	if leaf.capture(ctx) != nil || leaf.checkLink(ctx, workspace) != nil {
		return nil, ErrLinuxPressure
	}
	if unix.Fchown(leafFD, int(uid), int(gid)) != nil || unix.Fchmod(leafFD, 0o700) != nil {
		return nil, ErrLinuxPressure
	}
	leaf.uid, leaf.gid = uid, gid
	if leaf.capture(ctx) != nil || leaf.checkLink(ctx, workspace) != nil || workspace.Sync() != nil {
		return nil, ErrLinuxPressure
	}
	return leaf, nil
}

func (leaf *linuxPressureBorrowLeaf) capture(ctx context.Context) error {
	var filesystem unix.Statfs_t
	var extended unix.Statx_t
	fd := int(leaf.file.Fd())
	if unix.Fstat(fd, &leaf.stat) != nil || unix.Fstatfs(fd, &filesystem) != nil ||
		unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil || extended.Mask&unix.STATX_MNT_ID == 0 {
		return ErrLinuxPressure
	}
	leaf.fsid, leaf.mount = filesystem.Fsid.Val, extended.Mnt_id
	return leaf.check(ctx)
}

func (leaf *linuxPressureBorrowLeaf) check(ctx context.Context) error {
	if leaf == nil || leaf.closed || leaf.file == nil || ctx == nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	var extended unix.Statx_t
	fd := int(leaf.file.Fd())
	if unix.Fstat(fd, &stat) != nil || stat.Dev != leaf.stat.Dev || stat.Ino != leaf.stat.Ino || stat.Mode != unix.S_IFDIR|0o700 ||
		stat.Uid != leaf.uid || stat.Gid != leaf.gid || stat.Nlink == 0 || unix.Fstatfs(fd, &filesystem) != nil || filesystem.Fsid.Val != leaf.fsid ||
		unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil ||
		extended.Mask&unix.STATX_MNT_ID == 0 || extended.Mnt_id != leaf.mount || leaf.mount == 0 {
		return ErrLinuxPressure
	}
	flags, err := unix.FcntlInt(leaf.file.Fd(), unix.F_GETFD, 0)
	status, statusErr := unix.FcntlInt(leaf.file.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 || statusErr != nil || status&unix.O_ACCMODE != unix.O_RDONLY || status&unix.O_PATH != 0 {
		return ErrLinuxPressure
	}
	return nil
}

func (leaf *linuxPressureBorrowLeaf) checkLink(ctx context.Context, workspace *os.File) error {
	if leaf.check(ctx) != nil || workspace == nil {
		return ErrLinuxPressure
	}
	var linked, parent unix.Stat_t
	var filesystem unix.Statfs_t
	var extended unix.Statx_t
	fd := int(workspace.Fd())
	if unix.Fstat(fd, &parent) != nil || parent.Mode != unix.S_IFDIR|0o700 || parent.Uid != uint32(os.Geteuid()) ||
		unix.Fstatat(fd, linuxPressureBorrowLeafName, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || linked.Dev != leaf.stat.Dev || linked.Ino != leaf.stat.Ino ||
		parent.Dev != leaf.stat.Dev || unix.Fstatfs(fd, &filesystem) != nil || filesystem.Fsid.Val != leaf.fsid ||
		unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil ||
		extended.Mask&unix.STATX_MNT_ID == 0 || extended.Mnt_id != leaf.mount {
		return ErrLinuxPressure
	}
	return nil
}

func (leaf *linuxPressureBorrowLeaf) Close() error {
	if leaf == nil {
		return nil
	}
	if !leaf.closed {
		leaf.closed = true
		if leaf.file != nil {
			leaf.closeErr = leaf.observation.close(leaf.file.Close())
		}
	}
	return leaf.closeErr
}

// The child owns its process cwd until actual native Wait. No pathname export
// or ancestor chmod is needed. This is a fixed 4KiB preparation control only.
func (leaf *linuxPressureBorrowLeaf) work(ctx context.Context) (retErr error) {
	if leaf.check(ctx) != nil || leaf.file.Chdir() != nil {
		return ErrLinuxPressure
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return ErrLinuxPressure
	}
	defer func() { retErr = errors.Join(retErr, leaf.observation.close(root.Close())) }()
	held, heldErr := leaf.file.Stat()
	current, currentErr := root.Stat(".")
	if heldErr != nil || currentErr != nil || !os.SameFile(held, current) || leaf.check(ctx) != nil {
		return ErrLinuxPressure
	}
	file, err := root.OpenFile("prework.bin", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrLinuxPressure
	}
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, leaf.observation.close(file.Close()))
		}
	}()
	var expected, actual [4096]byte
	copy(expected[:], "phebs-linux-pressure-leaf-v1")
	if n, writeErr := file.Write(expected[:]); writeErr != nil || n != len(expected) || file.Sync() != nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	if n, readErr := file.ReadAt(actual[:], 0); readErr != nil || n != len(actual) || !bytes.Equal(expected[:], actual[:]) {
		return ErrLinuxPressure
	}
	var sentinel [1]byte
	if n, readErr := file.ReadAt(sentinel[:], int64(len(actual))); n != 0 || !errors.Is(readErr, io.EOF) || leaf.check(ctx) != nil {
		return ErrLinuxPressure
	}
	closeErr := leaf.observation.close(file.Close())
	closed = true
	if closeErr != nil || root.Remove("prework.bin") != nil || leaf.file.Sync() != nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	return nil
}
