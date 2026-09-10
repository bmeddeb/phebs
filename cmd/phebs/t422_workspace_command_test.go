package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

// Pure fixed-position checks, not evidence of a parent mutation or native walk.
func TestT422WorkspaceCommandSequence(t *testing.T) {
	rows := []struct {
		phase uint32
		step  uint8
		point string
	}{
		{9, 1, "start"}, {9, 3, "normalized"}, {9, 3, "ballast"}, {9, 4, "finish"},
		{10, 4, "start"}, {10, 4, "ballast"}, {10, 5, "finish"},
		{11, 5, "start"}, {11, 5, "ballast"}, {11, 6, "removed"}, {11, 9, "finish"},
	}
	for ordinal := uint8(0); ordinal <= 11; ordinal++ {
		for phase := uint32(1); phase <= 15; phase++ {
			for step := uint8(0); step <= 10; step++ {
				for _, point := range []string{"", "start", "normalized", "ballast", "removed", "finish", "unknown"} {
					want := int(ordinal) < len(rows) && rows[ordinal].phase == phase && rows[ordinal].step == step && rows[ordinal].point == point
					if t422WorkspacePointMatches(ordinal, phase, step, point) != want {
						t.Fatal(ordinal, phase, step, point)
					}
				}
			}
		}
	}
}

func TestT422WorkspaceCommandRequiredBeforeLifecycle(t *testing.T) {
	for _, row := range []struct {
		path    string
		ordinal uint8
	}{
		{t422LifecycleNormalDrive, 1}, {t422LifecycleNormalRead, 1}, {t422LifecycleCollectRead, 3},
		{t422LifecycleRefuseRead, 6}, {t422LifecycleLatchedRead, 9}, {t422LifecycleRecoveryDrive, 10},
		{t422LifecycleRecoveryRead, 10}, {t422LifecycleResumedRead, 10},
	} {
		for ordinal := uint8(0); ordinal < 12; ordinal++ {
			control := &t422LifecycleControl{workspacePoint: ordinal}
			if control.workspacePrecedes(row.path) != (ordinal == row.ordinal) {
				t.Fatal(row, ordinal)
			}
		}
	}
}

func TestT422WorkspaceCommandClosedRequestAndResponse(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, t422WorkspaceSamplePath, nil)
	request.Header.Set(t422WorkspacePointHeader, "start")
	if _, err := t422LifecycleRequest(request, true); err != nil || !t422SemanticRequestRoute(request) {
		t.Fatal("sample route rejected", err)
	}
	for _, path := range []string{t422LifecycleParkPath, t422LifecycleNormalDrive, t422LifecycleNormalRead} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		command := t422LifecycleCommand(path)
		if !command {
			request.Method = http.MethodGet
		}
		request.Header.Set(t422WorkspacePointHeader, "start")
		if _, err := t422LifecycleRequest(request, command); err == nil {
			t.Fatal("workspace header on unrelated route", path)
		}
	}
	raw, err := json.Marshal(t422WorkspaceSampleResponse{LogicalBytes: math.MaxUint64, AllocatedBytes: 0})
	if err != nil || string(raw) != `{"logical_bytes":18446744073709551615,"allocated_bytes":0}` {
		t.Fatal(string(raw), err)
	}
}

func TestT422WorkspaceCommandUnadmittedDoesNotMeasure(t *testing.T) {
	for _, points := range [][]string{nil, {"start"}, {"start", "start"}, {"unknown"}} {
		failures, calls := 0, 0
		control := &t422LifecycleControl{ctx: t.Context(), launch: &t422SemanticLaunch{fail: func(error) { failures++ }},
			workspaceSample: func(context.Context) (custodybytes.Sample, error) { calls++; return custodybytes.Sample{}, nil }}
		request := httptest.NewRequest(http.MethodPost, t422WorkspaceSamplePath, nil)
		for _, point := range points {
			request.Header.Add(t422WorkspacePointHeader, point)
		}
		response := httptest.NewRecorder()
		control.sampleWorkspaceCommand(response, request)
		if response.Code != http.StatusConflict || calls != 0 || failures != 1 || control.err == nil {
			t.Fatal("unadmitted measurement", points, response.Code, calls, failures)
		}
	}
}
