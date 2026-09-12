//go:build darwin

package main

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/recovery"
)

type t422ArchiveCustody struct {
	workspace *os.File // Borrowed FD6; never closed here.
	path      string
	info      os.FileInfo
	volume    [2]int32
	input     t422ArchiveInput
	backup    *os.File
	archive   *os.File
	archiveID unix.Stat_t
}

func openT422ArchiveCustody(ctx context.Context, workspace *os.File, path string, info os.FileInfo, volume [2]int32, input t422ArchiveInput) (_ *t422ArchiveCustody, retErr error) {
	custody := &t422ArchiveCustody{workspace: workspace, path: path, info: info, volume: volume, input: input}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, custody.close())
		}
	}()
	if !validT422ArchiveInput(input) || custody.checkWorkspace(ctx) != nil {
		return nil, errT422ArchiveControl
	}
	var err error
	custody.backup, err = openT422ArchiveDirectory(workspace, input.BackupRoot)
	if err != nil {
		return nil, err
	}
	custody.archive, err = openT422ArchiveDirectory(custody.backup, "archive")
	if err != nil {
		return nil, err
	}
	if unix.Fstat(int(custody.archive.Fd()), &custody.archiveID) != nil || custody.check(ctx) != nil {
		return nil, errT422ArchiveControl
	}
	return custody, nil
}

func openT422ArchiveDirectory(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errT422ArchiveControl
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (custody *t422ArchiveCustody) checkWorkspace(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || custody.workspace == nil || custody.info == nil {
		return errT422ArchiveControl
	}
	binding, err := dispatchadmission.DescribeProductionWorkspace(custody.workspace, custody.path)
	info, statErr := custody.workspace.Stat()
	if err != nil || statErr != nil || !os.SameFile(custody.info, info) || binding.FSID != custody.volume ||
		binding.FSID != custody.input.FSID || binding.Device != custody.input.Device {
		return errT422ArchiveControl
	}
	return nil
}

func (custody *t422ArchiveCustody) check(ctx context.Context) error {
	if custody.checkWorkspace(ctx) != nil || custody.backup == nil || custody.archive == nil {
		return errT422ArchiveControl
	}
	var backup, linkedBackup, archive, linkedArchive unix.Stat_t
	var backupFS, archiveFS unix.Statfs_t
	if unix.Fstat(int(custody.backup.Fd()), &backup) != nil ||
		unix.Fstatat(int(custody.workspace.Fd()), custody.input.BackupRoot, &linkedBackup, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		unix.Fstat(int(custody.archive.Fd()), &archive) != nil ||
		unix.Fstatat(int(custody.backup.Fd()), "archive", &linkedArchive, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		unix.Fstatfs(int(custody.backup.Fd()), &backupFS) != nil || unix.Fstatfs(int(custody.archive.Fd()), &archiveFS) != nil ||
		backupFS.Fsid.Val != custody.volume || archiveFS.Fsid.Val != custody.volume ||
		uint64(backup.Dev) != custody.input.Device || backup.Ino != custody.input.Inode ||
		!sameT422ArchiveDirectory(backup, linkedBackup) || !sameT422ArchiveDirectory(archive, linkedArchive) ||
		!sameT422ArchiveDirectory(archive, custody.archiveID) || backup.Dev != archive.Dev {
		return errT422ArchiveControl
	}
	return nil
}

func sameT422ArchiveDirectory(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode&unix.S_IFMT == unix.S_IFDIR && b.Mode&unix.S_IFMT == unix.S_IFDIR &&
		a.Mode&0o777 == 0o700 && b.Mode&0o777 == 0o700 && a.Uid == uint32(os.Geteuid()) && b.Uid == a.Uid
}

func (custody *t422ArchiveCustody) read(ctx context.Context, input t422ArchiveInput) (_ recovery.ArchiveTransitionManifest, retErr error) {
	if input != custody.input || custody.check(ctx) != nil {
		return recovery.ArchiveTransitionManifest{}, errT422ArchiveControl
	}
	fd, err := unix.Openat(int(custody.archive.Fd()), recovery.ManifestName, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return recovery.ArchiveTransitionManifest{}, errT422ArchiveControl
	}
	file := os.NewFile(uintptr(fd), recovery.ManifestName)
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	var before, after, linked unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Dev != custody.archiveID.Dev ||
		before.Uid != uint32(os.Geteuid()) || before.Size < 0 || before.Size > t422ArchiveTransitionBytes {
		return recovery.ArchiveTransitionManifest{}, errT422ArchiveControl
	}
	value, err := recovery.ReadArchiveTransitionManifestFile(ctx, file, input.BackupCommandSHA256, input.RestoreCommandSHA256)
	if err != nil {
		return recovery.ArchiveTransitionManifest{}, err
	}
	if custody.check(ctx) != nil || unix.Fstat(fd, &after) != nil ||
		unix.Fstatat(int(custody.archive.Fd()), recovery.ManifestName, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameT422ArchiveManifest(before, after) || !sameT422ArchiveManifest(after, linked) {
		return recovery.ArchiveTransitionManifest{}, errT422ArchiveControl
	}
	return value, nil
}

func sameT422ArchiveManifest(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode&unix.S_IFMT == unix.S_IFREG && b.Mode&unix.S_IFMT == unix.S_IFREG &&
		a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim && a.Uid == b.Uid && a.Mode == b.Mode
}

func (custody *t422ArchiveCustody) close() error {
	var err error
	if custody.archive != nil {
		err = custody.archive.Close()
		custody.archive = nil
	}
	if custody.backup != nil {
		err = errors.Join(err, custody.backup.Close())
		custody.backup = nil
	}
	return err
}
