//go:build linux

package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// A separate anonymous target-owned copy. This owns bytes, not reference or
// execution authority. The root image and its public custody remain unchanged.
type linuxPressureBorrowImage struct {
	file        *os.File
	stat        unix.Stat_t
	digest      string
	uid, gid    uint32
	observation *executionPreworkObservation
	closed      bool
	closeErr    error
}

func copyLinuxPressureBorrowImage(ctx context.Context, source *os.File, uid, gid uint32, observation *executionPreworkObservation) (_ *linuxPressureBorrowImage, retErr error) {
	if ctx == nil || ctx.Err() != nil || source == nil || uid == ^uint32(0) || gid == ^uint32(0) {
		return nil, ErrLinuxPressure
	}
	var before unix.Stat_t
	if unix.Fstat(int(source.Fd()), &before) != nil || before.Size <= 0 || before.Size > maxInputCustodyFileBytes {
		return nil, ErrLinuxPressure
	}
	entry := linuxExecutionInput{file: source, stat: before, identity: LinuxExecutionInputIdentity{Bytes: before.Size, Executable: true}}
	if !linuxInputProtected(source, entry) || !linuxInputELF(source, before.Size) {
		return nil, ErrLinuxPressure
	}
	fd, err := unix.MemfdCreate("phebs-pressure-target", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	writer := os.NewFile(uintptr(fd), "pressure-target-writer")
	writerClosed := false
	owner := &linuxPressureBorrowImage{uid: uid, gid: gid, observation: observation}
	defer func() {
		if !writerClosed {
			retErr = errors.Join(retErr, observation.close(writer.Close()))
		}
		if retErr != nil {
			_ = owner.Close()
		}
	}()
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	written, err := io.CopyBuffer(io.MultiWriter(writer, hash), executionInputReader{ctx, io.NewSectionReader(source, 0, before.Size)}, buffer)
	if err != nil || written != before.Size || ctx.Err() != nil || !linuxInputProtected(source, entry) ||
		unix.Fchown(fd, int(uid), int(gid)) != nil || unix.Fchmod(fd, 0o500) != nil {
		return nil, ErrLinuxPressure
	}
	if _, err = unix.FcntlInt(writer.Fd(), unix.F_ADD_SEALS, linuxInputSeals); err != nil || unix.Fstat(fd, &owner.stat) != nil {
		return nil, ErrLinuxPressure
	}
	owner.file, err = openLinuxInputReadOnly(writer)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	closeErr := observation.close(writer.Close())
	writerClosed = true
	owner.digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if closeErr != nil || owner.check(ctx) != nil || !linuxInputProtected(source, entry) || owner.stat.Ino == before.Ino && owner.stat.Dev == before.Dev {
		return nil, ErrLinuxPressure
	}
	return owner, nil
}

func (owner *linuxPressureBorrowImage) check(ctx context.Context) error {
	if owner == nil || owner.closed || owner.file == nil || ctx == nil || ctx.Err() != nil || !validExecutionSHA256(owner.digest) {
		return ErrLinuxPressure
	}
	var current unix.Stat_t
	fd := int(owner.file.Fd())
	seals, sealErr := unix.FcntlInt(owner.file.Fd(), unix.F_GET_SEALS, 0)
	flags, flagsErr := unix.FcntlInt(owner.file.Fd(), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(owner.file.Fd(), unix.F_GETFD, 0)
	if unix.Fstat(fd, &current) != nil || !sameLinuxInputStat(owner.stat, current) || current.Mode != unix.S_IFREG|0o500 ||
		current.Uid != owner.uid || current.Gid != owner.gid || current.Nlink != 0 || current.Size <= 0 || current.Size > maxInputCustodyFileBytes ||
		sealErr != nil || seals&linuxInputSeals != linuxInputSeals || flagsErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY ||
		fdErr != nil || fdFlags&unix.FD_CLOEXEC == 0 {
		return ErrLinuxPressure
	}
	return nil
}

func (owner *linuxPressureBorrowImage) Close() error {
	if owner == nil {
		return nil
	}
	if !owner.closed {
		owner.closed = true
		if owner.file != nil {
			owner.closeErr = owner.observation.close(owner.file.Close())
		}
	}
	return owner.closeErr
}
