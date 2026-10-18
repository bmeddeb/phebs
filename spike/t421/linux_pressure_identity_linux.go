//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"golang.org/x/sys/unix"
)

type linuxPressureDirectory struct {
	path    string
	file    *os.File
	info    os.FileInfo
	node    linuxPressureNode
	fsid    [2]int32
	mountID uint64
}

func linuxPressurePrivateFile(stat unix.Stat_t) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Mode&0o7777 == 0o600 &&
		stat.Uid == 0 && stat.Gid == 0 && stat.Nlink == 1
}

func linuxPressureDirectoryFacts(file *os.File) (linuxPressureDirectory, error) {
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	var extended unix.Statx_t
	if file == nil || unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Mode&0o7777 != 0o700 || stat.Uid != 0 || stat.Gid != 0 || stat.Nlink == 0 ||
		unix.Fstatfs(int(file.Fd()), &filesystem) != nil || filesystem.Fsid.Val == ([2]int32{}) ||
		unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil ||
		extended.Mask&unix.STATX_MNT_ID == 0 || extended.Mnt_id == 0 {
		return linuxPressureDirectory{}, ErrLinuxPressure
	}
	info, err := file.Stat()
	if err != nil {
		return linuxPressureDirectory{}, ErrLinuxPressure
	}
	return linuxPressureDirectory{file: file, info: info, node: linuxPressureNode{uint64(stat.Dev), stat.Ino},
		fsid: filesystem.Fsid.Val, mountID: extended.Mnt_id}, nil
}

func openLinuxPressureDirectory(ctx context.Context, path string, freshMount bool) (_ linuxPressureDirectory, retErr error) {
	if linuxPressureContext(ctx) != nil {
		return linuxPressureDirectory{}, ErrLinuxPressure
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return linuxPressureDirectory{}, ErrLinuxPressure
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return linuxPressureDirectory{}, ErrLinuxPressure
	}
	file := os.NewFile(uintptr(fd), "linux-pressure-owned-directory")
	defer func() {
		if retErr != nil {
			_ = file.Close()
		}
	}()
	if freshMount {
		// Only the newly formatted, owned filesystem root enters here. This is
		// not a chmod-only admission of a supplied or borrowed source directory.
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != 0 || stat.Gid != 0 ||
			unix.Fchmod(fd, 0o700) != nil || file.Sync() != nil {
			return linuxPressureDirectory{}, ErrLinuxPressure
		}
	}
	directory, err := linuxPressureDirectoryFacts(file)
	directory.path = path
	if err != nil || directory.check(ctx) != nil {
		return linuxPressureDirectory{}, ErrLinuxPressure
	}
	return directory, nil
}

func (directory linuxPressureDirectory) checkHeld(ctx context.Context) error {
	if linuxPressureContext(ctx) != nil {
		return ErrLinuxPressure
	}
	current, err := linuxPressureDirectoryFacts(directory.file)
	if err != nil || current.node != directory.node || current.fsid != directory.fsid || current.mountID != directory.mountID {
		return ErrLinuxPressure
	}
	return nil
}

func (directory linuxPressureDirectory) check(ctx context.Context) error {
	if directory.checkHeld(ctx) != nil {
		return ErrLinuxPressure
	}
	canonical, err := filepath.EvalSymlinks(directory.path)
	var stat unix.Stat_t
	var extended unix.Statx_t
	if err != nil || canonical != directory.path || unix.Lstat(directory.path, &stat) != nil ||
		stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o7777 != 0o700 || stat.Uid != 0 || stat.Gid != 0 ||
		(linuxPressureNode{uint64(stat.Dev), stat.Ino}) != directory.node ||
		unix.Statx(unix.AT_FDCWD, directory.path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil ||
		extended.Mask&unix.STATX_MNT_ID == 0 || extended.Mnt_id != directory.mountID {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *LinuxPressureVolume) acquireLock(ctx context.Context) error {
	if owner.parent.check(ctx) != nil {
		return ErrLinuxPressure
	}
	fd, err := unix.Openat(int(owner.parent.file.Fd()), ".t42h2m.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return ErrLinuxPressure
	}
	owner.lock = os.NewFile(uintptr(fd), "linux-pressure-owner-lock")
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || !linuxPressurePrivateFile(stat) || stat.Size != 0 || uint64(stat.Dev) != owner.parent.node.Device ||
		unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil || owner.lock.Sync() != nil || owner.parent.file.Sync() != nil {
		return ErrLinuxPressure
	}
	owner.lockNode = linuxPressureNode{uint64(stat.Dev), stat.Ino}
	return owner.checkLock(ctx)
}

func (owner *LinuxPressureVolume) checkLock(ctx context.Context) error {
	if linuxPressureContext(ctx) != nil || owner.lock == nil {
		return ErrLinuxPressure
	}
	var held, linked unix.Stat_t
	if unix.Fstat(int(owner.lock.Fd()), &held) != nil || !linuxPressurePrivateFile(held) || held.Size != 0 ||
		unix.Fstatat(int(owner.parent.file.Fd()), ".t42h2m.lock", &linked, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!linuxPressurePrivateFile(linked) || linked.Size != 0 ||
		(linuxPressureNode{uint64(held.Dev), held.Ino}) != owner.lockNode ||
		(linuxPressureNode{uint64(linked.Dev), linked.Ino}) != owner.lockNode {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *LinuxPressureVolume) checkRoots(ctx context.Context) error {
	if owner.parent.check(ctx) != nil || owner.root.check(ctx) != nil || owner.checkLock(ctx) != nil ||
		owner.root.node.Device != owner.parent.node.Device || owner.root.mountID != owner.parent.mountID || owner.root.fsid != owner.parent.fsid {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *LinuxPressureVolume) checkBacking(ctx context.Context, reserveImage bool) error {
	if owner.parent.check(ctx) != nil {
		return ErrLinuxPressure
	}
	raw, err := linuxPressureMountTable(ctx)
	device := strconv.FormatUint(uint64(unix.Major(owner.parent.node.Device)), 10) + ":" + strconv.FormatUint(uint64(unix.Minor(owner.parent.node.Device)), 10)
	if err != nil || !linuxPressurePrivateBacking(raw, owner.parent.mountID, device) {
		return ErrLinuxPressure
	}
	var filesystem unix.Statfs_t
	if unix.Fstatfs(int(owner.parent.file.Fd()), &filesystem) != nil ||
		(filesystem.Type != unix.EXT4_SUPER_MAGIC && filesystem.Type != unix.XFS_SUPER_MAGIC) ||
		filesystem.Fsid.Val != owner.parent.fsid || filesystem.Bsize <= 0 || filesystem.Bfree > filesystem.Blocks || filesystem.Bavail > filesystem.Bfree {
		return ErrLinuxPressure
	}
	total, available, err := linuxPressureFilesystemBytes(filesystem.Blocks, filesystem.Bavail, uint64(filesystem.Bsize))
	if err != nil || !owner.rootCreated && available < linuxPressureDiskFloor {
		return ErrLinuxPressure
	}
	var reservation uint64
	if reserveImage {
		reservation, err = linuxPressureBackingBudget(owner.request.ImageBytes, uint64(filesystem.Bsize))
	}
	if err != nil || !linuxPressureBackingProjection(total, available, reservation) {
		return ErrLinuxPressure
	}
	if owner.backingGate == nil {
		owner.backingGate = lifecycle.NewGate(owner.request.Parent)
	}
	// Observation boundary ONLY: zero here applies the persistent production
	// pressure latch to actual held-filesystem facts; it does not admit/reserve
	// this image as a zero-byte unit. The full fixed-image projection above is
	// separate from ordinary production's unchanged 48-GiB per-unit API.
	capacity, err := owner.backingGate.CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(total),
		AvailableBytes: int64(available), UsedBytes: int64(total - available)}, 0)
	if err != nil || capacity.Pressure != lifecycle.PressureNormal || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	return nil
}

func linuxPressureDevice(fd int, path string, kind uint32, major, minor uint32) (linuxPressureNode, error) {
	var held, linked unix.Stat_t
	if unix.Fstat(fd, &held) != nil || unix.Lstat(path, &linked) != nil || held.Mode&unix.S_IFMT != kind ||
		held.Uid != 0 || held.Mode&0o7002 != 0 || held.Nlink != 1 || held.Dev != linked.Dev || held.Ino != linked.Ino ||
		held.Mode != linked.Mode || held.Uid != linked.Uid || held.Gid != linked.Gid || held.Rdev != linked.Rdev ||
		unix.Major(uint64(held.Rdev)) != major || unix.Minor(uint64(held.Rdev)) != minor {
		return linuxPressureNode{}, ErrLinuxPressure
	}
	// Standard root:disk group access is a named privileged-host trust boundary,
	// not an executor grant. No chmod/chown/group mutation is performed. Future
	// executors must exclude block-admin groups/caps/device FDs independently.
	return linuxPressureNode{uint64(held.Dev), held.Ino}, nil
}

func (owner *LinuxPressureVolume) selectLoop(ctx context.Context) (retErr error) {
	if linuxPressureContext(ctx) != nil {
		return ErrLinuxPressure
	}
	fd, err := unix.Open("/dev/loop-control", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrLinuxPressure
	}
	defer func() { retErr = errors.Join(retErr, unix.Close(fd)) }()
	if _, err = linuxPressureDevice(fd, "/dev/loop-control", unix.S_IFCHR, 10, 237); err != nil {
		return err
	}
	number, err := unix.IoctlRetInt(fd, unix.LOOP_CTL_GET_FREE)
	if err != nil || number < 0 || number > 1048575 || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	path := "/dev/loop" + strconv.Itoa(number)
	loopFD, err := unix.Open(path, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrLinuxPressure
	}
	owner.loop, owner.loopNumber = os.NewFile(uintptr(loopFD), path), number
	owner.loopNode, err = linuxPressureDevice(loopFD, path, unix.S_IFBLK, 7, uint32(number))
	if err != nil {
		return err
	}
	if _, err = unix.IoctlLoopGetStatus64(loopFD); !errors.Is(err, unix.ENXIO) {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *LinuxPressureVolume) checkLoop(ctx context.Context) error {
	if linuxPressureContext(ctx) != nil || owner.loop == nil || owner.record.Loop != owner.loopNumber {
		return ErrLinuxPressure
	}
	node, err := linuxPressureDevice(int(owner.loop.Fd()), owner.loop.Name(), unix.S_IFBLK, 7, uint32(owner.loopNumber))
	if err != nil || node != owner.loopNode {
		return ErrLinuxPressure
	}
	status, err := unix.IoctlLoopGetStatus64(int(owner.loop.Fd()))
	if err != nil || !linuxPressureLoopMatches(owner.record, linuxPressureLoop{Number: status.Number, Flags: status.Flags,
		Encryption: status.Encrypt_type, KeyBytes: status.Encrypt_key_size, Device: status.Device, Inode: status.Inode,
		Offset: status.Offset, Limit: status.Sizelimit}) {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *LinuxPressureVolume) checkImage(ctx context.Context) error {
	if owner.image == nil || owner.checkRoots(ctx) != nil {
		return ErrLinuxPressure
	}
	var held, linked unix.Stat_t
	if unix.Fstat(int(owner.image.Fd()), &held) != nil || !linuxPressurePrivateFile(held) ||
		unix.Fstatat(int(owner.root.file.Fd()), "image.ext4", &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || !linuxPressurePrivateFile(linked) ||
		(linuxPressureNode{uint64(held.Dev), held.Ino}) != owner.record.Image ||
		(linuxPressureNode{uint64(linked.Dev), linked.Ino}) != owner.record.Image || held.Size != linked.Size || held.Blocks != linked.Blocks ||
		held.Size < 0 || uint64(held.Size) != owner.request.ImageBytes || held.Blocks < 0 ||
		uint64(held.Blocks) < owner.request.ImageBytes/512 {
		return ErrLinuxPressure
	}
	// Aggregate st_blocks is not a per-range hole proof. The owner additionally
	// requires its fresh zero-created full fallocate lineage, non-discard/eager
	// formatter recipe and trusted root-only writable backing custody.
	return nil
}

func linuxPressureMountTable(ctx context.Context) (_ []byte, retErr error) {
	if linuxPressureContext(ctx) != nil {
		return nil, ErrLinuxPressure
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, ErrLinuxPressure
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(file, linuxPressureMountLimit+1))
	if err != nil || len(raw) > linuxPressureMountLimit || ctx.Err() != nil {
		return nil, ErrLinuxPressure
	}
	return raw, nil
}

func (owner *LinuxPressureVolume) observeGeometry(ctx context.Context) (LinuxPressureGeometry, error) {
	if owner.mount.check(ctx) != nil || owner.workspace.check(ctx) != nil || owner.underlay.checkHeld(ctx) != nil ||
		owner.workspace.node.Device != owner.mount.node.Device || owner.workspace.fsid != owner.mount.fsid || owner.workspace.mountID != owner.mount.mountID ||
		owner.mount.node.Device == owner.parent.node.Device || unix.Major(owner.mount.node.Device) != 7 || unix.Minor(owner.mount.node.Device) != uint32(owner.loopNumber) {
		return LinuxPressureGeometry{}, ErrLinuxPressure
	}
	var filesystem unix.Statfs_t
	if unix.Fstatfs(int(owner.mount.file.Fd()), &filesystem) != nil || filesystem.Type != unix.EXT4_SUPER_MAGIC ||
		filesystem.Bsize != 4096 || filesystem.Bfree > filesystem.Blocks || filesystem.Bavail > filesystem.Bfree || filesystem.Ffree > filesystem.Files {
		return LinuxPressureGeometry{}, ErrLinuxPressure
	}
	_, available, err := linuxPressureFilesystemBytes(filesystem.Blocks, filesystem.Bavail, uint64(filesystem.Bsize))
	if err != nil {
		return LinuxPressureGeometry{}, err
	}
	raw, err := linuxPressureMountTable(ctx)
	if err != nil {
		return LinuxPressureGeometry{}, err
	}
	mount, err := linuxPressureMountObservation(raw, owner.mount.path, owner.loopNumber, owner.underlay.mountID, true)
	geometry := LinuxPressureGeometry{ImageBytes: owner.request.ImageBytes, Device: owner.mount.node.Device, FSID: filesystem.Fsid.Val,
		MountID: mount.ID, BlockBytes: uint64(filesystem.Bsize), Blocks: filesystem.Blocks, AvailableBytes: available, Inodes: filesystem.Files, FreeInodes: filesystem.Ffree}
	if err != nil || mount.ID != owner.mount.mountID || !validLinuxPressureGeometry(geometry) || ctx.Err() != nil {
		return LinuxPressureGeometry{}, ErrLinuxPressure
	}
	return geometry, nil
}

func (owner *LinuxPressureVolume) checkUnderlay(ctx context.Context) error {
	if owner.underlay.check(ctx) != nil || owner.underlay.node.Device != owner.parent.node.Device ||
		owner.underlay.fsid != owner.parent.fsid || owner.underlay.mountID != owner.parent.mountID {
		return ErrLinuxPressure
	}
	raw, err := linuxPressureMountTable(ctx)
	if err != nil {
		return err
	}
	_, err = linuxPressureMountObservation(raw, owner.underlay.path, owner.loopNumber, owner.parent.mountID, false)
	return err
}

func (owner *LinuxPressureVolume) check(ctx context.Context) (LinuxPressureGeometry, error) {
	if owner == nil || !owner.ready || owner.failed || owner.closed || owner.removalUsed || owner.removed ||
		owner.checkRoots(ctx) != nil || owner.checkRecord(ctx) != nil || owner.checkImage(ctx) != nil ||
		owner.checkLoop(ctx) != nil || owner.checkBacking(ctx, false) != nil {
		return LinuxPressureGeometry{}, ErrLinuxPressure
	}
	geometry, err := owner.observeGeometry(ctx)
	if err != nil || !sameLinuxPressureGeometry(owner.record.Geometry, geometry) {
		return LinuxPressureGeometry{}, ErrLinuxPressure
	}
	return geometry, nil
}

func (owner *LinuxPressureVolume) closedRootEntries(ctx context.Context) (retErr error) {
	if owner.checkRoots(ctx) != nil {
		return ErrLinuxPressure
	}
	fd, err := unix.Openat(int(owner.root.file.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrLinuxPressure
	}
	file := os.NewFile(uintptr(fd), "linux-pressure-root-inventory")
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	entries, readErr := file.ReadDir(4)
	if readErr != nil && !errors.Is(readErr, io.EOF) || len(entries) != 3 {
		return ErrLinuxPressure
	}
	want := map[string]bool{"owner.json": true, "image.ext4": true, "mount": true}
	for _, entry := range entries {
		if !want[entry.Name()] {
			return ErrLinuxPressure
		}
		delete(want, entry.Name())
	}
	if len(want) != 0 || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *LinuxPressureVolume) removeFreshLostFound(ctx context.Context) (retErr error) {
	if owner.mount.check(ctx) != nil {
		return ErrLinuxPressure
	}
	path := filepath.Join(owner.mount.path, "lost+found")
	// mke2fs's freshly created recovery directory is the only admitted extra.
	directory, err := openLinuxPressureDirectory(ctx, path, false)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, directory.file.Close()) }()
	entries, readErr := directory.file.ReadDir(1)
	if !errors.Is(readErr, io.EOF) || len(entries) != 0 || directory.node.Device != owner.mount.node.Device ||
		directory.fsid != owner.mount.fsid || directory.mountID != owner.mount.mountID || directory.check(ctx) != nil ||
		unix.Unlinkat(int(owner.mount.file.Fd()), "lost+found", unix.AT_REMOVEDIR) != nil {
		return fmt.Errorf("%w: unexpected formatter recovery directory", ErrLinuxPressure)
	}
	return nil
}
