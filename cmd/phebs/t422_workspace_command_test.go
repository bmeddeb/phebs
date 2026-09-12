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

// Positions and prerequisites only: these supplied states do not assert native
// samples, restored authority, a query, or a completed lifecycle cycle.
func TestT422WorkspaceEpochFiveSequence(t *testing.T) {
	rows := []struct {
		phase uint32
		step  uint8
		point string
	}{
		{12, 1, "archive_finish"}, {13, 1, "start"}, {13, 3, "finish"}, {14, 3, "start"}, {14, 3, "finish"},
	}
	for ordinal := uint8(0); ordinal <= 5; ordinal++ {
		control := &t422LifecycleControl{workspacePoint: ordinal, launch: &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: 5}}}
		for phase := uint32(1); phase <= 15; phase++ {
			for step := uint8(0); step <= 4; step++ {
				for _, point := range []string{"", "archive_finish", "start", "finish", "normalized", "ballast", "unknown"} {
					want := int(ordinal) < len(rows) && rows[ordinal].phase == phase && rows[ordinal].step == step && rows[ordinal].point == point
					if control.workspacePointMatches(phase, step, point) != want {
						t.Fatal(ordinal, phase, step, point)
					}
				}
			}
		}
	}
	for _, epoch := range []uint64{0, 1, 2, 3, 6} {
		control := &t422LifecycleControl{launch: &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: epoch}}}
		if control.workspacePointMatches(12, 1, "archive_finish") {
			t.Fatal("unselected epoch admitted a boundary", epoch)
		}
	}
}

func TestT422WorkspaceEpochFiveLifecyclePrerequisites(t *testing.T) {
	for _, bound := range []bool{false, true} {
		for point := uint8(0); point <= 5; point++ {
			control := &t422LifecycleControl{workspacePoint: point, launch: &t422SemanticLaunch{
				request: t422SemanticLaunchRequest{ServerEpoch: 5}}}
			control.launch.initial.Phase = 12
			if bound {
				control.workspaceBytes = &custodybytes.Observer{}
			}
			if !control.expected(t422LifecycleParkPath, 12) || control.expected(t422LifecycleParkPath, 13) || control.expected(t422LifecycleFreshDrive, 13) {
				t.Fatal("initial phase12 Park must precede fresh drive")
			}
			for _, row := range []struct {
				step uint8
				path string
			}{{1, t422LifecycleFreshDrive}, {2, t422LifecycleFreshRead}} {
				control.step = row.step
				want := !bound || point == 2
				if control.expected(row.path, 13) != want || control.expected(row.path, 12) || control.expected(row.path, 14) {
					t.Fatal(bound, point, row)
				}
			}
			control.step = 3
			if control.expected(t422LifecycleFreshDrive, 13) || control.expected(t422LifecycleFreshRead, 13) {
				t.Fatal("completed fresh cycle repeated")
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

func TestT422WorkspaceEpochFiveCanceledDoesNotMeasure(t *testing.T) {
	for _, point := range []string{"archive_finish", "start", "finish"} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls, failures := 0, 0
		observer := &custodybytes.Observer{}
		control := &t422LifecycleControl{ctx: ctx, step: 1, workspaceBytes: observer,
			launch:          &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: 5}, fail: func(error) { failures++ }},
			workspaceSample: func(context.Context) (custodybytes.Sample, error) { calls++; return custodybytes.Sample{}, nil }}
		request := httptest.NewRequest(http.MethodPost, t422WorkspaceSamplePath, nil).WithContext(ctx)
		request.Header.Set(t422WorkspacePointHeader, point)
		response := httptest.NewRecorder()
		control.sampleWorkspaceCommand(response, request)
		if response.Code != http.StatusConflict || calls != 0 || failures != 1 || control.err == nil || !observer.Snapshot().Unavailable || control.workspacePoint != 0 {
			t.Fatal("canceled boundary changed state or sampled", point, response.Code, calls, failures)
		}
	}
}
