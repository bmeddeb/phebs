//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func createExecutionSignerCeremonyClaim(
	ctx context.Context,
	namespace executionSignerNamespaceBinding,
	ceremonyID string,
	names executionSignerRegistryNames,
	raw []byte,
) (*executionSignerCeremonyClaimCustody, error) {
	owner := namespace.owner
	if owner == nil {
		return nil, ErrExecutionEpochOne
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if err := checkExecutionSignerNamespaceLocked(ctx, namespace); err != nil {
		return nil, err
	}
	for _, name := range names.absentBeforeClaim() {
		path, err := executionSignerJoinedPath(owner.path, name, maxExecutionSignerPathBytes)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("T42.2 signer registry destination already exists or cannot be inspected")
		}
	}
	path, err := executionSignerJoinedPath(owner.path, names.claim, maxExecutionSignerPathBytes)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(owner.file.Fd()), names.claim,
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, errors.New("T42.2 signer ceremony ID is already claimed or cannot be claimed")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("T42.2 signer ceremony claim descriptor is unavailable")
	}
	claim := &executionSignerCeremonyClaimCustody{
		file: file, path: path, name: names.claim, namespace: namespace,
		idSHA256: names.idSHA256, raw: append([]byte(nil), raw...), ceremonyID: ceremonyID, names: names,
	}
	fail := func(message string) (*executionSignerCeremonyClaimCustody, error) {
		return claim, errors.New(message)
	}
	if n, writeErr := file.Write(raw); writeErr != nil || n != len(raw) {
		return fail("T42.2 signer ceremony claim write failed")
	}
	if file.Sync() != nil || owner.file.Sync() != nil {
		return fail("T42.2 signer ceremony claim durability failed")
	}
	identity, err := observeExecutionSignerClaim(ctx, file, path, int64(len(raw)))
	if err != nil {
		return fail("T42.2 signer ceremony claim identity is invalid")
	}
	claim.identity = identity
	if err := checkExecutionSignerNamespaceLocked(ctx, namespace); err != nil {
		return fail("T42.2 signer namespace changed while claiming the ceremony ID")
	}
	return claim, nil
}

func checkExecutionSignerNamespaceLocked(ctx context.Context, binding executionSignerNamespaceBinding) error {
	owner := binding.owner
	if owner == nil || owner.file == nil || owner.token == nil || owner.token != binding.token || owner.digest != binding.digest ||
		ctx == nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	identity, err := observeExecutionSignerNamespace(ctx, owner.file, owner.path)
	if err != nil || identity != owner.identity {
		return ErrExecutionEpochOne
	}
	digest, err := executionSignerNamespaceSHA256(owner.path, identity)
	if err != nil || digest != binding.digest {
		return ErrExecutionEpochOne
	}
	return nil
}

func checkExecutionSignerCeremonyClaim(ctx context.Context, claim *executionSignerCeremonyClaimCustody) error {
	identity, err := observeExecutionSignerClaim(ctx, claim.file, claim.path, int64(len(claim.raw)))
	if err != nil || identity != claim.identity {
		return ErrExecutionEpochOne
	}
	if _, err := claim.file.Seek(0, io.SeekStart); err != nil {
		return ErrExecutionEpochOne
	}
	raw := make([]byte, len(claim.raw)+1)
	n, readErr := io.ReadFull(claim.file, raw)
	if readErr != io.ErrUnexpectedEOF || n != len(claim.raw) || !bytes.Equal(raw[:n], claim.raw) {
		return ErrExecutionEpochOne
	}
	return nil
}

func observeExecutionSignerClaim(ctx context.Context, file *os.File, path string, expectedSize int64) (executionSignerFileIdentity, error) {
	if ctx == nil || ctx.Err() != nil || file == nil || expectedSize < 1 || expectedSize > maxExecutionSignerClaimBytes {
		return executionSignerFileIdentity{}, ErrExecutionEpochOne
	}
	identity, err := observeExecutionSignerFile(ctx, file, path, expectedSize, expectedSize)
	if err != nil || identity.size != expectedSize {
		return executionSignerFileIdentity{}, ErrExecutionEpochOne
	}
	return identity, nil
}

func observeExecutionSignerFile(ctx context.Context, file *os.File, path string, minimum, maximum int64) (executionSignerFileIdentity, error) {
	if ctx == nil || ctx.Err() != nil || file == nil || minimum < 1 || maximum < minimum {
		return executionSignerFileIdentity{}, ErrExecutionEpochOne
	}
	held, heldErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if heldErr != nil || pathErr != nil || held == nil || current == nil {
		return executionSignerFileIdentity{}, ErrExecutionEpochOne
	}
	heldStat, heldOK := held.Sys().(*syscall.Stat_t)
	pathStat, pathOK := current.Sys().(*syscall.Stat_t)
	descriptorFlags, descriptorErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	statusFlags, statusErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	wantMode := uint32(syscall.S_IFREG | 0o600)
	if !heldOK || heldStat == nil || !pathOK || pathStat == nil ||
		current.Mode()&os.ModeSymlink != 0 || !held.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(held, current) ||
		descriptorErr != nil || statusErr != nil || descriptorFlags&unix.FD_CLOEXEC == 0 || statusFlags&unix.O_ACCMODE != unix.O_RDWR ||
		uint32(heldStat.Mode) != wantMode || uint32(pathStat.Mode) != wantMode ||
		heldStat.Uid != uint32(os.Geteuid()) || pathStat.Uid != uint32(os.Geteuid()) ||
		heldStat.Nlink != 1 || pathStat.Nlink != 1 || held.Size() != current.Size() || held.Size() < minimum || held.Size() > maximum ||
		int64(heldStat.Dev) < 0 || heldStat.Ino == 0 || ctx.Err() != nil {
		return executionSignerFileIdentity{}, ErrExecutionEpochOne
	}
	identity := executionSignerFileIdentity{
		device: int64(heldStat.Dev), inode: uint64(heldStat.Ino), mode: uint32(heldStat.Mode),
		uid: heldStat.Uid, size: held.Size(), links: uint64(heldStat.Nlink),
	}
	other := executionSignerFileIdentity{
		device: int64(pathStat.Dev), inode: uint64(pathStat.Ino), mode: uint32(pathStat.Mode),
		uid: pathStat.Uid, size: current.Size(), links: uint64(pathStat.Nlink),
	}
	if identity != other {
		return executionSignerFileIdentity{}, ErrExecutionEpochOne
	}
	return identity, nil
}
