package t421

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
)

type ExecutionIndexOfferCount struct {
	Offers, StartedChildren, EndedChildren, FailedChildren, SettledOffers uint64
}

// This is only the native input-offer subset. It is not a whole-work receipt,
// and an unknown failed tail is never assigned zero attempted work.
type ExecutionIndexObservation struct {
	Phases          [15]ExecutionIndexOfferCount
	Bound, Complete bool
}

// The owner calls this only after native Wait joined every output pump. Stable
// failed output may retain a positive prefix, but never a complete attestation.
func observeExecutionIndexOffers(raw []byte, plan Plan, producer uint32, input [32]byte, joined, healthy bool) (out ExecutionIndexObservation, err error) {
	if !joined || plan.Schema != PlanV3Schema || len(raw) > 64<<20 || input == ([32]byte{}) ||
		producer < 2 || producer > 6 || !slices.Equal(plan.PhaseOrder, frozenPhaseOrder()) || len(plan.WorkEnvelope.Phases) != len(out.Phases) {
		return out, errExecutionAttempts
	}
	want := fmt.Sprintf("IXB1:%d:sha256:%s\n", producer, hex.EncodeToString(input[:]))
	phases := executionProducerPhases(producer)
	reader := bufio.NewReaderSize(bytes.NewReader(raw), maxExecutionAttemptLine)
	for {
		line, readErr := reader.ReadSlice('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if bytes.HasPrefix(line, []byte("IXB")) {
			if out.Bound || string(line) != want || readErr != nil {
				return out, errExecutionAttempts
			}
			out.Bound = true
		} else if len(line) >= 2 && line[0] == 'I' {
			if !out.Bound || readErr != nil {
				return out, errExecutionAttempts
			}
			phaseIndex := 1
			if line[1] == 'b' || line[1] == 'e' || line[1] == 'f' {
				phaseIndex = 2
			}
			if len(line) <= phaseIndex {
				return out, errExecutionAttempts
			}
			phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[phaseIndex])
			if phase < 1 || !slices.Contains(phases, uint32(phase)) || plan.WorkEnvelope.Phases[phase-1].Phase != plan.PhaseOrder[phase-1] {
				return out, errExecutionAttempts
			}
			count := &out.Phases[phase-1]
			switch {
			case phaseIndex == 1 && len(line) == 3:
				if count.StartedChildren == count.EndedChildren || count.Offers == math.MaxUint64 {
					return out, errExecutionAttempts
				}
				count.Offers++
				if count.Offers > plan.WorkEnvelope.Phases[phase-1].IndexFiles.Maximum {
					return out, errExecutionAttempts
				}
			case line[1] == 'b' && len(line) == 4:
				if count.StartedChildren == math.MaxUint64 {
					return out, errExecutionAttempts
				}
				count.StartedChildren++
			case (line[1] == 'e' || line[1] == 'f') && len(line) >= 6 && line[3] == ':':
				value := string(line[4 : len(line)-1])
				settled, err := strconv.ParseUint(value, 10, 64)
				if err != nil || strconv.FormatUint(settled, 10) != value || count.EndedChildren >= count.StartedChildren || settled > math.MaxUint64-count.SettledOffers {
					return out, errExecutionAttempts
				}
				count.EndedChildren++
				if line[1] == 'f' {
					count.FailedChildren++
				}
				count.SettledOffers += settled
				if count.SettledOffers > count.Offers {
					return out, errExecutionAttempts
				}
			default:
				return out, errExecutionAttempts
			}
		} else if bytes.HasPrefix(line, []byte("ZI")) {
			return out, errExecutionAttempts // Raw/unbound child instrumentation is not forwarded evidence.
		}
		for errors.Is(readErr, bufio.ErrBufferFull) {
			_, readErr = reader.ReadSlice('\n')
		}
		if readErr != nil {
			return out, errExecutionAttempts
		}
	}
	if !out.Bound || !healthy {
		return out, errExecutionAttempts
	}
	for _, phase := range out.Phases {
		if phase.StartedChildren != phase.EndedChildren || phase.Offers != phase.SettledOffers {
			return out, errExecutionAttempts
		}
	}
	out.Complete = true
	return out, nil
}
