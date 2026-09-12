//go:build darwin

package t421

import (
	"context"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// No new timeout is created. Sysctl/Fstatfs are synchronous kernel calls;
// checks around each call reject cancellation, not promise interruptibility.
func executionHostContext(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	deadline, bounded := ctx.Deadline()
	return bounded && time.Now().Before(deadline)
}

func observeExecutionHostScalars(ctx context.Context) (ExecutionHost, error) {
	var host ExecutionHost
	if !executionHostContext(ctx) || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return host, errPressureVolume
	}
	product, err := unix.Sysctl("kern.osproductversion")
	if err != nil || !executionHostContext(ctx) {
		return host, errPressureVolume
	}
	build, err := unix.Sysctl("kern.osversion")
	if err != nil || !executionHostContext(ctx) {
		return host, errPressureVolume
	}
	cpus, err := unix.SysctlUint32("hw.logicalcpu")
	if err != nil || !executionHostContext(ctx) {
		return host, errPressureVolume
	}
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil || !executionHostContext(ctx) || !validVersionToken(product, 32) ||
		!validSanitizedToken(strings.ToLower(build), 32) || cpus == 0 || memory == 0 {
		return host, errPressureVolume
	}
	return ExecutionHost{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, OSProductVersion: product,
		OSBuildVersion: build, LogicalCPUs: uint64(cpus), MemoryBytes: memory}, nil
}

// This private projection takes only observations made by the native reader.
// It deliberately does not impose the admission floor on available capacity.
func executionHostFilesystemFacts(host ExecutionHost, stats [3]unix.Statfs_t) (executionHostObservation, error) {
	var out executionHostObservation
	for i, stat := range stats {
		if stat.Fsid.Val == ([2]int32{}) || stat.Bsize == 0 || stat.Blocks == 0 || stat.Bavail > stat.Blocks ||
			stat.Blocks > math.MaxUint64/uint64(stat.Bsize) {
			return executionHostObservation{}, errPressureVolume
		}
		out.FSIDs[i] = stat.Fsid.Val
	}
	if out.FSIDs[0] == out.FSIDs[1] || out.FSIDs[1] != out.FSIDs[2] {
		return executionHostObservation{}, errPressureVolume
	}
	for _, stat := range stats[1:] {
		if unix.ByteSliceToString(stat.Fstypename[:]) != "apfs" || stat.Flags&(unix.MNT_RDONLY|unix.MNT_IGNORE_OWNERSHIP) != 0 ||
			stat.Bsize != 4096 || stat.Blocks != frozenSafetyEnvelope().PressureVolumeBytes/4096 {
			return executionHostObservation{}, errPressureVolume
		}
	}
	host.BackingTotalDiskBytes = stats[0].Blocks * uint64(stats[0].Bsize)
	host.BackingAvailableDiskBytes = stats[0].Bavail * uint64(stats[0].Bsize)
	host.PressureTotalDiskBytes = stats[1].Blocks * uint64(stats[1].Bsize)
	host.PressureAvailableDiskBytes = stats[1].Bavail * uint64(stats[1].Bsize)
	host.PressureAllocationUnitBytes = uint64(stats[1].Bsize)
	host.BackingVolumeIdentity = executionFSIDIdentity(out.FSIDs[0])
	host.DataVolumeIdentity = executionFSIDIdentity(out.FSIDs[1])
	host.BallastVolumeIdentity = executionFSIDIdentity(out.FSIDs[2])
	host.VolumeIdentityMethod = "statfs-fsid-sha256-v1"
	out.Host = host
	return out, nil
}

// Caller owns file lifetimes and their pre/post identity checks. Native reads
// are sampled, not a coherent snapshot of other processes' backing allocation.
func observeExecutionHost(ctx context.Context, files [3]*os.File) (executionHostObservation, error) {
	host, err := observeExecutionHostScalars(ctx)
	if err != nil {
		return executionHostObservation{}, err
	}
	var stats [3]unix.Statfs_t
	for i, file := range files {
		if file == nil || !executionHostContext(ctx) || unix.Fstatfs(int(file.Fd()), &stats[i]) != nil || !executionHostContext(ctx) {
			return executionHostObservation{}, errPressureVolume
		}
	}
	observed, err := executionHostFilesystemFacts(host, stats)
	if err != nil || !executionHostContext(ctx) {
		return executionHostObservation{}, errPressureVolume
	}
	return observed, nil
}

// Bound to the genuine pressure workspace and zero ballast before AuthorA.
// The existing v -> flow -> author -> epochs order excludes owner closure.
// Check repeats fixed native identity work, never source trees or byte walks.
func (v *executionPressureVolume) observeProfileHost(ctx context.Context, flow *ExecutionEpochOne) error {
	if v == nil || flow == nil || !executionHostContext(ctx) || flow.epochs == nil || flow.epochs.author == nil {
		return errPressureVolume
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if flow.plan.Schema != PlanV3Schema || flow.closed || flow.used || flow.authored || !flow.authorStarted.IsZero() ||
		flow.profileHostUsed || v.flow != flow || !v.borrowed || !v.ready || v.removed || flow.workspace == nil ||
		flow.workspace.file != v.workspace.file || author.active || author.borrowedBy != nil || author.closed || author.err != nil || author.next != 0 ||
		epochs.active || epochs.closed || epochs.err != nil || epochs.released != 0 || len(epochs.roots) != 4 ||
		epochs.epochs[0].DataRoot != epochs.roots[0].path || v.ballast == nil || v.ballast.volume != v || v.ballast.failed || v.ballast.removed || v.ballast.next != 0 {
		return errPressureVolume
	}
	flow.profileHostUsed = true
	check := func() bool {
		if !executionHostContext(ctx) || pressureRootsUnchanged(epochs.roots[0]) != nil {
			return false
		}
		sample, err := v.ballast.sample() // Actual inode check plus existing four-root/image/capacity checks.
		return err == nil && sample.Allocated == 0 && executionHostContext(ctx)
	}
	if !check() {
		return errPressureVolume
	}
	observed, err := observeExecutionHost(ctx, [3]*os.File{v.parent.file, epochs.roots[0].file, v.ballast.file})
	if err != nil || !check() || observed.FSIDs[0] != v.parent.volume ||
		observed.FSIDs[1] != epochs.roots[0].volume || observed.FSIDs[2] != v.workspace.volume {
		return errPressureVolume
	}
	flow.profileHost = &observed
	return nil
}
