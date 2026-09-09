package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

func reservedResolverEvent(line []byte) bool {
	return bytes.Contains(line, []byte("RMB")) || reservedBlobEvent(line, "RM")
}

// One success-return event adds one read and actual returned bytes, including
// zero bytes and later validation failure. Failed native calls add neither;
// their source-read attempts are an independently observed unit.
func observeResolverEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionAttemptObservation) (bool, error) {
	if !reservedResolverEvent(line) {
		return false, nil
	}
	if bytes.Contains(line, []byte("RMB")) {
		if out.ResolverBound || string(line) != fmt.Sprintf("RMB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		out.ResolverBound = true
		return true, nil
	}
	if !out.ResolverBound || len(line) != 25 || !bytes.Equal(line[:4], []byte("RM1:")) || line[4] != byte('0'+producer) || line[5] != ':' || line[7] != ':' || line[24] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[6])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	var size uint64
	for _, digit := range line[8:24] {
		value := bytes.IndexByte([]byte("0123456789abcdef"), digit)
		if value < 0 {
			return true, errExecutionAttempts
		}
		size = size<<4 | uint64(value)
	}
	next := out.Phases[phase-1]
	if next.ResolverBlobReads == math.MaxUint64 || size > math.MaxUint64-next.ResolverBlobBytes {
		return true, errExecutionAttempts
	}
	next.ResolverBlobReads++
	next.ResolverBlobBytes += size
	out.Phases[phase-1] = next // Preserve both full values on the first excess.
	bound := plan.WorkEnvelope.Phases[phase-1]
	if next.ResolverBlobReads > bound.ResolverBlobReads.Maximum || next.ResolverBlobBytes > bound.ResolverBlobBytes.Maximum {
		return true, errExecutionAttempts
	}
	return true, nil
}
