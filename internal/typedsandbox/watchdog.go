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
	started       time.Time
	report        WatchdogReport
	stageObserved int64
}

func newWatchdogSnapshots(wall time.Duration) *watchdogSnapshots {
	s := &watchdogSnapshots{started: time.Now(), report: WatchdogReport{Schema: "phebs-typed-watchdog-partial-v1", ExitCode: 124, WallNanoseconds: wall.Nanoseconds()}}
	s.publish()
	return s
}

// Caller owns mu after initialization. No caller mutates a published byte slice.
func (s *watchdogSnapshots) publish() {
	s.report.SnapshotNanoseconds = min(time.Since(s.started).Nanoseconds(), s.report.WallNanoseconds)
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
	s.report.ResourceSampleNanoseconds = min(time.Since(s.started).Nanoseconds(), s.report.WallNanoseconds)
	s.publish()
}

func (s *watchdogSnapshots) progress(id byte, elapsed int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == 0 || int(id) >= len(progressStages) || elapsed < 0 || elapsed > s.report.WallNanoseconds || elapsed < s.report.WorkerStageStartedNanoseconds || len(s.report.CompletedStages) >= 15 {
		s.report.WorkerProgressAvailable = false
		s.publish()
		return false
	}
	if s.report.WorkerProgressAvailable {
		s.report.CompletedStages = append(s.report.CompletedStages, WatchdogTiming{s.report.WorkerStage, elapsed - s.report.WorkerStageStartedNanoseconds})
	}
	s.report.WorkerProgressAvailable = true
	s.report.WorkerStage = progressStages[id]
	s.report.WorkerStageStartedNanoseconds = elapsed
	s.stageObserved = min(time.Since(s.started).Nanoseconds(), s.report.WallNanoseconds)
	s.publish()
	return true
}

func decodeWatchdog(raw []byte, wall time.Duration) (*WatchdogReport, error) {
	var r WatchdogReport
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > watchdogBytes || d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.Schema != "phebs-typed-watchdog-partial-v1" || r.ExitCode != 124 || r.WallNanoseconds != wall.Nanoseconds() || r.SnapshotNanoseconds < 0 || r.SnapshotNanoseconds > r.WallNanoseconds || r.ResourceSampleNanoseconds < 0 || r.ResourceSampleNanoseconds > r.SnapshotNanoseconds || r.WorkerStageStartedNanoseconds < 0 || r.WorkerStageStartedNanoseconds > r.WallNanoseconds || len(r.CompletedStages) > 15 {
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
