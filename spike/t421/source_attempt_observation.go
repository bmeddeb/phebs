package t421

import (
	"bytes"
	"fmt"
	"math"
	"slices"
)

// SR counts offered content attempts; OP counts successful native ParsedBlobs
// events. Neither counts unique blobs. EP counts actual PublishDomain calls,
// not authority movement. All three use the same eight-byte framing.
func observeBlobEvent(line []byte, plan Plan, producer uint32, input string, out *ExecutionAttemptObservation) (bool, error) {
	family, binding, record := "SR", []byte("SRB1:"), []byte("SR1:")
	bound := &out.SourceBound
	if reservedBlobEvent(line, "OP") {
		family, binding, record = "OP", []byte("OPB1:"), []byte("OP1:")
		bound = &out.ObservationBound
	} else if reservedBlobEvent(line, "EP") {
		family, binding, record = "EP", []byte("EPB1:"), []byte("EP1:")
		bound = &out.PublicationBound
	}
	if bytes.Contains(line, binding) {
		if *bound || string(line) != fmt.Sprintf("%sB1:%d:%s\n", family, producer, input) {
			return true, errExecutionAttempts
		}
		*bound = true
		return true, nil
	}
	if !bytes.Contains(line, record) {
		if reservedBlobEvent(line, family) {
			return true, errExecutionAttempts
		}
		return false, nil
	}
	if !*bound || len(line) != 8 || !bytes.Equal(line[:4], record) || line[4] != byte('0'+producer) || line[5] != ':' || line[7] != '\n' {
		return true, errExecutionAttempts
	}
	phase := bytes.IndexByte([]byte("0123456789ABCDEF"), line[6])
	if phase < 1 || !slices.Contains(executionProducerPhases(producer), uint32(phase)) {
		return true, errExecutionAttempts
	}
	count := &out.Phases[phase-1].SourceBlobAttempts
	maximum := plan.WorkEnvelope.Phases[phase-1].GitReads.Maximum
	switch family {
	case "OP":
		count = &out.Phases[phase-1].ObservationParses
		maximum = plan.WorkEnvelope.Phases[phase-1].ObservationParses.Maximum
	case "EP":
		count = &out.Phases[phase-1].PublicationWrites
		maximum = plan.WorkEnvelope.Phases[phase-1].PublicationWrites.Maximum
	}
	if *count == math.MaxUint64 {
		return true, errExecutionAttempts
	}
	*count++
	if *count > maximum {
		return true, errExecutionAttempts
	}
	return true, nil
}

func reservedBlobEvent(line []byte, family string) bool {
	if (family == "OP" || family == "EP") && bytes.Contains(line, []byte(family+"B")) {
		return true
	}
	for len(line) > 3 {
		index := bytes.Index(line, []byte(family))
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
