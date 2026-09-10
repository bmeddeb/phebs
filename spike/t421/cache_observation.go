package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

type ExecutionCacheCount struct {
	Lookups, Hits, Misses              uint64
	RootReads, MemberReads             uint64
	RootValidations, MemberValidations uint64
}

// Only classified native cache decisions, not generic read counts or a Stats
// endpoint. A joined source error may complete its result-admission attempt;
// an unpaired load or later native failure never becomes a complete receipt.
type ExecutionCacheObservation struct {
	Phases          [15]ExecutionCacheCount
	Bound, Complete bool
}

func reservedCacheEvent(line []byte) bool {
	return bytes.Contains(line, []byte("CCB")) || reservedBlobEvent(line, "CC")
}

func observeCacheEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionCacheObservation) (bool, error) {
	if !reservedCacheEvent(line) {
		return false, nil
	}
	if bytes.Contains(line, []byte("CCB")) {
		if out.Bound || string(line) != fmt.Sprintf("CCB1:%d:%s\n", producer, input) {
			return true, errExecutionAttempts
		}
		out.Bound = true
		return true, nil
	}
	if !out.Bound || len(line) != 9 || !bytes.Equal(line[:4], []byte("CC1:")) || line[4] != executionWorkProducerByte(producer) || line[5] != ':' || line[8] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[6])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	next := out.Phases[phase-1]
	var increments []*uint64
	switch line[7] {
	case 'H':
		increments = []*uint64{&next.Lookups, &next.Hits}
	case 'R':
		increments = []*uint64{&next.Lookups, &next.Misses, &next.RootReads}
	case 'M':
		increments = []*uint64{&next.Lookups, &next.Misses, &next.MemberReads}
	case 'r':
		if next.RootValidations >= next.RootReads {
			return true, errExecutionAttempts
		}
		increments = []*uint64{&next.RootValidations}
	case 'm':
		if next.MemberValidations >= next.MemberReads {
			return true, errExecutionAttempts
		}
		increments = []*uint64{&next.MemberValidations}
	default:
		return true, errExecutionAttempts
	}
	for _, value := range increments {
		if *value == math.MaxUint64 {
			return true, errExecutionAttempts
		}
		*value++
	}
	out.Phases[phase-1] = next // Preserve the first actual excess, never clamp.
	bound := plan.WorkEnvelope.Phases[phase-1]
	if next.Lookups > bound.CacheLookups.Maximum || next.Hits > bound.CacheHits.Maximum || next.Misses > bound.CacheMisses.Maximum ||
		next.RootReads > bound.CacheRootReads.Maximum || next.MemberReads > bound.CacheMemberReads.Maximum {
		return true, errExecutionAttempts
	}
	return true, nil
}

func (out ExecutionCacheObservation) complete() bool {
	if !out.Bound {
		return false
	}
	for _, phase := range out.Phases {
		if phase.RootReads != phase.RootValidations || phase.MemberReads != phase.MemberValidations {
			return false
		}
	}
	return true
}
