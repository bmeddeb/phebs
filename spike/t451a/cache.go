package t451a

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
	"golang.org/x/sys/unix"
)

const gazelleCompilerCache = "/scratch/bazel-output/external/gazelle++non_module_deps+bazel_gazelle_go_repository_cache/gocache"

// Cache eviction runs only after every tool process has exited. Threads share
// their process's directory; PID1 and this worker are the only allowed leaders.
func ensureQuiescentWorker() error {
	proc, err := os.Open("/proc")
	if err != nil {
		return err
	}
	defer func() { _ = proc.Close() }()
	names, err := proc.Readdirnames(sandbox.TaskLimit + 129)
	if err != nil && !errors.Is(err, io.EOF) || len(names) > sandbox.TaskLimit+128 {
		return errors.New("process census refused before cache eviction")
	}
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err == nil && pid != 1 && pid != os.Getpid() {
			return errors.New("tool process remains before cache eviction")
		}
	}
	return nil
}

type cacheEviction struct {
	Files             int    `json:"files"`
	LogicalBytes      int64  `json:"logical_bytes"`
	ScratchBytesFreed uint64 `json:"scratch_bytes_freed"`
}

// This fixed Go build cache is not a repository source or Bazel action output.
// Go 1.25 uses hash-prefix directories and nested cached-executable directories.
// Inventory completes before mutation; unfamiliar structure refuses.
func evictCompilerCache(name string) (cacheEviction, error) {
	var result cacheEviction
	canonical, err := filepath.EvalSymlinks(name)
	if err != nil || canonical != name {
		return result, errors.New("compiler cache path is not canonical")
	}
	parent, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return result, err
	}
	defer func() { _ = parent.Close() }()
	base := filepath.Base(name)
	info, err := parent.Lstat(base)
	if err != nil || !info.IsDir() {
		return result, errors.New("compiler cache directory refused")
	}
	root, err := parent.OpenRoot(base)
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	pending := []string{"."}
	entries := 0
	for len(pending) != 0 {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		file, err := root.Open(dir)
		if err != nil {
			return result, err
		}
		for {
			batch, readErr := file.ReadDir(128)
			for _, entry := range batch {
				entries++
				if entries > 50000 || !validPath(entry.Name()) {
					_ = file.Close()
					return result, errors.New("compiler cache entry bound")
				}
				info, err := entry.Info()
				if err != nil {
					_ = file.Close()
					return result, err
				}
				if info.IsDir() && (dir == "." || !strings.Contains(dir, "/")) && len(pending) < 4096 {
					pending = append(pending, filepath.Join(dir, entry.Name()))
				} else if info.Mode().IsRegular() && info.Size() >= 0 {
					if info.Size() > 1<<30-result.LogicalBytes {
						_ = file.Close()
						return result, errors.New("compiler cache byte bound")
					}
					result.Files++
					result.LogicalBytes += info.Size()
				} else {
					_ = file.Close()
					return result, errors.New("compiler cache entry type refused")
				}
			}
			if readErr != nil {
				if err := file.Close(); err != nil || !errors.Is(readErr, io.EOF) {
					return result, errors.Join(err, readErr)
				}
				break
			}
		}
	}
	var before, after unix.Statfs_t
	if err := unix.Statfs(name, &before); err != nil {
		return result, err
	}
	if err := parent.RemoveAll(base); err != nil {
		return result, err
	}
	if err := parent.Mkdir(base, 0700); err != nil {
		return result, err
	}
	if err := unix.Statfs(name, &after); err != nil {
		return result, err
	}
	if before.Bsize != after.Bsize || before.Bsize <= 0 || after.Bfree < before.Bfree {
		return result, errors.New("compiler cache reclamation observation refused")
	}
	result.ScratchBytesFreed = (after.Bfree - before.Bfree) * uint64(before.Bsize)
	return result, nil
}
