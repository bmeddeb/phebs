//go:build darwin

package dispatchadmission

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func captureInheritedProductionWorkspace() *ProductionWorkspaceBinding {
	var stat unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(6, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 ||
		stat.Uid != uint32(os.Geteuid()) || unix.Fstatfs(6, &fs) != nil || fs.Fsid.Val == ([2]int32{}) {
		return nil
	}
	return &ProductionWorkspaceBinding{Device: uint64(stat.Dev), Inode: stat.Ino, FSID: fs.Fsid.Val}
}

// DescribeProductionWorkspace checks a held directory against its current
// canonical location. It describes mechanical identity, not private admission.
func DescribeProductionWorkspace(file *os.File, path string) (ProductionWorkspaceBinding, error) {
	if file == nil || !validProductionPath(path) {
		return ProductionWorkspaceBinding{}, ErrProductionBootstrap
	}
	held, err := file.Stat()
	current, pathErr := os.Lstat(path)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	var stat unix.Stat_t
	var fs unix.Statfs_t
	if err != nil || pathErr != nil || canonicalErr != nil || canonical != path || !held.IsDir() || held.Mode().Perm() != 0o700 ||
		!os.SameFile(held, current) || unix.Fstat(int(file.Fd()), &stat) != nil || stat.Uid != uint32(os.Geteuid()) ||
		unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Fsid.Val == ([2]int32{}) {
		return ProductionWorkspaceBinding{}, ErrProductionBootstrap
	}
	return ProductionWorkspaceBinding{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino, FSID: fs.Fsid.Val}, nil
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
