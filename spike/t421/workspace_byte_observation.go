package t421

import (
	"bytes"
	"fmt"
	"math"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

type ExecutionWorkspaceBytePhase struct {
	Attempts, Completed uint64
	Maximum             custodybytes.Sample
}

// These are joined child native observations. The separate synchronous command
// sequence proves pressure boundary positions; this stream alone does not.
// Maxima are not sums and never assert atomic or unique physical usage.
type ExecutionWorkspaceByteObservation struct {
	Phases                       [15]ExecutionWorkspaceBytePhase
	Bound, Complete, Unavailable bool
	LimitExceeded                bool
	sequence                     uint64
	phase                        uint32
	pending                      bool
}

func reservedWorkspaceByteEvent(line []byte) bool {
	return bytes.Contains(line, []byte("WBB")) || reservedBlobEvent(line, "WB")
}

// Five lifecycle capacity sites plus the fixed four/three/four pressure boundary
// commands. The latter add no native lifecycle turn or capacity probe.
func workspaceCheckpointMaximum(producer, phase uint32) uint64 {
	if producer == 5 {
		switch phase {
		case 9:
			return uint64(lifecycle.MaxCycleObservationTurns) + 1 + 4
		case 10:
			return 1 + 3
		case 11:
			return uint64(lifecycle.MaxCycleObservationTurns) + 2 + 4
		}
	}
	if producer == 6 && phase == 13 {
		return uint64(lifecycle.MaxCycleObservationTurns)
	}
	return 0
}

func workspaceHex64(raw []byte) (uint64, bool) {
	if len(raw) != 16 {
		return 0, false
	}
	var value uint64
	for _, digit := range raw {
		n := bytes.IndexByte([]byte("0123456789abcdef"), digit)
		if n < 0 {
			return 0, false
		}
		value = value<<4 | uint64(n)
	}
	return value, true
}

// Runs in the existing immutable joined-output pass. A begin reserves no byte
// total; only its exact success commits a full native observation. Refusal and
// damaged suffixes keep previously completed maxima visible.
func observeWorkspaceByteEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionWorkspaceByteObservation) (observed bool, retErr error) {
	if !reservedWorkspaceByteEvent(line) {
		return false, nil
	}
	defer func() {
		if retErr != nil {
			out.Complete = false
			if !out.LimitExceeded {
				out.Unavailable = true
			}
		}
	}()
	if plan.Schema != PlanV3Schema || producer != 5 && producer != 6 || out.Unavailable || out.LimitExceeded {
		return true, errExecutionAttempts
	}
	if bytes.Contains(line, []byte("WBB")) {
		if out.Bound || string(line) != fmt.Sprintf("WBB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		out.Bound = true
		return true, nil
	}
	if !out.Bound || len(line) != 26 && len(line) != 60 || !bytes.HasPrefix(line, []byte("WB1:")) ||
		line[4] != executionWorkProducerByte(producer) || line[5] != ':' || line[8] != ':' || line[len(line)-1] != '\n' {
		return true, errExecutionAttempts
	}
	phase := uint32(bytes.IndexByte([]byte("0123456789ABCDEF"), line[6]))
	maximum := workspaceCheckpointMaximum(producer, phase)
	sequence, valid := workspaceHex64(line[9:25])
	if maximum == 0 || phase < out.phase || !valid || sequence == 0 {
		return true, errExecutionAttempts
	}
	row := &out.Phases[phase-1]
	switch line[7] {
	case 'B':
		if len(line) != 26 || out.pending || out.sequence == math.MaxUint64 || sequence != out.sequence+1 || row.Attempts >= maximum {
			return true, errExecutionAttempts
		}
		out.sequence, out.phase, out.pending = sequence, phase, true
		row.Attempts++
	case 'F':
		if len(line) != 26 || !out.pending || sequence != out.sequence || phase != out.phase {
			return true, errExecutionAttempts
		}
		out.pending = false
		return true, errExecutionAttempts
	case 'S':
		if len(line) != 60 || !out.pending || sequence != out.sequence || phase != out.phase || line[25] != ':' || line[42] != ':' {
			return true, errExecutionAttempts
		}
		logical, logicalOK := workspaceHex64(line[26:42])
		allocated, allocatedOK := workspaceHex64(line[43:59])
		if !logicalOK || !allocatedOK {
			return true, errExecutionAttempts
		}
		row.Completed++
		row.Maximum.LogicalBytes = max(row.Maximum.LogicalBytes, logical)
		row.Maximum.AllocatedBytes = max(row.Maximum.AllocatedBytes, allocated)
		out.pending = false
		if logical > plan.WorkEnvelope.MaximumDataLogicalBytes || allocated > plan.SafetyEnvelope.MaximumDataAllocatedBytes {
			out.LimitExceeded = true // Keep the full completed excess, never clamp it.
			return true, errExecutionAttempts
		}
	default:
		return true, errExecutionAttempts
	}
	return true, nil
}

func (out *ExecutionWorkspaceByteObservation) finish() bool {
	if !out.Bound {
		return false
	}
	if out.pending {
		out.Unavailable = true
	}
	out.Complete = !out.Unavailable && !out.LimitExceeded
	return out.Complete
}
