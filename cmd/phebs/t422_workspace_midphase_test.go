package main

import "testing"

// Position-only models; actual selected auth, engine guard and byte walks are
// not supplied by these tables.
func TestT422WorkspaceMidphasePositions(t *testing.T) {
	for _, epoch := range []uint64{1, 2, 3} {
		control := &t422LifecycleControl{launch: &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: epoch}}}
		for ordinal := uint8(0); ordinal < 5; ordinal++ {
			control.workspacePoint = ordinal
			for phase := uint32(4); phase <= 8; phase++ {
				for _, point := range []string{"start", "finish", "ballast"} {
					want := epoch == 1 && phase == 4 && (ordinal == 2 && point == "start" || ordinal == 3 && point == "finish") ||
						epoch > 1 && phase == uint32(epoch)+3 && ordinal == 0 && point == "finish" ||
						epoch == 3 && (phase == 7 && (ordinal == 1 && point == "start" || ordinal == 3 && point == "finish") || phase == 8 && ordinal == 4 && point == "start")
					if control.workspacePointMatches(phase, 0, point) != want || control.workspacePointMatches(phase, 1, point) {
						t.Fatal(epoch, ordinal, phase, point)
					}
				}
			}
		}
	}
	for _, row := range []struct {
		producer, phase uint32
		slot            int
		maximum         uint64
	}{{2, 4, 9, 3}, {3, 5, 10, 1}, {4, 6, 11, 2}} {
		slot, maximum := t422WorkspaceSampleSlot(row.producer, row.phase)
		if slot != row.slot || maximum != row.maximum {
			t.Fatal(row, slot, maximum)
		}
	}
}
