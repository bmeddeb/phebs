package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

type ExecutionSourceCensusObservation struct {
	Started, Finished [15]uint64
	// Successful census completions and actual regular owners, not receipt
	// physical passes. Failed E closures retain bytes but add neither value.
	Succeeded, RegularOwners [15]uint64
	Bound, Complete          bool
}

func (observation ExecutionSourceCensusObservation) complete() bool {
	return observation.Bound && observation.Started == observation.Finished
}

func reservedCensusEvent(line []byte) bool {
	return bytes.Contains(line, []byte("SBB")) || reservedBlobEvent(line, "SB")
}

func observeCensusEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionAttemptObservation) (bool, error) {
	if !reservedCensusEvent(line) {
		return false, nil
	}
	observation := &out.SourceCensus
	if bytes.Contains(line, []byte("SBB")) {
		if observation.Bound || string(line) != fmt.Sprintf("SBB2:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		observation.Bound = true
		return true, nil
	}
	if !observation.Bound || len(line) != 9 && len(line) != 26 && len(line) != 43 || !bytes.Equal(line[:4], []byte("SB2:")) || line[4] != executionWorkProducerByte(producer) || line[5] != ':' || line[len(line)-1] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[6])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	index := phase - 1
	switch line[7] {
	case 'B':
		if len(line) != 9 || observation.Started[index] == math.MaxUint64 {
			return true, errExecutionAttempts
		}
		observation.Started[index]++
	case 'E':
		if len(line) != 9 || observation.Finished[index] >= observation.Started[index] {
			return true, errExecutionAttempts
		}
		observation.Finished[index]++
	case 'C':
		if len(line) != 26 || line[8] != ':' || observation.Finished[index] >= observation.Started[index] || observation.Succeeded[index] == math.MaxUint64 {
			return true, errExecutionAttempts
		}
		var owners uint64
		for _, digit := range line[9:25] {
			value := bytes.IndexByte([]byte("0123456789abcdef"), digit)
			if value < 0 {
				return true, errExecutionAttempts
			}
			owners = owners<<4 | uint64(value)
		}
		if owners > math.MaxUint64-observation.RegularOwners[index] {
			return true, errExecutionAttempts
		}
		observation.Finished[index]++
		observation.Succeeded[index]++
		observation.RegularOwners[index] += owners
	case 'D':
		if len(line) != 43 || line[8] != ':' || line[25] != ':' || observation.Finished[index] >= observation.Started[index] {
			return true, errExecutionAttempts
		}
		var logical, unique uint64
		for i := 0; i < 16; i++ {
			left := bytes.IndexByte([]byte("0123456789abcdef"), line[9+i])
			right := bytes.IndexByte([]byte("0123456789abcdef"), line[26+i])
			if left < 0 || right < 0 {
				return true, errExecutionAttempts
			}
			logical, unique = logical<<4|uint64(left), unique<<4|uint64(right)
		}
		next := out.Phases[index]
		if logical == 0 || unique > logical || logical > math.MaxUint64-next.SourceLogicalBytes || unique > math.MaxUint64-next.SourceUniqueBytes {
			return true, errExecutionAttempts
		}
		next.SourceLogicalBytes += logical
		next.SourceUniqueBytes += unique
		out.Phases[index] = next
		bound := plan.WorkEnvelope.Phases[index]
		if next.SourceLogicalBytes > bound.SourceLogicalBytes.Maximum || next.SourceUniqueBytes > bound.SourceUniqueBytes.Maximum {
			return true, errExecutionAttempts
		}
	default:
		return true, errExecutionAttempts
	}
	return true, nil
}
