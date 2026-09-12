package main

import (
	"bytes"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Position/report tests use supplied state and values. Actual FD6 inheritance
// is covered by TestProductionWorkspaceEarlyInherited; native engine/HTTP
// composition and complete early-phase sampling remain separate gates.
func TestT422WorkspaceEarlyFinishPositionsAndReports(t *testing.T) {
	control := &t422LifecycleControl{launch: &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: 1}}}
	for ordinal := uint8(0); ordinal < 3; ordinal++ {
		control.workspacePoint = ordinal
		for phase := uint32(1); phase <= 4; phase++ {
			for step := uint8(0); step < 2; step++ {
				for _, point := range []string{"start", "finish", "archive_finish"} {
					want := ordinal < 2 && phase == uint32(ordinal)+2 && step == 0 && point == "finish"
					if control.workspacePointMatches(phase, step, point) != want {
						t.Fatal(ordinal, phase, step, point)
					}
				}
			}
		}
	}
	var output bytes.Buffer
	state := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3,
		ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	reports := &t422WorkspaceReports{writer: &output, initial: state}
	for phase := uint32(2); phase <= 3; phase++ {
		state.Phase = phase
		if reports.begin(state) != nil || reports.complete(custodybytes.Sample{LogicalBytes: 11, AllocatedBytes: 22}) != nil {
			t.Fatal("fixed finish report refused")
		}
	}
	if output.Len() != 2*(26+60) || reports.begin(state) == nil || output.Len() != 172 {
		t.Fatal("repeated finish emitted or pair size changed", output.Len())
	}
}
