//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestExecutionPressureBallastSize(t *testing.T) {
	geometry, err := expectedExecutionPressureGeometry(Plan{SafetyEnvelope: frozenSafetyEnvelope()}, ExecutionHost{
		PressureTotalDiskBytes: 96 << 30, PressureAllocationUnitBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []uint64{8 << 30, 40 << 30, 68 << 30} {
		before := executionPressureBallastSample{Used: base, Available: 96<<30 - base}
		for _, target := range geometry.Targets {
			size, err := pressureBallastSize(before, target)
			if err != nil || size > 80<<30 || size%4096 != 0 ||
				!withinTolerance(base+size, target.TargetUsedBytes, 4096) ||
				usedPercentCeiling(base+size, 96<<30) != target.TargetUsedPercent {
				t.Fatalf("base %d target %d: size=%d error=%v", base, target.TargetUsedPercent, size, err)
			}
			before = executionPressureBallastSample{Used: base + size, Available: 96<<30 - base - size, Allocated: size}
		}
	}
	for _, test := range []struct {
		name   string
		before executionPressureBallastSample
		target PressureTargetGeometry
	}{
		{"overflow", executionPressureBallastSample{Used: ^uint64(0)}, geometry.Targets[0]},
		{"noncontiguous", executionPressureBallastSample{Used: 8 << 30}, geometry.Targets[0]},
		{"ballast_exceeds_used", executionPressureBallastSample{Used: 8 << 30, Available: 88 << 30, Allocated: 9 << 30}, geometry.Targets[0]},
		{"already_above_target", executionPressureBallastSample{Used: 90 << 30, Available: 6 << 30}, geometry.Targets[0]},
		{"unknown_action", executionPressureBallastSample{Used: 8 << 30, Available: 88 << 30}, PressureTargetGeometry{TargetUsedBytes: 80 << 30, Action: "other"}},
		{"remove_would_grow", executionPressureBallastSample{Used: 8 << 30, Available: 88 << 30}, geometry.Targets[2]},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pressureBallastSize(test.before, test.target); err == nil {
				t.Fatal("invalid size admitted")
			}
		})
	}
	before := executionPressureBallastSample{Used: 8 << 30, Allocated: 4096}
	for _, test := range []struct {
		name   string
		after  executionPressureBallastSample
		action string
		want   bool
	}{
		{"real_growth", executionPressureBallastSample{Used: before.Used + 8192, Allocated: 12288}, "add", true},
		{"one_unit_tolerance", executionPressureBallastSample{Used: before.Used + 12288, Allocated: 12288}, "add", true},
		{"excess_metadata", executionPressureBallastSample{Used: before.Used + 16384, Allocated: 12288}, "add", false},
		{"sparse_hole", executionPressureBallastSample{Used: before.Used + 8192, Allocated: 4096}, "add", false},
		{"real_shrink", executionPressureBallastSample{Used: before.Used - 4096}, "remove", true},
		{"unreleased_blocks", executionPressureBallastSample{Used: before.Used}, "remove", false},
		{"unknown", executionPressureBallastSample{Used: before.Used - 4096}, "other", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := pressureBallastDeltaMatches(test.action, before, test.after); got != test.want {
				t.Fatalf("delta matched=%t, want %t", got, test.want)
			}
		})
	}
}

func TestExecutionPressureBallastRefusals(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled, t.Context()} {
		for _, v := range []*executionPressureVolume{nil, {}, {ready: true}, {ready: true, borrowed: true}} {
			if _, err := prepareExecutionPressureBallast(ctx, v); err == nil {
				t.Fatal("invalid volume issued ballast")
			}
		}
		for _, b := range []*executionPressureBallast{nil, {}, {volume: &executionPressureVolume{}}} {
			if _, err := b.nextTarget(ctx, nil); err == nil {
				t.Fatal("invalid run issued mutation")
			}
			if _, err := b.remove(ctx, &ExecutionEpochOneRun{}); err == nil {
				t.Fatal("invalid run removed ballast")
			}
		}
	}
	v := &executionPressureVolume{ready: true, ballast: &executionPressureBallast{}}
	if v.removeEmpty(t.Context()) == nil || v.finishRehearsal(t.Context(), &ExecutionEpochOneRun{}) == nil {
		t.Fatal("outstanding ballast released volume")
	}
}

// A <=1-MiB native syscall fixture, not pressure evidence or a manufactured
// phase/run. It never calls nextTarget or changes the frozen geometry.
func TestExecutionPressureBallastOptionalNative(t *testing.T) {
	if os.Getenv("PHEBS_T422_BALLAST_NATIVE_REHEARSAL") != "1" {
		t.Skip("requires explicitly selected tiny APFS allocation gate")
	}
	requireExternalToolFrozenHost(t)
	parent, err := os.MkdirTemp("", "t422-ballast-syscall-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained on failure: %s", parent)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	v, err := prepareExecutionPressureVolume(ctx, parent)
	if v != nil {
		defer func() { _ = v.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(v.workspace.path, "pressure-ballast")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
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
	// Read the actual inode/capacity through the component sampler. This is
	// deliberately not a borrowed epoch or permission to use nextTarget.
	ballast := &executionPressureBallast{volume: v, file: file, info: info}
	before := uint64(0)
	var beforeRemoval executionPressureBallastSample
	for _, size := range []uint64{512 << 10, 1 << 20, 512 << 10, 0} {
		capacityBefore, err := ballast.sample()
		if err != nil {
			t.Fatal("native pre-resize capacity", err)
		}
		if size == 0 {
			beforeRemoval = capacityBefore
		}
		if err := resizeExecutionPressureBallast(ctx, file, before, size); err != nil {
			t.Fatalf("native resize %d to %d: %v", before, size, err)
		}
		var stat unix.Stat_t
		volume, err := inputCustodyVolume(file)
		if err != nil || volume != v.workspace.volume || unix.Fstat(int(file.Fd()), &stat) != nil ||
			stat.Size != int64(size) || stat.Blocks*512 != int64(size) {
			t.Fatalf("native allocation mismatch: size=%d stat=%+v error=%v", size, stat, err)
		}
		capacityAfter, err := ballast.sample()
		action := "add"
		if size < before {
			action = "remove"
		}
		t.Logf("native %s %d to %d: before=%+v after=%+v", action, before, size, capacityBefore, capacityAfter)
		if err != nil || !pressureBallastDeltaMatches(action, capacityBefore, capacityAfter) {
			t.Fatalf("native capacity delta exceeds unchanged 4096-byte tolerance: %v", err)
		}
		before = size
	}
	if file.Close() != nil {
		t.Fatal("close ballast")
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) || os.Remove(path) != nil || v.workspace.file.Sync() != nil {
		t.Fatal("exact ballast removal", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("ballast remains after unlink", err)
	}
	afterRemoval, err := ballast.capacity()
	t.Logf("native final removal: before=%+v after=%+v", beforeRemoval, afterRemoval)
	if err != nil || afterRemoval.Allocated != 0 || !pressureBallastDeltaMatches("remove", beforeRemoval, afterRemoval) {
		t.Fatal("post-unlink native capacity delta exceeds unchanged 4096-byte tolerance", err)
	}
	if err := v.removeEmpty(ctx); err != nil {
		t.Fatal("native empty detach", err)
	}
	if v.Close() != nil || os.Remove(filepath.Join(parent, ".t4013-operation.lock")) != nil || os.Remove(parent) != nil {
		t.Fatal("exact outer cleanup")
	}
}
