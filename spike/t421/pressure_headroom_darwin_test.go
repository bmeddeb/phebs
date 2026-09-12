//go:build darwin

package t421

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestExecutionPressureBallastHeadroom(t *testing.T) {
	geometry, err := expectedExecutionPressureGeometry(Plan{SafetyEnvelope: frozenSafetyEnvelope()}, ExecutionHost{
		PressureTotalDiskBytes: 96 << 30, PressureAllocationUnitBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	const base = uint64(8 << 30)
	const residual = uint64(32 << 30)
	// Independent arithmetic oracle: floor each target's excess over the
	// non-ballast capacity to a 4096-byte allocation unit.
	var size [3]uint64
	for i, target := range geometry.Targets {
		size[i] = (target.TargetUsedBytes - base) / 4096 * 4096
	}
	before := executionPressureBallastSample{Used: base, Available: 96<<30 - base}
	workspace := custodyByteSample{LogicalBytes: residual, AllocatedBytes: residual}
	maximum := custodyByteSample{LogicalBytes: residual + size[1], AllocatedBytes: residual + size[1]}
	// Model the selected 128-GiB logical / 96-GiB allocated limits directly.
	frozenBefore := executionPressureBallastSample{Used: 42 << 30, Available: 54 << 30}
	frozenWorkspace := custodyByteSample{LogicalBytes: 60 << 30, AllocatedBytes: 60 << 30}
	frozenMaximum := custodyByteSample{LogicalBytes: 128 << 30, AllocatedBytes: 96 << 30}
	badFuture := append([]PressureTargetGeometry(nil), geometry.Targets...)
	badFuture[1].Action = "unknown"
	for _, test := range []struct {
		name          string
		before        executionPressureBallastSample
		workspace     custodyByteSample
		targets       []PressureTargetGeometry
		maximum       custodyByteSample
		want          uint64
		wantError     bool
		wantErrorText string
	}{
		{name: "frozen_ceiling_80_only_fits", before: frozenBefore, workspace: frozenWorkspace, targets: geometry.Targets[:1], maximum: frozenMaximum, want: (geometry.Targets[0].TargetUsedBytes - 42<<30) / 4096 * 4096},
		{name: "frozen_ceiling_90_peak_refuses", before: frozenBefore, workspace: frozenWorkspace, targets: geometry.Targets, maximum: frozenMaximum, wantError: true, wantErrorText: "projected pressure-90 workspace headroom refused"},
		{name: "both_equal_at_future_peak", before: before, workspace: workspace, targets: geometry.Targets, maximum: maximum, want: size[0]},
		{name: "workspace_logical_already_over", before: before, workspace: custodyByteSample{LogicalBytes: maximum.LogicalBytes + 1, AllocatedBytes: residual}, targets: geometry.Targets, maximum: maximum, wantError: true},
		{name: "workspace_allocated_already_over", before: before, workspace: custodyByteSample{LogicalBytes: residual, AllocatedBytes: maximum.AllocatedBytes + 1}, targets: geometry.Targets, maximum: maximum, wantError: true},
		{name: "logical_one_over", before: before, workspace: custodyByteSample{LogicalBytes: residual + 1, AllocatedBytes: residual}, targets: geometry.Targets, maximum: maximum, wantError: true},
		{name: "allocated_one_over", before: before, workspace: custodyByteSample{LogicalBytes: residual, AllocatedBytes: residual + 1}, targets: geometry.Targets, maximum: maximum, wantError: true},
		{name: "first_target_fits", before: before, workspace: workspace, targets: geometry.Targets[:1], maximum: custodyByteSample{LogicalBytes: residual + size[0], AllocatedBytes: residual + size[0]}, want: size[0]},
		{name: "later_90_target_does_not_fit", before: before, workspace: workspace, targets: geometry.Targets, maximum: custodyByteSample{LogicalBytes: residual + size[0], AllocatedBytes: residual + size[0]}, wantError: true},
		{name: "90_subtracts_existing_80_ballast", before: executionPressureBallastSample{Used: base + size[0], Available: 96<<30 - base - size[0], Allocated: size[0]}, workspace: custodyByteSample{LogicalBytes: residual + size[0], AllocatedBytes: residual + size[0]}, targets: geometry.Targets[1:], maximum: maximum, want: size[1]},
		{name: "75_subtracts_existing_90_ballast", before: executionPressureBallastSample{Used: base + size[1], Available: 96<<30 - base - size[1], Allocated: size[1]}, workspace: custodyByteSample{LogicalBytes: residual + size[1], AllocatedBytes: residual + size[1]}, targets: geometry.Targets[2:], maximum: maximum, want: size[2]},
		{name: "logical_subtraction_underflow", before: executionPressureBallastSample{Used: base + size[0], Available: 96<<30 - base - size[0], Allocated: size[0]}, workspace: custodyByteSample{LogicalBytes: size[0] - 1, AllocatedBytes: size[0]}, targets: geometry.Targets[1:], maximum: maximum, wantError: true},
		{name: "allocated_subtraction_underflow", before: executionPressureBallastSample{Used: base + size[0], Available: 96<<30 - base - size[0], Allocated: size[0]}, workspace: custodyByteSample{LogicalBytes: size[0], AllocatedBytes: size[0] - 1}, targets: geometry.Targets[1:], maximum: maximum, wantError: true},
		{name: "logical_addition_overflow", before: before, workspace: custodyByteSample{LogicalBytes: ^uint64(0), AllocatedBytes: residual}, targets: geometry.Targets, maximum: custodyByteSample{LogicalBytes: ^uint64(0), AllocatedBytes: maximum.AllocatedBytes}, wantError: true},
		{name: "allocated_addition_overflow", before: before, workspace: custodyByteSample{LogicalBytes: residual, AllocatedBytes: ^uint64(0)}, targets: geometry.Targets, maximum: custodyByteSample{LogicalBytes: maximum.LogicalBytes, AllocatedBytes: ^uint64(0)}, wantError: true},
		{name: "missing_targets", before: before, workspace: workspace, maximum: maximum, wantError: true},
		{name: "too_many_targets", before: before, workspace: workspace, targets: append(append([]PressureTargetGeometry(nil), geometry.Targets...), geometry.Targets[0]), maximum: maximum, wantError: true},
		{name: "invalid_future_target", before: before, workspace: workspace, targets: badFuture, maximum: maximum, wantError: true},
		{name: "missing_target", before: before, workspace: workspace, targets: []PressureTargetGeometry{{}}, maximum: maximum, wantError: true},
		{name: "incoherent_capacity", before: executionPressureBallastSample{Used: base}, workspace: workspace, targets: geometry.Targets, maximum: maximum, wantError: true},
		{name: "ballast_exceeds_used", before: executionPressureBallastSample{Used: base, Available: 96<<30 - base, Allocated: base + 4096}, workspace: workspace, targets: geometry.Targets, maximum: maximum, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := pressureBallastTargetSize(test.before, test.workspace, test.targets, test.maximum)
			if test.wantError {
				if !errors.Is(err, errPressureVolume) || got != 0 {
					t.Fatalf("refusal returned size=%d error=%v", got, err)
				}
				if test.wantErrorText != "" && !strings.Contains(err.Error(), test.wantErrorText) {
					t.Fatalf("refusal %q does not name %q", err, test.wantErrorText)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("size=%d error=%v; want %d", got, err, test.want)
			}
		})
	}
}

func TestExecutionPressureBallastHeadroomHardLinks(t *testing.T) {
	owner, ctx := custodyByteFixture(t)
	path := filepath.Join(owner.path, "allocated")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	const fileBytes = uint64(1 << 20)
	if err := resizeExecutionPressureBallast(ctx, file, 0, fileBytes); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(owner.path, "alias")); err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	allocated := uint64(stat.Blocks) * 512
	if stat.Nlink != 2 || uint64(info.Size()) != fileBytes || allocated != fileBytes {
		t.Fatalf("fixture is not one fully allocated hard-linked MiB: links=%d size=%d allocated=%d", stat.Nlink, info.Size(), allocated)
	}
	actual, err := walkCustodyBytes(ctx, owner)
	if err != nil || actual != actualCustodyByteFixtureTotal(t, owner.path) || actual.LogicalBytes != 2*fileBytes || actual.AllocatedBytes < 2*allocated {
		t.Fatalf("per-path custody sample=%+v error=%v", actual, err)
	}
	// Only the file and hard link are real. The 96-GiB native capacity and
	// subsequent ballast are modeled: this is not an APFS pressure rehearsal.
	geometry, err := expectedExecutionPressureGeometry(Plan{SafetyEnvelope: frozenSafetyEnvelope()}, ExecutionHost{
		PressureTotalDiskBytes: 96 << 30, PressureAllocationUnitBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	const base = uint64(40 << 30)
	before := executionPressureBallastSample{Used: base, Available: 96<<30 - base}
	peakBallast := (geometry.Targets[1].TargetUsedBytes - base) / 4096 * 4096
	inodeOnce := custodyByteSample{LogicalBytes: actual.LogicalBytes - fileBytes, AllocatedBytes: actual.AllocatedBytes - allocated}
	maximum := custodyByteSample{LogicalBytes: inodeOnce.LogicalBytes + peakBallast, AllocatedBytes: inodeOnce.AllocatedBytes + peakBallast}
	if _, err := pressureBallastTargetSize(before, inodeOnce, geometry.Targets, maximum); err != nil {
		t.Fatalf("modeled inode-once assumption should fit exactly: %v", err)
	}
	if size, err := pressureBallastTargetSize(before, actual, geometry.Targets, maximum); !errors.Is(err, errPressureVolume) || size != 0 {
		t.Fatalf("actual per-path alias gauge admitted size=%d error=%v", size, err)
	}
}
