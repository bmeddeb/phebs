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
// R batches count the admitted references in successfully installed members,
// including verified reuse, not append attempts or durable-current authority.
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
	if !out.RelationshipBound || len(line) != 9 && len(line) != 26 || !bytes.Equal(line[:4], []byte("RL1:")) || line[4] != executionWorkProducerByte(producer) || line[5] != ':' || line[len(line)-1] != '\n' {
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
	quantity := uint64(1)
	switch line[7] {
	case 'B':
		if len(line) != 9 {
			return true, errExecutionAttempts
		}
		count, maximum = &row.RelationshipBuildAttempts, bound.RelationshipBuildAttempts.Maximum
	case 'P':
		if len(line) != 9 {
			return true, errExecutionAttempts
		}
		count, maximum = &row.RelationshipProjections, bound.RelationshipProjections.Maximum
	case 'R':
		if len(line) != 26 || line[8] != ':' {
			return true, errExecutionAttempts
		}
		quantity = 0
		for _, digit := range line[9:25] {
			value := bytes.IndexByte([]byte("0123456789abcdef"), digit)
			if value < 0 {
				return true, errExecutionAttempts
			}
			quantity = quantity<<4 | uint64(value)
		}
		if quantity == 0 {
			return true, errExecutionAttempts
		}
		count, maximum = &row.ServiceReferences, bound.ServiceReferences.Maximum
	default:
		return true, errExecutionAttempts
	}
	if quantity > math.MaxUint64-*count {
		return true, errExecutionAttempts
	}
	*count += quantity // Preserve the full first excess batch without clamping.
	if *count > maximum {
		return true, errExecutionAttempts
	}
	return true, nil
}
