package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

type outputBudget struct {
	mu        sync.Mutex
	remaining int
	cancel    context.CancelFunc
	exceeded  bool
}
type outputWriter struct {
	budget *outputBudget
	data   []byte
}

func (w *outputWriter) Write(b []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	if len(b) > w.budget.remaining {
		w.budget.exceeded = true
		w.budget.cancel()
		return 0, typedindex.Capacity
	}
	w.budget.remaining -= len(b)
	w.data = append(w.data, b...)
	return len(b), nil
}
func runCommand(ctx context.Context, executable string, args, env []string, budget *outputBudget) ([]byte, []byte, error) {
	return runCommandAt(ctx, executable, args, env, budget, launcher.Workspace)
}
func runCommandAt(ctx context.Context, executable string, args, env []string, budget *outputBudget, directory string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, launcher.MaxWall)
	defer cancel()
	budget.cancel = cancel
	c := exec.CommandContext(ctx, executable, args...)
	c.Dir = directory
	c.Env = env
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = time.Second
	stdout, stderr := outputWriter{budget: budget}, outputWriter{budget: budget}
	c.Stdout = &stdout
	c.Stderr = &stderr
	e := c.Run()
	if budget.exceeded {
		e = typedindex.Capacity
	} else if ctx.Err() != nil {
		e = ctx.Err()
	}
	return stdout.data, stderr.data, e
}
func runClient(ctx context.Context, executable string, args, env []string) ([]byte, []byte, error) {
	return runCommand(ctx, executable, args, env, &outputBudget{remaining: maxClientBytes})
}

// Production always uses the fixed native commands. The argument is internal
// only so neutral tests can supply exact synthetic command outputs without tools.
func buildPlan(ctx context.Context, roots []string, command func(context.Context, string, []string, []string, *outputBudget) ([]byte, []byte, error), quiesce func() error, evict func(string) (CacheEviction, error), read func(string, int64) ([]byte, error)) (planner.Plan, error) {
	commands, e := planner.NativeCommands(roots)
	if e != nil {
		return planner.Plan{}, e
	}
	budget := outputBudget{remaining: 16 << 20}
	outputs := make([][]byte, 0, 3)
	for index, suffix := range commands {
		if e = ctx.Err(); e != nil {
			return planner.Plan{}, e
		}
		args := append(append(append([]string{}, launcher.BazelStartup()...), suffix...), launcher.NativeBazelCommon()...)
		stdout, _, err := command(ctx, "/inputs/tools/bin/bazel", args, launcher.BazelEnvironment(), &budget)
		if err != nil {
			return planner.Plan{}, planningFailure(err)
		}
		outputs = append(outputs, stdout)
		if index == 1 {
			if err = quiesce(); err != nil {
				return planner.Plan{}, err
			}
			if _, err = evict(gazelleCompilerCache); err != nil {
				return planner.Plan{}, err
			}
		}
	}
	paths, e := planner.ProjectionPaths(outputs[1])
	if e != nil {
		return planner.Plan{}, e
	}
	projections := make(map[string][]byte, len(paths))
	var total int
	for _, name := range paths {
		if e = ctx.Err(); e != nil {
			return planner.Plan{}, e
		}
		if !safeRelative(name) {
			return planner.Plan{}, typedindex.Invalid
		}
		b, err := read(name, int64(planner.MaxProjectionBytes))
		if err != nil {
			return planner.Plan{}, err
		}
		if len(b) > planner.MaxProtoBytes-total {
			return planner.Plan{}, typedindex.Capacity
		}
		total += len(b)
		projections[name] = b
	}
	return planner.AssembleRootsV2(outputs[0], outputs[1], projections, roots)
}
func nativePlan(ctx context.Context, roots []string) (planner.Plan, error) {
	var root *os.Root
	defer func() {
		if root != nil {
			_ = root.Close()
		}
	}()
	return buildPlan(ctx, roots, runCommand, ensureQuiescentWorker, evictCompilerCache, func(name string, limit int64) ([]byte, error) {
		if root == nil {
			var err error
			root, err = os.OpenRoot(launcher.ExecRoot)
			if err != nil {
				return nil, err
			}
		}
		st, err := root.Lstat(name)
		if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
			return nil, typedindex.Invalid
		}
		f, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(f, limit+1))
		err = errors.Join(err, f.Close())
		if err != nil || int64(len(b)) > limit {
			return nil, typedindex.Invalid
		}
		return b, nil
	})
}
