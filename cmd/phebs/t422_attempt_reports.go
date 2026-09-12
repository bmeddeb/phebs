package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// These accepted report limits are not the store's generation retry capacity.
const (
	t422JobAcceptedAttempts   = 3
	t422ChunkAcceptedAttempts = 5
)

var errT422AttemptReport = errors.New("T42.2 phase attempt report unavailable")

type t422AttemptSinks struct {
	job   store.JobLifecycleSink
	chunk func([]byte) error
}

// One binding precedes all selected workers. Each valid native report is still
// checked, but only the four nonzero metric events write five bytes. Native
// schemas/units stay unchanged; ordinary/T40 and candidate reporting bypass this.
func newT422AttemptSinks(fail func(error)) (*t422AttemptSinks, error) {
	if !dispatchadmission.ProductionSemanticSelected() {
		return nil, nil
	}
	initial, err := dispatchadmission.ProductionSemanticState()
	if err != nil || fail == nil {
		return nil, errT422AttemptReport
	}
	writer, ok := log.Writer().(*os.File)
	if !ok || writer != os.Stderr {
		return nil, errT422AttemptReport
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422AttemptReport
	}
	binding, err := t422SourceBinding(initial)
	if err != nil {
		return nil, err
	}
	binding = bytes.Replace(binding, []byte("SRB1:"), []byte("ATB1:"), 1)
	write := func(raw []byte) error {
		n, err := writer.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		if err != nil {
			fail(errT422AttemptReport)
			return errT422AttemptReport
		}
		return nil
	}
	if err := write(binding); err != nil {
		return nil, err
	}
	sink := func(kind string) func([]byte) error {
		return func(raw []byte) error {
			current, err := dispatchadmission.ProductionSemanticState()
			var record [5]byte
			if err == nil {
				record, err = t422AttemptRecord(current, initial, kind, raw)
			}
			if err != nil {
				fail(errT422AttemptReport)
				return errT422AttemptReport
			}
			if record == ([5]byte{}) {
				return nil
			}
			return write(record[:])
		}
	}
	return &t422AttemptSinks{job: sink("job"), chunk: sink("chunk")}, nil
}

func (sinks *t422AttemptSinks) bindJobs(runners ...*store.Runner) {
	if sinks == nil {
		return
	}
	for _, runner := range runners {
		if runner != nil {
			runner.LifecycleReports = sinks.job
		}
	}
}

func (sinks *t422AttemptSinks) bindChunk(scheduler *generationscheduler.Scheduler) {
	if sinks != nil {
		scheduler.ChunkReports = sinks.chunk
	}
}

func t422AttemptRecord(current, initial dispatchadmission.ProductionSemanticSnapshot, kind string, raw []byte) ([5]byte, error) {
	if _, err := t422SourceRecord(current, initial); err != nil {
		return [5]byte{}, err
	}
	opcode, depth, err := t422NativeAttempt(kind, raw)
	if err != nil {
		return [5]byte{}, err
	}
	if opcode == 0 {
		return [5]byte{}, nil
	}
	return [5]byte{'A', "0123456789ABCDEF"[current.Phase], opcode, '0' + depth, '\n'}, nil
}

func strictT422AttemptJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errT422AttemptReport
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errT422AttemptReport // Includes duplicate keys, omitted fields, and trailing content.
	}
	return nil
}

func t422NativeAttempt(kind string, raw []byte) (opcode, depth byte, err error) {
	var out [2]byte
	// These are the existing selected accepted-report limits, not the
	// store's retry capacity or the receipt's five-retry safety ceiling.
	switch kind {
	case "job":
		var report store.JobLifecycleReport
		if len(raw) > store.MaxJobLifecycleReportSize || strictT422AttemptJSON(raw, &report) != nil ||
			report.Schema != store.JobLifecycleSchema || report.JobID == "" || report.Target == "" || report.Attempt < 1 || uint64(report.Attempt) > t422JobAcceptedAttempts ||
			report.QueueWaitMS < 0 || report.HandleMS < 0 || !executionAttemptJobKind(report.Kind) {
			return 0, 0, errT422AttemptReport
		}
		want := executionJobOutcome(report.Event)
		if report.Event == "failed" && (report.Outcome == "terminal" || report.Outcome == "attempts_exhausted") {
			want = report.Outcome
		}
		if want == "" || want != report.Outcome || report.Event == "requeued" && uint64(report.Attempt) >= t422JobAcceptedAttempts {
			return 0, 0, errT422AttemptReport
		}
		if report.Event == "started" {
			out = [2]byte{'j', byte(report.Attempt)}
		}
		if report.Event == "requeued" {
			out = [2]byte{'r', byte(report.Attempt)}
		}
	case "chunk":
		var report generationscheduler.ChunkLifecycleReport
		if len(raw) > generationscheduler.MaxChunkLifecycleReportSize || strictT422AttemptJSON(raw, &report) != nil ||
			report.Schema != generationscheduler.ChunkLifecycleSchema || !t422AttemptDigest(report.Identity) || !t422AttemptDigest(report.Generation) || report.Attempt < 0 || uint64(report.Attempt) >= t422ChunkAcceptedAttempts ||
			report.DurationMS < 0 || !executionAttemptStage(report.Stage) {
			return 0, 0, errT422AttemptReport
		}
		if report.Event == "started" && report.Outcome == "running" && report.DurationMS == 0 {
			out = [2]byte{'c', byte(report.Attempt)}
		} else if report.Event != "settled" || !executionAttemptSettled(report.Outcome) {
			return 0, 0, errT422AttemptReport
		}
		if report.Outcome == "retried" {
			if uint64(report.Attempt)+1 >= t422ChunkAcceptedAttempts {
				return 0, 0, errT422AttemptReport
			}
			out = [2]byte{'t', byte(report.Attempt + 1)}
		}
	default:
		return 0, 0, errT422AttemptReport
	}
	return out[0], out[1], nil
}

func executionAttemptJobKind(kind store.JobKind) bool {
	switch kind {
	case store.JobSync, store.JobIndex, store.JobFetch, store.JobCandidate, store.JobExtract, store.JobResolverCatalog, store.JobCallerLeaf:
		return true
	}
	return false
}

func executionJobOutcome(event string) string {
	switch event {
	case "claimed":
		return "claimed"
	case "started":
		return "running"
	case "done":
		return "success"
	case "released":
		return "canceled"
	case "deferred":
		return "dependency"
	case "yielded":
		return "yield"
	case "requeued":
		return "retryable"
	}
	return ""
}

func executionAttemptStage(stage string) bool {
	switch stage {
	case observationpublication.PlanningScheduleStage, observationpublication.InventoryScheduleStageV2, observationpublication.ScheduleStage,
		extractionpublication.ScheduleStage, relationshippublication.ScheduleStage, relationshippublication.ScheduleStageV3,
		store.ServiceStateV3ReconcileStage, store.ServiceStateV3ActivateStage:
		return true
	}
	return false
}

func executionAttemptSettled(outcome string) bool {
	switch outcome {
	case "handler_failed", "heartbeat_failed", "stale_fenced", "released", "release_failed", "pre_heartbeat_failed", "completed", "completion_failed",
		"terminal", "terminal_record_failed", "deferred", "deferral_failed", "retried", "exhausted":
		return true
	}
	return false
}

func t422AttemptDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}
