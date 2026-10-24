//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	linuxPressureAuthorScaffold       = "/tmp/phebs-h2tc-v1"
	linuxPressureAuthorWork           = linuxPressureAuthorScaffold + "/work"
	linuxPressureAuthorInputs         = linuxPressureAuthorScaffold + "/inputs"
	linuxPressureAuthorScaffoldBytes  = 4 << 20
	linuxPressureAuthorScaffoldInodes = 16
)

// A source-created mount counterpart, never a path alias that grants pressure
// authority. Original and view are held through every borrower and actual Wait.
type linuxPressureAuthorView struct {
	self                            *linuxPressureAuthorView
	original, file                  *os.File
	originalStat, stat              unix.Stat_t
	fsid                            [2]int32
	originalMount, mount            uint64
	namespaceDevice, namespaceInode uint64
	uid, gid                        uint32
	observation                     *executionPreworkObservation
	closed                          bool
	closeErr                        error
}

func linuxPressureAuthorOriginalPaths(value linuxExecutorPreworkSelection) bool {
	for _, path := range []string{value.RepositoryRoot, value.GoRoot, value.ModuleCache} {
		if !executionGitAbsolutePath(path) {
			return false
		}
		for _, excluded := range []string{"/tmp", "/proc", "/dev", "/sys"} {
			if inputTreePathsOverlap(path, excluded) {
				return false
			}
		}
	}
	return true
}

// Only the closed root bootstrap or mapped-root helper calls this after
// authenticating its parent, image, original descriptor and new namespace.
// No host directory is created or changed: mount first, prove new tmpfs, then
// create only in that new view. Failure preserves the exact private namespace.
func createLinuxPressureAuthorView(ctx context.Context, original, priorNamespace *os.File, uid, gid uint32, observation *executionPreworkObservation) (_ *linuxPressureAuthorView, retErr error) {
	if ctx == nil || ctx.Err() != nil || original == nil || priorNamespace == nil || os.Getuid() != 0 || os.Geteuid() != 0 {
		return nil, ErrLinuxPressure
	}
	if !linuxPressureAuthorDirectoryFD(int(original.Fd())) {
		return nil, ErrLinuxPressure
	}
	var priorNS, currentNS unix.Stat_t
	var namespaceFS unix.Statfs_t
	kind, kindErr := unix.IoctlRetInt(int(priorNamespace.Fd()), unix.NS_GET_NSTYPE)
	if !linuxPressureAuthorNamespaceFD(int(priorNamespace.Fd())) || kindErr != nil || kind != unix.CLONE_NEWNS || unix.Fstatfs(int(priorNamespace.Fd()), &namespaceFS) != nil || namespaceFS.Type != unix.NSFS_MAGIC ||
		unix.Fstat(int(priorNamespace.Fd()), &priorNS) != nil || unix.Stat("/proc/self/ns/mnt", &currentNS) != nil || priorNS.Dev == currentNS.Dev && priorNS.Ino == currentNS.Ino {
		return nil, ErrLinuxPressure
	}
	var before, current unix.Stat_t
	var originalFS unix.Statfs_t
	if unix.Fstat(int(original.Fd()), &before) != nil || before.Mode != unix.S_IFDIR|0o700 || before.Uid != uid || before.Gid != gid || before.Nlink == 0 || unix.Fstatfs(int(original.Fd()), &originalFS) != nil {
		return nil, ErrLinuxPressure
	}
	status, statusErr := unix.FcntlInt(original.Fd(), unix.F_GETFL, 0)
	flags, flagsErr := unix.FcntlInt(original.Fd(), unix.F_GETFD, 0)
	originalMount, err := inputTreeMountID(int(original.Fd()))
	if statusErr != nil || flagsErr != nil || status&unix.O_ACCMODE != unix.O_RDONLY || status&unix.O_PATH != 0 || flags&unix.FD_CLOEXEC == 0 || err != nil {
		return nil, ErrLinuxPressure
	}
	// /tmp already exists; only its namespace-local covering mount is new.
	tmpFD, err := unix.Open("/tmp", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	tmp := os.NewFile(uintptr(tmpFD), "pressure-original-tmp")
	defer func() { retErr = errors.Join(retErr, observation.close(tmp.Close())) }()
	var oldTmp unix.Stat_t
	priorMount, mountErr := inputTreeMountID(tmpFD)
	if mountErr != nil || unix.Fstat(tmpFD, &oldTmp) != nil || unix.Lstat("/tmp", &current) != nil || oldTmp.Dev != current.Dev || oldTmp.Ino != current.Ino || oldTmp.Mode&unix.S_IFMT != unix.S_IFDIR ||
		unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "") != nil || unix.Mount("tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0700,size=4194304,nr_inodes=16") != nil {
		return nil, ErrLinuxPressure
	}
	fresh, err := os.Open("/tmp")
	if err != nil {
		return nil, ErrLinuxPressure
	}
	defer func() { retErr = errors.Join(retErr, observation.close(fresh.Close())) }()
	var freshStat unix.Stat_t
	var freshFS unix.Statfs_t
	freshMount, freshMountErr := inputTreeMountID(int(fresh.Fd()))
	if freshMountErr != nil || freshMount == priorMount || unix.Fstat(int(fresh.Fd()), &freshStat) != nil || freshStat.Dev == oldTmp.Dev || freshStat.Mode != unix.S_IFDIR|0o700 || unix.Fstatfs(int(fresh.Fd()), &freshFS) != nil || freshFS.Type != unix.TMPFS_MAGIC || freshFS.Bsize <= 0 || freshFS.Blocks > linuxPressureAuthorScaffoldBytes/uint64(freshFS.Bsize) || freshFS.Files > linuxPressureAuthorScaffoldInodes || freshFS.Flags&(unix.ST_NOSUID|unix.ST_NODEV) != unix.ST_NOSUID|unix.ST_NODEV {
		return nil, ErrLinuxPressure
	}
	rows, readErr := fresh.ReadDir(1)
	if len(rows) != 0 || readErr != io.EOF {
		return nil, ErrLinuxPressure
	}
	// New tmpfs/scaffold ownership only; never the covered /tmp or original leaf.
	if unix.Fchown(int(fresh.Fd()), int(uid), int(gid)) != nil || os.Mkdir(linuxPressureAuthorScaffold, 0o700) != nil || os.Chown(linuxPressureAuthorScaffold, int(uid), int(gid)) != nil {
		return nil, ErrLinuxPressure
	}
	for _, path := range []string{linuxPressureAuthorInputs, linuxPressureAuthorWork} {
		if os.Mkdir(path, 0o700) != nil || os.Chown(path, int(uid), int(gid)) != nil {
			return nil, ErrLinuxPressure
		}
	}
	targetFD, err := unix.Open(linuxPressureAuthorWork, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	target := os.NewFile(uintptr(targetFD), "pressure-work-placeholder")
	defer func() { retErr = errors.Join(retErr, observation.close(target.Close())) }()
	clone, err := unix.OpenTree(int(original.Fd()), "", uint(unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_EMPTY_PATH))
	if err != nil {
		return nil, ErrLinuxPressure
	}
	defer func() { retErr = errors.Join(retErr, observation.close(unix.Close(clone))) }()
	if unix.MoveMount(clone, "", targetFD, "", unix.MOVE_MOUNT_F_EMPTY_PATH|unix.MOVE_MOUNT_T_EMPTY_PATH) != nil {
		return nil, ErrLinuxPressure
	}
	file, err := os.Open(linuxPressureAuthorWork)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	view := &linuxPressureAuthorView{original: original, file: file, originalStat: before, fsid: originalFS.Fsid.Val, originalMount: originalMount, uid: uid, gid: gid, observation: observation}
	view.self = view
	if unix.Fstat(int(file.Fd()), &view.stat) != nil {
		_ = view.Close()
		return nil, ErrLinuxPressure
	}
	view.mount, err = inputTreeMountID(int(file.Fd()))
	var namespace unix.Stat_t
	if err != nil || unix.Stat("/proc/self/ns/mnt", &namespace) != nil {
		_ = view.Close()
		return nil, ErrLinuxPressure
	}
	view.namespaceDevice, view.namespaceInode = uint64(namespace.Dev), namespace.Ino
	if view.check(ctx) != nil {
		_ = view.Close()
		return nil, ErrLinuxPressure
	}
	return view, nil
}

func (view *linuxPressureAuthorView) check(ctx context.Context) error {
	if view == nil || view.self != view || view.closed || view.original == nil || view.file == nil || ctx == nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	var original, held, linked, namespace unix.Stat_t
	originalMount, originalErr := inputTreeMountID(int(view.original.Fd()))
	var linkedMount unix.Statx_t
	linkedErr := unix.Statx(unix.AT_FDCWD, linuxPressureAuthorWork, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &linkedMount)
	var filesystem unix.Statfs_t
	fd := int(view.file.Fd())
	mount, err := inputTreeMountID(fd)
	if originalErr != nil || originalMount != view.originalMount || linkedErr != nil || linkedMount.Mask&unix.STATX_MNT_ID == 0 || linkedMount.Mnt_id != view.mount || !linuxPressureAuthorDirectoryFD(fd) || !linuxPressureAuthorDirectoryFD(int(view.original.Fd())) || err != nil || mount != view.mount || mount == view.originalMount || unix.Fstat(int(view.original.Fd()), &original) != nil || original.Dev != view.originalStat.Dev || original.Ino != view.originalStat.Ino || original.Mode != unix.S_IFDIR|0o700 || original.Uid != view.uid || original.Gid != view.gid ||
		unix.Fstat(fd, &held) != nil || unix.Lstat(linuxPressureAuthorWork, &linked) != nil || held.Dev != view.originalStat.Dev || held.Ino != view.originalStat.Ino || held.Dev != linked.Dev || held.Ino != linked.Ino ||
		held.Mode != unix.S_IFDIR|0o700 || held.Uid != view.uid || held.Gid != view.gid || held.Nlink == 0 || unix.Fstatfs(fd, &filesystem) != nil || filesystem.Fsid.Val != view.fsid || filesystem.Flags&unix.ST_RDONLY != 0 ||
		unix.Stat("/proc/self/ns/mnt", &namespace) != nil || uint64(namespace.Dev) != view.namespaceDevice || namespace.Ino != view.namespaceInode {
		return ErrLinuxPressure
	}
	for _, path := range []string{"/tmp", linuxPressureAuthorScaffold, linuxPressureAuthorInputs} {
		var info unix.Stat_t
		if unix.Lstat(path, &info) != nil || info.Mode != unix.S_IFDIR|0o700 || info.Uid != view.uid || info.Gid != view.gid {
			return ErrLinuxPressure
		}
	}
	canonical, pathErr := filepath.EvalSymlinks(linuxPressureAuthorWork)
	if pathErr != nil || canonical != linuxPressureAuthorWork {
		return ErrLinuxPressure
	}
	return nil
}

func (view *linuxPressureAuthorView) Close() error {
	if view == nil {
		return nil
	}
	if !view.closed {
		view.closed = true
		view.closeErr = view.observation.close(view.file.Close())
	}
	return view.closeErr
}

func linuxPressureAuthorDirectoryFD(fd int) bool {
	flags, e := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	closeFlags, c := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	return e == nil && c == nil && flags&unix.O_ACCMODE == unix.O_RDONLY && flags&unix.O_PATH == 0 && closeFlags&unix.FD_CLOEXEC != 0
}
func adoptLinuxPressureAuthorView(ctx context.Context, original *os.File, value linuxPressureAuthorSelection, observation *executionPreworkObservation) (*linuxPressureAuthorView, error) {
	if original == nil || ctx == nil || ctx.Err() != nil || value.ViewNamespaceInode == 0 || value.ViewMount == 0 || value.ViewMount == value.Borrow.LeafMount {
		return nil, ErrLinuxPressure
	}
	file, err := os.Open(linuxPressureAuthorWork)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	view := &linuxPressureAuthorView{original: original, file: file, fsid: value.Borrow.LeafFSID, originalMount: value.Borrow.LeafMount, mount: value.ViewMount, namespaceDevice: value.ViewNamespaceDevice, namespaceInode: value.ViewNamespaceInode, uid: value.Borrow.UID, gid: value.Borrow.GID, observation: observation}
	view.self = view
	if unix.Fstat(int(original.Fd()), &view.originalStat) != nil || uint64(view.originalStat.Dev) != value.Borrow.LeafDevice || view.originalStat.Ino != value.Borrow.LeafInode || unix.Fstat(int(file.Fd()), &view.stat) != nil || view.check(ctx) != nil {
		_ = view.Close()
		return nil, ErrLinuxPressure
	}
	return view, nil
}

func linuxPressureAuthorNamespaceFD(fd int) bool {
	status, e := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	flags, f := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	return e == nil && f == nil && status&unix.O_ACCMODE == unix.O_RDONLY && status&unix.O_PATH == 0 && flags&unix.FD_CLOEXEC != 0
}
