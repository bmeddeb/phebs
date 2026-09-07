package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

// These are offered content attempts, not successfully returned blobs or
// unique bytes. Every record is eight bytes, including its newline.
func observeSourceAttempt(line []byte, plan Plan, producer uint32, input string, out *ExecutionAttemptObservation) (bool, error) {
	if bytes.Contains(line, []byte("SRB1:")) {
		if out.SourceBound || string(line) != fmt.Sprintf("SRB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		out.SourceBound = true
		return true, nil
	}
	if !bytes.Contains(line, []byte("SR1:")) {
		if reservedSourceAttempt(line) {
			return true, errExecutionAttempts
		}
		return false, nil
	}
	if !out.SourceBound || len(line) != 8 || !bytes.Equal(line[:4], []byte("SR1:")) || line[4] != byte('0'+producer) || line[5] != ':' || line[7] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[6])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	count := &out.Phases[phase-1].SourceBlobAttempts
	if *count == math.MaxUint64 {
		return true, errExecutionAttempts
	}
	*count++
	if *count > plan.WorkEnvelope.Phases[phase-1].GitReads.Maximum {
		return true, errExecutionAttempts
	}
	return true, nil
}

func reservedSourceAttempt(line []byte) bool {
	for len(line) > 3 {
		index := bytes.Index(line, []byte("SR"))
		if index < 0 {
			return false
		}
		line = line[index:]
		if len(line) > 3 && line[3] == ':' || len(line) > 4 && line[2] == 'B' && line[4] == ':' {
			return true
		}
		line = line[2:]
	}
	return false
}
