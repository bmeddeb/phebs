package t421

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"slices"

	"github.com/bmeddeb/phebs/internal/store"
)

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
	Phases       [15]ExecutionAttemptCount
	Complete     bool
	SourceBound  bool
	AttemptBound bool
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
			if !out.SourceBound || !out.AttemptBound {
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
		if attempt, err := observeCompactAttempt(line, plan, producer, wantInput, &out); attempt {
			if err != nil || readErr != nil {
				return out, errExecutionAttempts
			}
			continue
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
		if long && (reservedSourceAttempt(raw[start:consumed]) || reservedCompactAttempt(raw[start:consumed])) {
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
