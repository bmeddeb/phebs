//go:build linux

package t4013

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const maxLinuxProcessPathBytes = 4096

func processExecutablePaths(pids []int) ([]string, error) {
	paths := make([]string, 0, len(pids))
	for _, pid := range pids {
		before, err := linuxProcessStatAt("/proc", pid)
		if err != nil {
			return nil, err
		}
		buffer := make([]byte, maxLinuxProcessPathBytes+1)
		size, err := unix.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"), buffer)
		if err != nil {
			return nil, fmt.Errorf("read Linux process executable: %w", err)
		}
		path, err := validateLinuxProcessExecutablePath(string(buffer[:size]))
		if err != nil {
			return nil, err
		}
		after, err := linuxProcessStatAt("/proc", pid)
		if err != nil {
			return nil, err
		}
		if before.snapshot != after.snapshot {
			return nil, errors.New("linux process changed during executable observation")
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func validateLinuxProcessExecutablePath(path string) (string, error) {
	if len(path) == 0 || len(path) > maxLinuxProcessPathBytes || strings.HasSuffix(path, " (deleted)") ||
		!utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("linux process executable path is invalid or deleted")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return "", errors.Join(err, errors.New("linux process executable path is not canonical"))
	}
	return path, nil
}
