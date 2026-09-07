package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

// ATB1 binds this metric-only format once. j/c starts and r/t retries are
// distinct native observations, never deduplicated by identity or depth.
func observeCompactAttempt(line []byte, plan Plan, producer uint32, input string, out *ExecutionAttemptObservation) (bool, error) {
	if bytes.Contains(line, []byte("ATB")) {
		if out.AttemptBound || string(line) != fmt.Sprintf("ATB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		out.AttemptBound = true
		return true, nil
	}
	if !reservedCompactAttempt(line) {
		return false, nil
	}
	if !out.AttemptBound || len(line) != 5 || line[0] != 'A' || line[4] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[1])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	depth := uint64(line[3] - '0')
	runtime := frozenExecutionRuntime(plan)
	var starts, retries uint64
	switch line[2] {
	case 'j':
		if depth < 1 || depth > runtime.StoreRunnerMaxAttempts {
			return true, errExecutionAttempts
		}
		starts = 1
	case 'c':
		if depth >= runtime.GenerationMaxAttempts {
			return true, errExecutionAttempts
		}
		starts = 1
	case 'r':
		if depth < 1 || depth >= runtime.StoreRunnerMaxAttempts {
			return true, errExecutionAttempts
		}
		retries = 1
	case 't':
		if depth < 1 || depth >= runtime.GenerationMaxAttempts {
			return true, errExecutionAttempts
		}
		retries = 1
	default:
		return true, errExecutionAttempts
	}
	count := &out.Phases[phase-1]
	if count.JobAttempts > math.MaxUint64-starts || count.Retries > math.MaxUint64-retries {
		return true, errExecutionAttempts
	}
	count.JobAttempts += starts
	count.Retries += retries
	if retries != 0 {
		count.MaxRetriesUnit = max(count.MaxRetriesUnit, depth)
	}
	if count.JobAttempts > plan.WorkEnvelope.Phases[phase-1].JobAttempts.Maximum || count.MaxRetriesUnit > plan.WorkEnvelope.MaximumRetriesPerUnit {
		return true, errExecutionAttempts // Preserve the first actual excess.
	}
	return true, nil
}

func reservedCompactAttempt(line []byte) bool {
	if bytes.HasPrefix(line, []byte("A")) || bytes.Contains(line, []byte("ATB")) || bytes.Contains(line, []byte(executionAttemptPrefix)) ||
		bytes.Contains(line, []byte("job lifecycle: ")) || bytes.Contains(line, []byte("generation chunk lifecycle: ")) {
		return true
	}
	// Complete compact records are single writes/newline-terminated. Also
	// reject an embedded/split record suffix without reserving arbitrary A/hex
	// substrings (for example SHA256) in timestamped ordinary diagnostics.
	return len(line) >= 5 && line[len(line)-5] == 'A' && line[len(line)-1] == '\n' &&
		bytes.IndexByte([]byte("jcrt"), line[len(line)-3]) >= 0
}
