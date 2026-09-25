//go:build darwin

package t421

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	executionReclaimSampleCadence = pressureBallastSettleCadence
	executionReclaimDefaultQuiet  = pressureBallastQuietWindow
	executionReclaimMaximumQuiet  = 20 * time.Minute
	executionReclaimAllowance     = 20 * time.Minute
	executionReclaimReleaseLimit  = 3 * time.Minute
)

// This selector answers one question the retained d4318be7 evidence cannot:
// whether the frozen eighty/ninety/seventy-five ballast geometry, its
// allocation syscalls and the bounded settler by themselves move the pressure
// volume's non-ballast footprint. Nothing else runs on the volume: no server,
// no epoch, no lifecycle turn, no admission, no request, no recorded event. It
// is an observation selector, never a rehearsal; its output is not receipt,
// readiness or ceremony evidence and authorizes no gate, seal, freeze or merge.
//
// Reading the result. Each mutation keeps the unchanged production predicates,
// so a refusal inside one reproduces the native refusal with no database
// present. Between mutations nothing touches the volume, so any non-ballast
// movement in a quiet window is background accounting alone and
// first_change_after_ms is its observed arrival lag. A quiet result narrows the
// d4318be7 release to the owned store; it does not exonerate the real layout,
// because the stand-in below occupies the pre-pressure envelope with one
// preallocated extent rather than the store's many files, and a quiet window is
// a sampled interval rather than proof of continuous stability.
//
// Cost. The image reaches the frozen ninety-percent target, so this allocates
// about 86 GiB of real backing on the host and returns it on success. A failure
// retains the attached image, both files and the observation for review.
func TestExecutionPressureReclaimLatencyOptionalNative(t *testing.T) {
	if os.Getenv("PHEBS_T422_RECLAIM_LATENCY_REHEARSAL") != "1" {
		t.Skip("requires explicitly selected empty-volume reclaim observation")
	}
	requireExternalToolFrozenHost(t)
	if !testing.Verbose() {
		t.Fatal("selected reclaim observation requires go test -v so successful rows are not discarded")
	}
	quiet := executionReclaimQuietWindow(t)
	if deadline, ok := t.Deadline(); ok {
		required := executionReclaimWall(quiet) + executionReclaimReleaseLimit + time.Minute
		if remaining := time.Until(deadline); remaining < required {
			t.Fatalf("go test timeout leaves %s; reclaim observation requires at least %s", remaining.Round(time.Second), required)
		}
	}
	geometry, err := expectedExecutionPressureGeometry(Plan{SafetyEnvelope: frozenSafetyEnvelope()}, ExecutionHost{
		PressureTotalDiskBytes: 96 << 30, PressureAllocationUnitBytes: 4096,
	})
	if err != nil {
		t.Fatal("frozen pressure geometry", err)
	}
	parent, err := os.MkdirTemp("", "t422-reclaim-latency-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("observation custody %s; quiet window %s; peak allocation %d bytes",
		parent, quiet, geometry.Targets[1].TargetUsedBytes)
	ctx, cancel := context.WithTimeout(t.Context(), executionReclaimWall(quiet))
	defer cancel()
	v, err := prepareExecutionPressureVolume(ctx, parent)
	if v == nil {
		t.Fatalf("retained pressure volume preparation %s: %v", parent, err)
	}
	var evidence []string
	t.Cleanup(func() {
		if !t.Failed() || len(evidence) == 0 {
			return
		}
		if err := os.WriteFile(filepath.Join(parent, "reclaim-latency.txt"), []byte(strings.Join(evidence, "\n")+"\n"), 0o600); err != nil {
			t.Error("retain reclaim observation", err)
		}
	})
	// Release runs first. A prior failure retains custody; a release failure
	// marks the test failed. The evidence cleanup then records either case.
	t.Cleanup(func() { executionReclaimRelease(t, v, parent) })
	if err != nil {
		t.Fatalf("retained pressure volume preparation %s: %v", parent, err)
	}
	// Prepare the zero-length ballast inode before the stand-in so its
	// directory metadata is not attributed to a later ballast allocation.
	file, err := os.OpenFile(filepath.Join(v.workspace.path, "pressure-ballast"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if file.Sync() != nil || v.workspace.file.Sync() != nil {
		t.Fatal("prepare native metadata baseline")
	}
	ballast := &executionPressureBallast{volume: v, file: file, info: info}
	baseline, err := ballast.sample()
	if err != nil {
		t.Fatal("native empty-volume baseline", err)
	}
	// Occupy the frozen pre-pressure envelope so the three targets meet the
	// same fill levels a real run meets. The stand-in reuses the ballast resize
	// so its allocation semantics cannot diverge; it is never sampled as
	// ballast and is never mutated again after this allocation.
	standinSize := executionReclaimStandinSize(baseline, geometry)
	if standinSize == 0 {
		t.Fatalf("empty volume is already outside the pre-pressure envelope: %+v", baseline)
	}
	standin, err := os.OpenFile(filepath.Join(v.workspace.path, "pressure-standin"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = standin.Close() }()
	if resizeExecutionPressureBallast(ctx, standin, 0, standinSize) != nil {
		t.Fatalf("native stand-in allocation of %d bytes", standinSize)
	}
	before, err := ballast.sample()
	if err != nil || before.Used < geometry.MinimumPrePressureUsedBytes || before.Used > geometry.MaximumPrePressureUsedBytes {
		t.Fatalf("stand-in did not reach the pre-pressure envelope: sample=%+v error=%v", before, err)
	}
	header := fmt.Sprintf("quiet_window_seconds=%d cadence_ms=%d standin_bytes=%d pre_pressure_used=%d pre_pressure_free_blocks=%d",
		int(quiet/time.Second), int(executionReclaimSampleCadence/time.Millisecond),
		standinSize, before.Used, before.FreeBlocks)
	evidence = append(evidence, "private unsigned observation; not receipt evidence", header)
	t.Log(header)
	settled, settledErr := observeExecutionReclaimWindow(ctx, ballast, quiet)
	evidence = append(evidence, executionReclaimRow("pre_pressure_quiet", settled))
	t.Logf("%s", executionReclaimRow("pre_pressure_quiet", settled))
	if settledErr != nil {
		t.Fatal("pre-pressure quiet observation", settledErr)
	}
	if settled.maximum-settled.minimum > geometry.Targets[0].ToleranceBytes {
		t.Fatalf("the volume is not quiet before any mutation: %s", executionReclaimRow("pre_pressure_quiet", settled))
	}
	if before, err = ballast.sample(); err != nil {
		t.Fatal("post-quiet baseline", err)
	}
	for _, target := range geometry.Targets {
		var size uint64
		size, err = pressureBallastSize(before, target)
		if err != nil {
			t.Fatalf("target %d%%: frozen size unavailable from %+v: %v", target.TargetUsedPercent, before, err)
		}
		if resizeExecutionPressureBallast(ctx, ballast.file, before.Allocated, size) != nil {
			t.Fatalf("target %d%%: native resize %d to %d", target.TargetUsedPercent, before.Allocated, size)
		}
		accept := func(value executionPressureBallastSample) bool {
			return value.Allocated == size && withinTolerance(value.Used, target.TargetUsedBytes, target.ToleranceBytes) &&
				pressureBallastDeltaMatches(target.Action, before, value)
		}
		after, settlement, err := settleExecutionPressureBallast(ctx, size, before.Allocated,
			func(context.Context) (executionPressureBallastSample, uint64, error) { return ballast.observe() }, accept)
		mutation := fmt.Sprintf(
			"target_percent=%d action=%s size=%d before_used=%d before_allocated=%d after_used=%d after_allocated=%d after_free_blocks=%d accepted=%t settle_samples=%d settle_used_changes=%d settle_max_step=%d",
			target.TargetUsedPercent, target.Action, size, before.Used, before.Allocated,
			after.Used, after.Allocated, after.FreeBlocks, err == nil && accept(after),
			settlement.Samples, settlement.UsedChanges, settlement.MaxUsedStep)
		evidence = append(evidence, mutation)
		t.Log(mutation)
		if err != nil || !accept(after) {
			t.Fatalf("target %d%% (%s): the production predicates refuse an empty-volume mutation; target=%d after=%+v error=%v",
				target.TargetUsedPercent, target.Action, target.TargetUsedBytes, after, err)
		}
		name := fmt.Sprintf("quiet_after_%d", target.TargetUsedPercent)
		var window executionReclaimWindow
		window, err = observeExecutionReclaimWindow(ctx, ballast, quiet)
		evidence = append(evidence, executionReclaimRow(name, window))
		t.Logf("%s", executionReclaimRow(name, window))
		if err != nil {
			t.Fatalf("%s observation: %v", name, err)
		}
		if window.maximum-window.minimum > target.ToleranceBytes {
			t.Fatalf("the untouched volume moved beyond tolerance after target %d%%: %s",
				target.TargetUsedPercent, executionReclaimRow(name, window))
		}
		before, err = ballast.sample()
		if err != nil || !pressureBallastAllocationUnchanged(after, before) {
			t.Fatalf("post-quiet target %d%% baseline is unavailable or changed allocation: before=%+v after=%+v error=%v",
				target.TargetUsedPercent, after, before, err)
		}
	}
}

// The three frozen targets, four quiet windows, one settle limit and a
// generous host allowance for the stand-in and ballast allocations.
func executionReclaimWall(quiet time.Duration) time.Duration {
	return 4*quiet + pressureBallastSettleLimit + executionReclaimAllowance
}

// executionReclaimStandinSize forecasts only the stand-in allocation needed to
// enter the frozen pre-pressure envelope, at its midpoint. It returns zero when
// the empty volume already sits outside that envelope. The actual
// post-allocation sample remains mandatory and never uses this forecast.
func executionReclaimStandinSize(baseline executionPressureBallastSample, geometry ExecutionPressureGeometry) uint64 {
	midpoint := geometry.MinimumPrePressureUsedBytes + (geometry.MaximumPrePressureUsedBytes-geometry.MinimumPrePressureUsedBytes)/2
	if baseline.Allocated != 0 || midpoint <= baseline.Used || midpoint > 96<<30 {
		return 0
	}
	size := (midpoint - baseline.Used) / 4096 * 4096
	if size == 0 || size > 80<<30 {
		return 0
	}
	return size
}

// Bounded read-only observations of one untouched volume. Non-ballast bytes are
// the whole-volume used bytes less this inode's allocation; no other owner
// exists here, so movement is background accounting.
type executionReclaimWindow struct {
	samples, changes                uint64
	first, last, minimum, maximum   uint64
	maximumStep                     uint64
	firstFreeBlocks, lastFreeBlocks uint64
	minimumFree, maximumFree        uint64
	firstChangeAfter                time.Duration
}

// observeExecutionReclaimWindow samples at the production settle cadence and
// mutates nothing. It keeps every custody check the settler's observer keeps
// and fails closed rather than reporting a partial window as quiet.
func observeExecutionReclaimWindow(ctx context.Context, ballast *executionPressureBallast, window time.Duration) (executionReclaimWindow, error) {
	var out executionReclaimWindow
	if ctx == nil || ctx.Err() != nil || ballast == nil || window <= 0 {
		return out, errPressureVolume
	}
	started := time.Now()
	deadline := started.Add(window)
	if selected, ok := ctx.Deadline(); ok && selected.Before(deadline) {
		return out, errPressureVolume
	}
	ticker := time.NewTicker(executionReclaimSampleCadence)
	defer ticker.Stop()
	for {
		value, _, err := ballast.observe()
		if err != nil {
			return out, err
		}
		if value.Used < value.Allocated {
			return out, errPressureVolume
		}
		other := value.Used - value.Allocated
		if out.samples == 0 {
			out.first, out.minimum, out.maximum = other, other, other
			out.firstFreeBlocks, out.minimumFree, out.maximumFree = value.FreeBlocks, value.FreeBlocks, value.FreeBlocks
		} else {
			if other != out.last {
				out.changes++
				if out.firstChangeAfter == 0 {
					out.firstChangeAfter = time.Since(started)
				}
			}
			out.maximumStep = max(out.maximumStep, max(other, out.last)-min(other, out.last))
			out.minimum, out.maximum = min(out.minimum, other), max(out.maximum, other)
			out.minimumFree, out.maximumFree = min(out.minimumFree, value.FreeBlocks), max(out.maximumFree, value.FreeBlocks)
		}
		out.last, out.lastFreeBlocks = other, value.FreeBlocks
		out.samples++
		if !time.Now().Before(deadline) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-ticker.C:
		}
	}
}

func executionReclaimRow(name string, window executionReclaimWindow) string {
	return fmt.Sprintf(
		"window=%s samples=%d changes=%d first=%d last=%d min=%d max=%d spread=%d max_step=%d first_change_after_ms=%d first_free_blocks=%d last_free_blocks=%d min_free_blocks=%d max_free_blocks=%d",
		name, window.samples, window.changes, window.first, window.last, window.minimum, window.maximum,
		window.maximum-window.minimum, window.maximumStep, window.firstChangeAfter.Milliseconds(),
		window.firstFreeBlocks, window.lastFreeBlocks, window.minimumFree, window.maximumFree)
}

// executionReclaimRelease reclaims the whole image when the observation passed.
// Any failure retains the attached volume, both files and the parent, and names
// them. Release uses the existing flow-less empty removal, so it refuses
// exactly where production refuses and never forces a detach.
func executionReclaimRelease(t *testing.T, v *executionPressureVolume, parent string) {
	t.Helper()
	if t.Failed() {
		_ = v.Close()
		t.Logf("retained attached pressure volume and observation custody: %s", parent)
		return
	}
	// The test context is already canceled by the time cleanup runs; release
	// owns its own bounded one and never inherits the observation deadline.
	ctx, cancel := context.WithTimeout(context.Background(), executionReclaimReleaseLimit)
	defer cancel()
	for _, name := range []string{"pressure-ballast", "pressure-standin"} {
		if err := os.Remove(filepath.Join(v.workspace.path, name)); err != nil {
			t.Errorf("retained %s: %v", parent, err)
			return
		}
	}
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"detach and image removal", func() error { return v.removeEmpty(ctx) }},
		{"volume close", v.Close},
		{"operation lock", func() error { return os.Remove(filepath.Join(parent, ".t4013-operation.lock")) }},
		{"observation custody", func() error { return os.Remove(parent) }},
	} {
		if err := step.run(); err != nil {
			t.Errorf("retained %s, %s refused: %v", parent, step.name, err)
			return
		}
	}
}

func executionReclaimQuietWindow(t *testing.T) time.Duration {
	t.Helper()
	raw := os.Getenv("PHEBS_T422_RECLAIM_QUIET_SECONDS")
	if raw == "" {
		return executionReclaimDefaultQuiet
	}
	seconds, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || seconds < 1 || time.Duration(seconds)*time.Second > executionReclaimMaximumQuiet {
		t.Fatal("explicit quiet window must be a positive second count within the selector maximum")
	}
	return time.Duration(seconds) * time.Second
}
