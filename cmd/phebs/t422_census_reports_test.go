package main

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestT422CensusFraming(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	for _, test := range []struct {
		event           readaccounting.SourceCensusEvent
		phase           uint32
		logical, unique uint64
		want            string
	}{
		{readaccounting.SourceCensusBegin, 0, 0, 0, "SB2:2:2B\n"},
		{readaccounting.SourceCensusEnd, 2, 0, 0, "SB2:2:2E\n"},
		{readaccounting.SourceCensusComplete, 2, 0, 0, "SB2:2:2C:0000000000000000\n"},
		{readaccounting.SourceCensusComplete, 2, 10, 0, "SB2:2:2C:000000000000000a\n"},
		{readaccounting.SourceCensusComplete, 2, ^uint64(0), 0, "SB2:2:2C:ffffffffffffffff\n"},
		{readaccounting.SourceCensusBatch, 2, 10, 3, "SB2:2:2D:000000000000000a:0000000000000003\n"},
	} {
		raw, n, err := t422CensusRecord(initial, initial, test.event, test.phase, test.logical, test.unique)
		if test.event == readaccounting.SourceCensusComplete && n-9 != 17 {
			t.Fatal("successful-terminal byte delta", n)
		}
		if err != nil || string(raw[:n]) != test.want {
			t.Fatal(string(raw[:n]), err)
		}
	}
	for _, mode := range []string{"phase", "producer", "input", "mode", "phase_capture", "event", "zero", "unique", "complete_unique", "complete_phase"} {
		t.Run(mode, func(t *testing.T) {
			current := initial
			event, phase, logical, unique := readaccounting.SourceCensusBatch, uint32(2), uint64(5), uint64(3)
			switch mode {
			case "phase":
				current.Phase = 3
			case "producer":
				current.ProducerID = 3
			case "input":
				current.InputSHA256 = [32]byte{2}
			case "mode":
				current.Mode = ""
			case "phase_capture":
				phase = 0
			case "event":
				event = '?'
			case "zero":
				logical, unique = 0, 0
			case "complete_unique":
				event = readaccounting.SourceCensusComplete
			case "complete_phase":
				event, unique, phase = readaccounting.SourceCensusComplete, 0, 3
			case "unique":
				unique = 6
			}
			if _, n, err := t422CensusRecord(current, initial, event, phase, logical, unique); err == nil || n != 0 {
				t.Fatal("invalid/changed phase would emit bytes", n, err)
			}
		})
	}
}
