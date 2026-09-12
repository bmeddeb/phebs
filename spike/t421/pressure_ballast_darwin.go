//go:build darwin

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Native filesystem facts only. The caller must separately measure complete
// custody allocation and bind lifecycle, authority and event observations.
type executionPressureBallastSample struct {
	Used, Available, Allocated uint64
}

type executionPressureBallastMutation struct {
	Before, After executionPressureBallastSample
	Fence         time.Time
}

// The volume's existing mutex and mutation lease serialize the four fixed
// mutations. No arbitrary path, desired size, or capacity assertion is accepted.
type executionPressureBallast struct {
	volume  *executionPressureVolume
	file    *os.File
	info    os.FileInfo
	next    int
	last    executionPressureBallastSample
	failed  bool
	removed bool
}

// Prepare the zero-length inode before pre-pressure normalization so its
// directory metadata is not attributed to the subsequent ballast allocation.
func prepareExecutionPressureBallast(ctx context.Context, volume *executionPressureVolume) (_ *executionPressureBallast, retErr error) {
	if ctx == nil || ctx.Err() != nil || volume == nil {
		return nil, errPressureVolume
	}
	volume.mu.Lock()
	defer volume.mu.Unlock()
	if !volume.ready || !volume.borrowed || volume.ballast != nil || volume.check() != nil {
		return nil, errPressureVolume
	}
	b := &executionPressureBallast{volume: volume}
	volume.ballast = b
	defer func() { b.failed = retErr != nil }()
	var err error
	b.file, err = os.OpenFile(filepath.Join(volume.workspace.path, "pressure-ballast"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return b, errPressureVolume
	}
	b.info, err = b.file.Stat()
	if err != nil || b.file.Sync() != nil || volume.workspace.file.Sync() != nil || ctx.Err() != nil {
		return b, errPressureVolume
	}
	sample, err := b.sample()
	if err != nil || sample.Allocated != 0 {
		return b, errPressureVolume
	}
	return b, nil
}

// nextTarget accepts only the bound epoch-four server, with requests fenced
// and the shared store controller in the corresponding phase 9, 10, or 11.
// The owning phase operation must retain the ordinary-owner/lifecycle fences
// across this call. This native helper does not manufacture those ACKs.
// workspace is the owning phase's latest successful native HTTP sample, not
// a filesystem-used estimate. Requests are fenced after that required sample.
func (b *executionPressureBallast) nextTarget(ctx context.Context, run *ExecutionEpochOneRun, workspace custodyByteSample) (out executionPressureBallastMutation, retErr error) {
	if b == nil || b.volume == nil || ctx == nil || ctx.Err() != nil || run == nil {
		return out, errPressureVolume
	}
	v := b.volume
	v.mu.Lock()
	defer v.mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	if b.next >= 3 || b.authorize(ctx, run, uint32(9+b.next)) != nil {
		return out, errPressureVolume
	}
	defer func() { b.failed = retErr != nil }()
	geometry, err := expectedExecutionPressureGeometry(run.flow.plan, ExecutionHost{
		PressureTotalDiskBytes: 96 << 30, PressureAllocationUnitBytes: 4096,
	})
	if err != nil {
		return out, errPressureVolume
	}
	target := geometry.Targets[b.next]
	out.Before, err = b.sample()
	if err != nil || b.next > 0 && out.Before != b.last ||
		b.next == 0 && (out.Before.Used < geometry.MinimumPrePressureUsedBytes || out.Before.Used > geometry.MaximumPrePressureUsedBytes) {
		return out, errPressureVolume
	}
	size, err := pressureBallastTargetSize(out.Before, workspace, geometry.Targets[b.next:], custodyByteSample{
		LogicalBytes:   run.flow.plan.WorkEnvelope.MaximumDataLogicalBytes,
		AllocatedBytes: run.flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes,
	})
	if err != nil {
		return out, err
	}
	if resizeExecutionPressureBallast(ctx, b.file, out.Before.Allocated, size) != nil {
		return out, errPressureVolume
	}
	out.After, err = b.sample()
	if err != nil || b.authorize(ctx, run, uint32(9+b.next)) != nil || out.After.Allocated != size ||
		!withinTolerance(out.After.Used, target.TargetUsedBytes, target.ToleranceBytes) ||
		!pressureBallastDeltaMatches(target.Action, out.Before, out.After) {
		return out, errPressureVolume
	}
	out.Fence = time.Now()
	b.last, b.next = out.After, b.next+1
	return out, nil
}

// remove releases only this exact inode after the third target, never an
// arbitrary directory or all workspace contents. Failure retains the volume.
func (b *executionPressureBallast) remove(ctx context.Context, run *ExecutionEpochOneRun) (out executionPressureBallastMutation, retErr error) {
	if b == nil || b.volume == nil || ctx == nil || ctx.Err() != nil || run == nil {
		return out, errPressureVolume
	}
	v := b.volume
	v.mu.Lock()
	defer v.mu.Unlock()
	run.mu.Lock()
	defer run.mu.Unlock()
	if b.next != 3 || b.authorize(ctx, run, 11) != nil {
		return out, errPressureVolume
	}
	defer func() { b.failed = retErr != nil }()
	var err error
	out.Before, err = b.sample()
	if err != nil || out.Before != b.last || resizeExecutionPressureBallast(ctx, b.file, out.Before.Allocated, 0) != nil {
		return out, errPressureVolume
	}
	out.After, err = b.sample()
	if err != nil || out.After.Allocated != 0 || usedPercentCeiling(out.After.Used, 96<<30) > 74 ||
		!pressureBallastDeltaMatches("remove", out.Before, out.After) || b.authorize(ctx, run, 11) != nil {
		return out, errPressureVolume
	}
	if b.file.Close() != nil {
		return out, errPressureVolume
	}
	b.file = nil
	path := filepath.Join(v.workspace.path, "pressure-ballast")
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(b.info, current) || os.Remove(path) != nil || v.workspace.file.Sync() != nil {
		return out, errPressureVolume
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return out, errPressureVolume
	}
	// Capture after inode removal too; directory metadata must not masquerade
	// as a successful zero-ballast capacity observation.
	out.After, err = b.capacity()
	if err != nil || ctx.Err() != nil || usedPercentCeiling(out.After.Used, 96<<30) > 74 ||
		!pressureBallastDeltaMatches("remove", out.Before, out.After) {
		return out, errPressureVolume
	}
	out.Fence, b.removed = time.Now(), true
	return out, nil
}

// Called under the volume and run locks, in that order.
func (b *executionPressureBallast) authorize(ctx context.Context, run *ExecutionEpochOneRun, phase uint32) error {
	v := b.volume
	if b.failed || b.removed || b.file == nil || v.ballast != b || !v.borrowed || !v.ready || v.flow == nil ||
		run.flow != v.flow || run.epoch.Epoch != 4 || !run.healthy || run.stopping || run.err != nil ||
		run.control == nil || run.control.Context().Err() != nil || run.control.RequestToken() != "" ||
		v.flow.store == nil || ctx.Err() != nil || v.check() != nil {
		return errPressureVolume
	}
	select {
	case <-run.done:
		return errPressureVolume
	default:
	}
	snapshot, err := v.flow.store.Snapshot()
	if err != nil || snapshot.Store.Phase != phase {
		return errPressureVolume
	}
	return nil
}

func (b *executionPressureBallast) sample() (executionPressureBallastSample, error) {
	if b.file == nil {
		return executionPressureBallastSample{}, errPressureVolume
	}
	held, err := b.file.Stat()
	current, pathErr := os.Lstat(filepath.Join(b.volume.workspace.path, "pressure-ballast"))
	var stat unix.Stat_t
	volume, volumeErr := inputCustodyVolume(b.file)
	if err != nil || pathErr != nil || volumeErr != nil || volume != b.volume.workspace.volume ||
		!os.SameFile(b.info, held) || !os.SameFile(held, current) || !inputCustodyOwned(current) ||
		!current.Mode().IsRegular() || current.Mode().Perm() != 0o600 ||
		unix.Fstat(int(b.file.Fd()), &stat) != nil || stat.Nlink != 1 || stat.Blocks < 0 ||
		stat.Blocks > (80<<30)/512 || stat.Size != stat.Blocks*512 {
		return executionPressureBallastSample{}, errPressureVolume
	}
	out, err := b.capacity()
	out.Allocated = uint64(stat.Blocks) * 512
	return out, err
}

func (b *executionPressureBallast) capacity() (executionPressureBallastSample, error) {
	var stat unix.Statfs_t
	if b.volume.check() != nil || unix.Fstatfs(int(b.volume.workspace.file.Fd()), &stat) != nil ||
		stat.Fsid.Val != b.volume.workspace.volume || stat.Blocks != (96<<30)/4096 || stat.Bsize != 4096 || stat.Bavail > stat.Blocks {
		return executionPressureBallastSample{}, errPressureVolume
	}
	available := stat.Bavail * 4096
	return executionPressureBallastSample{Used: 96<<30 - available, Available: available}, nil
}

func pressureBallastSize(before executionPressureBallastSample, target PressureTargetGeometry) (uint64, error) {
	if before.Used > 96<<30 || before.Available != 96<<30-before.Used || before.Allocated > before.Used ||
		before.Allocated > 80<<30 || before.Allocated%4096 != 0 || target.TargetUsedBytes > 96<<30 ||
		target.TargetUsedBytes <= before.Used-before.Allocated {
		return 0, errPressureVolume
	}
	size := (target.TargetUsedBytes - (before.Used - before.Allocated)) / 4096 * 4096
	if size > 80<<30 || target.Action == "add" && size <= before.Allocated ||
		target.Action == "remove" && size >= before.Allocated || target.Action != "add" && target.Action != "remove" {
		return 0, errPressureVolume
	}
	return size, nil
}

// Forecast only the known ballast change using both measured byte units. Linked
// paths need not equal filesystem-used bytes. Check the remaining peak before
// the first allocation, then refresh at each phase. This is not a coherent future
// workspace measurement; the actual post-mutation samples remain mandatory.
func pressureBallastTargetSize(before executionPressureBallastSample, workspace custodyByteSample, targets []PressureTargetGeometry, maximum custodyByteSample) (uint64, error) {
	if len(targets) == 0 || len(targets) > 3 || workspace.LogicalBytes < before.Allocated || workspace.AllocatedBytes < before.Allocated ||
		workspace.LogicalBytes > maximum.LogicalBytes || workspace.AllocatedBytes > maximum.AllocatedBytes {
		return 0, errPressureVolume
	}
	logical, allocated := workspace.LogicalBytes-before.Allocated, workspace.AllocatedBytes-before.Allocated
	var first uint64
	for i, target := range targets {
		size, err := pressureBallastSize(before, target)
		if err != nil {
			return 0, err
		}
		// Subtraction bounds the addition without overflowing either byte unit.
		if size > maximum.LogicalBytes-logical || size > maximum.AllocatedBytes-allocated {
			return 0, fmt.Errorf("%w: projected pressure-%d workspace headroom refused: non-ballast logical=%d allocated=%d ballast=%d limits=%d/%d",
				errPressureVolume, target.TargetUsedPercent, logical, allocated, size, maximum.LogicalBytes, maximum.AllocatedBytes)
		}
		if i == 0 {
			first = size
		}
		used := before.Used - before.Allocated + size // pressureBallastSize bounds this by the volume.
		before = executionPressureBallastSample{Used: used, Available: 96<<30 - used, Allocated: size}
	}
	return first, nil
}

func pressureBallastDeltaMatches(action string, before, after executionPressureBallastSample) bool {
	if action == "add" {
		return after.Used > before.Used && after.Allocated > before.Allocated &&
			withinTolerance(after.Used-before.Used, after.Allocated-before.Allocated, 4096)
	}
	return action == "remove" && before.Used > after.Used && before.Allocated > after.Allocated &&
		withinTolerance(before.Used-after.Used, before.Allocated-after.Allocated, 4096)
}

// Native syscalls remain cooperative. Each fixed mutation has one allocation
// (growth only), one truncate and one sync; no sparse-hole success is accepted.
func resizeExecutionPressureBallast(ctx context.Context, file *os.File, before, after uint64) error {
	if ctx == nil || ctx.Err() != nil || file == nil || before > 80<<30 || after > 80<<30 || before%4096 != 0 || after%4096 != 0 {
		return errPressureVolume
	}
	if after > before {
		allocation := &unix.Fstore_t{Flags: unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: int64(after - before)}
		if unix.FcntlFstore(file.Fd(), unix.F_PREALLOCATE, allocation) != nil || allocation.Bytesalloc != int64(after-before) {
			return errPressureVolume
		}
	}
	if ctx.Err() != nil || file.Truncate(int64(after)) != nil || file.Sync() != nil || ctx.Err() != nil {
		return errPressureVolume
	}
	return nil
}
