package t421

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	handoffRetentionPrefix = "exact retention turn: "
	handoffCleanupPrefix   = "exact selector cleanup turn: "
	handoffLogTimestamp    = "2006/01/02 15:04:05 "
	maxHandoffEventBytes   = 4096
)

// Native synchronous reports, decoded only after the owning process and its
// output pump join. A clean scan is not a successful command or accepted phase.
// Cleanup reports are cumulative; each slot keeps the last actual prefix.
type ExecutionHandoffObservation struct {
	Cleanup      [15]SelectorCleanupEvidence
	Retention    [2]epochRetentionSweep
	ScanComplete bool
}

type executionRetentionEvent struct {
	Schema string `json:"schema"`
	Epoch  uint64 `json:"epoch"`
	Phase  uint32 `json:"phase"`
	epochRetentionSweep
}

func reservedHandoffEvent(line []byte) bool {
	return bytes.Contains(line, []byte("exact retention turn:")) || bytes.Contains(line, []byte("exact selector cleanup turn:")) ||
		bytes.Contains(line, []byte("t422-retention-turn-")) || bytes.Contains(line, []byte(SelectorHandoffCleanupSchema))
}

func observeHandoffEvent(line []byte, plan Plan, producer uint32, input string, sourceBound bool, out *ExecutionHandoffObservation) (bool, error) {
	// Historical plans retain their existing interpretation of these logs.
	if plan.Schema != PlanV5Schema || !reservedHandoffEvent(line) {
		return false, nil
	}
	if !sourceBound || len(line) <= len(handoffLogTimestamp) || len(line) > maxHandoffEventBytes || line[len(line)-1] != '\n' {
		return true, errExecutionAttempts
	}
	stamp := string(line[:len(handoffLogTimestamp)])
	parsed, err := time.Parse(handoffLogTimestamp, stamp)
	if err != nil || parsed.Format(handoffLogTimestamp) != stamp {
		return true, errExecutionAttempts
	}
	raw := line[len(handoffLogTimestamp) : len(line)-1]
	switch {
	case bytes.HasPrefix(raw, []byte(handoffRetentionPrefix)):
		raw = raw[len(handoffRetentionPrefix):]
		var value executionRetentionEvent
		if !decodeCanonicalHandoff(raw, &value) || producer != 2 || value.Schema != "t422-retention-turn-v1" ||
			value.Epoch != 1 || value.Phase != 4 || value.Attempt == 0 || value.Attempt > 2 ||
			value.Scanned < 0 || value.Deleted < 0 ||
			value.Completeness != "exact" && value.Completeness != "lower_bound" && value.Completeness != "unavailable" ||
			out.Retention[value.Attempt-1] != (epochRetentionSweep{}) ||
			value.Attempt == 2 && (out.Retention[0] != (epochRetentionSweep{Attempt: 1, Completeness: "exact"})) || out.Cleanup[3].Turns != 0 {
			return true, errExecutionAttempts
		}
		out.Retention[value.Attempt-1] = value.epochRetentionSweep
		// Positive native excess survives refusal; no successful zero is inferred.
		if uint64(value.Deleted) > lifecycleDeleteLimit(plan.Schema, lifecycle.SearchOwner) || value.Scanned > lifecycle.MaxCandidatesPerTick {
			return true, errExecutionAttempts
		}
		return true, nil
	case bytes.HasPrefix(raw, []byte(handoffCleanupPrefix)):
		raw = raw[len(handoffCleanupPrefix):]
		var value SelectorCleanupEvidence
		if !decodeCanonicalHandoff(raw, &value) || value.Phase == 0 || value.Phase > uint32(len(out.Cleanup)) ||
			len(plan.PhaseOrder) != len(out.Cleanup) || value.Schema != SelectorHandoffCleanupSchema ||
			value.InputSHA256 != input || !validDigest(value.SelectedRuntimeSHA256) {
			return true, errExecutionAttempts
		}
		bound, ok := SelectorHandoffCleanupForPhase(plan, plan.PhaseOrder[value.Phase-1])
		if !ok || uint64(producer) != bound.ServerEpoch+1 {
			return true, errExecutionAttempts
		}
		prior := out.Cleanup[value.Phase-1]
		if prior.Done || prior.Failed || prior.Turns == math.MaxUint64 || value.Turns != prior.Turns+1 ||
			prior.Turns != 0 && value.SelectedRuntimeSHA256 != prior.SelectedRuntimeSHA256 ||
			value.Deleted < prior.Deleted || value.StoreReadAttempts < prior.StoreReadAttempts || value.StoreWriteAttempts < prior.StoreWriteAttempts {
			return true, errExecutionAttempts
		}
		for index := int(value.Phase); index < len(out.Cleanup); index++ {
			if out.Cleanup[index].Turns != 0 {
				return true, errExecutionAttempts
			}
		}
		deleted := value.Deleted - prior.Deleted
		reads, writes := value.StoreReadAttempts-prior.StoreReadAttempts, value.StoreWriteAttempts-prior.StoreWriteAttempts
		if value.MaxDeleted != max(prior.MaxDeleted, deleted) {
			return true, errExecutionAttempts
		}
		out.Cleanup[value.Phase-1] = value
		if value.Turns > bound.OwnerTurns.Maximum || value.Deleted > bound.LifecycleDeleted.Maximum ||
			deleted > plan.SelectorHandoffCleanup.MaximumDeletesPerTurn || reads > store.ServiceStateV3PreimageHandoffStoreReadMaximum || writes > store.ServiceStateV3PreimageHandoffStoreWriteMaximum ||
			value.StoreReadAttempts > bound.StoreReadAttempts.Maximum || value.StoreWriteAttempts > bound.StoreWriteAttempts.Maximum {
			return true, errExecutionAttempts
		}
		if !value.Failed && (value.Done && validateSelectorCleanupObservation(plan, value, bound, input, value.SelectedRuntimeSHA256) != nil ||
			!value.Done && (deleted != plan.SelectorHandoffCleanup.MaximumDeletesPerTurn || reads != 7 || writes != 1)) {
			return true, errExecutionAttempts
		}
		return true, nil
	default:
		return true, errExecutionAttempts
	}
}

func decodeCanonicalHandoff(raw []byte, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return false
	}
	canonical, err := json.Marshal(value)
	return err == nil && bytes.Equal(canonical, raw)
}

func executionHandoffPhaseClosed(plan Plan, record executionJoinedWorkRecord, index int) bool {
	value := record.Attempts.Handoff
	if plan.Schema != PlanV5Schema {
		return value == (ExecutionHandoffObservation{})
	}
	if index < 0 || index >= len(value.Cleanup) || !value.ScanComplete || len(plan.PhaseOrder) != len(value.Cleanup) {
		return false
	}
	cleanup := value.Cleanup[index]
	bound, selected := SelectorHandoffCleanupForPhase(plan, plan.PhaseOrder[index])
	if selected {
		input := "sha256:" + hex.EncodeToString(record.Input[:])
		if record.Input == ([32]byte{}) || uint64(record.Producer) != bound.ServerEpoch+1 ||
			validateSelectorCleanupObservation(plan, cleanup, bound, input, cleanup.SelectedRuntimeSHA256) != nil {
			return false
		}
	} else if cleanup != (SelectorCleanupEvidence{}) {
		return false
	}
	if record.Producer == 2 && index == 3 {
		return value.Retention == ([2]epochRetentionSweep{{Attempt: 1, Completeness: "exact"}, {Attempt: 2, Completeness: "exact"}})
	}
	return true
}

func validExecutionHandoffRecord(plan Plan, record executionJoinedWorkRecord) bool {
	value := record.Attempts.Handoff
	if plan.Schema != PlanV5Schema {
		return value == (ExecutionHandoffObservation{})
	}
	if !value.ScanComplete || executionWorkProducerByte(record.Producer) == 0 || record.Producer != 2 && value.Retention != ([2]epochRetentionSweep{}) {
		return false
	}
	owned := [15]bool{}
	for _, phase := range executionProducerPhases(record.Producer) {
		owned[phase-1] = true
		if !executionHandoffPhaseClosed(plan, record, int(phase-1)) {
			return false
		}
	}
	for index, present := range owned {
		if !present && value.Cleanup[index] != (SelectorCleanupEvidence{}) {
			return false
		}
	}
	return true
}
