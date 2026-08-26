//go:build darwin

package t4013

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	processAllPIDs      = 1
	darwinProcessZombie = 5
)

func privateServerSessionPIDs(sessionID int) ([]int, error) {
	if sessionID <= 0 {
		return nil, errors.New("T40.13 private process session is invalid")
	}
	allPIDs, err := darwinAllProcessPIDs()
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, 16)
	for _, pid := range allPIDs {
		session, err := unix.Getsid(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect T40.13 process session identity: %w", err)
		}
		if session != sessionID {
			continue
		}
		status, present, err := darwinProcessStatus(pid)
		if err != nil {
			return nil, err
		}
		if !present || status == darwinProcessZombie {
			continue
		}
		confirmedSession, err := unix.Getsid(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reinspect T40.13 process session identity: %w", err)
		}
		if confirmedSession != sessionID {
			continue
		}
		pids = append(pids, pid)
		if len(pids) > 1024 {
			return nil, errors.New("T40.13 private process session exceeds its process bound")
		}
	}
	return pids, nil
}

func darwinAllProcessPIDs() ([]int, error) {
	// PROC_ALL_PIDS includes PID 0; retain another slot as the
	// non-kernel-process overflow sentinel.
	buffer := make([]int32, maxProcessSnapshotRows+2)
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, //nolint:staticcheck // x/sys has no proc_listpids wrapper.
		processInfoCallListPIDs, processAllPIDs, 0, 0,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer))*unsafe.Sizeof(buffer[0]),
	)
	if errno != 0 {
		return nil, fmt.Errorf("list T40.13 host processes: %w", errno)
	}
	if returned%unsafe.Sizeof(buffer[0]) != 0 ||
		returned > uintptr(len(buffer))*unsafe.Sizeof(buffer[0]) {
		return nil, errors.New("T40.13 host process inventory is invalid")
	}
	count := int(returned / unsafe.Sizeof(buffer[0]))
	pids := make([]int, 0, count)
	seen := make(map[int]struct{}, count)
	for _, rawPID := range buffer[:count] {
		pid := int(rawPID)
		if pid == 0 { // Darwin includes the kernel process in PROC_ALL_PIDS.
			continue
		}
		if pid < 0 {
			return nil, errors.New("T40.13 host process inventory contains an invalid PID")
		}
		if _, duplicate := seen[pid]; duplicate {
			return nil, errors.New("T40.13 host process inventory contains a duplicate PID")
		}
		seen[pid] = struct{}{}
		pids = append(pids, pid)
		if len(pids) > maxProcessSnapshotRows {
			return nil, errors.New("T40.13 host process inventory exceeds its bound")
		}
	}
	return pids, nil
}

func darwinProcessStatus(pid int) (uint32, bool, error) {
	if pid <= 0 {
		return 0, false, errors.New("T40.13 process identity is invalid")
	}
	var record darwinProcessShortBSDInfo
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, //nolint:staticcheck // x/sys has no proc_pidinfo wrapper.
		processInfoCallPIDInfo, uintptr(pid), processPIDShortBSDInfo, 0,
		uintptr(unsafe.Pointer(&record)), unsafe.Sizeof(record),
	)
	if errors.Is(errno, unix.ESRCH) || errno == 0 && returned == 0 {
		return 0, false, nil
	}
	if errno != 0 {
		return 0, false, fmt.Errorf("inspect T40.13 process PID %d: %w", pid, errno)
	}
	if returned != unsafe.Sizeof(record) || record.pid != uint32(pid) {
		return 0, false, errors.New("T40.13 process record is invalid")
	}
	return record.status, true, nil
}
