//go:build linux || darwin

package typedsandbox

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Never enable the timeout writer for a regular file: O_NONBLOCK cannot make
// filesystem writes nonblocking. Docker's stderr transport is a pipe/socket.
func nonblockingReportFD(fd int) bool {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil {
		return false
	}
	kind := st.Mode & unix.S_IFMT
	return (kind == unix.S_IFIFO || kind == unix.S_IFSOCK) && unix.SetNonblock(fd, true) == nil
}

func watchdogExit(s *watchdogSnapshots, fd int, available bool) {
	if available {
		// One <= PIPE_BUF write: never retry EAGAIN/EINTR/short writes. An unavailable
		// transport cannot prevent exit; missing evidence remains unavailable.
		frame := s.frame.Load()
		if frame != nil {
			_, _ = unix.Write(fd, frame.data)
		}
	}
	os.Exit(124)
}

// The fixed extra descriptor belongs only to the worker. Mark it CLOEXEC before
// request validation or any target/tool launch, and never use os.File.Write
// (which can wait on the Go poller even when O_NONBLOCK is set).
func InitializeWorkerProgress() error {
	unix.CloseOnExec(3)
	var st unix.Stat_t
	if unix.Fstat(3, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFSOCK || unix.SetNonblock(3, true) != nil {
		return ErrRefused
	}
	return nil
}

func WorkerStage(stage string, elapsed time.Duration) {
	var id byte
	for i, s := range progressStages {
		if s == stage {
			id = byte(i)
			break
		}
	}
	if id == 0 {
		return
	}
	var frame [9]byte
	frame[0] = id
	binary.LittleEndian.PutUint64(frame[1:], uint64(elapsed.Nanoseconds()))
	_, _ = unix.Write(3, frame[:])
}

func readWorkerProgress(r *os.File, s *watchdogSnapshots) {
	for i := 0; i < 16; i++ {
		var frame [10]byte
		n, err := r.Read(frame[:])
		if err != nil || n != 9 {
			if n != 0 || !errors.Is(err, io.EOF) {
				s.progress(0, 0)
			}
			return
		}
		if !s.progress(frame[0], int64(binary.LittleEndian.Uint64(frame[1:9]))) {
			return
		}
	}
}

func workerProgressPair() (*os.File, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return nil, nil, err
	}
	for _, fd := range fds {
		unix.CloseOnExec(fd)
		if err := unix.SetNonblock(fd, true); err != nil {
			_ = unix.Close(fds[0])
			_ = unix.Close(fds[1])
			return nil, nil, err
		}
	}
	return os.NewFile(uintptr(fds[0]), "progress-reader"), os.NewFile(uintptr(fds[1]), "progress-writer"), nil
}
