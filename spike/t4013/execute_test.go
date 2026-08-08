package t4013

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoppedExecutionDestroysOnlyExactCustodyAndRemainsReceiptable(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	outside := filepath.Join(root, "outside")
	for _, path := range []string{module, workspace, outside} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "private"), []byte("destroy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "retained"), []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	observation := emptyObservation(EnvironmentObservation{
		OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
		FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30, InitialUsedPercent: 72,
	})
	run := &execution{moduleRoot: module, workspace: workspace, observation: observation, phase: 5}
	stopped, err := run.stopAfterFailure(errors.New("injected exact failure"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
		t.Fatal("exact custody survived stopped-run teardown")
	}
	if _, err := os.Lstat(filepath.Join(outside, "retained")); err != nil {
		t.Fatal("stopped-run teardown crossed custody boundary")
	}
	if stopped.Outcome != "stopped" || stopped.Decision.Selected != "p6_investigation" ||
		!stopped.Teardown.Completed || len(stopped.Failures) != 1 {
		t.Fatalf("stopped observation = %+v", stopped)
	}
}

func TestObservationWriterIsExclusiveAndAbsolute(t *testing.T) {
	value := completedObservation()
	path := filepath.Join(t.TempDir(), "observation.json")
	if err := WriteObservation(path, value); err != nil {
		t.Fatal(err)
	}
	if err := WriteObservation(path, value); err == nil {
		t.Fatal("observation writer replaced an existing output")
	}
	if err := WriteObservation("relative.json", value); err == nil {
		t.Fatal("observation writer accepted a relative path")
	}
}

func TestExecutionSafetyUsesPhaseGaugeMaximaAndTotalWall(t *testing.T) {
	run := &execution{plan: Plan{Safety: frozenSafety}, observation: emptyObservation(EnvironmentObservation{
		OS: "darwin", Arch: "arm64", MemoryBytes: 24 << 30,
		FilesystemTotalBytes: 460 << 30, FilesystemAvailableBytes: 130 << 30,
	})}
	run.observation.Phases[0] = succeededPhase("preflight", PhaseMetrics{
		WallMS: frozenSafety.MaximumTotalWallMS,
	})
	if err := run.enforceSafety(); err != nil {
		t.Fatalf("exact wall ceiling failed: %v", err)
	}
	run.observation.Phases[1] = succeededPhase("cold", PhaseMetrics{WallMS: 1})
	if err := run.enforceSafety(); !errors.Is(err, errReviewCeiling) {
		t.Fatalf("wall overflow = %v", err)
	}
	run.observation.Phases[1] = succeededPhase("cold", PhaseMetrics{
		PeakRSSBytes: frozenSafety.MaximumPeakRSSBytes + 1,
	})
	if err := run.enforceSafety(); !errors.Is(err, errReviewCeiling) {
		t.Fatalf("RSS overflow = %v", err)
	}
	run.observation.Phases[1] = succeededPhase("cold", PhaseMetrics{
		DataAllocatedBytes: frozenSafety.MaximumDataAllocatedBytes + 1,
	})
	if err := run.enforceSafety(); !errors.Is(err, errReviewCeiling) {
		t.Fatalf("allocation overflow = %v", err)
	}
}
