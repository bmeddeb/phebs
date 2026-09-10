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

// Called under the existing control mutex (or by immutable test fixtures).
func (control *t422LifecycleControl) workspacePrecedes(path string) bool {
	switch path {
	case t422LifecycleNormalDrive, t422LifecycleNormalRead:
		return control.workspacePoint == 1
	case t422LifecycleCollectRead:
		return control.workspacePoint == 3
	case t422LifecycleRefuseRead:
		return control.workspacePoint == 6
	case t422LifecycleLatchedRead:
		return control.workspacePoint == 9
	case t422LifecycleRecoveryDrive, t422LifecycleRecoveryRead, t422LifecycleResumedRead:
		return control.workspacePoint == 10
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
	if len(points) != 1 || !control.current(ctx, true) || control.workspaceSample == nil || control.workspaceBytes == nil ||
		control.launch.request.ServerEpoch != 4 {
		http.Error(writer, "workspace sample refused", http.StatusConflict)
		return
	}
	admitted := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	control.mu.Lock()
	valid := control.err == nil && !control.busy && t422WorkspacePointMatches(control.workspacePoint, admitted.Phase, control.step, points[0])
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
	valid = control.err == nil && control.busy && t422WorkspacePointMatches(control.workspacePoint, admitted.Phase, control.step, points[0])
	if valid {
		control.workspacePoint++
		control.busy = false
	}
	control.mu.Unlock()
	completed = valid
}
