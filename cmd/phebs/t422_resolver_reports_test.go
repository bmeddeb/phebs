package main

import (
	"math"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestT422ResolverFraming(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	header, err := t422ResolverBinding(initial)
	if err != nil || string(header) != "RMB1:2:sha256:01"+strings.Repeat("00", 31)+"\n" || len(header) != 79 {
		t.Fatal(string(header), err)
	}
	for _, test := range []struct {
		size uint64
		want string
	}{
		{0, "0000000000000000"}, {10, "000000000000000a"}, {math.MaxUint64, "ffffffffffffffff"},
	} {
		raw, err := t422ResolverRecord(initial, initial, test.size)
		if err != nil || string(raw[:]) != "RM1:2:2:"+test.want+"\n" {
			t.Fatal(raw, err)
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
			if _, err := t422ResolverRecord(state, initial, 10); err == nil {
				t.Fatal("unbound resolver return accepted")
			}
		})
	}
}
