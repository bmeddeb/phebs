package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestT422SourceFraming(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	header, err := t422SourceBinding(initial)
	if err != nil || string(header) != "SRB1:2:sha256:01"+strings.Repeat("00", 31)+"\n" || len(header) != 79 {
		t.Fatal(string(header), err)
	}
	for _, name := range []string{"valid", "phase", "producer", "input", "mode"} {
		t.Run(name, func(t *testing.T) {
			state := initial
			switch name {
			case "phase":
				state.Phase = 5
			case "producer":
				state.ProducerID = 3
			case "input":
				state.InputSHA256 = [32]byte{2}
			case "mode":
				state.Mode = ""
			}
			raw, err := t422SourceRecord(state, initial)
			if (err == nil) != (name == "valid") {
				t.Fatal(err)
			}
			if err == nil && string(raw[:]) != "SR1:2:2\n" {
				t.Fatal(raw)
			}
		})
	}
	ctx, err := bindT422SourceReports(t.Context(), nil)
	if err != nil || ctx != t.Context() {
		t.Fatal("ordinary observer binding changed", err)
	}
}

func TestT422OfflineWorkFraming(t *testing.T) {
	for _, producer := range []uint32{10, 11} {
		initial := dispatchadmission.ProductionSemanticSnapshot{ProducerID: producer, Phase: 12, InputSHA256: [32]byte{1}}
		binding, err := t422SourceBinding(initial)
		if err != nil || len(binding) != 80 || string(binding) != fmt.Sprintf("SRB1:%d:sha256:01%s\n", producer, strings.Repeat("00", 31)) {
			t.Fatal(string(binding), err)
		}
		for _, test := range []string{"valid", "producer", "phase", "mode", "input", "initial"} {
			state, start := initial, initial
			switch test {
			case "producer":
				state.ProducerID = 6
			case "phase":
				state.Phase = 13
			case "mode":
				state.Mode = dispatchadmission.ProductionSemanticV3
			case "input":
				state.InputSHA256 = [32]byte{2}
			case "initial":
				start.InputSHA256 = [32]byte{}
			}
			raw, err := t422SourceRecord(state, start)
			if (err == nil) != (test == "valid") || err == nil && string(raw[:]) != fmt.Sprintf("SR1:%X:C\n", producer) {
				t.Fatal(producer, test, raw, err)
			}
		}
	}
}
