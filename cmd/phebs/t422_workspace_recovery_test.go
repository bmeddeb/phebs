package main

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Supplied positions and report bytes; no engine, preparation mutation or
// selected authentication is asserted by this closed-table unit test.
func TestT422WorkspaceRecoveryPositions(t *testing.T) {
	points := []struct {
		phase uint32
		point string
	}{{6, "finish"}, {7, "start"}, {7, "prepared"}, {7, "finish"}, {8, "start"}, {8, "prepared"}}
	control := &t422LifecycleControl{launch: &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: 3}}}
	for ordinal := uint8(0); int(ordinal) <= len(points); ordinal++ {
		control.workspacePoint = ordinal
		for phase := uint32(1); phase <= 15; phase++ {
			for _, point := range []string{"start", "prepared", "finish", "removed", "unknown"} {
				want := int(ordinal) < len(points) && points[ordinal].phase == phase && points[ordinal].point == point
				if control.workspacePointMatches(phase, 0, point) != want || control.workspacePointMatches(phase, 1, point) {
					t.Fatal(ordinal, phase, point)
				}
			}
		}
	}
	control.launch.request.ServerEpoch = 4
	control.workspacePoint = 0
	if !control.workspacePointMatches(8, 0, "finish") || !control.workspacePointMatches(8, 1, "finish") || control.workspacePointMatches(9, 1, "start") {
		t.Fatal("missing epoch-four finish boundary")
	}
	control.workspacePoint = 1
	if control.workspacePointMatches(8, 1, "finish") || !control.workspacePointMatches(9, 1, "start") || control.workspacePrecedes(t422LifecycleNormalDrive) {
		t.Fatal("phase-nine start ordering")
	}
	control.workspacePoint = 2
	if !control.workspacePrecedes(t422LifecycleNormalDrive) {
		t.Fatal("phase-nine start did not release original normal guard")
	}
}

func TestT422WorkspaceRecoveryReports(t *testing.T) {
	for _, row := range []struct {
		producer, phase uint32
		maximum         uint64
	}{{4, 7, 3}, {4, 8, 2}, {5, 8, 1}} {
		var output bytes.Buffer
		initialPhase := uint32(6)
		if row.producer == 5 {
			initialPhase = 8
		}
		state := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: row.producer, Phase: initialPhase, InputSHA256: [32]byte{1}}
		reports := &t422WorkspaceReports{writer: &output, initial: state}
		state.Phase = row.phase
		for i := uint64(0); i < row.maximum; i++ {
			if reports.begin(state) != nil || reports.complete(custodybytes.Sample{LogicalBytes: i, AllocatedBytes: i + 1}) != nil {
				t.Fatal(row, i)
			}
		}
		if uint64(output.Len()) != row.maximum*86 || reports.begin(state) == nil {
			t.Fatal(row, output.Len())
		}
	}
}

// Wire omission is byte-exact. This tests optional-field serialization on an
// empty authority, not maximum full-response fit or native root authority.
func TestT422WorkspacePreparationOmission(t *testing.T) {
	base := t422StalePreparationObservation{}
	before, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Workspace = &t422WorkspaceSampleResponse{LogicalBytes: math.MaxUint64, AllocatedBytes: math.MaxUint64}
	after, err := json.Marshal(base)
	if err != nil || len(after)-len(before) != 90 || len(after) > 16<<10 {
		t.Fatal(len(before), len(after), err)
	}
	if !bytes.HasPrefix(after, append(append([]byte{}, before[:len(before)-1]...), []byte(",\"workspace\":")...)) {
		t.Fatal("old fields changed")
	}
	base.Workspace = &t422WorkspaceSampleResponse{}
	zero, err := json.Marshal(base)
	if err != nil || bytes.Equal(zero, before) || !bytes.Contains(zero, []byte("\"workspace\":{\"logical_bytes\":0,\"allocated_bytes\":0}")) {
		t.Fatal("bound zero lost", err)
	}
	base.Workspace = nil
	omitted, err := json.Marshal(base)
	if err != nil || !bytes.Equal(omitted, before) {
		t.Fatal("legacy omission changed", err)
	}
}
