//go:build linux

package t421

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
)

// The complete linked workspace is derived from the genuine closed owner
// graph, never a supplied ancestor. Anonymous payloads are charged separately.
func (held *linuxEpochHeldFlow) openWorkspace() error {
	if held.author == nil || len(held.author.roots) != 4 || held.epochs == nil || len(held.epochs.roots) != 4 {
		return ErrExecutionEpochOne
	}
	base := filepath.Dir(held.author.roots[0].path)
	if base == "/" || filepath.Dir(held.epochs.parent.path) != base || held.epochs.parent.path == held.author.roots[0].path {
		return ErrExecutionEpochOne
	}
	var err error
	held.workspace, err = openLinuxAuthorOwnedRoot(base)
	if err != nil {
		return ErrExecutionEpochOne
	}
	if held.checkWorkspace() != nil || held.checkWorkspaceLayout() != nil {
		_ = held.workspace.file.Close()
		held.workspace.file = nil
		return ErrExecutionEpochOne
	}
	return nil
}
func (held *linuxEpochHeldFlow) checkWorkspace() error {
	if held.workspace.file == nil || held.workspace.check() != nil || len(held.author.roots) != 4 || len(held.epochs.roots) != 4 || held.author.roots[0].check() != nil || held.epochs.parent.check() != nil || held.epochs.stage.check() != nil || held.builds.checkStage() != nil || held.git.checkStage() != nil {
		return ErrExecutionEpochOne
	}
	base, parent := held.workspace.path, held.author.roots[0].path
	if filepath.Dir(parent) != base || filepath.Dir(held.epochs.parent.path) != base || parent == held.epochs.parent.path || filepath.Dir(held.epochs.stage.path) != held.epochs.parent.path || filepath.Dir(held.builds.directory) != parent || filepath.Dir(held.git.directory) != parent {
		return ErrExecutionEpochOne
	}
	for _, root := range held.author.roots[1:] {
		if filepath.Dir(root.path) != parent || root.identity.Device != held.workspace.identity.Device || root.identity.Mount != held.workspace.identity.Mount || root.identity.FSID != held.workspace.identity.FSID {
			return ErrExecutionEpochOne
		}
	}
	for _, root := range held.epochs.roots {
		if filepath.Dir(root.path) != held.epochs.stage.path || root.identity.Device != held.workspace.identity.Device || root.identity.Mount != held.workspace.identity.Mount || root.identity.FSID != held.workspace.identity.FSID {
			return ErrExecutionEpochOne
		}
	}
	for _, tree := range []*LinuxExecutionInputTree{held.builds.tree, held.git.tree, held.epochs.catalogs, held.epochs.configs} {
		if tree == nil {
			return ErrExecutionEpochOne
		}
		relative, err := filepath.Rel(base, tree.stagePath)
		if err != nil || relative == "." || !filepath.IsLocal(relative) || tree.checkStage() != nil {
			return ErrExecutionEpochOne
		}
	}
	return nil
}

func (held *linuxEpochHeldFlow) checkWorkspaceLayout() error {
	if workspaceEntries(held.workspace, map[string]os.FileInfo{filepath.Base(held.author.roots[0].path): held.author.roots[0].info, filepath.Base(held.epochs.parent.path): held.epochs.parent.info}) != nil {
		return ErrExecutionEpochOne
	}
	expected := map[string]os.FileInfo{linuxAuthorLeaseName: held.author.leaseInfo, filepath.Base(held.builds.directory): held.builds.stageInfo, filepath.Base(held.git.directory): held.git.stageInfo}
	for _, root := range held.author.roots[1:] {
		expected[filepath.Base(root.path)] = root.info
	}
	if len(expected) != 6 || workspaceEntries(held.author.roots[0], expected) != nil || workspaceEntries(held.epochs.parent, map[string]os.FileInfo{filepath.Base(held.epochs.stage.path): held.epochs.stage.info}) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// Three initial bounded top-level inventories refuse an unowned sibling or
// failed prefix; operational work creates entries only inside the selected roots.
func workspaceEntries(root linuxAuthorRoot, expected map[string]os.FileInfo) (retErr error) {
	if root.check() != nil || len(expected) > 8 {
		return ErrExecutionEpochOne
	}
	fd, err := unix.Openat(int(root.file.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrExecutionEpochOne
	}
	directory := os.NewFile(uintptr(fd), "epoch-workspace-census")
	defer func() {
		if directory.Close() != nil {
			retErr = ErrExecutionEpochOne
		}
	}()
	names, err := directory.Readdirnames(len(expected) + 1)
	if err != nil && err != io.EOF || len(names) != len(expected) {
		return ErrExecutionEpochOne
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		wanted := expected[name]
		if wanted == nil || seen[name] || name == "." || filepath.Base(name) != name {
			return ErrExecutionEpochOne
		}
		seen[name] = true
		fd, err := unix.Openat(int(directory.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return ErrExecutionEpochOne
		}
		file := os.NewFile(uintptr(fd), "epoch-workspace-entry")
		actual, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil || closeErr != nil || actual.Mode()&os.ModeSymlink != 0 || !os.SameFile(wanted, actual) {
			return ErrExecutionEpochOne
		}
	}
	return root.check()
}
