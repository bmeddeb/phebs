package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

const (
	t422CleanupObservationFiles = 24_000
	t422CleanupSearchFiles      = 600
	// Inventory removes its objects, segment, inventory and collecting dirs;
	// search removes its abandoned stage after its regular shards.
	t422CleanupExpectedDeleted = t422CleanupObservationFiles + 4 + t422CleanupSearchFiles + 1
	t422CleanupExpectedTurns   = 16 * ((t422CleanupObservationFiles + 4 + 1023) / 1024)
)

func assertT422CleanupNativeReports(t *testing.T, raw string, input [32]byte) {
	t.Helper()
	var count, deleted, maxDeleted uint64
	bindings := 0
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "LCB") {
			if line != fmt.Sprintf("LCB1:5:sha256:%x", input) {
				t.Fatal("cleanup lifecycle binding", line)
			}
			bindings++
		}
		if !strings.HasPrefix(line, "LC1:") {
			continue
		}
		var event t422LifecycleEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "LC1:")), &event); err != nil ||
			event.Epoch != 4 || event.Phase != 9 || event.Failed || event.ReturnedTick != count+1 || event.OwnerTurns != count+1 ||
			event.Deleted < 0 || event.Deleted > lifecycle.SelectedCleanupDeleteLimit(event.Owner) {
			t.Fatal("cleanup lifecycle event", line, err)
		}
		count++
		deleted += uint64(event.Deleted)
		maxDeleted = max(maxDeleted, uint64(event.Deleted))
		if event.TotalDeleted != deleted || event.MaxDeleted != maxDeleted {
			t.Fatal("cleanup lifecycle prefix", line)
		}
	}
	if bindings != 1 || count != t422CleanupExpectedTurns || deleted != t422CleanupExpectedDeleted || maxDeleted != 1024 {
		t.Fatal("joined cleanup observation", bindings, count, deleted, maxDeleted)
	}
}

func workspaceReportFixture(producer uint32, writer io.Writer) (*t422WorkspaceReports, dispatchadmission.ProductionSemanticSnapshot) {
	phase := uint32(8)
	if producer == 6 {
		phase = 12
	}
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: producer, Phase: phase, InputSHA256: [32]byte{1}}
	return &t422WorkspaceReports{writer: writer, initial: initial}, initial
}

// Supplied byte values exercise framing only, not native measurement.
func TestT422WorkspaceReportPairs(t *testing.T) {
	var output bytes.Buffer
	reports, state := workspaceReportFixture(5, &output)
	binding, err := t422SourceBinding(state)
	if err != nil || len(binding) != 79 {
		t.Fatal("binding size", len(binding), err)
	}
	state.Phase = 9
	if reports.begin(state) != nil || reports.complete(custodybytes.Sample{LogicalBytes: math.MaxUint64, AllocatedBytes: 1}) != nil {
		t.Fatal("first pair")
	}
	want := "WB1:5:9B:0000000000000001\nWB1:5:9S:0000000000000001:ffffffffffffffff:0000000000000001\n"
	if output.String() != want || output.Len() != 26+60 {
		t.Fatal(output.String())
	}
	state.Phase = 10
	if reports.begin(state) != nil || reports.failed() == nil {
		t.Fatal("failed pair")
	}
	want += "WB1:5:AB:0000000000000002\nWB1:5:AF:0000000000000002\n"
	if output.String() != want || reports.begin(state) == nil {
		t.Fatal("failure did not retain prior positive and latch", output.String())
	}
}

func TestT422WorkspaceReportGuards(t *testing.T) {
	for _, mode := range []string{"missing_begin", "double_begin", "wrong_input", "wrong_producer", "wrong_phase", "backward_phase", "phase_limit", "short_sink", "failed_sink"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			reports, state := workspaceReportFixture(5, &output)
			state.Phase = 9
			var err error
			switch mode {
			case "missing_begin":
				err = reports.complete(custodybytes.Sample{})
			case "double_begin":
				if reports.begin(state) != nil {
					t.Fatal("begin")
				}
				err = reports.begin(state)
			case "wrong_input":
				state.InputSHA256[0]++
				err = reports.begin(state)
			case "wrong_producer":
				state.ProducerID = 6
				err = reports.begin(state)
			case "wrong_phase":
				state.Phase = 8
				err = reports.begin(state)
			case "backward_phase":
				reports.phase = 11
				err = reports.begin(state)
			case "phase_limit":
				_, reports.counts[0] = t422WorkspaceSampleSlot(5, 9)
				err = reports.begin(state)
			case "short_sink", "failed_sink":
				reports.writer = workspaceReportBadWriter{failed: mode == "failed_sink"}
				err = reports.begin(state)
			}
			if err == nil || reports.err == nil {
				t.Fatal("invalid report accepted")
			}
			before := output.String()
			if reports.complete(custodybytes.Sample{LogicalBytes: 99}) == nil || output.String() != before {
				t.Fatal("failed stream resumed")
			}
		})
	}
}

type workspaceReportBadWriter struct{ failed bool }

func (writer workspaceReportBadWriter) Write(raw []byte) (int, error) {
	if writer.failed {
		return 0, errors.New("fixture sink failure")
	}
	return len(raw) - 1, nil
}

func TestT422WorkspaceReportDerivedLimits(t *testing.T) {
	var total uint64
	for _, row := range []struct {
		producer, phase uint32
		maximum         uint64
	}{{5, 9, 4101}, {5, 10, 4}, {5, 11, 4102}, {6, 13, 4096}} {
		_, maximum := t422WorkspaceSampleSlot(row.producer, row.phase)
		if maximum != row.maximum {
			t.Fatal(row, maximum)
		}
		total += maximum
	}
	if total*86+2*79 != 1058216 {
		t.Fatal("derived successful pair headroom", total)
	}
}

func TestT422WorkspaceReportWholeSequence(t *testing.T) {
	for _, producer := range []uint32{5, 6} {
		reports, state := workspaceReportFixture(producer, io.Discard)
		var expected uint64
		for phase := uint32(1); phase <= 15; phase++ {
			_, maximum := t422WorkspaceSampleSlot(producer, phase)
			state.Phase = phase
			for count := uint64(0); count < maximum; count++ {
				if reports.begin(state) != nil || reports.complete(custodybytes.Sample{}) != nil {
					t.Fatal("derived exact endpoint refused", producer, phase, count)
				}
				expected++
			}
		}
		if reports.sequence != expected || reports.pending {
			t.Fatal("global sequence changed across phases", producer, reports.sequence, expected)
		}
		state.Phase = reports.phase
		if reports.begin(state) == nil || reports.sequence != expected {
			t.Fatal("derived endpoint admitted another sample")
		}
	}
}

func TestT422WorkspaceReportSinkFailureKeepsPrefix(t *testing.T) {
	var output bytes.Buffer
	reports, state := workspaceReportFixture(5, &output)
	state.Phase = 9
	if reports.begin(state) != nil || reports.complete(custodybytes.Sample{LogicalBytes: 7, AllocatedBytes: 512}) != nil || reports.begin(state) != nil {
		t.Fatal("actual report prefix")
	}
	prefix := output.String()
	reports.writer = workspaceReportBadWriter{failed: true}
	if reports.complete(custodybytes.Sample{LogicalBytes: 9}) == nil || reports.failed() == nil || !reports.pending || output.String() != prefix {
		t.Fatal("sink failure replaced prior positive or completed pending pair")
	}
}

// Consume only the actual joined helper stderr. These assertions are not a
// replacement for the independent parent parser or whole-phase completeness.
func assertT422NativeWorkspaceReports(t *testing.T, raw string, input [32]byte, wantSamples int) {
	t.Helper()
	binding := fmt.Sprintf("WBB1:5:sha256:%x", input)
	bindings, count := 0, 0
	pending := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "WBB") {
			if line != binding {
				t.Fatal("wrong native workspace binding", line)
			}
			bindings++
		}
		if !strings.HasPrefix(line, "WB1:") {
			continue
		}
		if !pending {
			count++
			if line != fmt.Sprintf("WB1:5:9B:%016x", count) {
				t.Fatal("native begin", line)
			}
			pending = true
			continue
		}
		var sequence, logical, allocated uint64
		n, err := fmt.Sscanf(line, "WB1:5:9S:%016x:%016x:%016x", &sequence, &logical, &allocated)
		if n != 3 || err != nil || len(line) != 59 || sequence != uint64(count) || logical < 1<<20 || allocated == 0 {
			t.Fatal("native sample", line, err)
		}
		pending = false
	}
	if bindings != 1 || count != wantSamples || pending {
		t.Fatal("native workspace report coverage", bindings, count, pending)
	}
}
