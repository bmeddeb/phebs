//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

type linuxExecutorParent struct {
	file, image  *os.File
	pidfd        int
	readFD       int
	parent, self t4013.NativeProcessRecord
	readStat     unix.Stat_t
	inner        bool
	alive        context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	watchErr     error // Published before done closes.
	closeOnce    sync.Once
	closeErr     error
	retired      bool
	observation  *executionPreworkObservation
}

func linuxExecutorRecord(ctx context.Context, root, pid int) (t4013.NativeProcessRecord, error) {
	rows, err := t4013.ObserveProcessTreeRecords(ctx, root)
	if err == nil {
		for _, row := range rows {
			if row.PID == pid {
				return row, nil
			}
		}
	}
	return t4013.NativeProcessRecord{}, ErrExecutionLauncher
}

func validLinuxExecutorStart(value string) bool {
	start, err := strconv.ParseUint(value, 10, 64)
	return err == nil && start > 0 && strconv.FormatUint(start, 10) == value
}

func linuxExecutorImage(ctx context.Context, image *os.File) error {
	if image == nil || ctx == nil || ctx.Err() != nil {
		return ErrExecutionLauncher
	}
	entry := linuxExecutionInput{file: image}
	if unix.Fstat(int(image.Fd()), &entry.stat) != nil || entry.stat.Size <= 0 || entry.stat.Size > maxInputCustodyFileBytes {
		return ErrExecutionLauncher
	}
	entry.identity = LinuxExecutionInputIdentity{Name: "t422-execute", Bytes: entry.stat.Size, Executable: true}
	if !linuxInputProtected(image, entry) || !linuxInputELF(image, entry.stat.Size) ||
		t4013.MatchLinuxProcessExecutableImage(ctx, os.Getpid(), image) != nil {
		return ErrExecutionLauncher
	}
	return nil
}

func adoptLinuxExecutorParent(ctx context.Context, selection linuxExecutorPreworkSelection, image, file *os.File, inner bool) (_ *linuxExecutorParent, retErr error) {
	return adoptLinuxExecutorParentObserved(ctx, selection, image, file, inner, nil)
}

func adoptLinuxExecutorParentObserved(ctx context.Context, selection linuxExecutorPreworkSelection, image, file *os.File, inner bool, observation *executionPreworkObservation) (_ *linuxExecutorParent, retErr error) {
	if ctx == nil || ctx.Err() != nil || image == nil || file == nil || selection.ParentPID != os.Getppid() ||
		!validLinuxExecutorStart(selection.ParentStart) || !time.Now().Before(time.Unix(0, selection.DeadlineUnixNano)) {
		return nil, ErrExecutionLauncher
	}
	unix.CloseOnExec(int(image.Fd()))
	// File.Fd forces a pollable pipe back to blocking. Observe it through the
	// raw connection so deadline reads and concurrent Close remain interruptible.
	raw, err := file.SyscallConn()
	fd := -1
	if err != nil || raw.Control(func(value uintptr) { fd = int(value) }) != nil || fd < 0 {
		return nil, ErrExecutionLauncher
	}
	unix.CloseOnExec(fd)
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_NONBLOCK == 0 {
		return nil, ErrExecutionLauncher
	}
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFIFO || stat.Uid != uint32(os.Getuid()) || stat.Gid != uint32(os.Getgid()) ||
		unix.Fstatfs(fd, &filesystem) != nil || filesystem.Type != unix.PIPEFS_MAGIC ||
		uint64(stat.Dev) != selection.ReadDevice || stat.Ino != selection.ReadInode || linuxExecutorImage(ctx, image) != nil {
		return nil, ErrExecutionLauncher
	}
	pidfd, err := unix.PidfdOpen(selection.ParentPID, 0)
	if err != nil {
		return nil, ErrExecutionLauncher
	}
	defer func() {
		if retErr != nil {
			_ = observation.close(unix.Close(pidfd))
		}
	}()
	parent, parentErr := linuxExecutorRecord(ctx, selection.ParentPID, selection.ParentPID)
	self, selfErr := linuxExecutorRecord(ctx, selection.ParentPID, os.Getpid())
	if parentErr != nil || selfErr != nil || parent.StartIdentity != selection.ParentStart || self.ParentPID != parent.PID || !validLinuxExecutorStart(self.StartIdentity) {
		return nil, ErrExecutionLauncher
	}
	alive, cancel := context.WithCancel(ctx)
	value := &linuxExecutorParent{file: file, image: image, pidfd: pidfd, readFD: fd, parent: parent, self: self, readStat: stat,
		inner: inner, alive: alive, cancel: cancel, done: make(chan struct{}), observation: observation}
	if value.check(ctx) != nil || file.SetReadDeadline(time.Unix(0, selection.DeadlineUnixNano)) != nil {
		cancel()
		return nil, ErrExecutionLauncher
	}
	go value.watch()
	return value, nil
}

func (parent *linuxExecutorParent) check(ctx context.Context) error {
	if parent == nil || ctx == nil || ctx.Err() != nil || parent.alive == nil || parent.alive.Err() != nil || parent.file == nil || parent.image == nil ||
		parent.parent.PID != os.Getppid() || parent.self.PID != os.Getpid() {
		return ErrExecutionLauncher
	}
	var current unix.Stat_t
	flags, flagsErr := unix.FcntlInt(uintptr(parent.readFD), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(uintptr(parent.readFD), unix.F_GETFD, 0)
	poll := []unix.PollFd{{Fd: int32(parent.pidfd), Events: unix.POLLIN}}
	n, pollErr := unix.Poll(poll, 0)
	if unix.Fstat(parent.readFD, &current) != nil || !sameLinuxInputStat(parent.readStat, current) || flagsErr != nil ||
		flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_NONBLOCK == 0 || fdErr != nil || fdFlags&unix.FD_CLOEXEC == 0 ||
		pollErr != nil || n != 0 || poll[0].Revents != 0 || linuxExecutorImage(ctx, parent.image) != nil {
		return ErrExecutionLauncher
	}
	rows, err := t4013.ObserveProcessTreeRecords(ctx, parent.parent.PID)
	if err != nil || len(rows) < 2 || parent.inner && len(rows) != 2 || rows[0].StartIdentity != parent.parent.StartIdentity {
		return ErrExecutionLauncher
	}
	found := false
	for _, row := range rows {
		if row.PID == parent.self.PID {
			found = row.ParentPID == parent.parent.PID && row.StartIdentity == parent.self.StartIdentity
		}
	}
	selfSession, selfErr := unix.Getsid(parent.self.PID)
	parentSession, parentErr := unix.Getsid(parent.parent.PID)
	if !found || selfErr != nil || parentErr != nil {
		return ErrExecutionLauncher
	}
	if parent.inner {
		if selfSession != parent.parent.PID || parentSession != parent.parent.PID ||
			t4013.MatchLinuxProcessExecutableImage(ctx, parent.parent.PID, parent.image) != nil {
			return ErrExecutionLauncher
		}
	} else if selfSession != parent.self.PID || parentSession == selfSession {
		return ErrExecutionLauncher
	}
	return nil
}

func (parent *linuxExecutorParent) watch() {
	defer close(parent.done)
	var one [1]byte
	count, err := parent.file.Read(one[:])
	if count != 0 || err == nil || errors.Is(err, io.EOF) {
		err = ErrExecutionLauncher
	}
	parent.watchErr = err
	parent.cancel()
}

// All callers observe one stored result; repeated or concurrent Close never
// consumes a single-use result channel or races native descriptor retirement.
func (parent *linuxExecutorParent) Close() error {
	if parent == nil || parent.file == nil || parent.done == nil {
		return ErrExecutionLauncher
	}
	parent.closeOnce.Do(func() {
		fileErr := parent.observation.close(parent.file.Close())
		parent.closeErr = fileErr
		parent.cancel()
		<-parent.done
		if !errors.Is(parent.watchErr, os.ErrClosed) {
			parent.closeErr = errors.Join(parent.closeErr, parent.watchErr)
		}
		pidfdErr := parent.observation.close(unix.Close(parent.pidfd))
		parent.retired = fileErr == nil && pidfdErr == nil
		parent.closeErr = errors.Join(parent.closeErr, pidfdErr)
	})
	return parent.closeErr
}
