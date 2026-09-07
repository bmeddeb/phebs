package t421

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"slices"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

const executionAttemptSchema = "t422-phase-attempt-report-v1"
const executionAttemptPrefix = "exact attempt: "
const maxExecutionAttemptLine = store.MaxJobLifecycleReportSize + 1024

var errExecutionAttempts = errors.New("execution attempt observation incomplete")

type ExecutionAttemptCount struct {
	JobAttempts, Retries, MaxRetriesUnit uint64
	SourceBlobAttempts                   uint64
}

// Complete refers only to this post-join report subset. It proves no live
// ceiling enforcement, handler invocation, complete phase work, or admission.
type ExecutionAttemptObservation struct {
	Phases      [15]ExecutionAttemptCount
	Complete    bool
	SourceBound bool
}

type executionAttemptReport struct {
	Schema      string          `json:"schema"`
	Producer    uint32          `json:"producer"`
	Phase       uint32          `json:"phase"`
	InputSHA256 string          `json:"input_sha256"`
	Kind        string          `json:"kind"`
	Report      json.RawMessage `json:"report"`
}

// The genuine caller supplies only output whose native Wait joined the copy
// goroutines and its independently retained bootstrap input identity. Never
// inspect a live checkoutCommandOutput or infer pipe EOF from a PC01 ACK.
func observeExecutionAttempts(raw []byte, plan Plan, producer uint32, input [32]byte, joined bool) (out ExecutionAttemptObservation, err error) {
	if !joined || plan.Schema != PlanV3Schema || len(plan.PhaseOrder) != len(out.Phases) || len(plan.WorkEnvelope.Phases) != len(out.Phases) ||
		producer < 2 || producer > 6 || input == ([32]byte{}) || len(raw) > 64<<20 || plan.ProcessAccounting == nil ||
		len(plan.ProcessAccounting.DispatchBudgets) != len(out.Phases) || !slices.Equal(plan.PhaseOrder, frozenPhaseOrder()) {
		return out, errExecutionAttempts
	}
	for index, name := range plan.PhaseOrder {
		if plan.WorkEnvelope.Phases[index].Phase != name || plan.ProcessAccounting.DispatchBudgets[index].Phase != name {
			return out, errExecutionAttempts
		}
	}
	wantInput := "sha256:" + hex.EncodeToString(input[:])
	reader := bufio.NewReaderSize(bytes.NewReader(raw), maxExecutionAttemptLine)
	consumed := 0
	for {
		start := consumed
		line, readErr := reader.ReadSlice('\n')
		consumed += len(line)
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			if !out.SourceBound {
				return out, errExecutionAttempts
			}
			for _, phase := range out.Phases {
				if phase.Retries > phase.JobAttempts {
					return out, errExecutionAttempts
				}
			}
			out.Complete = true
			return out, nil
		}
		if source, err := observeSourceAttempt(line, plan, producer, wantInput, &out); source {
			if err != nil || readErr != nil {
				return out, errExecutionAttempts
			}
			continue
		}
		index := bytes.Index(line, []byte(executionAttemptPrefix))
		if bytes.Contains(line, []byte("job lifecycle: ")) || bytes.Contains(line, []byte("generation chunk lifecycle: ")) {
			return out, errExecutionAttempts // Missing selected-source binding is never a zero event.
		}
		if index >= 0 {
			if readErr != nil || index > 1024 {
				return out, errExecutionAttempts
			}
			var event executionAttemptReport
			if strictExecutionAttemptJSON(line[index+len(executionAttemptPrefix):len(line)-1], &event) != nil ||
				event.Schema != executionAttemptSchema || event.Producer != producer || event.InputSHA256 != wantInput ||
				!slices.Contains(executionProducerPhases(producer), event.Phase) {
				return out, errExecutionAttempts
			}
			count, parseErr := executionAttemptDelta(event, frozenExecutionRuntime(plan))
			if parseErr != nil {
				return out, parseErr
			}
			phase := &out.Phases[event.Phase-1]
			if count.JobAttempts > math.MaxUint64-phase.JobAttempts || count.Retries > math.MaxUint64-phase.Retries {
				return out, errExecutionAttempts
			}
			phase.JobAttempts += count.JobAttempts
			phase.Retries += count.Retries
			phase.MaxRetriesUnit = max(phase.MaxRetriesUnit, count.MaxRetriesUnit)
			if phase.JobAttempts > plan.WorkEnvelope.Phases[event.Phase-1].JobAttempts.Maximum || phase.MaxRetriesUnit > plan.WorkEnvelope.MaximumRetriesPerUnit {
				return out, errExecutionAttempts // Retain the actual first excess, not a clamped count.
			}
		}
		// Unrelated long lines do not acquire a new log admission limit or a
		// proportional allocation. A partial final line is always incomplete.
		long := errors.Is(readErr, bufio.ErrBufferFull)
		for errors.Is(readErr, bufio.ErrBufferFull) {
			line, readErr = reader.ReadSlice('\n')
			consumed += len(line)
		}
		// Scan the original immutable line without copying: markers split
		// across reader fragments must not turn into unrelated output.
		if long && (reservedSourceAttempt(raw[start:consumed]) || bytes.Contains(raw[start:consumed], []byte(executionAttemptPrefix)) || bytes.Contains(raw[start:consumed], []byte("job lifecycle: ")) || bytes.Contains(raw[start:consumed], []byte("generation chunk lifecycle: "))) {
			return out, errExecutionAttempts
		}
		if readErr != nil {
			return out, errExecutionAttempts
		}
	}
}

// Called once by first-epoch finish, after native Wait joined the output pump.
// A stable buffer still need not be lossless: overflow, sink refusal, native
// failure and protocol failure all preserve counts but prevent completeness.
func (run *ExecutionEpochOneRun) finishAttemptObservation(result *ExecutionEpochOneResult, failure error) error {
	if !result.RootJoined || run.output == nil {
		return ErrExecutionEpochOne // Do not inspect a possibly live buffer.
	}
	var err error
	result.Attempts, err = observeExecutionAttempts(run.output.buffer.Bytes(), run.flow.plan, run.producer(), run.attemptInput, true)
	var indexErr error
	result.IndexOffers, indexErr = observeExecutionIndexOffers(run.output.buffer.Bytes(), run.flow.plan, run.producer(), run.attemptInput, true, err == nil && failure == nil)
	if err != nil || indexErr != nil || failure != nil {
		result.Attempts.Complete = false
		result.IndexOffers.Complete = false
		return ErrExecutionEpochOne
	}
	return nil
}

func strictExecutionAttemptJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errExecutionAttempts
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errExecutionAttempts // Includes duplicate keys, omitted fields, and trailing content.
	}
	return nil
}

func executionAttemptDelta(event executionAttemptReport, runtime ExecutionRuntimeProfile) (out ExecutionAttemptCount, err error) {
	switch event.Kind {
	case "job":
		var report store.JobLifecycleReport
		if len(event.Report) > store.MaxJobLifecycleReportSize || strictExecutionAttemptJSON(event.Report, &report) != nil ||
			report.Schema != store.JobLifecycleSchema || report.JobID == "" || report.Target == "" || report.Attempt < 1 || uint64(report.Attempt) > runtime.StoreRunnerMaxAttempts ||
			report.QueueWaitMS < 0 || report.HandleMS < 0 || !executionAttemptJobKind(report.Kind) {
			return out, errExecutionAttempts
		}
		want := executionJobOutcome(report.Event)
		if report.Event == "failed" && (report.Outcome == "terminal" || report.Outcome == "attempts_exhausted") {
			want = report.Outcome
		}
		if want == "" || want != report.Outcome || report.Event == "requeued" && uint64(report.Attempt) >= runtime.StoreRunnerMaxAttempts {
			return out, errExecutionAttempts
		}
		if report.Event == "started" {
			out.JobAttempts = 1
		}
		if report.Event == "requeued" {
			out.Retries, out.MaxRetriesUnit = 1, uint64(report.Attempt)
		}
	case "chunk":
		var report generationscheduler.ChunkLifecycleReport
		if len(event.Report) > generationscheduler.MaxChunkLifecycleReportSize || strictExecutionAttemptJSON(event.Report, &report) != nil ||
			report.Schema != generationscheduler.ChunkLifecycleSchema || !validDigest(report.Identity) || !validDigest(report.Generation) || report.Attempt < 0 || uint64(report.Attempt) >= runtime.GenerationMaxAttempts ||
			report.DurationMS < 0 || !executionAttemptStage(report.Stage) {
			return out, errExecutionAttempts
		}
		if report.Event == "started" && report.Outcome == "running" && report.DurationMS == 0 {
			out.JobAttempts = 1
		} else if report.Event != "settled" || !executionAttemptSettled(report.Outcome) {
			return out, errExecutionAttempts
		}
		if report.Outcome == "retried" {
			if uint64(report.Attempt)+1 >= runtime.GenerationMaxAttempts {
				return out, errExecutionAttempts
			}
			out.Retries, out.MaxRetriesUnit = 1, uint64(report.Attempt)+1
		}
	default:
		return out, errExecutionAttempts
	}
	return out, nil
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
