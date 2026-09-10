//go:build darwin

package lifecycle

import (
	"context"
	"github.com/bmeddeb/phebs/internal/custodybytes"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Real linked-file measurements at all five native runner/capacity seams;
// phase9 and the capacity probe are supplied fixture state, not ceremony proof.
func TestCapacityCheckpointActualWorkspaceAllSites(t *testing.T) {
	var calls []string
	owners := []Owner{recordingOwner{name: "a", calls: &calls}}
	runner := newControlledTestRunner(t, owners, nil)
	collector := testControlCollector(t, owners, 4)
	rootPath := filepath.Join(t.TempDir(), "custody")
	if err := os.Mkdir(rootPath, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	path, err := filepath.EvalSymlinks(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "native"), []byte("actual"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var stat unix.Statfs_t
	if err = unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	observer := custodybytes.NewBorrowed(file, path, info, stat.Fsid.Val)
	var sampled atomic.Int64
	if err = collector.SetCapacityCheckpoint(func(ctx context.Context) error {
		_, e := observer.Sample(ctx, 9)
		if e == nil {
			sampled.Add(1)
		} else {
			t.Errorf("actual checkpoint walk: %v", e)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.control.DriveNormal(runner.ctx, collector); err != nil {
		t.Fatal(err)
	}
	runner.used.Store(800)
	if result, err := runner.control.ReadPressure80Collect(runner.ctx, collector, time.Now()); err != nil || result.Capacity.Pressure != PressureCollect {
		t.Fatalf("80 = %+v / %v", result, err)
	}
	runner.used.Store(900)
	if result, err := runner.control.ReadPressure90Refusal(runner.ctx, collector, time.Now()); err != nil || result.Capacity.Pressure != PressureRefuse {
		t.Fatalf("90 = %+v / %v", result, err)
	}
	runner.used.Store(750)
	if result, err := runner.control.ReadPressure75Refusal(runner.ctx, collector, time.Now()); err != nil || result.Capacity.Pressure != PressureRefuse {
		t.Fatalf("75 = %+v / %v", result, err)
	}
	if runner.probes.Load() != 4 || len(calls) != 1 {
		t.Fatalf("pressure added a turn: %v / %d", calls, runner.probes.Load())
	}
	runner.used.Store(700)
	result, err := runner.control.DrivePressure75Recovery(runner.ctx, collector, time.Now())
	// First turn supplies the required preceding exact-normal capacity; the
	// second is the clean cycle. No Await goroutine or extra manual probe.
	if err != nil || result.OwnerTurns != 2 {
		t.Fatalf("recovery = %+v / %v", result, err)
	}
	if result, err := runner.control.ReadPressure75Normal(runner.ctx, collector); err != nil || result.Capacity.Pressure != PressureNormal {
		t.Fatalf("normal = %+v / %v", result, err)
	}
	if runner.probes.Load() != 7 || len(calls) != 3 || sampled.Load() != 7 || observer.Snapshot().Phases[8].Maximum.LogicalBytes != 6 {
		t.Fatal("checkpoint coverage", runner.probes.Load(), sampled.Load(), observer.Snapshot())
	}
}
