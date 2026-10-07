//go:build darwin || linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func openExecutionSignerNamespace(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, ErrExecutionEpochOne
	}
	return file, nil
}

func observeExecutionSignerNamespace(ctx context.Context, file *os.File, path string) (executionSignerNamespaceIdentity, error) {
	if ctx == nil || ctx.Err() != nil || file == nil {
		return executionSignerNamespaceIdentity{}, ErrExecutionEpochOne
	}
	pathInfo, pathErr := os.Lstat(path)
	fileInfo, fileErr := file.Stat()
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	pathStat, pathOK := signerNamespaceSyscallStat(pathInfo)
	fileStat, fileOK := signerNamespaceSyscallStat(fileInfo)
	descriptorFlags, descriptorErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	statusFlags, statusErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	wantMode := uint32(syscall.S_IFDIR | 0o700)
	if pathErr != nil || fileErr != nil || canonicalErr != nil || canonical != path || !pathOK || !fileOK || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!pathInfo.IsDir() || !fileInfo.IsDir() || !os.SameFile(pathInfo, fileInfo) ||
		descriptorErr != nil || statusErr != nil || descriptorFlags&unix.FD_CLOEXEC == 0 || statusFlags&unix.O_ACCMODE != unix.O_RDONLY ||
		uint32(pathStat.Mode) != wantMode || uint32(fileStat.Mode) != wantMode ||
		pathStat.Uid != uint32(os.Geteuid()) || fileStat.Uid != uint32(os.Geteuid()) || ctx.Err() != nil {
		return executionSignerNamespaceIdentity{}, ErrExecutionEpochOne
	}
	pathIdentity := executionSignerNamespaceIdentity{
		device: int64(pathStat.Dev), inode: uint64(pathStat.Ino), mode: uint32(pathStat.Mode), uid: pathStat.Uid,
	}
	fileIdentity := executionSignerNamespaceIdentity{
		device: int64(fileStat.Dev), inode: uint64(fileStat.Ino), mode: uint32(fileStat.Mode), uid: fileStat.Uid,
	}
	if pathIdentity != fileIdentity || pathIdentity.device < 0 || pathIdentity.inode == 0 {
		return executionSignerNamespaceIdentity{}, ErrExecutionEpochOne
	}
	return pathIdentity, nil
}

func signerNamespaceSyscallStat(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return stat, ok && stat != nil
}

type executionSignerNamespaceIdentity struct {
	device int64
	inode  uint64
	mode   uint32
	uid    uint32
}

func holdExecutionSignerNamespace(ctx context.Context, path string) (*executionSignerNamespaceCustody, error) {
	if ctx == nil || ctx.Err() != nil || !validExecutionLauncherPath(path) {
		return nil, ErrExecutionEpochOne
	}
	file, err := openExecutionSignerNamespace(path)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	token := byte(1)
	custody := &executionSignerNamespaceCustody{file: file, path: path, token: &token}
	identity, err := observeExecutionSignerNamespace(ctx, file, path)
	if err != nil {
		_ = file.Close()
		return nil, ErrExecutionEpochOne
	}
	digest, err := executionSignerNamespaceSHA256(path, identity)
	if err != nil {
		_ = file.Close()
		return nil, ErrExecutionEpochOne
	}
	custody.identity, custody.digest = identity, digest
	return custody, nil
}
