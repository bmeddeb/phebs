package provider

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const gazelleCompilerCache = "/scratch/bazel-output/external/gazelle++non_module_deps+bazel_gazelle_go_repository_cache/gocache"

// QuiescenceError retains only a best-effort diagnostic for the process that
// caused the existing refusal. A missing diagnostic never changes that refusal.
type QuiescenceError struct {
	Process *ProcessDiagnostic
}

func (*QuiescenceError) Error() string { return "tool process remains before cache eviction" }

// Cache eviction runs only after every tool process has exited. Threads share
// their process's directory; PID1 and this worker are the only allowed live
// leaders. A validated zombie has no descriptors or cache-writing capability.
func ensureQuiescentWorker() error {
	return ensureQuiescentWorkerAt("/proc", os.Getpid())
}

func ensureQuiescentWorkerAt(procPath string, workerPID int) error {
	proc, err := os.Open(procPath)
	if err != nil {
		return err
	}
	defer func() { _ = proc.Close() }()
	names, err := proc.Readdirnames(256 + 129)
	if err != nil && !errors.Is(err, io.EOF) || len(names) > 256+128 {
		return errors.New("process census refused before cache eviction")
	}
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err == nil && pid != 1 && pid != workerPID {
			var diagnostic *ProcessDiagnostic
			if pid > 0 && uint64(pid) <= 1<<32-1 && strconv.Itoa(pid) == name {
				root := os.DirFS(procPath)
				start, state, readErr := ReadProcessState(root, uint32(pid))
				if ProcessGone(readErr) || readErr == nil && state == 'Z' {
					continue
				}
				diagnostic = ReadProcessDiagnostic(root, uint32(pid), start)
			}
			return &QuiescenceError{Process: diagnostic}
		}
	}
	return nil
}

// CacheEviction records the bounded private compiler-cache reclamation.
type CacheEviction struct {
	Files             int    `json:"files"`
	LogicalBytes      int64  `json:"logical_bytes"`
	ScratchBytesFreed uint64 `json:"scratch_bytes_freed"`
}

// This fixed Go build cache is not a repository source or Bazel action output.
// Go 1.25 uses hash-prefix directories and nested cached-executable directories.
// Inventory completes before mutation; unfamiliar structure refuses.
func evictCompilerCache(name string) (CacheEviction, error) {
	var result CacheEviction
	canonical, err := filepath.EvalSymlinks(name)
	if errors.Is(err, os.ErrNotExist) && filepath.IsAbs(name) && filepath.Clean(name) == name {
		// A graph without go_repository may never create this private cache.
		// Validate the nearest existing ancestor before accepting absence;
		// dangling links and aliases remain refusals, including below a link.
		for ancestor := name; ; ancestor = filepath.Dir(ancestor) {
			info, statErr := os.Lstat(ancestor)
			if errors.Is(statErr, os.ErrNotExist) && ancestor != "/" {
				continue
			}
			resolved, resolveErr := filepath.EvalSymlinks(ancestor)
			if statErr != nil || !info.IsDir() || resolveErr != nil || resolved != ancestor {
				return result, errors.New("compiler cache path is not canonical")
			}
			return result, nil
		}
	}
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
				if entries > 50000 || !safeRelative(entry.Name()) {
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
