package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

func reservedRelationshipEvent(line []byte) bool {
	return bytes.Contains(line, []byte("RLB")) || reservedBlobEvent(line, "RL")
}

// Builds and projections are independent actual function-entry attempts.
// Successful publication, distinct projections and job starts are other units.
func observeRelationshipEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionAttemptObservation) (bool, error) {
	if !reservedRelationshipEvent(line) {
		return false, nil
	}
	if bytes.Contains(line, []byte("RLB")) {
		if out.RelationshipBound || string(line) != fmt.Sprintf("RLB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		out.RelationshipBound = true
		return true, nil
	}
	if !out.RelationshipBound || len(line) != 9 || !bytes.Equal(line[:4], []byte("RL1:")) || line[4] != byte('0'+producer) || line[5] != ':' || line[8] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[6])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	row := &out.Phases[phase-1]
	bound := plan.WorkEnvelope.Phases[phase-1]
	var count *uint64
	var maximum uint64
	switch line[7] {
	case 'B':
		count, maximum = &row.RelationshipBuildAttempts, bound.RelationshipBuildAttempts.Maximum
	case 'P':
		count, maximum = &row.RelationshipProjections, bound.RelationshipProjections.Maximum
	default:
		return true, errExecutionAttempts
	}
	if *count == math.MaxUint64 {
		return true, errExecutionAttempts
	}
	*count++ // Preserve the full first excess without clamping.
	if *count > maximum {
		return true, errExecutionAttempts
	}
	return true, nil
}
