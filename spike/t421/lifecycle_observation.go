package t421

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

const maxExecutionLifecycleEvent = 1 << 10

type ExecutionLifecycleCount struct {
	ReturnedTicks, OwnerTurns, Deleted, MaxDeleted, FailedTicks uint64
}

// This is the joined native returned-turn subset, not a successful lifecycle
// cycle, a live ceiling or complete whole-work measurement.
type ExecutionLifecycleObservation struct {
	Phases          [15]ExecutionLifecycleCount
	Bound, Complete bool
}

// Keep the exact existing source-free native event shape. The independent
// LCB1 binding attributes even a zero prefix to the actual producer/input.
type executionLifecycleEvent struct {
	Schema          string `json:"schema"`
	Epoch           uint64 `json:"epoch"`
	Phase           uint32 `json:"phase"`
	ReturnedTick    uint64 `json:"returned_tick"`
	Owner           string `json:"owner,omitempty"`
	AttemptedAtNano int64  `json:"attempted_at_unix_nano,omitempty"`
	Scanned         int    `json:"scanned"`
	Deleted         int    `json:"deleted"`
	LogicalBytes    int64  `json:"logical_bytes"`
	RootBytes       int64  `json:"root_bytes"`
	MemberBytes     int64  `json:"member_bytes"`
	Completeness    string `json:"completeness"`
	Failed          bool   `json:"failed"`
	OwnerTurns      uint64 `json:"owner_turns"`
	TotalDeleted    uint64 `json:"total_deleted"`
	MaxDeleted      uint64 `json:"max_deleted"`
}

func reservedLifecycleEvent(line []byte) bool {
	return bytes.Contains(line, []byte("LCB")) || reservedBlobEvent(line, "LC") ||
		bytes.Contains(line, []byte("exact lifecycle turn:")) || bytes.Contains(line, []byte("phebs-t422-lifecycle-turn-"))
}

// Called inside the existing joined attempt pass; there is no additional scan.
func observeLifecycleEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionLifecycleObservation) (bool, error) {
	if !reservedLifecycleEvent(line) {
		return false, nil
	}
	if bytes.Contains(line, []byte("LCB")) {
		if out.Bound || string(line) != fmt.Sprintf("LCB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		out.Bound = true
		return true, nil
	}
	if !out.Bound || !bytes.HasPrefix(line, []byte("LC1:")) || len(line) < 6 || len(line) > maxExecutionLifecycleEvent+5 || line[len(line)-1] != '\n' {
		return true, errExecutionAttempts
	}
	raw := line[4 : len(line)-1]
	var event executionLifecycleEvent
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&event) != nil {
		return true, errExecutionAttempts
	}
	canonical, err := json.Marshal(event)
	validPhase := producer == 5 && (event.Phase == 9 || event.Phase == 11) || producer == 6 && event.Phase == 13
	if err != nil || !bytes.Equal(canonical, raw) || event.Schema != "phebs-t422-lifecycle-turn-v1" ||
		event.Epoch+1 != uint64(producer) || !validPhase ||
		len(plan.WorkEnvelope.Phases) != len(out.Phases) || event.Scanned < 0 || event.Deleted < 0 || event.LogicalBytes < 0 || event.RootBytes < 0 || event.MemberBytes < 0 ||
		!slices.Contains([]string{string(lifecycle.Exact), string(lifecycle.LowerBound), string(lifecycle.Unavailable)}, event.Completeness) {
		return true, errExecutionAttempts
	}
	count := &out.Phases[event.Phase-1]
	next := *count
	if count.ReturnedTicks >= uint64(lifecycle.MaxCycleObservationTurns) || event.ReturnedTick != count.ReturnedTicks+1 {
		return true, errExecutionAttempts
	}
	next.ReturnedTicks++
	if event.AttemptedAtNano > 0 && slices.Contains(plan.WorkEnvelope.LifecycleOwners, event.Owner) {
		if next.OwnerTurns == math.MaxUint64 || uint64(event.Deleted) > math.MaxUint64-next.Deleted {
			return true, errExecutionAttempts
		}
		next.OwnerTurns++
		next.Deleted += uint64(event.Deleted)
		next.MaxDeleted = max(next.MaxDeleted, uint64(event.Deleted))
	} else if event.Owner != "" && !slices.Contains(plan.WorkEnvelope.LifecycleOwners, event.Owner) || event.AttemptedAtNano != 0 || !event.Failed || event.Scanned != 0 || event.Deleted != 0 || event.LogicalBytes != 0 || event.RootBytes != 0 || event.MemberBytes != 0 {
		return true, errExecutionAttempts
	}
	if event.OwnerTurns != next.OwnerTurns || event.TotalDeleted != next.Deleted || event.MaxDeleted != next.MaxDeleted {
		return true, errExecutionAttempts
	}
	if event.Failed {
		next.FailedTicks++ // Errors remain measured attempts; later cycles may recover.
	}
	*count = next // Retain genuine positive excess before refusing its bound.
	bound := plan.WorkEnvelope.Phases[event.Phase-1]
	if next.OwnerTurns > bound.LifecycleOwnerTurns.Maximum || next.Deleted > bound.LifecycleDeleted.Maximum ||
		next.MaxDeleted > plan.WorkEnvelope.MaximumLifecycleDeletesPerTurn || event.Scanned > lifecycle.MaxCandidatesPerTick || event.Deleted > event.Scanned {
		return true, errExecutionAttempts
	}
	return true, nil
}
