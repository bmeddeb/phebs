//go:build linux

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func (owner *LinuxPressureVolume) checkRecord(ctx context.Context) (retErr error) {
	if owner.checkRoots(ctx) != nil {
		return ErrLinuxPressure
	}
	fd, err := unix.Openat(int(owner.root.file.Fd()), "owner.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrLinuxPressure
	}
	file := os.NewFile(uintptr(fd), "linux-pressure-retained-record")
	defer func() { retErr = errors.Join(retErr, owner.observation.close(file.Close())) }()
	var before, after, linked unix.Stat_t
	if unix.Fstat(fd, &before) != nil || !linuxPressurePrivateFile(before) || before.Size < 1 || before.Size > linuxPressureRecordLimit ||
		uint64(before.Dev) != owner.root.node.Device {
		return ErrLinuxPressure
	}
	raw, err := io.ReadAll(io.LimitReader(file, linuxPressureRecordLimit+1))
	if err != nil || int64(len(raw)) != before.Size || ctx.Err() != nil || unix.Fstat(fd, &after) != nil ||
		unix.Fstatat(int(owner.root.file.Fd()), "owner.json", &linked, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameLinuxInputStat(before, after) || !sameLinuxInputStat(after, linked) {
		return ErrLinuxPressure
	}
	record, err := decodeLinuxPressureRecord(raw)
	if err != nil || record != owner.record {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *LinuxPressureVolume) writeRecord(ctx context.Context, next linuxPressureRecord, initial bool) (retErr error) {
	if owner.checkRoots(ctx) != nil || !validLinuxPressureRecord(next) ||
		initial && (next.Phase != "created" || next != owner.record) ||
		!initial && (!linuxPressureNextRecord(owner.record, next) || owner.checkRecord(ctx) != nil) {
		return ErrLinuxPressure
	}
	raw, err := json.Marshal(next)
	if err != nil || len(raw) > linuxPressureRecordLimit {
		return ErrLinuxPressure
	}
	name := "owner.next"
	if initial {
		name = "owner.json"
	}
	fd, err := unix.Openat(int(owner.root.file.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return ErrLinuxPressure
	}
	file := os.NewFile(uintptr(fd), "linux-pressure-record-writer")
	defer func() { retErr = errors.Join(retErr, owner.observation.close(file.Close())) }()
	var before, after, linked unix.Stat_t
	if unix.Fstat(fd, &before) != nil || !linuxPressurePrivateFile(before) || before.Size != 0 || uint64(before.Dev) != owner.root.node.Device {
		return ErrLinuxPressure
	}
	written, err := file.Write(raw)
	if err != nil || written != len(raw) || file.Sync() != nil || ctx.Err() != nil || unix.Fstat(fd, &after) != nil ||
		!linuxPressurePrivateFile(after) || before.Dev != after.Dev || before.Ino != after.Ino || after.Size != int64(len(raw)) ||
		unix.Fstatat(int(owner.root.file.Fd()), name, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameLinuxInputStat(after, linked) {
		return ErrLinuxPressure
	}
	if !initial {
		// Check the old durable record again immediately before replacement.
		// No adopted journal or retry of an incomplete owner.next is admitted.
		if owner.checkRecord(ctx) != nil || unix.Renameat(int(owner.root.file.Fd()), name, int(owner.root.file.Fd()), "owner.json") != nil {
			return ErrLinuxPressure
		}
	}
	if owner.root.file.Sync() != nil || ctx.Err() != nil {
		return ErrLinuxPressure
	}
	owner.record = next
	owner.durablePhase = next.Phase
	return owner.checkRecord(ctx)
}

func (owner *LinuxPressureVolume) advance(ctx context.Context, phase string) error {
	next := owner.record
	next.Phase = phase
	return owner.writeRecord(ctx, next, false)
}
