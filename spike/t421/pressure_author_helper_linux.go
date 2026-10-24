//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func protectLinuxPressureAuthorInputHelper(ctx context.Context, request ReferenceToolRequest, view *linuxPressureAuthorView, observation *executionPreworkObservation) (*LinuxInputTreeHelper, error) {
	if view.check(ctx) != nil {
		return nil, ErrLinuxInputTree
	}
	request.Role, request.PlanSchema = linuxInputTreeRole, linuxPressureAuthorInputRecipe
	tool, err := protectLinuxPressureAuthorOfflineTool(ctx, request, view, observation)
	if tool == nil {
		return nil, ErrLinuxInputTree
	}
	return &LinuxInputTreeHelper{tool: tool, layout: view}, err
}
func protectLinuxPressureAuthorReferenceHelper(ctx context.Context, request ReferenceToolRequest, view *linuxPressureAuthorView, observation *executionPreworkObservation) (*LinuxGoReferenceHelper, error) {
	if view.check(ctx) != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	request.Role, request.PlanSchema = linuxGoReferenceRole, linuxPressureAuthorReferenceRecipe
	tool, err := protectLinuxPressureAuthorOfflineTool(ctx, request, view, observation)
	if tool == nil {
		return nil, ErrExecutionGoBuildCustody
	}
	return &LinuxGoReferenceHelper{tool: tool, layout: view}, err
}

// Only the closed helper-v2 entry reaches this. The original descriptor is
// mapped UID0 and belongs to this helper's exact native parent. All mounts are
// in the helper's birth namespace; no path is reinterpreted in its caller.
func setupLinuxPressureAuthorHelperMount(ctx context.Context, workFD, namespaceFD int) (retErr error) {
	unix.Umask(0o077)
	work := os.NewFile(uintptr(workFD), "pressure-helper-original-work")
	prior := os.NewFile(uintptr(namespaceFD), "pressure-helper-parent-namespace")
	defer func() { retErr = errors.Join(retErr, prior.Close()) }()
	view, err := createLinuxPressureAuthorView(ctx, work, prior, 0, 0, nil)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, view.Close()) }()
	// The immutable input copy has its existing independent 2GiB/100k policy.
	if unix.Mount("tmpfs", linuxPressureAuthorInputs, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0700,size=2147483648,nr_inodes=100002") != nil {
		return ErrLinuxInputTree
	}
	file, err := os.Open(linuxPressureAuthorInputs)
	if err != nil {
		return ErrLinuxInputTree
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	var stat unix.Stat_t
	var fs unix.Statfs_t
	rows, readErr := file.ReadDir(1)
	if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode != unix.S_IFDIR|0o700 || stat.Uid != 0 || unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Type != unix.TMPFS_MAGIC || len(rows) != 0 || readErr != io.EOF || unix.Fchdir(int(file.Fd())) != nil {
		return ErrLinuxInputTree
	}
	// FD5 only names the old covered placeholder. WorkFD6 is consumed by the
	// reference workspace check, or by the input-only helper here.
	if unix.Close(5) != nil {
		return ErrLinuxInputTree
	}
	if namespaceFD == 7 {
		return work.Close()
	}
	return nil
}
func linuxPressureAuthorHelperWorkspace() (string, error) {
	var held, view unix.Stat_t
	if unix.Fstat(6, &held) != nil || unix.Stat(linuxPressureAuthorWork, &view) != nil || held.Dev != view.Dev || held.Ino != view.Ino || held.Mode != unix.S_IFDIR|0o700 || held.Uid != 0 || unix.Close(6) != nil {
		return "", ErrExecutionGoBuildCustody
	}
	for _, child := range []string{"home", "tmp", "cache"} {
		if !linuxGitPrivateDirectory(filepath.Join(linuxPressureAuthorWork, child)) {
			return "", ErrExecutionGoBuildCustody
		}
	}
	return linuxPressureAuthorWork, nil
}
func observeLinuxPressureAuthorHelperLayout(pid int, original *os.File) error {
	if pid <= 0 || original == nil {
		return ErrLinuxInputTree
	}
	var held, view, scaffold, scaffoldDir unix.Stat_t
	var left, right unix.Statfs_t
	root := fmt.Sprintf("/proc/%d/root", pid)
	if unix.Fstat(int(original.Fd()), &held) != nil || unix.Stat(root+linuxPressureAuthorWork, &view) != nil || held.Dev != view.Dev || held.Ino != view.Ino || held.Mode != view.Mode || view.Uid != held.Uid || view.Gid != held.Gid ||
		unix.Fstatfs(int(original.Fd()), &left) != nil || unix.Statfs(root+linuxPressureAuthorWork, &right) != nil || left.Fsid.Val != right.Fsid.Val ||
		unix.Lstat(root+"/tmp", &scaffold) != nil || scaffold.Mode != unix.S_IFDIR|0o700 || scaffold.Uid != held.Uid || scaffold.Gid != held.Gid || unix.Statfs(root+"/tmp", &right) != nil || right.Type != unix.TMPFS_MAGIC || right.Bsize <= 0 || right.Blocks > linuxPressureAuthorScaffoldBytes/uint64(right.Bsize) || right.Files > linuxPressureAuthorScaffoldInodes || right.Flags&(unix.ST_NOSUID|unix.ST_NODEV) != unix.ST_NOSUID|unix.ST_NODEV ||
		unix.Lstat(root+linuxPressureAuthorScaffold, &scaffoldDir) != nil || scaffoldDir.Mode != unix.S_IFDIR|0o700 || scaffoldDir.Uid != held.Uid || scaffoldDir.Gid != held.Gid {
		return ErrLinuxInputTree
	}
	file, err := os.Open(root + linuxPressureAuthorWork)
	if err != nil {
		return ErrLinuxInputTree
	}
	tmp, openErr := os.Open(root + "/tmp")
	if openErr != nil {
		_ = file.Close()
		return ErrLinuxInputTree
	}
	prior, e := inputTreeMountID(int(original.Fd()))
	actual, a := inputTreeMountID(int(file.Fd()))
	tmpMount, m := inputTreeMountID(int(tmp.Fd()))
	closeErr := errors.Join(file.Close(), tmp.Close())
	if e != nil || a != nil || m != nil || closeErr != nil || prior == actual || prior == tmpMount || actual == tmpMount {
		return ErrLinuxInputTree
	}
	return nil
}

func protectLinuxPressureAuthorOfflineTool(ctx context.Context, request ReferenceToolRequest, view *linuxPressureAuthorView, observation *executionPreworkObservation) (*LinuxExecutionToolCustody, error) {
	if view.check(ctx) != nil {
		return nil, ErrExecutionToolCustody
	}
	if _, _, _, _, _, err := referenceToolRoleForSchema(request.Role, request.PlanSchema, request.SourceCommit); err != nil {
		return nil, ErrExecutionToolCustody
	}
	tool, err := protectLinuxExecutionToolObserved(ctx, request.Role, request.Binary, observation)
	if err != nil {
		return nil, err
	}
	var identity ExecutionToolIdentity
	err = tool.input.WithInput(ctx, request.Role, func(file *os.File) error {
		var err error
		identity, err = verifyExecutionReferenceToolWorkspace(ctx, request, file, observation, filepath.Join(linuxPressureAuthorWork, "derived", "preparation"))
		if err == nil && view.check(ctx) != nil {
			err = ErrExecutionToolCustody
		}
		return err
	})
	return finishLinuxExecutionTool(ctx, tool, request.Role, identity, err)
}
