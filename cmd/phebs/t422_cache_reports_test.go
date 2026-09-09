package main

import (
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestT422CacheFramingAndPhase(t *testing.T) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	for _, event := range []readaccounting.CacheEvent{readaccounting.CacheHit, readaccounting.CacheRootLoad, readaccounting.CacheMemberLoad, readaccounting.CacheRootValidation, readaccounting.CacheMemberValidation} {
		phase := uint32(0)
		if event == readaccounting.CacheRootValidation || event == readaccounting.CacheMemberValidation {
			phase = 2
		}
		raw, err := t422CacheRecord(initial, initial, event, phase)
		if err != nil || string(raw[:]) != "CC1:2:2"+string(event)+"\n" {
			t.Fatal(raw, err)
		}
	}
	for _, mode := range []string{"different-phase", "missing-phase", "hit-with-phase", "bad-kind", "different-input", "different-producer", "different-mode"} {
		t.Run(mode, func(t *testing.T) {
			current, event, phase := initial, readaccounting.CacheRootValidation, uint32(2)
			switch mode {
			case "different-phase":
				current.Phase = 3 // A legal producer phase, but not this load's.
			case "missing-phase":
				phase = 0
			case "hit-with-phase":
				event = readaccounting.CacheHit
			case "bad-kind":
				event, phase = readaccounting.CacheEvent('x'), 0
			case "different-input":
				current.InputSHA256[0]++
			case "different-producer":
				current.ProducerID++
			case "different-mode":
				current.Mode = ""
			}
			if _, err := t422CacheRecord(current, initial, event, phase); err == nil {
				t.Fatal("invalid native cache frame accepted")
			}
		})
	}
}

func TestT422CacheBoundPipe(t *testing.T) {
	oldStderr, oldWriter := os.Stderr, log.Writer()
	t.Cleanup(func() { os.Stderr = oldStderr; log.SetOutput(oldWriter) })
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = write.Close() }()
	os.Stderr = write
	log.SetOutput(write)
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	failures := 0
	ctx, err := bindT422CacheReports(t.Context(), initial, func(error) { failures++ })
	if err != nil {
		t.Fatal(err)
	}
	// A real pipe binding never creates a production lifetime by itself.
	if _, err := readaccounting.ObserveCache(ctx, true, readaccounting.CacheHit, 0); err == nil || failures != 1 {
		t.Fatal("unavailable actual phase admitted", failures, err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(read)
	if err != nil || string(raw) != "CCB1:2:sha256:01"+strings.Repeat("00", 31)+"\n" || len(raw) != 79 {
		t.Fatal(string(raw), err)
	}
	if _, err := bindT422CacheReports(t.Context(), initial, func(error) { failures++ }); err == nil || failures != 1 {
		t.Fatal("closed output binding admitted", failures, err)
	}
	if _, err := bindT422CacheReports(t.Context(), initial, nil); err == nil {
		t.Fatal("missing failure owner admitted")
	}
	log.SetOutput(io.Discard)
	if _, err := bindT422CacheReports(t.Context(), initial, func(error) {}); err == nil {
		t.Fatal("non-native output admitted")
	}
}
