//go:build linux

package t421

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type linuxExecutorPreworkFailure struct {
	Schema                    string `json:"schema"`
	Attempt                   string `json:"attempt"`
	SourceCommit              string `json:"source_commit"`
	RetainedWorkspace         string `json:"retained_workspace,omitempty"`
	WorkspaceNotClean         bool   `json:"workspace_not_clean"`
	HelperPID                 int    `json:"helper_pid,omitempty"`
	NativeWaitPresent         bool   `json:"native_wait_present"`
	NativeCloseFailed         bool   `json:"native_close_failed"`
	FirstObservedJoined       bool   `json:"first_observed_joined"`
	FirstObservedSessionEmpty bool   `json:"first_observed_session_empty"`
}

// First-observed private failure facts only. Actual terminal receipts and Waits
// remain separate; publication never authorizes cleanup or accepts provenance.
func writeLinuxExecutorPreworkFailure(value linuxExecutorPreworkSelection, observation *executionPreworkObservation) (retErr error) {
	if observation == nil {
		return nil
	}
	observation.mu.Lock()
	record := linuxExecutorPreworkFailure{Schema: "t422-linux-prework-private-failure-v1", Attempt: value.Attempt, SourceCommit: value.SourceCommit,
		RetainedWorkspace: observation.retainedWorkspace, WorkspaceNotClean: observation.workspaceRetained,
		FirstObservedJoined: observation.unknownJoined, FirstObservedSessionEmpty: observation.unknownEmpty, NativeCloseFailed: observation.nativeCloseFailed.Load()}
	if command := observation.unknownCommand; command != nil && command.Process != nil {
		record.HelperPID, record.NativeWaitPresent = command.Process.Pid, command.ProcessState != nil
	}
	observation.mu.Unlock()
	if !record.WorkspaceNotClean && record.HelperPID == 0 && !record.NativeCloseFailed {
		return nil // No healthy-path filesystem work.
	}
	if !validLinuxExecutorAttempt(value.Attempt) || !validCommit(value.SourceCommit) || !linuxGitPrivateDirectory(value.PreparationRoot) ||
		len(record.RetainedWorkspace) > maxInputCustodyPathBytes || record.RetainedWorkspace != "" && !executionGitAbsolutePath(record.RetainedWorkspace) {
		return ErrExecutionLauncher
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > 8<<10 {
		return ErrExecutionLauncher
	}
	rootFD, err := unix.Open(value.PreparationRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrExecutionLauncher
	}
	root := os.NewFile(uintptr(rootFD), "executor-failure-root")
	defer func() { retErr = errors.Join(retErr, observation.close(root.Close())) }()
	var before, linked unix.Stat_t
	if unix.Fstat(rootFD, &before) != nil || unix.Lstat(value.PreparationRoot, &linked) != nil || !sameLinuxInputStat(before, linked) ||
		before.Mode != unix.S_IFDIR|0o700 || before.Uid != uint32(os.Geteuid()) {
		return ErrExecutionLauncher
	}
	name := "executor-prework-" + value.Attempt + "-failure.json"
	fd, err := unix.Openat(rootFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return ErrExecutionLauncher
	}
	file := os.NewFile(uintptr(fd), "executor-private-failure")
	defer func() { retErr = errors.Join(retErr, observation.close(file.Close())) }()
	var initial, after unix.Stat_t
	if unix.Fstat(fd, &initial) != nil || initial.Mode != unix.S_IFREG|0o600 || initial.Uid != uint32(os.Geteuid()) || initial.Nlink != 1 || initial.Size != 0 {
		return ErrExecutionLauncher
	}
	written, err := file.Write(raw)
	if err != nil || written != len(raw) || file.Sync() != nil || unix.Fstat(fd, &after) != nil ||
		after.Mode != initial.Mode || after.Uid != initial.Uid || after.Nlink != 1 || after.Dev != initial.Dev || after.Ino != initial.Ino || after.Size != int64(len(raw)) ||
		unix.Fstatat(rootFD, name, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameLinuxInputStat(after, linked) ||
		root.Sync() != nil || unix.Lstat(value.PreparationRoot, &linked) != nil || before.Dev != linked.Dev || before.Ino != linked.Ino || before.Mode != linked.Mode || before.Uid != linked.Uid ||
		filepath.Dir(filepath.Join(value.PreparationRoot, name)) != value.PreparationRoot {
		return ErrExecutionLauncher
	}
	return nil
}
