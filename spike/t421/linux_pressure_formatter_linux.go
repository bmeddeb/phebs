//go:build linux

package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const linuxPressureFormatterPath = "/usr/sbin/mkfs.ext4"

// A distinct direct-image recipe. The expected digest is supplied by future
// operator admission; observing whatever occupies the fixed path is not enough.
// This is not loader/shared-library/helper closure, vendor attestation, protected
// input custody or a complete execution-tool/plan admission.
type linuxPressureFormatter struct {
	file     *os.File
	info     os.FileInfo
	resolved string
	expected string
	paths    []linuxPressureFormatterComponent
}

type linuxPressureFormatterComponent struct {
	path   string
	info   os.FileInfo
	target string
}

// Follow only the fixed recipe path, recording its complete bounded symlink
// and directory chain. Distro mkfs.ext4 -> mke2fs links are admitted; arbitrary
// caller-selected executable paths and group/world-writable components are not.
func linuxPressureFormatterChain(ctx context.Context) (string, []linuxPressureFormatterComponent, error) {
	pending := strings.Split(strings.TrimPrefix(linuxPressureFormatterPath, "/"), "/")
	current := "/"
	var paths []linuxPressureFormatterComponent
	links := 0
	for len(pending) > 0 {
		if linuxPressureContext(ctx) != nil || len(paths) >= 128 {
			return "", nil, ErrLinuxPressure
		}
		current = filepath.Join(current, pending[0])
		pending = pending[1:]
		if len(current) > 1023 {
			return "", nil, ErrLinuxPressure
		}
		info, err := os.Lstat(current)
		if err != nil {
			return "", nil, ErrLinuxPressure
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || stat.Gid != 0 ||
			info.Mode()&os.ModeSymlink == 0 && stat.Mode&(unix.S_ISUID|unix.S_ISGID|0o022) != 0 {
			return "", nil, ErrLinuxPressure
		}
		entry := linuxPressureFormatterComponent{path: current, info: info}
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 16 {
				return "", nil, ErrLinuxPressure
			}
			entry.target, err = os.Readlink(current)
			if err != nil || len(entry.target) == 0 || len(entry.target) > 1023 {
				return "", nil, ErrLinuxPressure
			}
			resolved := entry.target
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(current), resolved)
			}
			resolved = filepath.Clean(resolved)
			pending = append(strings.Split(strings.TrimPrefix(resolved, "/"), "/"), pending...)
			current = "/"
		} else if len(pending) > 0 && !info.IsDir() || len(pending) == 0 && !info.Mode().IsRegular() {
			return "", nil, ErrLinuxPressure
		}
		paths = append(paths, entry)
	}
	if current == "/" {
		return "", nil, ErrLinuxPressure
	}
	return current, paths, nil
}

func holdLinuxPressureFormatterObserved(ctx context.Context, expected string, observation *executionPreworkObservation) (_ *linuxPressureFormatter, retErr error) {
	if ctx == nil || ctx.Err() != nil || runtime.GOARCH != "amd64" || !validExecutionSHA256(expected) {
		return nil, ErrLinuxPressure
	}
	resolved, paths, err := linuxPressureFormatterChain(ctx)
	if err != nil || !filepath.IsAbs(resolved) || filepath.Clean(resolved) != resolved || len(resolved) > 1023 {
		return nil, ErrLinuxPressure
	}
	fd, err := unix.Open(resolved, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrLinuxPressure
	}
	file := os.NewFile(uintptr(fd), "linux-pressure-formatter")
	defer func() {
		if retErr != nil {
			_ = observation.close(file.Close())
		}
	}()
	info, err := file.Stat()
	var stat unix.Stat_t
	if err != nil || unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != 0 || stat.Gid != 0 ||
		stat.Mode&(unix.S_ISUID|unix.S_ISGID|0o022) != 0 || stat.Mode&0o111 == 0 || stat.Nlink != 1 ||
		stat.Size <= 0 || stat.Size > 32<<20 || !linuxInputELF(file, stat.Size) {
		return nil, ErrLinuxPressure
	}
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	var read int64
	for read < stat.Size {
		if ctx.Err() != nil {
			return nil, ErrLinuxPressure
		}
		n, readErr := file.Read(buffer[:min(int64(len(buffer)), stat.Size-read)])
		if n <= 0 || readErr != nil && readErr != io.EOF {
			return nil, ErrLinuxPressure
		}
		_, _ = hash.Write(buffer[:n])
		read += int64(n)
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expected {
		return nil, ErrLinuxPressure
	}
	tool := &linuxPressureFormatter{file: file, info: info, resolved: resolved, expected: expected, paths: paths}
	if tool.check(ctx) != nil {
		return nil, ErrLinuxPressure
	}
	return tool, nil
}

func (tool *linuxPressureFormatter) check(ctx context.Context) error {
	if tool == nil || ctx == nil || ctx.Err() != nil || tool.file == nil || !validExecutionSHA256(tool.expected) {
		return ErrLinuxPressure
	}
	held, err := tool.file.Stat()
	current, pathErr := os.Lstat(tool.resolved)
	resolved, resolveErr := filepath.EvalSymlinks(linuxPressureFormatterPath)
	if err != nil || pathErr != nil || resolveErr != nil || resolved != tool.resolved ||
		!inputCustodySame(tool.info, held) || !inputCustodySame(held, current) {
		return ErrLinuxPressure
	}
	if len(tool.paths) == 0 || len(tool.paths) > 128 {
		return ErrLinuxPressure
	}
	for _, path := range tool.paths {
		current, err := os.Lstat(path.path)
		if err != nil || ctx.Err() != nil || !inputCustodySame(path.info, current) {
			return ErrLinuxPressure
		}
		if path.target != "" {
			target, err := os.Readlink(path.path)
			if err != nil || target != path.target {
				return ErrLinuxPressure
			}
		}
	}
	return nil
}

func linuxPressureFormatterArguments() []string {
	// Never discard/punch the complete backing reservation, and leave no lazy
	// initialization worker beyond the one joined formatter invocation.
	// The empty formatter configuration needs explicit extents for fallocate.
	return []string{"-q", "-F", "-t", "ext4", "-b", "4096", "-m", "0", "-O", "extent", "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0", "/proc/self/fd/4"}
}

type linuxPressureOutput struct {
	mu     sync.Mutex
	bytes  int
	cancel context.CancelFunc
	failed bool
}

func (output *linuxPressureOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.failed || len(value) > (64<<10)-output.bytes {
		output.failed = true
		output.cancel()
		return 0, ErrLinuxPressure
	}
	output.bytes += len(value)
	return len(value), nil
}

// Uses a real private session and the caller's actual native Wait. This proves
// only this one formatter's joined/session-empty prefix, never callback users,
// every possible escaped descendant or a complete pressure-phase drain.
func (owner *LinuxPressureVolume) format(ctx context.Context) error {
	if owner.formatter == nil || owner.formatter.check(ctx) != nil || owner.checkImage(ctx) != nil {
		return ErrLinuxPressure
	}
	operation, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(operation, "/proc/self/fd/3", linuxPressureFormatterArguments()...)
	command.Args[0] = linuxPressureFormatterPath
	command.Dir = owner.root.path
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TZ=UTC", "HOME=/nonexistent", "MKE2FS_CONFIG=/dev/null"}
	command.ExtraFiles = []*os.File{owner.formatter.file, owner.image}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
	command.WaitDelay = time.Second
	output := &linuxPressureOutput{cancel: cancel}
	command.Stdout, command.Stderr = output, output
	// Pdeathsig belongs to the creating thread, not the abstract Go process.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if owner.formatter.check(operation) != nil || owner.checkImage(operation) != nil {
		return ErrLinuxPressure
	}
	if err := command.Start(); err != nil {
		return errors.Join(ErrLinuxPressure, err)
	}
	owner.formatterSession = command.Process.Pid
	if owner.observation != nil {
		owner.formatterCommand = command
	}
	waitErr := command.Wait()
	deadline, _ := operation.Deadline()
	joined, empty, finishErr := finishExecutionProcessSession(command.Process.Pid, nil, true, waitErr, deadline)
	owner.formatterJoined, owner.formatterEmpty = joined, empty
	owner.observation.process(command, joined, empty)
	output.mu.Lock()
	failed := output.failed
	output.mu.Unlock()
	if finishErr != nil || !joined || !empty || failed || operation.Err() != nil || owner.formatter.check(ctx) != nil || owner.checkImage(ctx) != nil {
		return errors.Join(ErrLinuxPressure, finishErr)
	}
	return nil
}
