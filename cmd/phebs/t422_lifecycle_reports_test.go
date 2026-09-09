package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func TestT422LifecycleBoundPipe(t *testing.T) {
	// The selected seam requires the logger's actual stderr file object.
	// This test is serial and restores both globals before returning.
	originalStderr, originalWriter := os.Stderr, log.Writer()
	t.Cleanup(func() { os.Stderr = originalStderr; log.SetOutput(originalWriter) })
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	os.Stderr = write
	log.SetOutput(write)
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 5, Phase: 8, InputSHA256: [32]byte{1}}
	failures := 0
	sink, err := newT422LifecycleSink(initial, func(error) { failures++ })
	if err != nil {
		t.Fatal(err)
	}
	if err := sink([]byte(`{"actual":"native-json"}`)); err != nil {
		t.Fatal(err)
	}
	if err := sink(make([]byte, t422LifecycleEventBytes+1)); err == nil || failures != 1 {
		t.Fatal("oversized event did not refuse", failures, err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(read)
	want := "LCB1:5:sha256:01" + strings.Repeat("00", 31) + "\nLC1:{\"actual\":\"native-json\"}\n"
	if err != nil || string(raw) != want {
		t.Fatal(string(raw), err)
	}
	if err := sink([]byte("{}")); err == nil || failures != 2 {
		t.Fatal("closed output did not fail the launch", failures, err)
	}
}

type t422LifecycleCursorReadFailure struct{}

func (t422LifecycleCursorReadFailure) GetLifecycleCursor(_ context.Context, key string) (string, uint64, error) {
	if key == "rotation" {
		return "", 0, nil
	}
	return "", 0, errors.New("fixture owner cursor read failed")
}

func (t422LifecycleCursorReadFailure) CompareAndSwapLifecycleCursor(context.Context, string, uint64, string) error {
	panic("cursor-read refusal must precede every cursor write")
}

func TestT422LifecycleNativeCursorReadFailureEvent(t *testing.T) {
	controller, err := lifecycle.NewController(t422LifecycleCursorReadFailure{}, lifecycle.SearchGenerationOwnerImpl{})
	if err != nil {
		t.Fatal(err)
	}
	result := controller.Tick(t.Context())
	if result.Owner != lifecycle.SearchOwner || !result.AttemptedAt.IsZero() || result.Err == nil {
		t.Fatal("real pre-Sweep failure shape changed", result)
	}
	var event t422LifecycleEvent
	control := &t422LifecycleControl{ctx: t.Context(), names: []string{lifecycle.SearchOwner},
		launch: &t422SemanticLaunch{fail: func(err error) { t.Fatal(err) }}, busy: true,
		operation: t422LifecycleNormalDrive, phase: 9,
		sink: func(raw []byte) error { return json.Unmarshal(raw, &event) }}
	control.launch.request.ServerEpoch = 4
	control.prefix[0] = t422LifecyclePrefix{ReturnedTicks: 1, OwnerTurns: 1, Deleted: 1, MaxDeleted: 1}
	control.ObserveOwner(result)
	if event.ReturnedTick != 2 || event.Owner != lifecycle.SearchOwner || event.AttemptedAtNano != 0 || !event.Failed ||
		event.OwnerTurns != 1 || event.TotalDeleted != 1 || event.MaxDeleted != 1 || event.Scanned != 0 || event.Deleted != 0 {
		t.Fatal("pre-Sweep error lost its returned tick or invented owner work", event)
	}
}

func TestT422LifecycleBindingRefusals(t *testing.T) {
	original := log.Writer()
	defer log.SetOutput(original)
	var output bytes.Buffer
	log.SetOutput(&output)
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	if sink, err := newT422LifecycleSink(initial, func(error) {}); err == nil || sink != nil || output.Len() != 0 {
		t.Fatal("non-native output admitted", err)
	}
	if sink, err := newT422LifecycleSink(initial, nil); err == nil || sink != nil {
		t.Fatal("missing failure owner admitted", err)
	}
}
