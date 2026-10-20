//go:build linux

package t421

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func goReferenceMaskPaths(plan linuxGoReferencePlan, stage, workspace string) ([]string, error) {
	paths := []string{plan.OriginalSDK, plan.OriginalModules, plan.OriginalSource}
	for i, path := range paths {
		if !executionGitAbsolutePath(path) || strings.ContainsAny(path, " \t\\") {
			return nil, ErrExecutionGoBuildCustody
		}
		for _, excluded := range []string{stage, workspace, "/proc", "/dev", "/sys"} {
			if inputTreePathsOverlap(path, excluded) {
				return nil, ErrExecutionGoBuildCustody
			}
		}
		for _, earlier := range paths[:i] {
			if inputTreePathsOverlap(path, earlier) {
				return nil, ErrExecutionGoBuildCustody
			}
		}
	}
	return paths, nil
}

func maskGoReferenceOriginals(plan linuxGoReferencePlan, workspace string) error {
	stage, err := os.Getwd()
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	paths, err := goReferenceMaskPaths(plan, stage, workspace)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if maskGoReferenceDirectory(path) != nil {
			return ErrExecutionGoBuildCustody
		}
	}
	return nil
}

func maskGoReferenceDirectory(path string) (retErr error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return ErrExecutionGoBuildCustody
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	defer func() {
		if unix.Close(fd) != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	var before, linked unix.Stat_t
	priorMount, mountErr := inputTreeMountID(fd)
	if mountErr != nil || unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFDIR ||
		unix.Lstat(path, &linked) != nil || !sameLinuxInputStat(before, linked) ||
		unix.Mount("tmpfs", path, "tmpfs", unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size=4096,nr_inodes=1,mode=000") != nil {
		return ErrExecutionGoBuildCustody
	}
	view, err := os.Open(path)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	defer func() {
		if view.Close() != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	var actual unix.Stat_t
	var filesystem unix.Statfs_t
	actualMount, mountErr := inputTreeMountID(int(view.Fd()))
	rows, readErr := view.ReadDir(1)
	const flags = unix.ST_RDONLY | unix.ST_NOSUID | unix.ST_NODEV | unix.ST_NOEXEC
	if mountErr != nil || actualMount == priorMount || unix.Fstat(int(view.Fd()), &actual) != nil || actual.Dev == before.Dev ||
		actual.Mode != unix.S_IFDIR || unix.Fstatfs(int(view.Fd()), &filesystem) != nil || filesystem.Type != unix.TMPFS_MAGIC ||
		filesystem.Flags&flags != flags || len(rows) != 0 || readErr != io.EOF {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

func goReferenceInputShape(census inputTreeCensus) (string, bool) {
	required := map[string]uint32{
		"inputs": 0o700, "inputs/sdk": 0o700, "inputs/sdk/src": 0o700, "inputs/sdk/pkg": 0o700, "inputs/sdk/lib": 0o700,
		"inputs/sdk/bin/go": 0o500, "inputs/sdk/go.env": 0o400, "inputs/sdk/VERSION": 0o400,
		"inputs/modules": 0o700, "inputs/source": 0o700, "inputs/source/go.mod": 0o400, "inputs/source/go.sum": 0o400,
		"inputs/source/.git": 0o700, "inputs/source/.git/HEAD": 0o400, "inputs/source/.git/config": 0o400,
		"inputs/source/.git/index": 0o400, "inputs/source/.git/shallow": 0o400, "inputs/git": 0o700, "inputs/git/core": 0o700,
	}
	for _, name := range []string{"asm", "cgo", "compile", "cover", "fix", "link", "preprofile", "vet"} {
		required["inputs/sdk/pkg/tool/linux_amd64/"+name] = 0o500
	}
	aliases := make(map[string]bool)
	for _, name := range executionGitImageNames() {
		aliases["inputs/git/core/"+name] = false
	}
	var digest [sha256.Size]byte
	count := 0
	for _, row := range census.rows {
		if mode, exists := required[row.name]; exists {
			if mode == 0 || row.mode != mode {
				return "", false
			}
			required[row.name] = 0
			continue
		}
		if seen, exists := aliases[row.name]; exists {
			if seen || row.mode != 0o500 || count != 0 && row.digest != digest {
				return "", false
			}
			aliases[row.name], digest = true, row.digest
			count++
			continue
		}
		if strings.HasPrefix(row.name, "inputs/source/.git/") {
			if !goReferenceGitMetadata(row) {
				return "", false
			}
			continue
		}
		if !strings.HasPrefix(row.name, "inputs/sdk/") && !strings.HasPrefix(row.name, "inputs/modules/") && !strings.HasPrefix(row.name, "inputs/source/") {
			return "", false
		}
	}
	for _, mode := range required {
		if mode != 0 {
			return "", false
		}
	}
	for _, seen := range aliases {
		if !seen {
			return "", false
		}
	}
	return "sha256:" + hex.EncodeToString(digest[:]), true
}

func goReferenceGitMetadata(row inputTreeEntry) bool {
	name, ok := strings.CutPrefix(row.name, "inputs/source/.git/")
	if !ok {
		return false
	}
	if row.mode == 0o700 {
		return neutralGoBuildGitEntry(row)
	}
	if row.mode != 0o400 {
		return false
	}
	return name == "logs/HEAD" || strings.HasPrefix(name, "objects/") && neutralGoBuildGitEntry(row)
}
