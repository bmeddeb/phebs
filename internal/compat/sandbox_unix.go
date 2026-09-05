//go:build darwin || linux

package compat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const (
	maxOutputBytes = 4 << 20
	maxMemoryBytes = 512 << 20
)

type commandOutput struct {
	stdout   string
	stderr   string
	exitCode int
	overflow bool
}

type limitedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		keep := len(value)
		if keep > remaining {
			keep = remaining
		}
		_, _ = b.buffer.Write(value[:keep])
	}
	if len(value) > remaining {
		b.overflow = true
	}
	return len(value), nil
}

func (b *limitedBuffer) result() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String(), b.overflow
}

func sandboxSupported() error {
	var required []string
	if runtime.GOOS == "darwin" {
		required = []string{"/usr/bin/sandbox-exec", "/bin/sh"}
	} else {
		required = []string{"/bin/sh"}
		if _, err := exec.LookPath("bwrap"); err != nil {
			return fmt.Errorf("%w: Linux requires bubblewrap (bwrap)", ErrUnavailable)
		}
	}
	for _, name := range required {
		info, err := os.Stat(name)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("%w: required sandbox executable %s is unavailable", ErrUnavailable, name)
		}
	}
	return nil
}

func runSandboxed(ctx context.Context, root, bin string, arguments []string) (commandOutput, error) {
	command, err := sandboxCommand(root, bin, arguments)
	if err != nil {
		return commandOutput{}, err
	}
	command.Env = []string{
		"HOME=../home", "BUF_CACHE_DIR=../cache", "XDG_CACHE_HOME=../cache", "TMPDIR=../tmp",
		"LANG=C", "LC_ALL=C", "NO_COLOR=1", "PATH=/usr/bin:/bin",
	}
	command.Dir = root + "/exec"
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout := &limitedBuffer{limit: maxOutputBytes}
	stderr := &limitedBuffer{limit: maxOutputBytes}
	command.Stdout, command.Stderr = stdout, stderr
	handle, err := dispatchadmission.StartProduction(ctx, dispatchadmission.SiteCompatibilitySandbox, command)
	if err != nil {
		return commandOutput{}, fmt.Errorf("%w: start sandboxed buf: %v", ErrUnavailable, err)
	}

	done := make(chan error, 1)
	go func() { done <- handle.Wait() }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	memoryProbeFailures := 0
	var waitErr error
	for waitErr == nil {
		select {
		case waitErr = <-done:
			goto finished
		case <-ctx.Done():
			killProcessGroup(command.Process.Pid)
			<-done
			return commandOutput{}, fmt.Errorf("%w: wall-time limit: %v", ErrLimit, ctx.Err())
		case <-ticker.C:
			if runtime.GOOS != "darwin" {
				continue
			}
			resident, probeErr := processGroupRSS(command.Process.Pid)
			if probeErr != nil {
				memoryProbeFailures++
				if memoryProbeFailures >= 3 {
					killProcessGroup(command.Process.Pid)
					<-done
					return commandOutput{}, fmt.Errorf("%w: memory monitor failed closed", ErrUnavailable)
				}
				continue
			}
			memoryProbeFailures = 0
			if resident > maxMemoryBytes {
				killProcessGroup(command.Process.Pid)
				<-done
				return commandOutput{}, fmt.Errorf("%w: memory exceeds %d bytes", ErrLimit, maxMemoryBytes)
			}
		}
	}

finished:
	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			return commandOutput{}, fmt.Errorf("%w: wait for sandboxed buf: %v", ErrCheckFailed, waitErr)
		}
		exitCode = exitError.ExitCode()
	}
	stdoutText, stdoutOverflow := stdout.result()
	stderrText, stderrOverflow := stderr.result()
	return commandOutput{
		stdout: stdoutText, stderr: stderrText, exitCode: exitCode,
		overflow: stdoutOverflow || stderrOverflow,
	}, nil
}

func sandboxCommand(root, bin string, arguments []string) (*exec.Cmd, error) {
	if runtime.GOOS == "darwin" {
		// Buf is trusted and receives only paths in root. Seatbelt blocks every
		// network operation and every write outside that tree; a host-wide read
		// deny is intentionally avoided because the signed Go runtime needs
		// macOS system data before main starts.
		escaped := strings.ReplaceAll(root, `"`, `\"`)
		profile := `(version 1)(allow default)(deny network*)(deny file-write*)(allow file-write* (subpath "` + escaped + `"))`
		args := []string{"-p", profile, "/bin/sh", "-c", `ulimit -t 10 || exit 125; exec "$@"`, "phebs-buf", bin}
		args = append(args, arguments...)
		return exec.Command("/usr/bin/sandbox-exec", args...), nil
	}

	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("%w: Linux requires bubblewrap (bwrap)", ErrUnavailable)
	}
	// The child sees only its static Buf executable and the sanitized tree.
	// User, PID, IPC, UTS, cgroup, and network namespaces are isolated; the
	// shell applies hard CPU and virtual-memory rlimits before bwrap starts.
	bwrapArgs := []string{
		"--unshare-all", "--die-with-parent", "--new-session",
		"--ro-bind", bin, "/phebs-buf", "--bind", root, "/work",
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--chdir", "/work/exec",
		"--clearenv", "--setenv", "HOME", "/work/home", "--setenv", "BUF_CACHE_DIR", "/work/cache",
		"--setenv", "XDG_CACHE_HOME", "/work/cache", "--setenv", "TMPDIR", "/work/tmp",
		"--setenv", "LANG", "C", "--setenv", "LC_ALL", "C", "--setenv", "NO_COLOR", "1",
		"/phebs-buf",
	}
	bwrapArgs = append(bwrapArgs, arguments...)
	args := []string{"-c", `ulimit -t 10 || exit 125; ulimit -v 524288 || exit 125; exec "$@"`, "phebs-buf", bwrap}
	args = append(args, bwrapArgs...)
	return exec.Command("/bin/sh", args...), nil
}

func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
