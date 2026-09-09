package main

import (
	"io"
	"math"
	"os"
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

func TestT422RelationshipReferenceFraming(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	for _, test := range []struct {
		quantity uint64
		hex      string
	}{{1, "0000000000000001"}, {10, "000000000000000a"}, {math.MaxUint64, "ffffffffffffffff"}} {
		raw, err := t422RelationshipReferenceRecord(initial, initial, test.quantity)
		if err != nil || string(raw[:]) != "RL1:2:2R:"+test.hex+"\n" {
			t.Fatal(raw, err)
		}
	}
	if _, err := t422RelationshipReferenceRecord(initial, initial, 0); err == nil {
		t.Fatal("zero batch acquired a wire record")
	}
	if err := writeT422RelationshipRecord(nil, initial, initial, readaccounting.RelationshipReferences, 0); err != nil {
		t.Fatal("zero batch added sink work", err)
	}
	invalid := initial
	invalid.Phase = 5
	if err := writeT422RelationshipRecord(nil, invalid, initial, readaccounting.RelationshipReferences, 0); err == nil {
		t.Fatal("zero batch skipped phase validation")
	}
	for _, event := range []readaccounting.RelationshipEvent{readaccounting.RelationshipBuild, readaccounting.RelationshipProjection} {
		for _, quantity := range []uint64{0, 2} {
			if err := writeT422RelationshipRecord(nil, initial, initial, event, quantity); err == nil {
				t.Fatal("non-unit B/P accepted")
			}
		}
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	for _, event := range []readaccounting.RelationshipEvent{readaccounting.RelationshipBuild, readaccounting.RelationshipProjection, readaccounting.RelationshipReferences} {
		if err := writeT422RelationshipRecord(writer, initial, initial, event, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeT422RelationshipRecord(writer, initial, initial, readaccounting.RelationshipReferences, 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil || string(raw) != "RL1:2:2B\nRL1:2:2P\nRL1:2:2R:0000000000000001\n" {
		t.Fatal(string(raw), err)
	}
	if err := writeT422RelationshipRecord(writer, initial, initial, readaccounting.RelationshipReferences, 1); err == nil {
		t.Fatal("positive closed-pipe event accepted")
	}
}
