//go:build darwin

package compat

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	maxSandboxProcesses     = 128
	processInfoCallListPIDs = 1
	processInfoCallPIDInfo  = 2
	processGroupOnly        = 2
	processPIDTaskAllInfo   = 2
)

type darwinProcessBSDInfo struct {
	flags, status, exitStatus, pid, parent, uid, gid, realUID, realGID, savedUID, savedGID, reserved uint32
	command                                                                                          [16]byte
	name                                                                                             [32]byte
	files, group, jobControl, terminal, terminalGroup                                                uint32
	nice                                                                                             int32
	startedSeconds, startedMicroseconds                                                              uint64
}

type darwinProcessTaskInfo struct {
	virtualBytes, residentBytes, totalUser, totalSystem, threadsUser, threadsSystem uint64
	policy, faults, pageIns, copyOnWriteFaults, messagesSent, messagesReceived      int32
	machSyscalls, unixSyscalls, contextSwitches, threads, running, priority         int32
}

type darwinProcessTaskAllInfo struct {
	bsd  darwinProcessBSDInfo
	task darwinProcessTaskInfo
}

func processGroupRSS(pgid int) (int64, error) {
	pids, err := darwinProcessGroupPIDs(pgid)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, pid := range pids {
		resident, present, err := darwinProcessRSS(pid, pgid)
		if err != nil {
			return 0, err
		}
		if !present {
			continue
		}
		if resident > uint64(1<<63-1)-uint64(total) {
			return 0, errors.New("compatibility sandbox RSS overflowed")
		}
		total += int64(resident)
	}
	return total, nil
}

func darwinProcessGroupPIDs(pgid int) ([]int, error) {
	if pgid <= 0 {
		return nil, errors.New("compatibility sandbox process group is invalid")
	}
	buffer := make([]int32, maxSandboxProcesses+1)
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, //nolint:staticcheck // x/sys has no proc_listpids wrapper.
		processInfoCallListPIDs, processGroupOnly, uintptr(pgid), 0,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer))*unsafe.Sizeof(buffer[0]),
	)
	if errno != 0 {
		return nil, fmt.Errorf("list compatibility sandbox processes: %w", errno)
	}
	if returned%unsafe.Sizeof(buffer[0]) != 0 ||
		returned > uintptr(len(buffer))*unsafe.Sizeof(buffer[0]) {
		return nil, errors.New("compatibility sandbox process inventory is invalid")
	}
	count := int(returned / unsafe.Sizeof(buffer[0]))
	if count > maxSandboxProcesses {
		return nil, errors.New("compatibility sandbox process inventory exceeds its bound")
	}
	pids := make([]int, 0, count)
	seen := make(map[int]struct{}, count)
	for _, rawPID := range buffer[:count] {
		pid := int(rawPID)
		if pid <= 0 {
			return nil, errors.New("compatibility sandbox process inventory contains an invalid PID")
		}
		if _, duplicate := seen[pid]; duplicate {
			return nil, errors.New("compatibility sandbox process inventory contains a duplicate PID")
		}
		seen[pid] = struct{}{}
		pids = append(pids, pid)
	}
	return pids, nil
}

func darwinProcessRSS(pid, pgid int) (uint64, bool, error) {
	if pid <= 0 || pgid <= 0 {
		return 0, false, errors.New("compatibility sandbox process identity is invalid")
	}
	var record darwinProcessTaskAllInfo
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, //nolint:staticcheck // x/sys has no proc_pidinfo wrapper.
		processInfoCallPIDInfo, uintptr(pid), processPIDTaskAllInfo, 0,
		uintptr(unsafe.Pointer(&record)), unsafe.Sizeof(record),
	)
	if errors.Is(errno, unix.ESRCH) || errno == 0 && returned == 0 {
		return 0, false, nil
	}
	if errno != 0 {
		return 0, false, fmt.Errorf("inspect compatibility sandbox process PID %d: %w", pid, errno)
	}
	if returned != unsafe.Sizeof(record) || record.bsd.pid != uint32(pid) {
		return 0, false, errors.New("compatibility sandbox process record is invalid")
	}
	if record.bsd.group != uint32(pgid) {
		return 0, false, nil
	}
	return record.task.residentBytes, true, nil
}
