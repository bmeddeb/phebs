package main

import (
	"encoding/json"
	"net/http"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const (
	t422WorkspaceSamplePath  = "/api/t422/lifecycle/sample-workspace"
	t422WorkspacePointHeader = "X-Phebs-T422-Workspace-Point"
)

type t422WorkspaceSampleResponse struct {
	LogicalBytes   uint64 `json:"logical_bytes"`
	AllocatedBytes uint64 `json:"allocated_bytes"`
}

// These are callback positions, not proof that a parent mutated ballast or
// finished F. The owning parent must independently enforce those boundaries.
func t422WorkspacePointMatches(ordinal uint8, phase uint32, step uint8, point string) bool {
	sequence := [...]struct {
		phase uint32
		step  uint8
		point string
	}{
		{9, 1, "start"}, {9, 3, "normalized"}, {9, 3, "ballast"}, {9, 4, "finish"},
		{10, 4, "start"}, {10, 4, "ballast"}, {10, 5, "finish"},
		{11, 5, "start"}, {11, 5, "ballast"}, {11, 6, "removed"}, {11, 9, "finish"},
	}
	return int(ordinal) < len(sequence) && sequence[ordinal].phase == phase && sequence[ordinal].step == step && sequence[ordinal].point == point
}

func (control *t422LifecycleControl) workspacePointMatches(phase uint32, step uint8, point string) bool {
	switch control.launch.request.ServerEpoch {
	case 1:
		return step == 0 && (point == "finish" &&
			(control.workspacePoint == 0 && phase == 2 || control.workspacePoint == 1 && phase == 3 || control.workspacePoint == 3 && phase == 4) ||
			point == "start" && control.workspacePoint == 2 && phase == 4)
	case 2:
		return step == 0 && control.workspacePoint == 0 && point == "finish" && phase == 5
	case 3:
		sequence := [...]struct {
			phase uint32
			point string
		}{
			{6, "finish"}, {7, "start"}, {7, "prepared"}, {7, "finish"}, {8, "start"}, {8, "prepared"},
		}
		return step == 0 && int(control.workspacePoint) < len(sequence) &&
			sequence[control.workspacePoint].phase == phase && sequence[control.workspacePoint].point == point
	case 4:
		if phase == 8 {
			return step <= 1 && control.workspacePoint == 0 && point == "finish"
		}
		return control.workspacePoint > 0 && t422WorkspacePointMatches(control.workspacePoint-1, phase, step, point)
	case 5:
		sequence := [...]struct {
			phase uint32
			step  uint8
			point string
		}{
			{12, 1, "archive_finish"}, {13, 1, "start"}, {13, 3, "finish"},
			{14, 3, "start"}, {14, 3, "finish"},
		}
		return int(control.workspacePoint) < len(sequence) && sequence[control.workspacePoint].phase == phase &&
			sequence[control.workspacePoint].step == step && sequence[control.workspacePoint].point == point
	default:
		return false
	}
}

// Called under the existing control mutex (or by immutable test fixtures).
func (control *t422LifecycleControl) workspacePrecedes(path string) bool {
	if control.launch != nil && control.launch.request.ServerEpoch == 5 {
		return path != t422LifecycleFreshDrive && path != t422LifecycleFreshRead || control.workspacePoint == 2
	}
	point := control.workspacePoint
	if control.launch != nil && control.launch.request.ServerEpoch == 4 && point > 0 {
		point--
	}
	switch path {
	case t422LifecycleNormalDrive, t422LifecycleNormalRead:
		return point == 1
	case t422LifecycleCollectRead:
		return point == 3
	case t422LifecycleRefuseRead:
		return point == 6
	case t422LifecycleLatchedRead:
		return point == 9
	case t422LifecycleRecoveryDrive, t422LifecycleRecoveryRead, t422LifecycleResumedRead:
		return point == 10
	default:
		return true
	}
}

func (control *t422LifecycleControl) sampleWorkspaceCommand(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	completed := false
	defer func() {
		if !completed {
			if control.workspaceBytes != nil {
				_ = control.workspaceBytes.Fail()
			}
			_ = control.stop()
		}
	}()
	points := request.Header.Values(t422WorkspacePointHeader)
	if len(points) != 1 || points[0] == "prepared" || !control.current(ctx, true) || control.workspaceSample == nil || control.workspaceBytes == nil ||
		(control.launch.request.ServerEpoch < 1 || control.launch.request.ServerEpoch > 5) {
		http.Error(writer, "workspace sample refused", http.StatusConflict)
		return
	}
	admitted := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	control.mu.Lock()
	valid := control.err == nil && !control.busy && control.workspacePointMatches(admitted.Phase, control.step, points[0])
	if valid {
		control.busy = true
	}
	control.mu.Unlock()
	// An actual local runner ACK establishes the parked boundary. No Gate.Check,
	// store query, phase-control exchange or lock spanning the walk is added.
	if !valid || control.runner.Park(ctx) != nil {
		http.Error(writer, "workspace sample refused", http.StatusConflict)
		return
	}
	value, err := control.workspaceSample(ctx)
	if err != nil || !control.current(ctx, true) {
		http.Error(writer, "workspace sample refused", http.StatusConflict)
		return
	}
	raw, err := json.Marshal(t422WorkspaceSampleResponse{LogicalBytes: value.LogicalBytes, AllocatedBytes: value.AllocatedBytes})
	if err != nil {
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if n, err := writer.Write(raw); err != nil || n != len(raw) || !control.current(ctx, true) {
		return
	}
	control.mu.Lock()
	valid = control.err == nil && control.busy && control.workspacePointMatches(admitted.Phase, control.step, points[0])
	if valid {
		control.workspacePoint++
		control.busy = false
	}
	control.mu.Unlock()
	completed = valid
}
