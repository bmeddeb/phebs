//go:build linux

package typedsandbox

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
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Supervisor must run as namespace PID1 and its caller must immediately os.Exit
// with the returned code; PID1 exit is the kernel descendant shutdown boundary.
func Supervisor() int { return supervisorProfile() }

func supervisorProfile() int {
	if os.Getpid() != 1 || os.Getuid() != 0 || os.Getgid() != 0 {
		return 125
	}
	var published atomic.Pointer[watchdogSnapshots]
	var reportReady atomic.Bool
	allowance, phase, request, stop, err := bootstrapSupervisor(os.Args[1:], func(timerErr error) {
		if timerErr != nil {
			os.Exit(125)
		}
		if snapshots := published.Load(); snapshots != nil {
			watchdogExit(snapshots, 2, reportReady.Load())
		}
		supervisorExpired()
	})
	if err != nil {
		return 125
	}
	defer stop()
	now, err := bootNow()
	if err != nil {
		return 125
	}
	remaining, err := allowance.remaining(now)
	if err != nil {
		return 124
	}
	snapshots := newWatchdogClock(time.Duration(remaining), func() (int64, error) {
		current, e := bootNow()
		if e != nil {
			return 0, e
		}
		return current - now, nil
	})
	snapshots.invocation(invocationDigest(allowance, phase, request, os.Args[5]))
	published.Store(snapshots)
	reportReady.Store(nonblockingReportFD(2))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-signals:
			os.Exit(125)
		case <-finished:
		}
	}()
	if readSupervisorAllowance(allowance, phase, request, os.Args[5]) != nil {
		return 125
	}
	if validateProcess(false) != nil || unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) != nil {
		return 125
	}
	authority, err := readScratch()
	if err != nil {
		return 125
	}
	scratch := &authority
	report := supervise(ctx, cancel, scratch, snapshots, allowance, workerArgs(os.Args[1:]))
	report.Allowance, report.Phase, report.RequestDigest, report.SealDigest = allowance, phase, request, os.Args[5]
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return 125
	}
	return report.ExitCode
}

func validateWorker(scratch *ScratchAuthority) error {
	if os.Getpid() == 1 || os.Getuid() != 65534 || os.Geteuid() != 65534 || os.Getgid() != 65534 || os.Getegid() != 65534 {
		return ErrRefused
	}
	if err := validateProcess(true); err != nil {
		return err
	}
	// Dispatch alone is not a resource proof: require the sealed disk scratch
	// even when UID, environment and capabilities match.
	return verifyScratch(*scratch)
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

func supervise(ctx context.Context, cancel context.CancelFunc, scratch *ScratchAuthority, snapshots *watchdogSnapshots, allowance Allowance, args []string) supervisorReport {
	report := supervisorReport{Schema: reportSchema, ExitCode: 125, StopReason: "kernel_limits"}
	if verifyKernelLimits() != nil || observeResources(&report.Resources, scratch) != nil {
		return report
	}
	if !scratchEmpty("/scratch") {
		report.StopReason = "scratch_setup"
		return report
	}
	report.Resources.LimitsVerified = true
	report.Resources.PerProcessDescriptors = DescriptorLimit
	report.Resources.AggregateDescriptorCeiling = TaskLimit * DescriptorLimit
	snapshots.resources(report.Resources)
	if snapshots.failed.Load() {
		return report
	}
	for _, path := range []string{"/scratch/home", "/scratch/tmp", "/scratch/cache"} {
		// These contain only untrusted worker state inside private scratch. The
		// watchdog has no CHOWN capability and retains no controls here.
		if os.Mkdir(path, 0o777) != nil || os.Chmod(path, 0o777) != nil {
			report.StopReason = "scratch_setup"
			return report
		}
	}
	output := &childOutput{cancel: cancel, limit: OutputBytes - allowance.WorkerBytesUsed}
	command := exec.CommandContext(ctx, HelperPath, args...)
	command.Dir = "/scratch"
	command.Env = environment()
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{}}}
	command.Stdout = childStream{output, false}
	command.Stderr = childStream{output, true}
	command.WaitDelay = time.Second
	progressRead, progressWrite, pipeErr := workerProgressPair()
	if pipeErr != nil {
		report.StopReason = "worker_start"
		return report
	}
	defer func() { _ = progressRead.Close(); _ = progressWrite.Close() }()
	if sendWorkerInvocation(progressRead, args) != nil {
		report.StopReason = "worker_start"
		return report
	}
	command.ExtraFiles = []*os.File{progressWrite}
	if command.Start() != nil {
		report.StopReason = "worker_start"
		return report
	}
	_ = progressWrite.Close()
	go readWorkerProgress(progressRead, snapshots)
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var waitErr error
	measurementFailed := false
running:
	for {
		if observeResources(&report.Resources, scratch) != nil {
			measurementFailed = true
			cancel()
		} else {
			snapshots.resources(report.Resources)
			if snapshots.failed.Load() {
				measurementFailed = true
				cancel()
			}
		}
		select {
		case waitErr = <-wait:
			break running
		case <-ticker.C:
		}
	}
	if observeResources(&report.Resources, scratch) != nil {
		measurementFailed = true
	} else {
		snapshots.resources(report.Resources)
	}
	output.mu.Lock()
	report.Stdout = bytes.Clone(output.stdout.Bytes())
	report.Stderr = bytes.Clone(output.stderr.Bytes())
	overflow := output.overflow
	output.mu.Unlock()
	if snapshots.failed.Load() {
		measurementFailed = true
	}
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
	remaining, _, err := processSampleLimit(TaskLimit)
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
	limit          int64
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
	remaining := o.limit - int64(o.stdout.Len()) - int64(o.stderr.Len())
	if o.limit < 0 || o.limit > OutputBytes || int64(len(data)) > remaining {
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
	memory := uint64(MemoryBytes)
	cpu := "200000 100000"
	for name, want := range map[string]uint64{"memory.max": memory, "memory.swap.max": 0, "pids.max": TaskLimit} {
		actual, err := cgroupValue(name)
		if err != nil || actual != want {
			return ErrRefused
		}
	}
	raw, err := readSmall("/sys/fs/cgroup/cpu.max", 128)
	if err != nil || strings.TrimSpace(string(raw)) != cpu {
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

func observeResources(resources *Resources, scratch *ScratchAuthority) (err error) {
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
	processes, rss, err := processSampleLimit(TaskLimit)
	if err != nil {
		return err
	}
	resources.SampledPeakProcesses = max(resources.SampledPeakProcesses, processes)
	resources.SampledPeakRSSBytes = max(resources.SampledPeakRSSBytes, rss)
	var allocated uint64
	limits := map[string]uint64{"/scratch": ScratchBytes, "/dev/shm": SharedMemoryBytes}
	for path, limit := range limits {
		stage = "scratch_space"
		if path == "/dev/shm" {
			stage = "shared_memory_space"
		}
		var stat unix.Statfs_t
		fsType := int64(unix.TMPFS_MAGIC)
		if path == "/scratch" {
			fsType = unix.EXT4_SUPER_MAGIC
			if verifyScratch(*scratch) != nil {
				return ErrRefused
			}
		}
		if unix.Statfs(path, &stat) != nil || int64(stat.Type) != fsType || stat.Bsize <= 0 || stat.Blocks > limit/uint64(stat.Bsize) || stat.Bfree > stat.Blocks || stat.Ffree > stat.Files {
			return ErrRefused
		}
		if stat.Flags&(unix.ST_NOSUID|unix.ST_NODEV) != unix.ST_NOSUID|unix.ST_NODEV || stat.Flags&unix.ST_RDONLY != 0 ||
			path == "/scratch" && (stat.Flags&unix.ST_NOEXEC != 0 || stat.Files != ScratchInodes) ||
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

func processSampleLimit(limit uint64) (count, rss uint64, err error) {
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
		if count > limit {
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
