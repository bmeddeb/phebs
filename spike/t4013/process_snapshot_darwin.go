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
	processPIDShortBSDInfo  = 13
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

type darwinProcessShortBSDInfo struct {
	pid, parent, group, status uint32
	command                    [16]byte
	flags                      uint32
	uid, gid, realUID, realGID uint32
	savedUID, savedGID         uint32
	reserved                   uint32
}

func nativeProcessSnapshotProbe() func(context.Context, int) ([]int, map[int]processSnapshot, error) {
	return darwinProcessSnapshot
}

func darwinProcessSnapshot(
	ctx context.Context, root int,
) ([]int, map[int]processSnapshot, error) {
	return collectDarwinProcessSnapshot(ctx, root, darwinChildPIDs, darwinProcessObservation)
}

func collectDarwinProcessSnapshot(
	ctx context.Context, root int,
	childrenOf func(int) ([]int, error),
	observe func(int) (processSnapshot, error),
) ([]int, map[int]processSnapshot, error) {
	if ctx == nil || root <= 0 {
		return nil, nil, errors.New("T40.13 native process snapshot scope is invalid")
	}
	result := []int{root}
	processes := make(map[int]processSnapshot, maxProcessDescendants+1)
	seen := map[int]struct{}{root: {}}
	if observed, err := observe(root); err == nil {
		processes[root] = observed
	} else if !errors.Is(err, errProcessIdentityMissing) {
		return nil, nil, err
	}
	for index := 0; index < len(result); index++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		children, err := childrenOf(result[index])
		if err != nil {
			return nil, nil, err
		}
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if _, duplicate := seen[child]; duplicate {
				return nil, nil, errors.New("T40.13 native process snapshot contains a duplicate PID")
			}
			if len(seen) > maxProcessDescendants {
				return nil, nil, errors.New("T40.13 native process candidate inventory exceeds its bound")
			}
			seen[child] = struct{}{}
			observed, err := observe(child)
			if errors.Is(err, errProcessIdentityMissing) {
				continue
			}
			if errors.Is(err, unix.EPERM) {
				missing, reconcileErr := reconcileDarwinDeniedChild(
					child, result[index], darwinProcessShortParent,
				)
				if reconcileErr != nil {
					return nil, nil, errors.Join(err, reconcileErr)
				}
				if missing {
					continue
				}
			}
			if err != nil {
				return nil, nil, err
			}
			if observed.parent != result[index] {
				return nil, nil, errors.New("T40.13 native process parent changed during observation")
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
				fmt.Errorf("T40.13 inspect native process PID %d: %w", pid, errno))
		}
		return processSnapshot{}, fmt.Errorf("T40.13 inspect native process PID %d: %w", pid, errno)
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

func reconcileDarwinDeniedChild(
	pid, expectedParent int, shortParent func(int) (int, error),
) (bool, error) {
	parent, err := shortParent(pid)
	if errors.Is(err, errProcessIdentityMissing) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return parent != expectedParent, nil
}

func darwinProcessShortParent(pid int) (int, error) {
	var record darwinProcessShortBSDInfo
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, processInfoCallPIDInfo, uintptr(pid), processPIDShortBSDInfo, 0,
		uintptr(unsafe.Pointer(&record)), unsafe.Sizeof(record),
	)
	if errno != 0 {
		if errors.Is(errno, unix.ESRCH) {
			return 0, errors.Join(errProcessIdentityMissing,
				fmt.Errorf("T40.13 inspect native short process PID %d: %w", pid, errno))
		}
		return 0, fmt.Errorf("T40.13 inspect native short process PID %d: %w", pid, errno)
	}
	if returned == 0 {
		return 0, errProcessIdentityMissing
	}
	if returned != unsafe.Sizeof(record) || record.pid != uint32(pid) ||
		uint64(record.parent) > uint64(^uint(0)>>1) {
		return 0, errors.New("T40.13 native short process record is invalid")
	}
	return int(record.parent), nil
}
