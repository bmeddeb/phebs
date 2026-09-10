//go:build darwin

package t421

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// The existing native collector can census a controller and its sibling
// session roots together. This uses real test-binary processes, not admitted
// executor/server/archive images or a whole-ceremony measurement lifecycle.
func TestProcessObservationNativeSiblingSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var children [2]*exec.Cmd
	var joined [2]bool
	for i := range children {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecutionProcessSessionHelper$")
		command.Env = []string{"PHEBS_EXECUTION_SESSION_HELPER=child", "GORACE=atexit_sleep_ms=0"}
		prepareProductionSession(command)
		ready, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			_ = ready.Close()
			t.Fatal(err)
		}
		children[i] = command
		t.Cleanup(func() {
			if !joined[i] {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
			_ = ready.Close()
			if err := t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(6*time.Second)); err != nil {
				t.Error(err)
			}
		})
		var marker [1]byte
		if _, err := io.ReadFull(ready, marker[:]); err != nil || marker[0] != 1 {
			t.Fatal("native sibling readiness", err)
		}
	}
	rows, err := t4013.ObserveProcessTreeRecords(ctx, os.Getpid())
	if err != nil || len(rows) != 3 {
		t.Fatal("native controller/sibling census", len(rows), err)
	}
	name := filepath.Base(os.Args[0])
	name = name[:min(len(name), 16)]
	gauge, err := NewProcessObservationGauge(os.Getpid(), rows[0].StartIdentity, map[string]string{name: "controller"})
	if err != nil {
		t.Fatal(err)
	}
	// Retain the actual probe return for this assertion; no supplied rows or
	// separately measured RSS peaks are substituted into the production gauge.
	gauge.probe = func(ctx context.Context, pid int) ([]t4013.NativeProcessRecord, error) {
		rows, err = t4013.ObserveProcessTreeRecords(ctx, pid)
		return rows, err
	}
	first, err := gauge.Sample(ctx)
	if err != nil || !first.Available || first.ObservedDescendants != 2 {
		t.Fatal("one census omitted a sibling session", first, err)
	}
	var sum uint64
	seen := make(map[int]bool)
	for _, row := range rows {
		sum += uint64(row.RSSBytes)
		seen[row.PID] = true
	}
	if !seen[children[0].Process.Pid] || !seen[children[1].Process.Pid] || first.ObservedRSSBytes != sum {
		t.Fatal("native whole-root RSS does not equal its actual census sum", first)
	}
	if err := children[0].Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := children[0].Wait(); err == nil {
		t.Fatal("killed test child returned successful native Wait")
	}
	joined[0] = true
	second, err := gauge.Sample(ctx)
	if err != nil || !second.Available || second.ObservedDescendants != 1 || second.CompletedCensuses != 2 ||
		second.ObservedDescendantsHighWater != 2 || second.ObservedRSSHighWaterBytes != max(first.ObservedRSSBytes, second.ObservedRSSBytes) {
		t.Fatal("joined sibling erased or added an earlier sampled peak", second, err)
	}
}
