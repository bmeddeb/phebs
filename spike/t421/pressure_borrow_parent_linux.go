//go:build linux

package t421

import (
	"context"
	"os"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// This distinct root-peer contract does not change H2t.a's independent
// same-image parent predicate. Nonroot cannot claim a root proc-exe observation.
func adoptLinuxPressureParent(ctx context.Context, value linuxExecutorPreworkSelection, image, read *os.File, observation *executionPreworkObservation) (_ *linuxExecutorParent, retErr error) {
	if ctx == nil || ctx.Err() != nil || image == nil || read == nil || value.ParentPID != os.Getppid() || !validLinuxExecutorStart(value.ParentStart) || linuxExecutorImage(ctx, image) != nil {
		return nil, ErrLinuxPressure
	}
	raw, err := read.SyscallConn()
	fd := -1
	if err != nil || raw.Control(func(rawFD uintptr) { fd = int(rawFD) }) != nil || fd < 0 {
		return nil, ErrLinuxPressure
	}
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	flags, flagsErr := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if flagsErr != nil || fdErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_NONBLOCK == 0 || fdFlags&unix.FD_CLOEXEC == 0 ||
		unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFIFO || stat.Uid != 0 || stat.Gid != 0 ||
		uint64(stat.Dev) != value.ReadDevice || stat.Ino != value.ReadInode || unix.Fstatfs(fd, &filesystem) != nil || filesystem.Type != unix.PIPEFS_MAGIC {
		return nil, ErrLinuxPressure
	}
	pidfd, err := unix.PidfdOpen(value.ParentPID, 0)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	defer func() {
		if retErr != nil {
			_ = observation.close(unix.Close(pidfd))
		}
	}()
	parent, parentErr := linuxPressureBorrowRecord(ctx, value.ParentPID)
	self, selfErr := linuxPressureBorrowRecord(ctx, os.Getpid())
	if parentErr != nil || selfErr != nil || parent.StartIdentity != value.ParentStart || self.ParentPID != parent.PID || !validLinuxExecutorStart(self.StartIdentity) {
		return nil, ErrLinuxPressure
	}
	alive, cancel := context.WithCancel(ctx)
	owner := &linuxExecutorParent{file: read, image: image, pidfd: pidfd, readFD: fd, parent: parent, self: self, readStat: stat,
		alive: alive, cancel: cancel, done: make(chan struct{}), observation: observation}
	if checkLinuxPressureParent(ctx, owner) != nil || read.SetReadDeadline(time.Unix(0, value.DeadlineUnixNano)) != nil {
		cancel()
		return nil, ErrLinuxPressure
	}
	go owner.watch()
	return owner, nil
}

func checkLinuxPressureParent(ctx context.Context, parent *linuxExecutorParent) error {
	if parent == nil || ctx == nil || ctx.Err() != nil || parent.alive == nil || parent.alive.Err() != nil || parent.parent.PID != os.Getppid() || parent.self.PID != os.Getpid() || linuxExecutorImage(ctx, parent.image) != nil {
		return ErrLinuxPressure
	}
	var stat unix.Stat_t
	flags, flagErr := unix.FcntlInt(uintptr(parent.readFD), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(uintptr(parent.readFD), unix.F_GETFD, 0)
	poll := []unix.PollFd{{Fd: int32(parent.pidfd), Events: unix.POLLIN}}
	n, pollErr := unix.Poll(poll, 0)
	if flagErr != nil || fdErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_NONBLOCK == 0 || fdFlags&unix.FD_CLOEXEC == 0 ||
		unix.Fstat(parent.readFD, &stat) != nil || !sameLinuxInputStat(parent.readStat, stat) || pollErr != nil || n != 0 || poll[0].Revents != 0 {
		return ErrLinuxPressure
	}
	before, err := linuxPressureBorrowRecord(ctx, parent.parent.PID)
	self, selfErr := linuxPressureBorrowRecord(ctx, parent.self.PID)
	sid, sidErr := unix.Getsid(0)
	if err != nil || selfErr != nil || before.StartIdentity != parent.parent.StartIdentity || self.StartIdentity != parent.self.StartIdentity ||
		self.ParentPID != before.PID || sidErr != nil || sid != self.PID {
		return ErrLinuxPressure
	}
	return nil
}

type linuxPressureBorrowNamespace struct {
	file        *os.File
	device, ino uint64
	observation *executionPreworkObservation
	closed      bool
	closeErr    error
}

func pinLinuxPressureBorrowNamespace(observation *executionPreworkObservation) (*linuxPressureBorrowNamespace, error) {
	file, err := os.Open("/proc/self/ns/mnt")
	if err != nil {
		return nil, ErrLinuxPressure
	}
	var stat unix.Stat_t
	if unix.Fstat(int(file.Fd()), &stat) != nil {
		_ = observation.close(file.Close())
		return nil, ErrLinuxPressure
	}
	owner := &linuxPressureBorrowNamespace{file: file, device: uint64(stat.Dev), ino: stat.Ino, observation: observation}
	if owner.check() != nil {
		_ = owner.Close()
		return nil, ErrLinuxPressure
	}
	return owner, nil
}

func (owner *linuxPressureBorrowNamespace) check() error {
	if owner == nil || owner.closed || owner.file == nil || owner.ino == 0 {
		return ErrLinuxPressure
	}
	var held, current unix.Stat_t
	var filesystem unix.Statfs_t
	fd := int(owner.file.Fd())
	kind, err := unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
	flags, flagsErr := unix.FcntlInt(owner.file.Fd(), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(owner.file.Fd(), unix.F_GETFD, 0)
	if err != nil || kind != unix.CLONE_NEWNS || unix.Fstatfs(fd, &filesystem) != nil || filesystem.Type != unix.NSFS_MAGIC ||
		flagsErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_PATH != 0 || fdErr != nil || fdFlags&unix.FD_CLOEXEC == 0 ||
		unix.Fstat(fd, &held) != nil || uint64(held.Dev) != owner.device || held.Ino != owner.ino ||
		unix.Stat("/proc/self/ns/mnt", &current) != nil || held.Dev != current.Dev || held.Ino != current.Ino {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *linuxPressureBorrowNamespace) Close() error {
	if owner != nil && !owner.closed {
		owner.closed = true
		owner.closeErr = owner.observation.close(owner.file.Close())
	}
	if owner == nil {
		return nil
	}
	return owner.closeErr
}

func linuxPressureBorrowRecord(ctx context.Context, pid int) (t4013.NativeProcessRecord, error) {
	if ctx == nil || ctx.Err() != nil {
		return t4013.NativeProcessRecord{}, ErrLinuxPressure
	}
	before, err := linuxEpochProcessRow(pid)
	after, afterErr := linuxEpochProcessRow(pid)
	if err != nil || afterErr != nil || !linuxEpochSameProcessRow(before, after) || ctx.Err() != nil {
		return t4013.NativeProcessRecord{}, ErrLinuxPressure
	}
	return after, nil
}
