package main

import (
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
