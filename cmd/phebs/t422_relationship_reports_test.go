package main

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestT422RelationshipFraming(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	header, err := t422RelationshipBinding(initial)
	if err != nil || string(header) != "RLB1:2:sha256:01"+strings.Repeat("00", 31)+"\n" || len(header) != 79 {
		t.Fatal(string(header), err)
	}
	for _, event := range []readaccounting.RelationshipEvent{readaccounting.RelationshipBuild, readaccounting.RelationshipProjection, 0, 'X'} {
		raw, err := t422RelationshipRecord(initial, initial, event)
		valid := event == readaccounting.RelationshipBuild || event == readaccounting.RelationshipProjection
		if (err == nil) != valid || valid && string(raw[:]) != "RL1:2:2"+string(byte(event))+"\n" {
			t.Fatal(event, raw, err)
		}
	}
	for _, mode := range []string{"phase", "producer", "input", "mode"} {
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
			if _, err := t422RelationshipRecord(state, initial, readaccounting.RelationshipProjection); err == nil {
				t.Fatal("unbound relationship entry accepted")
			}
		})
	}
}
