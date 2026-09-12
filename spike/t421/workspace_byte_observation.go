package t421

import (
	"bytes"
	"fmt"
	"math"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
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
	// Exact producer-two first three S payloads, not a history or maxima.
	// The joined parser binds each endpoint independently before max folding.
	earlySamples       [3]custodybytes.Sample
	physicalReady      bool                   // Actual post-reopen record, separate from the completed walk.
	physicalPostAuthor custodybytes.Sample    // Exact middle phase-four S.
	midphaseSamples    [4]custodybytes.Sample // Exact fixed S payloads, not maxima or growing history.
	recoverySamples    [6]custodybytes.Sample // Five epoch-three points and epoch-four finish.
}

func reservedWorkspaceByteEvent(line []byte) bool {
	return bytes.Contains(line, []byte("WBB")) || reservedBlobEvent(line, "WB")
}

// Five lifecycle capacity sites plus the fixed four/three/four pressure and
// one/two/two restored-server boundary commands. Boundary commands add no native
// lifecycle turn or capacity probe.
func workspaceCheckpointMaximum(producer, phase uint32) uint64 {
	if producer == 4 && phase == 7 {
		return 3
	}
	if producer == 4 && phase == 8 {
		return 2
	}
	if producer == 5 && phase == 8 {
		return 1
	}
	if producer == 2 && phase == 4 {
		return 3
	}
	if producer == 3 && phase == 5 || producer == 4 && phase == 6 {
		return 1
	}
	if producer == 2 {
		if phase == 2 {
			return 1
		}
		if phase == 3 {
			return 2
		} // Post-Resume start and HTTP finish, not extra control pairs.
	}
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
	if producer == 6 {
		switch phase {
		case 12:
			return 1
		case 13:
			return uint64(lifecycle.MaxCycleObservationTurns) + 2
		case 14:
			return 2
		}
	}
	return 0
}

// The archive allowance is derived from the operation's closed call graph and
// the existing phase-twelve store ceiling, never from a supplied byte total.
func archiveCheckpointMaximum(plan Plan, producer uint32) (uint32, error) {
	if plan.Schema != PlanV3Schema || len(plan.WorkEnvelope.Phases) != 15 || plan.WorkEnvelope.Phases[11].Phase != frozenPhaseOrder()[11] {
		return 0, errExecutionAttempts
	}
	switch producer {
	case 10:
		return recovery.BackupCheckpointMaximum(), nil
	case 11:
		return recovery.RestoreCheckpointMaximum(plan.WorkEnvelope.Phases[11].StoreTransactions.Maximum)
	default:
		return 0, errExecutionAttempts
	}
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
	if plan.Schema != PlanV3Schema || producer != 2 && producer != 3 && producer != 4 && producer != 5 && producer != 6 && producer != 10 && producer != 11 || out.Unavailable || out.LimitExceeded {
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
	if (producer == 10 || producer == 11) && phase == 12 {
		derived, err := archiveCheckpointMaximum(plan, producer)
		if err != nil {
			return true, errExecutionAttempts
		}
		maximum = uint64(derived)
	}
	sequence, valid := workspaceHex64(line[9:25])
	if maximum == 0 || phase < out.phase || !valid || sequence == 0 {
		return true, errExecutionAttempts
	}
	row := &out.Phases[phase-1]
	switch line[7] {
	case 'B':
		if len(line) != 26 || out.pending || out.sequence == math.MaxUint64 || sequence != out.sequence+1 || row.Attempts >= maximum || producer == 2 && phase == 4 && row.Attempts == 2 && !out.physicalReady {
			return true, errExecutionAttempts
		}
		out.sequence, out.phase, out.pending = sequence, phase, true
		row.Attempts++
	case 'R':
		if len(line) != 26 || producer != 2 || phase != 4 || sequence != 5 || sequence != out.sequence ||
			out.pending || out.physicalReady || row.Attempts != 2 || row.Completed != 2 ||
			out.Phases[1].Completed != 1 || out.Phases[2].Completed != 2 {
			return true, errExecutionAttempts
		}
		out.physicalReady = true
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
		if producer == 2 && (phase == 2 && sequence == 1 || phase == 3 && (sequence == 2 || sequence == 3)) {
			out.earlySamples[sequence-1] = custodybytes.Sample{LogicalBytes: logical, AllocatedBytes: allocated}
		}
		if producer == 2 && phase == 4 {
			value := custodybytes.Sample{LogicalBytes: logical, AllocatedBytes: allocated}
			switch row.Completed {
			case 0:
				out.midphaseSamples[0] = value
			case 1:
				out.physicalPostAuthor = value
			case 2:
				out.midphaseSamples[1] = value
			}
		} else if producer == 3 && phase == 5 {
			out.midphaseSamples[2] = custodybytes.Sample{LogicalBytes: logical, AllocatedBytes: allocated}
		} else if producer == 4 && phase == 6 {
			out.midphaseSamples[3] = custodybytes.Sample{LogicalBytes: logical, AllocatedBytes: allocated}
		}
		if producer == 4 && (phase == 7 || phase == 8) {
			index := row.Completed
			if phase == 8 {
				index += 3
			}
			out.recoverySamples[index] = custodybytes.Sample{LogicalBytes: logical, AllocatedBytes: allocated}
		} else if producer == 5 && phase == 8 {
			out.recoverySamples[5] = custodybytes.Sample{LogicalBytes: logical, AllocatedBytes: allocated}
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
	if out.pending || out.Phases[3].Attempts >= 2 && !out.physicalReady {
		out.Unavailable = true
	}
	out.Complete = !out.Unavailable && !out.LimitExceeded
	return out.Complete
}
