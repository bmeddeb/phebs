//go:build darwin

package t4013

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	processInfoCallListPIDs = 1
	processInfoCallPIDInfo  = 2
	processParentOnly       = 6
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

func nativeProcessSnapshotProbe() func(context.Context, int) ([]int, map[int]processSnapshot, error) {
	return darwinProcessSnapshot
}

func darwinProcessSnapshot(
	ctx context.Context, root int,
) ([]int, map[int]processSnapshot, error) {
	if ctx == nil || root <= 0 {
		return nil, nil, errors.New("T40.13 native process snapshot scope is invalid")
	}
	result := []int{root}
	processes := make(map[int]processSnapshot, maxProcessDescendants+1)
	seen := map[int]struct{}{root: {}}
	if observed, err := darwinProcessObservation(root); err == nil {
		processes[root] = observed
	} else if !errors.Is(err, errProcessIdentityMissing) {
		return nil, nil, err
	}
	for index := 0; index < len(result); index++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		children, err := darwinChildPIDs(result[index])
		if err != nil {
			return nil, nil, err
		}
		for _, child := range children {
			if _, duplicate := seen[child]; duplicate {
				return nil, nil, errors.New("T40.13 native process snapshot contains a duplicate PID")
			}
			seen[child] = struct{}{}
			observed, err := darwinProcessObservation(child)
			if errors.Is(err, errProcessIdentityMissing) {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			if observed.parent != result[index] {
				continue
			}
			result = append(result, child)
			processes[child] = observed
			if len(result) > maxProcessDescendants+1 {
				return nil, nil, errors.New("T40.13 native process descendant inventory exceeds its bound")
			}
		}
	}
	return result, processes, nil
}

func darwinChildPIDs(parent int) ([]int, error) {
	if parent <= 0 {
		return nil, errors.New("T40.13 native process parent is invalid")
	}
	buffer := make([]int32, maxProcessDescendants+1)
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		processInfoCallListPIDs,
		processParentOnly,
		uintptr(parent),
		0,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer))*unsafe.Sizeof(buffer[0]),
	)
	if errno != 0 {
		return nil, fmt.Errorf("T40.13 list native process children: %w", errno)
	}
	if returned%unsafe.Sizeof(buffer[0]) != 0 || returned > uintptr(len(buffer))*unsafe.Sizeof(buffer[0]) {
		return nil, errors.New("T40.13 native process child inventory is invalid")
	}
	count := int(returned / unsafe.Sizeof(buffer[0]))
	if count > maxProcessDescendants {
		return nil, errors.New("T40.13 native process child inventory exceeds its bound")
	}
	children := make([]int, 0, count)
	for _, pid := range buffer[:count] {
		if pid <= 0 {
			continue
		}
		children = append(children, int(pid))
	}
	return children, nil
}

func darwinProcessObservation(pid int) (processSnapshot, error) {
	if pid <= 0 {
		return processSnapshot{}, errors.New("T40.13 native process PID is invalid")
	}
	var record darwinProcessTaskAllInfo
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO,
		processInfoCallPIDInfo,
		uintptr(pid),
		processPIDTaskAllInfo,
		0,
		uintptr(unsafe.Pointer(&record)),
		unsafe.Sizeof(record),
	)
	if errno != 0 {
		if errors.Is(errno, unix.ESRCH) {
			return processSnapshot{}, errors.Join(errProcessIdentityMissing,
				fmt.Errorf("T40.13 inspect native process: %w", errno))
		}
		return processSnapshot{}, fmt.Errorf("T40.13 inspect native process: %w", errno)
	}
	if returned == 0 {
		return processSnapshot{}, errProcessIdentityMissing
	}
	if returned != unsafe.Sizeof(record) {
		return processSnapshot{}, errors.New("T40.13 native process record has an invalid size")
	}
	name := record.bsd.command[:]
	if end := bytes.IndexByte(name, 0); end >= 0 {
		name = name[:end]
	}
	if record.bsd.pid != uint32(pid) || uint64(record.bsd.parent) > uint64(^uint(0)>>1) ||
		len(name) == 0 || record.bsd.startedSeconds == 0 ||
		record.bsd.startedMicroseconds >= 1_000_000 || record.task.residentBytes > 1<<63-1 {
		return processSnapshot{}, errors.New("T40.13 native process record is invalid")
	}
	return processSnapshot{
		parent: int(record.bsd.parent), rssBytes: int64(record.task.residentBytes), name: string(name),
		identityToken: strconv.FormatUint(record.bsd.startedSeconds, 10) + ":" +
			strconv.FormatUint(record.bsd.startedMicroseconds, 10),
		coherent: true,
	}, nil
}
