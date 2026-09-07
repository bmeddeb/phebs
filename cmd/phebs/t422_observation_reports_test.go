package main

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestT422ObservationFraming(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	header, err := t422ObservationBinding(initial)
	if err != nil || string(header) != "OPB1:2:sha256:01"+strings.Repeat("00", 31)+"\n" || len(header) != 79 {
		t.Fatal(string(header), err)
	}
	for _, mode := range []string{"valid", "phase", "producer", "input", "mode"} {
		t.Run(mode, func(t *testing.T) {
			state := initial
			switch mode {
			case "phase":
				state.Phase = 5
			case "producer":
				state.ProducerID = 3
			case "input":
				state.InputSHA256 = [32]byte{2}
			case "mode":
				state.Mode = ""
			}
			raw, err := t422ObservationRecord(state, initial)
			if (err == nil) != (mode == "valid") {
				t.Fatal(err)
			}
			if err == nil && string(raw[:]) != "OP1:2:2\n" {
				t.Fatal(raw)
			}
		})
	}
	ctx, err := bindT422ObservationReports(t.Context(), nil)
	if err != nil || ctx != t.Context() {
		t.Fatal("ordinary observer binding changed", err)
	}
}
