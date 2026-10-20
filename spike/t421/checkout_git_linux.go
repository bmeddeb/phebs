//go:build linux

package t421

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// linuxCheckoutGit keeps the H2p loan and launching thread in the worker until
// actual Wait and session drain. A caller deadline does not release either.
// It is private to source reconstruction, never an arbitrary public runner.
type linuxCheckoutGit struct {
	mu        sync.Mutex
	git       *LinuxExecutionGitCustody // Borrowed owner, not closed here.
	digest    string
	home, tmp string
	active    *linuxCheckoutGitAttempt
	closing   bool
}

type linuxCheckoutGitAttempt struct {
	done          chan struct{}
	pid           int
	joined, empty bool
	output        []byte
	err           error
}

func newLinuxCheckoutGit(ctx context.Context, git *LinuxExecutionGitCustody, home, temporary string) (*linuxCheckoutGit, error) {
	if ctx == nil || ctx.Err() != nil || git == nil || !linuxGitPrivateDirectory(home) || !linuxGitPrivateDirectory(temporary) {
		return nil, ErrExecutionGitCustody
	}
	identity, err := git.Check(ctx)
	if err != nil || !validExecutionSHA256(identity.SHA256) {
		return nil, ErrExecutionGitCustody
	}
	return &linuxCheckoutGit{git: git, digest: identity.SHA256, home: home, tmp: temporary}, nil
}

func (git *linuxCheckoutGit) checkGit(ctx context.Context, digest string) error {
	if git == nil || ctx == nil || ctx.Err() != nil {
		return ErrExecutionGitCustody
	}
	git.mu.Lock()
	defer git.mu.Unlock()
	if git.closing || git.active != nil && !linuxCheckoutAttemptDone(git.active) || digest != git.digest || git.git == nil {
		return ErrExecutionGitCustody
	}
	identity, err := git.git.Check(ctx)
	if err != nil || identity.SHA256 != digest {
		return ErrExecutionGitCustody
	}
	return nil
}

func linuxCheckoutAttemptDone(attempt *linuxCheckoutGitAttempt) bool {
	select {
	case <-attempt.done:
		return true
	default:
		return false
	}
}

func (git *linuxCheckoutGit) runInput(ctx context.Context, root string, input io.Reader, limit int64, args ...string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || !executionGitAbsolutePath(root) || limit < 0 || limit > maxCheckoutInventoryBytes {
		return nil, ErrExecutionGitCustody
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	git.mu.Lock()
	if git.closing || git.active != nil && !linuxCheckoutAttemptDone(git.active) {
		git.mu.Unlock()
		return nil, ErrExecutionGitCustody
	}
	attempt := &linuxCheckoutGitAttempt{done: make(chan struct{})}
	git.active = attempt
	git.mu.Unlock()
	go git.run(ctx, cancel, attempt, root, input, limit, slices.Clone(args))
	select {
	case <-attempt.done:
		if attempt.err != nil || ctx.Err() != nil {
			return nil, ErrExecutionGitCustody
		}
		return attempt.output, nil
	case <-ctx.Done():
		return nil, ErrExecutionGitCustody // The same worker still owns its loan.
	}
}

func (git *linuxCheckoutGit) run(ctx context.Context, cancel context.CancelFunc, attempt *linuxCheckoutGitAttempt, root string, input io.Reader, limit int64, args []string) {
	defer close(attempt.done)
	err := git.git.WithGit(ctx, git.home, git.tmp, func(identity ExecutionToolIdentity, tree *os.File, environment []string) error {
		if identity.SHA256 != git.digest {
			return ErrExecutionGitCustody
		}
		command := exec.CommandContext(ctx, linuxGitCore+"/git", append([]string{
			"--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false",
			"-c", "core.hooksPath=" + os.DevNull, "-c", "core.commitGraph=false",
		}, args...)...)
		stdout, stderr := checkoutCommandOutput{remaining: limit, cancel: cancel}, checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}
		command.Dir, command.Env, command.ExtraFiles, command.Stdin = root, environment, []*os.File{tree}, input
		command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
		if prepareReferenceCommand(command) != nil {
			return ErrExecutionGitCustody
		}
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if command.Start() != nil {
			return ErrExecutionGitCustody
		}
		attempt.pid = command.Process.Pid
		waited := make(chan error, 1)
		go func() { waited <- command.Wait() }()
		deadline, _ := ctx.Deadline()
		waitErr, joined := waitExecutionProcessRoot(waited, deadline)
		if !joined {
			waitErr = <-waited
		} // Retain the launching thread and Git loan.
		attempt.joined = true
		for t4013.WaitPrivateProcessSession(attempt.pid, time.Now().Add(5*time.Second)) != nil {
			// No post-Wait bare-PID signal: uncertain descendants retain custody.
			time.Sleep(5 * time.Second)
		}
		attempt.empty = true
		if waitErr != nil || ctx.Err() != nil || stdout.err != nil || stderr.err != nil {
			return ErrExecutionGitCustody
		}
		attempt.output = stdout.buffer.Bytes()
		return nil
	})
	if err != nil {
		attempt.err = ErrExecutionGitCustody
	}
}

// close joins the retained preparation worker before its caller may dispose
// source/workspace prefixes. The borrowed Git resource has its own owner.
func (git *linuxCheckoutGit) close(ctx context.Context) error {
	if git == nil {
		return nil
	}
	if ctx == nil {
		return ErrExecutionGitCustody
	}
	git.mu.Lock()
	git.closing = true
	attempt := git.active
	git.mu.Unlock()
	if attempt == nil {
		return nil
	}
	select {
	case <-attempt.done:
		if attempt.pid != 0 && (!attempt.joined || !attempt.empty) {
			return ErrExecutionGitCustody
		}
		git.mu.Lock()
		if git.active == attempt {
			git.active = nil
		}
		git.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ErrExecutionGitCustody
	}
}
