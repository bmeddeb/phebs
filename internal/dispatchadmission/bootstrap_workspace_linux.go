//go:build linux

package dispatchadmission

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Observe the original inherited slot without adopting any unproved FD. Exec
// clears CLOEXEC on supplied descriptors; adoption protects it before nested use.
func captureInheritedProductionWorkspace() *ProductionWorkspaceBinding {
	var stat unix.Stat_t
	var fs unix.Statfs_t
	access, accessErr := unix.FcntlInt(6, unix.F_GETFL, 0)
	if accessErr != nil || access&unix.O_ACCMODE != unix.O_RDONLY || access&unix.O_PATH != 0 || unix.Fstat(6, &stat) != nil || stat.Mode != unix.S_IFDIR|0o700 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink == 0 || unix.Fstatfs(6, &fs) != nil || fs.Fsid.Val == ([2]int32{}) || fs.Flags&unix.ST_RDONLY != 0 {
		return nil
	}
	return &ProductionWorkspaceBinding{Device: stat.Dev, Inode: stat.Ino, FSID: fs.Fsid.Val}
}

// Mechanical full-width identity only; no resource or execution admission.
func DescribeProductionWorkspace(file *os.File, path string) (ProductionWorkspaceBinding, error) {
	if file == nil || !validProductionPath(path) {
		return ProductionWorkspaceBinding{}, ErrProductionBootstrap
	}
	held, err := file.Stat()
	current, pathErr := os.Lstat(path)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	var stat unix.Stat_t
	var fs unix.Statfs_t
	flags, flagErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	access, accessErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if accessErr != nil || access&unix.O_ACCMODE != unix.O_RDONLY || access&unix.O_PATH != 0 || err != nil || pathErr != nil || canonicalErr != nil || canonical != path || !held.IsDir() || held.Mode().Perm() != 0o700 || !os.SameFile(held, current) || unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode != unix.S_IFDIR|0o700 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink == 0 || unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Fsid.Val == ([2]int32{}) || fs.Flags&unix.ST_RDONLY != 0 || flagErr != nil || flags&unix.FD_CLOEXEC == 0 {
		return ProductionWorkspaceBinding{}, ErrProductionBootstrap
	}
	return ProductionWorkspaceBinding{Path: path, Device: stat.Dev, Inode: stat.Ino, FSID: fs.Fsid.Val}, nil
}
func adoptProductionWorkspace(file *os.File, expected ProductionWorkspaceBinding) (*productionWorkspace, error) {
	if file == nil || protectInheritance(file) != nil {
		return nil, ErrProductionBootstrap
	}
	actual, err := DescribeProductionWorkspace(file, expected.Path)
	if err != nil || actual != expected {
		return nil, ErrProductionBootstrap
	}
	info, err := file.Stat()
	if err != nil {
		return nil, ErrProductionBootstrap
	}
	return &productionWorkspace{file: file, info: info, binding: actual}, nil
}
