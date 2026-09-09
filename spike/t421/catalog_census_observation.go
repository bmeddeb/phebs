package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

type ExecutionCatalogCensusObservation struct {
	Started, Finished [15]uint64
	ClosedChildren    [15]uint64
	Bound, Complete   bool
}

func (observation ExecutionCatalogCensusObservation) complete() bool {
	return observation.Bound && observation.Started == observation.Finished
}

func reservedCatalogCensusEvent(line []byte) bool {
	return bytes.Contains(line, []byte("GCB")) || reservedBlobEvent(line, "GC")
}

func observeCatalogCensusEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionAttemptObservation) (bool, error) {
	if !reservedCatalogCensusEvent(line) {
		return false, nil
	}
	observation := &out.CatalogCensus
	if bytes.Contains(line, []byte("GCB")) {
		if observation.Bound || string(line) != fmt.Sprintf("GCB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		observation.Bound = true
		return true, nil
	}
	if !observation.Bound || len(line) != 9 && len(line) != 26 || !bytes.Equal(line[:4], []byte("GC1:")) || line[4] != byte('0'+producer) || line[5] != ':' || line[len(line)-1] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[6])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	index := phase - 1
	next := out.Phases[index]
	open := observation.Started[index] - observation.Finished[index]
	children := next.CensusChildren - observation.ClosedChildren[index]
	switch line[7] {
	case 'B':
		if len(line) != 9 || observation.Started[index] == math.MaxUint64 {
			return true, errExecutionAttempts
		}
		observation.Started[index]++
	case 'E':
		if len(line) != 9 || children == 0 || open == 0 {
			return true, errExecutionAttempts
		}
		observation.Finished[index]++
		observation.ClosedChildren[index]++
	case 'N':
		if len(line) != 9 || open <= children {
			return true, errExecutionAttempts
		}
		observation.Finished[index]++
	case 'S':
		if len(line) != 9 || open <= children || next.CensusChildren == math.MaxUint64 {
			return true, errExecutionAttempts
		}
		next.CensusChildren++
	case 'D':
		if len(line) != 26 || line[8] != ':' || open == 0 || children == 0 {
			return true, errExecutionAttempts
		}
		var records uint64
		for i := 0; i < 16; i++ {
			digit := bytes.IndexByte([]byte("0123456789abcdef"), line[9+i])
			if digit < 0 {
				return true, errExecutionAttempts
			}
			records = records<<4 | uint64(digit)
		}
		if records == 0 || records > math.MaxUint64-next.CensusRecords {
			return true, errExecutionAttempts
		}
		next.CensusRecords += records
		observation.Finished[index]++
		observation.ClosedChildren[index]++
	default:
		return true, errExecutionAttempts
	}
	out.Phases[index] = next
	bound := plan.WorkEnvelope.Phases[index]
	if next.CensusChildren > bound.CensusChildren.Maximum || next.CensusRecords > bound.CensusRecords.Maximum {
		return true, errExecutionAttempts
	}
	return true, nil
}
