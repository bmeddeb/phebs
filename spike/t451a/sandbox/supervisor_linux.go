//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Supervisor must be dispatched only for the fixed __supervisor command. Its
// caller must immediately os.Exit with the returned code: PID 1 exit is the
// kernel-owned descendant shutdown boundary, including escaped sessions.
func Supervisor() int {
	if os.Getpid() != 1 || os.Getuid() != 0 || os.Getgid() != 0 || validateProcess(false) != nil ||
		unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) != nil {
		return 125
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	timer := time.NewTimer(WallLimit)
	defer timer.Stop()
	finished := make(chan struct{})
	defer close(finished)
	// This watchdog never waits on a child, a pipe, a filesystem walk or the
	// host controller. Non-root children cannot stop or signal their root PID 1.
	go func() {
		select {
		case <-timer.C:
			os.Exit(124)
		case <-signals:
			os.Exit(125)
		case <-finished:
		}
	}()
	report := supervise(ctx, cancel)
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return 125
	}
	return report.ExitCode
}

// ValidateWorker checks __worker before it reads its request or executes a tool.
// Action helpers inherit containment but use Bazel's action environment.
func ValidateWorker() error {
	if os.Getpid() == 1 || os.Getuid() != 65534 || os.Geteuid() != 65534 || os.Getgid() != 65534 || os.Getegid() != 65534 {
		return ErrRefused
	}
	return validateProcess(true)
}

func validateProcess(worker bool) error {
	want, actual := environment(), os.Environ()
	slices.Sort(want)
	slices.Sort(actual)
	if !slices.Equal(want, actual) {
		return ErrRefused
	}
	status, err := readSmall("/proc/self/status", 8192)
	if err != nil {
		return ErrRefused
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(status), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	capability := uint64(0xc0)
	if worker {
		capability = 0
	}
	for _, key := range []string{"CapEff", "CapPrm"} {
		value, err := strconv.ParseUint(values[key], 16, 64)
		if err != nil || value != capability {
			return ErrRefused
		}
	}
	for _, key := range []string{"CapInh", "CapAmb"} {
		value, err := strconv.ParseUint(values[key], 16, 64)
		if err != nil || value != 0 {
			return ErrRefused
		}
	}
	if values["NoNewPrivs"] != "1" || values["Seccomp"] != "2" {
		return ErrRefused
	}
	var limit unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &limit) != nil || limit.Cur != DescriptorLimit || limit.Max != DescriptorLimit {
		return ErrRefused
	}
	if unix.Getrlimit(unix.RLIMIT_CORE, &limit) != nil || limit.Cur != 0 || limit.Max != 0 {
		return ErrRefused
	}
	return nil
}

func supervise(ctx context.Context, cancel context.CancelFunc) supervisorReport {
	report := supervisorReport{Schema: "t451a-supervisor-v1", ExitCode: 125, StopReason: "kernel_limits"}
	if verifyKernelLimits() != nil || observeResources(&report.Resources) != nil {
		return report
	}
	report.Resources.LimitsVerified = true
	report.Resources.PerProcessDescriptors = DescriptorLimit
	report.Resources.AggregateDescriptorCeiling = TaskLimit * DescriptorLimit
	for _, path := range []string{"/scratch/home", "/scratch/tmp", "/scratch/cache"} {
		// These contain only untrusted worker state inside private tmpfs. The
		// watchdog has no CHOWN capability and retains no controls here.
		if os.Mkdir(path, 0o777) != nil || os.Chmod(path, 0o777) != nil {
			report.StopReason = "scratch_setup"
			return report
		}
	}
	output := &childOutput{cancel: cancel}
	command := exec.CommandContext(ctx, "/inputs/t451a", "__worker")
	command.Dir = "/scratch"
	command.Env = environment()
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{}}}
	command.Stdout = childStream{output, false}
	command.Stderr = childStream{output, true}
	command.WaitDelay = time.Second
	if command.Start() != nil {
		report.StopReason = "worker_start"
		return report
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var waitErr error
	measurementFailed := false
running:
	for {
		if observeResources(&report.Resources) != nil {
			measurementFailed = true
			cancel()
		}
		select {
		case waitErr = <-wait:
			break running
		case <-ticker.C:
		}
	}
	if observeResources(&report.Resources) != nil {
		measurementFailed = true
	}
	output.mu.Lock()
	report.Stdout = bytes.Clone(output.stdout.Bytes())
	report.Stderr = bytes.Clone(output.stderr.Bytes())
	overflow := output.overflow
	output.mu.Unlock()
	if measurementFailed || overflow || ctx.Err() != nil || verifyKernelLimits() != nil {
		report.Resources.SamplingUnavailable = measurementFailed
		report.StopReason = "resource_observation"
		if overflow {
			report.StopReason = "output_limit"
		}
		return report
	}
	if report.Resources.MemoryOOMEvents > 0 || report.Resources.MemoryOOMKills > 0 {
		report.StopReason = "memory_limit"
		return report
	}
	if report.Resources.TaskLimitEvents > 0 {
		report.StopReason = "process_limit"
		return report
	}
	remaining, err := liveProcesses()
	if err != nil || remaining != 1 {
		report.StopReason = "descendants_retained"
		return report
	}
	if waitErr != nil {
		report.StopReason = "worker_failed"
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) && exit.ExitCode() > 0 {
			report.ExitCode = exit.ExitCode()
		}
		return report
	}
	report.ExitCode = 0
	report.Complete = true
	report.StopReason = ""
	return report
}

type childOutput struct {
	mu             sync.Mutex
	stdout, stderr bytes.Buffer
	overflow       bool
	cancel         context.CancelFunc
}
type childStream struct {
	output *childOutput
	stderr bool
}

func (stream childStream) Write(data []byte) (int, error) {
	o := stream.output
	o.mu.Lock()
	defer o.mu.Unlock()
	remaining := OutputBytes - o.stdout.Len() - o.stderr.Len()
	if len(data) > remaining {
		o.overflow = true
		o.cancel()
		return 0, ErrExecution
	}
	if stream.stderr {
		return o.stderr.Write(data)
	}
	return o.stdout.Write(data)
}

func cgroupValue(name string) (uint64, error) {
	raw, err := readSmall("/sys/fs/cgroup/"+name, 4096)
	if err != nil {
		return 0, ErrRefused
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, ErrRefused
	}
	return value, nil
}

func verifyKernelLimits() error {
	for name, want := range map[string]uint64{"memory.max": MemoryBytes, "memory.swap.max": 0, "pids.max": TaskLimit} {
		actual, err := cgroupValue(name)
		if err != nil || actual != want {
			return ErrRefused
		}
	}
	raw, err := readSmall("/sys/fs/cgroup/cpu.max", 128)
	if err != nil || strings.TrimSpace(string(raw)) != "100000 100000" {
		return ErrRefused
	}
	raw, err = readSmall("/proc/self/cgroup", 128)
	if err != nil || strings.TrimSpace(string(raw)) != "0::/" {
		return ErrRefused
	}
	return nil
}

func observeKernelEvents(resources *Resources) error {
	for _, name := range []string{"memory.events", "pids.events"} {
		raw, err := readSmall("/sys/fs/cgroup/"+name, 4096)
		if err != nil {
			return ErrRefused
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				return ErrRefused
			}
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return ErrRefused
			}
			if name == "memory.events" {
				switch fields[0] {
				case "oom":
					resources.MemoryOOMEvents = value
				case "oom_kill":
					resources.MemoryOOMKills = value
				case "max":
					resources.MemoryLimitEvents = value
				}
			} else if fields[0] == "max" {
				resources.TaskLimitEvents = value
			}
		}
	}
	return nil
}

func observeResources(resources *Resources) (err error) {
	stage := "kernel_events"
	defer func() {
		if err != nil && resources.SamplingFailureStage == "" {
			var stopped *StageError
			if errors.As(err, &stopped) {
				stage = stopped.Stage
			}
			resources.SamplingFailureStage = stage
			resources.SamplingUnavailable = true
		}
	}()
	if err := observeKernelEvents(resources); err != nil {
		return err
	}
	stage = "memory_peak"
	peak, err := cgroupValue("memory.peak")
	if err != nil {
		return err
	}
	resources.MemoryPeakBytes = max(resources.MemoryPeakBytes, peak)
	processes, rss, err := processSample()
	if err != nil {
		return err
	}
	resources.SampledPeakProcesses = max(resources.SampledPeakProcesses, processes)
	resources.SampledPeakRSSBytes = max(resources.SampledPeakRSSBytes, rss)
	var allocated uint64
	for path, limit := range map[string]uint64{"/scratch": ScratchBytes, "/dev/shm": SharedMemoryBytes} {
		stage = "scratch_space"
		if path == "/dev/shm" {
			stage = "shared_memory_space"
		}
		var stat unix.Statfs_t
		if unix.Statfs(path, &stat) != nil || stat.Type != unix.TMPFS_MAGIC || stat.Bsize <= 0 || stat.Blocks > limit/uint64(stat.Bsize) || stat.Bfree > stat.Blocks || stat.Ffree > stat.Files {
			return ErrRefused
		}
		if stat.Flags&(unix.ST_NOSUID|unix.ST_NODEV) != unix.ST_NOSUID|unix.ST_NODEV || stat.Flags&unix.ST_RDONLY != 0 ||
			path == "/scratch" && (stat.Flags&unix.ST_NOEXEC != 0 || stat.Files > ScratchInodes) ||
			path == "/dev/shm" && (stat.Flags&unix.ST_NOEXEC == 0 || stat.Files > 1<<20) {
			return ErrRefused
		}
		allocated += (stat.Blocks - stat.Bfree) * uint64(stat.Bsize)
		if path == "/scratch" {
			resources.SampledPeakScratchInodes = max(resources.SampledPeakScratchInodes, stat.Files-stat.Ffree)
		}
	}
	resources.SampledPeakScratchBytes = max(resources.SampledPeakScratchBytes, allocated)
	resources.Samples++
	return nil
}

func liveProcesses() (uint64, error) { count, _, err := processSample(); return count, err }

func processSample() (count, rss uint64, err error) {
	stage := "process_inventory"
	defer func() {
		if err != nil {
			err = &StageError{Stage: stage, Cause: err}
		}
	}()
	file, err := os.Open("/proc")
	if err != nil {
		return 0, 0, ErrRefused
	}
	entries, readErr := file.ReadDir(512)
	closeErr := file.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) == 512 {
		return 0, 0, ErrRefused
	}
	for _, entry := range entries {
		pid, err := strconv.ParseUint(entry.Name(), 10, 32)
		if err != nil || pid == 0 {
			continue
		}
		stage = "process_stat_read"
		raw, err := readSmall("/proc/"+entry.Name()+"/stat", 8192)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return 0, 0, ErrRefused
		}
		stage = "process_stat_shape"
		end := strings.LastIndex(string(raw), ") ")
		if end < 0 {
			return 0, 0, ErrRefused
		}
		fields := strings.Fields(string(raw)[end+2:])
		if len(fields) < 22 {
			return 0, 0, ErrRefused
		}
		if fields[0] == "Z" {
			continue
		}
		pages, err := strconv.ParseUint(fields[21], 10, 64)
		if err != nil || pages > 1<<40 {
			return 0, 0, ErrRefused
		}
		stage = "process_count"
		count++
		if count > TaskLimit {
			return 0, 0, ErrRefused
		}
		rss += pages * uint64(os.Getpagesize())
	}
	stage = "process_count"
	if count == 0 {
		return 0, 0, ErrRefused
	}
	return count, rss, nil
}
