package typedsandbox

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const watchdogBytes = 4096

var progressStages = [...]string{"", "profile", "planning", "package-load/typecheck", "indexer-unlocalized", "validation", "containment/measurement", "tool-verification", "materialization", "compiler-setup", "workspace-verification", "go-file-sealing"}

// WatchdogReport is diagnostic only. Ages are relative to the fixed deadline,
// not a claim about scheduler latency or the moment the kernel exits PID 1.
// Resources are the last completed cumulative sample, never a partial sample.
type WatchdogReport struct {
	Invocation                       string           `json:"invocation,omitempty"`
	Schema                           string           `json:"schema"`
	ExitCode                         int              `json:"exit_code"`
	WallNanoseconds                  int64            `json:"wall_nanoseconds"`
	SnapshotNanoseconds              int64            `json:"snapshot_nanoseconds"`
	ResourceSampleNanoseconds        int64            `json:"resource_sample_nanoseconds"`
	ResourceAgeAtDeadlineNanoseconds int64            `json:"resource_age_at_deadline_nanoseconds"`
	ResourceSampleAvailable          bool             `json:"resource_sample_available"`
	Resources                        Resources        `json:"resources"`
	WorkerProgressAvailable          bool             `json:"worker_progress_available"`
	WorkerStage                      string           `json:"worker_stage,omitempty"`
	WorkerStageStartedNanoseconds    int64            `json:"worker_stage_started_nanoseconds"`
	StageAgeAtDeadlineNanoseconds    int64            `json:"stage_age_at_deadline_nanoseconds"`
	CompletedStages                  []WatchdogTiming `json:"completed_stages,omitempty"`
}

type WatchdogTiming struct {
	Stage       string `json:"stage"`
	Nanoseconds int64  `json:"nanoseconds"`
}

type watchdogFrame struct{ data []byte }

type watchdogSnapshots struct {
	// Publication may block; the watchdog reads only the atomic encoded frame.
	mu            sync.Mutex
	frame         atomic.Pointer[watchdogFrame]
	elapsed       func() (int64, error)
	failed        atomic.Bool
	report        WatchdogReport
	stageObserved int64
	workerElapsed int64
}

func newWatchdogSnapshots(wall time.Duration) *watchdogSnapshots {
	started := time.Now()
	return newWatchdogClock(wall, func() (int64, error) { return time.Since(started).Nanoseconds(), nil })
}
func newWatchdogClock(wall time.Duration, elapsed func() (int64, error)) *watchdogSnapshots {
	s := &watchdogSnapshots{elapsed: elapsed, report: WatchdogReport{Schema: "phebs-typed-watchdog-partial-v3", ExitCode: 124, WallNanoseconds: wall.Nanoseconds()}}
	s.publish()
	return s
}
func (s *watchdogSnapshots) now() (int64, bool) {
	n, err := s.elapsed()
	if s.failed.Load() || err != nil || n < 0 || n < s.report.SnapshotNanoseconds {
		s.failed.Store(true)
		s.frame.Store(nil)
		return 0, false
	}
	return min(n, s.report.WallNanoseconds), true
}

// Caller owns mu after initialization. No caller mutates a published byte slice.
func (s *watchdogSnapshots) publish() {
	now, ok := s.now()
	if !ok {
		return
	}
	s.report.SnapshotNanoseconds = now
	if s.report.ResourceSampleAvailable {
		s.report.ResourceAgeAtDeadlineNanoseconds = s.report.WallNanoseconds - s.report.ResourceSampleNanoseconds
	}
	if s.report.WorkerProgressAvailable {
		s.report.StageAgeAtDeadlineNanoseconds = s.report.WallNanoseconds - s.stageObserved
	}
	raw, err := json.Marshal(s.report)
	if err == nil && len(raw)+1 <= watchdogBytes {
		s.frame.Store(&watchdogFrame{append(raw, '\n')})
	}
}

func (s *watchdogSnapshots) resources(r Resources) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.Resources = r
	s.report.ResourceSampleAvailable = true
	now, ok := s.now()
	if !ok {
		return
	}
	s.report.ResourceSampleNanoseconds = now
	s.publish()
}

func (s *watchdogSnapshots) progress(id byte, elapsed int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == 0 || int(id) >= len(progressStages) || elapsed < 0 || elapsed > s.report.WallNanoseconds || elapsed < s.workerElapsed || len(s.report.CompletedStages) >= 15 {
		s.report.WorkerProgressAvailable = false
		s.publish()
		return false
	}
	now, ok := s.now()
	if !ok {
		return false
	}
	if s.report.WorkerProgressAvailable {
		s.report.CompletedStages = append(s.report.CompletedStages, WatchdogTiming{s.report.WorkerStage, now - s.report.WorkerStageStartedNanoseconds})
	}
	s.report.WorkerProgressAvailable = true
	s.report.WorkerStage = progressStages[id]
	s.workerElapsed = elapsed
	s.report.WorkerStageStartedNanoseconds = now
	s.stageObserved = now
	s.publish()
	return true
}

func decodeWatchdog(raw []byte, wall time.Duration) (*WatchdogReport, error) {
	var r WatchdogReport
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > watchdogBytes || d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.Schema != "phebs-typed-watchdog-partial-v3" || r.ExitCode != 124 || r.WallNanoseconds != wall.Nanoseconds() || r.SnapshotNanoseconds < 0 || r.SnapshotNanoseconds > r.WallNanoseconds || r.ResourceSampleNanoseconds < 0 || r.ResourceSampleNanoseconds > r.SnapshotNanoseconds || r.WorkerStageStartedNanoseconds < 0 || r.WorkerStageStartedNanoseconds > r.WallNanoseconds || len(r.CompletedStages) > 15 {
		return nil, ErrExecution
	}
	if r.ResourceSampleAvailable {
		if r.ResourceAgeAtDeadlineNanoseconds != r.WallNanoseconds-r.ResourceSampleNanoseconds {
			return nil, ErrExecution
		}
	} else if r.ResourceSampleNanoseconds != 0 || r.ResourceAgeAtDeadlineNanoseconds != 0 || r.Resources != (Resources{}) {
		return nil, ErrExecution
	}
	if r.WorkerStage != "" && !watchdogStage(r.WorkerStage) || r.WorkerProgressAvailable && r.WorkerStage == "" || r.StageAgeAtDeadlineNanoseconds < 0 || r.StageAgeAtDeadlineNanoseconds > r.WallNanoseconds || !slices.Contains([]string{"", "kernel_events", "memory_peak", "scratch_space", "shared_memory_space", "process_inventory", "process_stat_read", "process_stat_shape", "process_count"}, r.Resources.SamplingFailureStage) {
		return nil, ErrExecution
	}
	var total int64
	for _, timing := range r.CompletedStages {
		if !watchdogStage(timing.Stage) || timing.Nanoseconds < 0 || timing.Nanoseconds > r.WorkerStageStartedNanoseconds-total {
			return nil, ErrExecution
		}
		total += timing.Nanoseconds
	}
	return &r, nil
}

func watchdogStage(stage string) bool {
	for _, s := range progressStages[1:] {
		if stage == s {
			return true
		}
	}
	return false
}

func (s *watchdogSnapshots) invocation(digest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.Invocation = digest
	s.publish()
}
func decodeInvocationWatchdog(raw []byte, o Options) (*WatchdogReport, error) {
	var r WatchdogReport
	if len(raw) > watchdogBytes || json.Unmarshal(raw, &r) != nil || r.Invocation != invocationDigest(o.Allowance, o.Control.Phase, o.Control.RequestDigest, o.Control.SealDigest) || r.WallNanoseconds <= 0 || r.WallNanoseconds > int64(WallLimit) {
		return nil, ErrExecution
	}
	return decodeWatchdog(raw, time.Duration(r.WallNanoseconds))
}
