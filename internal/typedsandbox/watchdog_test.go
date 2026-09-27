package typedsandbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWatchdogSnapshots(t *testing.T) {
	var observed int64
	s := newWatchdogClock(WallLimit, func() (int64, error) { return observed, nil })
	initial, err := decodeWatchdog(s.frame.Load().data, WallLimit)
	if err != nil || initial.WorkerProgressAvailable || initial.ResourceSampleAvailable {
		t.Fatal(initial, err)
	}
	r := Resources{Samples: 1, LimitsVerified: true, SampledPeakRSSBytes: 42}
	s.resources(r)
	for i, id := range []byte{1, 2, 6} {
		observed = int64(i) * int64(time.Second)
		if !s.progress(id, observed) {
			t.Fatal("valid stages refused")
		}
	}
	frame := s.frame.Load()
	got, err := decodeWatchdog(frame.data, WallLimit)
	if err != nil || got.Resources != r || got.WorkerStage != "containment/measurement" || len(got.CompletedStages) != 2 || got.CompletedStages[1].Stage != "planning" || got.CompletedStages[1].Nanoseconds != int64(time.Second) || got.ResourceAgeAtDeadlineNanoseconds != int64(WallLimit)-got.ResourceSampleNanoseconds {
		t.Fatal(got, err)
	}
	// A later partial observation changes only its local value, never the frame.
	r.SampledPeakRSSBytes = 99
	if string(frame.data) != string(s.frame.Load().data) {
		t.Fatal("published frame mutated")
	}
	if s.progress(255, 3) {
		t.Fatal("unknown stage accepted")
	}
	got, err = decodeWatchdog(s.frame.Load().data, WallLimit)
	if err != nil || got.WorkerProgressAvailable || got.Resources.SampledPeakRSSBytes != 42 {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*WatchdogReport){
		func(r *WatchdogReport) { r.ExitCode = 0 },
		func(r *WatchdogReport) { r.WorkerProgressAvailable = false; r.WorkerStage = "arbitrary" },
		func(r *WatchdogReport) { r.Resources.SamplingFailureStage = "private text" }, func(r *WatchdogReport) { r.Schema = "PASS" },
		func(r *WatchdogReport) { r.WallNanoseconds++ }, func(r *WatchdogReport) { r.ResourceAgeAtDeadlineNanoseconds++ },
		func(r *WatchdogReport) { r.WorkerProgressAvailable = true; r.WorkerStage = "arbitrary" },
		func(r *WatchdogReport) { r.CompletedStages[0].Nanoseconds = r.WallNanoseconds + 1 },
	} {
		copyRaw := append([]byte(nil), frame.data...)
		var bad WatchdogReport
		if json.Unmarshal(copyRaw, &bad) != nil {
			t.Fatal("invalid fixture")
		}
		mutate(&bad)
		raw, _ := json.Marshal(bad)
		if _, err := decodeWatchdog(raw, WallLimit); err == nil {
			t.Fatal("accepted forged diagnostic", string(raw))
		}
	}
	if _, err := decodeWatchdog([]byte(strings.Repeat(" ", watchdogBytes+1)), WallLimit); err == nil {
		t.Fatal("oversize")
	}
}

func TestWatchdogControllerPartialCannotPass(t *testing.T) {
	for _, fault := range []string{"watchdog only", "watchdog mixed stdout"} {
		t.Run(fault, func(t *testing.T) {
			_, o := fakeDaemon(t, fault)
			got, err := runFake(context.Background(), o)
			if err == nil || !got.Removed || got.ExitCode != 124 || got.Watchdog == nil || got.Watchdog.WorkerStage != "planning" || got.Resources.Samples != 1 || len(got.Stdout) != 0 || got.StopReason != "wall_limit" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestWatchdogMaximumFrame(t *testing.T) {
	var observed int64
	s := newWatchdogClock(WallLimit, func() (int64, error) { return observed, nil })
	s.invocation(testImage)
	s.resources(Resources{MemoryOOMEvents: ^uint64(0), MemoryOOMKills: ^uint64(0), MemoryLimitEvents: ^uint64(0), TaskLimitEvents: ^uint64(0), SamplingUnavailable: true, SamplingFailureStage: "shared_memory_space", LimitsVerified: true, MemoryPeakBytes: ^uint64(0), SampledPeakRSSBytes: ^uint64(0), SampledPeakProcesses: ^uint64(0), SampledPeakScratchBytes: ^uint64(0), SampledPeakScratchInodes: ^uint64(0), Samples: ^uint64(0), PerProcessDescriptors: ^uint64(0), AggregateDescriptorCeiling: ^uint64(0)})
	for i := 0; i < 16; i++ {
		observed = int64(i) * int64(time.Second)
		if !s.progress(6, int64(i)*int64(time.Second)) {
			t.Fatal("bounded stage count refused")
		}
	}
	frame := s.frame.Load().data
	r, err := decodeWatchdog(frame, WallLimit)
	if err != nil || len(frame) > watchdogBytes || len(r.CompletedStages) != 15 || r.WorkerStageStartedNanoseconds != int64(15*time.Second) {
		t.Fatal("snapshot silently lost at maximum size", len(frame), err)
	}
}
