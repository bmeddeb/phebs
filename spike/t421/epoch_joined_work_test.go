package t421

import (
	"errors"
	"math"
	"testing"
)

// Supplied records test only retention/coverage and detached value copies.
// They are not native joins, parsed stream proof or a whole-phase receipt.
func joinedWorkTestRecord(producer uint32) executionJoinedWorkRecord {
	return executionJoinedWorkRecord{
		Producer: producer, Input: [32]byte{byte(producer)}, Joined: true, SessionEmpty: true,
		Attempts:    ExecutionAttemptObservation{Complete: true},
		IndexOffers: ExecutionIndexObservation{Bound: producer <= 6, Complete: producer <= 6},
	}
}

func TestExecutionJoinedWorkRetention(t *testing.T) {
	flow := &ExecutionEpochOne{}
	for _, producer := range []uint32{2, 3, 4, 5, 6, 10, 11} {
		record := joinedWorkTestRecord(producer)
		// The two independent phase-eight streams are retained, not summed.
		// The maximum value deliberately cannot tolerate another addition.
		if producer == 4 {
			record.Attempts.Phases[7].SourceBlobAttempts = math.MaxUint64
		}
		if producer == 5 {
			record.Attempts.Phases[7].SourceBlobAttempts = 7
		}
		if err := flow.retainJoinedWork(record); err != nil {
			t.Fatal(err)
		}
	}
	work := flow.joinedWorkSnapshot()
	if !work.complete() || work.Records[2].Attempts.Phases[7].SourceBlobAttempts != math.MaxUint64 ||
		work.Records[3].Attempts.Phases[7].SourceBlobAttempts != 7 {
		t.Fatal("independent supplied producer prefixes were lost or combined")
	}
	work.Records[2].Attempts.Phases[7].SourceBlobAttempts = 0
	if flow.joinedWorkSnapshot().Records[2].Attempts.Phases[7].SourceBlobAttempts != math.MaxUint64 {
		t.Fatal("snapshot escaped the fixed value-copy boundary")
	}
	original := flow.joinedWorkSnapshot().Records[5]
	replay := joinedWorkTestRecord(10)
	replay.Attempts.Phases[11].PublicationWrites = 99
	if !errors.Is(flow.retainJoinedWork(replay), errExecutionAttempts) {
		t.Fatal("archive result-copy replay accepted")
	}
	if got := flow.joinedWorkSnapshot(); got.complete() || got.Records[5] != original || got.Err == nil {
		t.Fatal("replay overwrote its original slot or lost sticky refusal")
	}
}

func TestExecutionJoinedWorkMissingCoverage(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*executionJoinedWorkRecord)
	}{
		{"missing slot", func(record *executionJoinedWorkRecord) { *record = executionJoinedWorkRecord{} }},
		{"not joined", func(record *executionJoinedWorkRecord) { record.Joined = false }},
		{"session not empty", func(record *executionJoinedWorkRecord) { record.SessionEmpty = false }},
		{"failed attempts", func(record *executionJoinedWorkRecord) { record.Attempts.Complete = false }},
		{"missing index binding", func(record *executionJoinedWorkRecord) { record.IndexOffers.Bound = false }},
		{"failed index tail", func(record *executionJoinedWorkRecord) { record.IndexOffers.Complete = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			flow := &ExecutionEpochOne{}
			for _, producer := range []uint32{2, 3, 4, 5, 6, 10, 11} {
				record := joinedWorkTestRecord(producer)
				if producer == 4 {
					record.Attempts.Phases[7].SourceBlobAttempts = 23
					test.edit(&record)
				}
				if record.Producer != 0 {
					if err := flow.retainJoinedWork(record); err != nil {
						t.Fatal(err)
					}
				}
			}
			got := flow.joinedWorkSnapshot()
			if got.complete() {
				t.Fatal("missing or failed producer coverage became complete")
			}
			if test.name != "missing slot" && got.Records[2].Attempts.Phases[7].SourceBlobAttempts != 23 {
				t.Fatal("failed producer lost its positive prefix")
			}
		})
	}
}

func TestExecutionJoinedWorkInvalidOwner(t *testing.T) {
	for _, producer := range []uint32{0, 1, 7, 8, 9, 12} {
		flow := &ExecutionEpochOne{}
		if !errors.Is(flow.retainJoinedWork(joinedWorkTestRecord(producer)), errExecutionAttempts) ||
			flow.joinedWorkSnapshot().complete() {
			t.Fatalf("unsupported supplied producer %d accepted", producer)
		}
	}
	flow := &ExecutionEpochOne{}
	record := joinedWorkTestRecord(2)
	record.Input = [32]byte{}
	if !errors.Is(flow.retainJoinedWork(record), errExecutionAttempts) {
		t.Fatal("missing actual parser input identity accepted")
	}
	var absent *ExecutionEpochOne
	if !errors.Is(absent.retainJoinedWork(joinedWorkTestRecord(2)), errExecutionAttempts) ||
		absent.joinedWorkSnapshot().complete() {
		t.Fatal("nil owner accepted")
	}
}
