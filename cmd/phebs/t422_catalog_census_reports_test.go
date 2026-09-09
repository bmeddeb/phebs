package main

import (
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"testing"
)

func TestT422CatalogCensusFraming(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	for _, test := range []struct {
		event   readaccounting.CatalogCensusEvent
		phase   uint32
		records uint64
		want    string
	}{
		{readaccounting.CatalogCensusBegin, 0, 0, "GC1:2:2B\n"},
		{readaccounting.CatalogCensusChild, 2, 0, "GC1:2:2S\n"},
		{readaccounting.CatalogCensusRecords, 2, 10, "GC1:2:2D:000000000000000a\n"},
		{readaccounting.CatalogCensusEnd, 2, 0, "GC1:2:2E\n"},
		{readaccounting.CatalogCensusNoChild, 2, 0, "GC1:2:2N\n"},
	} {
		raw, n, err := t422CatalogCensusRecord(initial, initial, test.event, test.phase, test.records)
		if err != nil || string(raw[:n]) != test.want {
			t.Fatal(string(raw[:n]), err)
		}
	}
	for _, mode := range []string{"phase", "producer", "input", "mode", "capture", "event", "zero", "child_quantity"} {
		t.Run(mode, func(t *testing.T) {
			current := initial
			event, phase, records := readaccounting.CatalogCensusRecords, uint32(2), uint64(10)
			switch mode {
			case "phase":
				current.Phase = 3
			case "producer":
				current.ProducerID = 3
			case "input":
				current.InputSHA256 = [32]byte{2}
			case "mode":
				current.Mode = ""
			case "capture":
				phase = 0
			case "event":
				event = '?'
			case "zero":
				records = 0
			case "child_quantity":
				event = readaccounting.CatalogCensusChild
			}
			if _, n, err := t422CatalogCensusRecord(current, initial, event, phase, records); err == nil || n != 0 {
				t.Fatal(n, err)
			}
		})
	}
}
