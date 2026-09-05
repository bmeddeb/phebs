package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestT422LifecycleFixedRecipe(t *testing.T) {
	type operation struct {
		path  string
		phase uint32
	}
	pressure := []operation{
		{t422LifecycleParkPath, 8},
		{t422LifecycleNormalDrive, 9}, {t422LifecycleNormalRead, 9}, {t422LifecycleCollectRead, 9},
		{t422LifecycleRefuseRead, 10}, {t422LifecycleLatchedRead, 11},
		{t422LifecycleRecoveryDrive, 11}, {t422LifecycleRecoveryRead, 11}, {t422LifecycleResumedRead, 11},
	}
	candidates := append(append([]operation{}, pressure...), operation{t422LifecycleFreshDrive, 13}, operation{t422LifecycleFreshRead, 13})
	for epoch := uint64(1); epoch <= 5; epoch++ {
		initial := map[uint64]uint32{1: 2, 2: 5, 3: 6, 4: 8, 5: 12}[epoch]
		control := &t422LifecycleControl{launch: &t422SemanticLaunch{
			initial: dispatchadmission.ProductionSemanticSnapshot{Phase: initial},
		}}
		control.launch.request.ServerEpoch = epoch
		for step := uint8(0); step <= 9; step++ {
			control.step = step
			for _, candidate := range candidates {
				for phase := uint32(1); phase <= 15; phase++ {
					want := step == 0 && candidate.path == t422LifecycleParkPath && phase == initial ||
						epoch == 4 && int(step) < len(pressure) && candidate.path == pressure[step].path && phase == pressure[step].phase ||
						epoch == 5 && step == 1 && candidate.path == t422LifecycleFreshDrive && phase == 13 ||
						epoch == 5 && step == 2 && candidate.path == t422LifecycleFreshRead && phase == 13
					if got := control.expected(candidate.path, phase); got != want {
						t.Fatalf("epoch=%d step=%d path=%s phase=%d got=%v want=%v", epoch, step, candidate.path, phase, got, want)
					}
				}
			}
		}
	}
	readCounts := map[uint32]int{}
	for _, operation := range pressure {
		if t422LifecycleRead(operation.path) {
			readCounts[operation.phase]++
		}
	}
	if readCounts[9] != 2 || readCounts[10] != 1 || readCounts[11] != 3 {
		t.Fatal("pressure recipe changed its frozen 2/1/3 report counts")
	}
}

// These are returned native-result/event tests, not a live store cycle or
// production bootstrap. Every numeric prefix comes from the supplied result;
// no fixture asserts an end-to-end phase pass.
func TestT422LifecycleNativeReturnedPrefix(t *testing.T) {
	for _, mode := range []string{"success", "native-error", "before-sweep", "sink-error", "sink-panic", "overshoot", "unknown-owner", "outside-drive"} {
		t.Run(mode, func(t *testing.T) {
			failures, calls := 0, 0
			control := &t422LifecycleControl{ctx: t.Context(), names: []string{lifecycle.SearchOwner},
				launch: &t422SemanticLaunch{fail: func(error) { failures++ }}, busy: true,
				operation: t422LifecycleNormalDrive, phase: 9}
			control.launch.request.ServerEpoch = 4
			result := lifecycle.OwnerResult{Owner: lifecycle.SearchOwner, AttemptedAt: time.Now().UTC(),
				Completeness: lifecycle.Exact, Scanned: 3, Deleted: 2, LogicalBytes: 5, RootBytes: 2, MemberBytes: 3}
			if mode == "native-error" || mode == "before-sweep" {
				result.Err = errors.New("private native path and cursor must not appear")
				result.Completeness = lifecycle.Unavailable
			}
			if mode == "before-sweep" {
				result = lifecycle.OwnerResult{Completeness: lifecycle.Unavailable, Err: result.Err}
			}
			if mode == "unknown-owner" {
				result.Owner = "/private/unregistered-owner"
			}
			if mode == "outside-drive" {
				control.operation = t422LifecycleCollectRead
			}
			if mode == "overshoot" {
				result.Deleted = lifecycle.MaxDeletesPerTick + 1
				result.Scanned = result.Deleted
			}
			var event t422LifecycleEvent
			control.sink = func(raw []byte) error {
				calls++
				if len(raw) > t422LifecycleEventBytes || strings.Contains(string(raw), "private") || json.Unmarshal(raw, &event) != nil {
					t.Fatal("event escaped its bounded source-free shape")
				}
				control.mu.Lock()
				prefix := control.prefix[0]
				control.mu.Unlock()
				if prefix.ReturnedTicks != event.ReturnedTick || prefix.Deleted != event.TotalDeleted {
					t.Fatal("sink ran before actual prefix retention")
				}
				if mode == "sink-error" {
					return errors.New("private sink failure")
				}
				if mode == "sink-panic" {
					panic("private sink failure")
				}
				return nil
			}
			control.ObserveOwner(result)
			if mode == "outside-drive" || mode == "unknown-owner" {
				if calls != 0 || failures != 1 {
					t.Fatal("unbound native event was emitted")
				}
				return
			}
			if calls != 1 || (failures != 0) != (mode == "sink-error" || mode == "sink-panic" || mode == "overshoot") {
				t.Fatal("event/sink completion changed", calls, failures)
			}
			if mode == "before-sweep" {
				if event.ReturnedTick != 1 || event.OwnerTurns != 0 || event.TotalDeleted != 0 || event.Owner != "" || event.AttemptedAtNano != 0 {
					t.Fatal("pre-sweep Tick refusal invented owner work")
				}
			} else if event.OwnerTurns != 1 || event.TotalDeleted != uint64(result.Deleted) || event.MaxDeleted != uint64(result.Deleted) {
				t.Fatal("actual positive prefix was lost or clamped")
			}
		})
	}
}

func TestT422LifecycleNativeEventBound(t *testing.T) {
	failures := 0
	control := &t422LifecycleControl{ctx: t.Context(), busy: true, operation: t422LifecycleNormalDrive,
		launch: &t422SemanticLaunch{fail: func(error) { failures++ }},
		sink:   func([]byte) error { t.Fatal("cap+1 event emitted"); return nil }}
	control.prefix[0].ReturnedTicks = uint64(lifecycle.MaxCycleObservationTurns)
	control.ObserveOwner(lifecycle.OwnerResult{})
	if failures != 1 || control.prefix[0].ReturnedTicks != uint64(lifecycle.MaxCycleObservationTurns) {
		t.Fatal("event cap changed the retained prefix")
	}
}

func TestT422LifecycleCanonicalFenceRequests(t *testing.T) {
	now := time.Now().Add(-time.Second).UnixNano()
	canonical := strconv.FormatInt(now, 10)
	for _, test := range []struct {
		name   string
		modify func(*http.Request)
	}{
		{"missing", func(r *http.Request) { r.Header.Del(t422LifecycleFenceHeader) }},
		{"duplicate", func(r *http.Request) { r.Header.Add(t422LifecycleFenceHeader, canonical) }},
		{"zero", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, "0") }},
		{"negative", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, "-1") }},
		{"leading-zero", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, "0"+canonical) }},
		{"leading-plus", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, "+"+canonical) }},
		{"space", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, " "+canonical) }},
		{"fraction", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, "1.0") }},
		{"overflow", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, "9223372036854775808") }},
		{"oversize", func(r *http.Request) { r.Header.Set(t422LifecycleFenceHeader, strings.Repeat("9", 20)) }},
		{"future", func(r *http.Request) {
			r.Header.Set(t422LifecycleFenceHeader, strconv.FormatInt(time.Now().Add(time.Hour).UnixNano(), 10))
		}},
		{"method", func(r *http.Request) { r.Method = http.MethodPost }},
		{"query", func(r *http.Request) { r.URL.RawQuery = "percent=80" }},
		{"empty-query", func(r *http.Request) { r.URL.ForceQuery = true }},
		{"escaped", func(r *http.Request) { r.URL.RawPath = "/api/t422/lifecycle/pressure-%38%30" }},
		{"body", func(r *http.Request) { r.ContentLength = 1 }},
		{"unknown-body", func(r *http.Request) { r.ContentLength = -1 }},
		{"chunked", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, t422LifecycleCollectRead, nil)
			request.Header.Set(t422LifecycleFenceHeader, canonical)
			fence, err := t422LifecycleRequest(request, false)
			if err != nil || fence.UnixNano() != now {
				t.Fatal("canonical nanosecond fence changed", err)
			}
			test.modify(request)
			if _, err := t422LifecycleRequest(request, false); err == nil {
				t.Fatal("malformed private request accepted")
			}
		})
	}
	for _, path := range []string{t422LifecycleParkPath, t422LifecycleNormalDrive, t422LifecycleFreshDrive,
		t422LifecycleNormalRead, t422LifecycleRecoveryRead, t422LifecycleResumedRead, t422LifecycleFreshRead} {
		command := t422LifecycleCommand(path)
		method := http.MethodGet
		if command {
			method = http.MethodPost
		}
		request := httptest.NewRequest(method, path, nil)
		if _, err := t422LifecycleRequest(request, command); err != nil {
			t.Fatal("closed fence-free request refused", path)
		}
		request.Header.Set(t422LifecycleFenceHeader, canonical)
		if _, err := t422LifecycleRequest(request, command); err == nil {
			t.Fatal("unsolicited fence accepted", path)
		}
	}
}

func TestT422LifecycleContextRetainsRequestAndJoinsCancellation(t *testing.T) {
	lifetime, stop := context.WithCancel(t.Context())
	defer stop()
	control := &t422LifecycleControl{ctx: lifetime}
	caller, cancelCaller := context.WithTimeout(t.Context(), time.Second)
	defer cancelCaller()
	reservation := dispatchadmission.ProductionSemanticSnapshot{Phase: 9, OrdinaryOwnersDrained: true}
	caller = context.WithValue(caller, t422SemanticRequestKey{}, reservation)
	caller, ledger, err := readaccounting.Start(caller, readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, release := control.operationContext(caller, t422LifecycleCollectRead)
	defer release()
	if ctx.Value(t422SemanticRequestKey{}) != reservation {
		t.Fatal("native operation detached request identity")
	}
	if deadline, _ := ctx.Deadline(); deadline.After(time.Now().Add(2 * time.Second)) {
		t.Fatal("native operation extended earlier request deadline")
	}
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err == nil {
		t.Fatal("native operation detached zero-limit R ledger")
	}
	if _, err := ledger.Finish(); err == nil {
		t.Fatal("failed R charge disappeared")
	}
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shared exact failure did not cancel the operation")
	}
	release()
	release()
}

func TestT422LifecycleUnadmittedOperationsRefuseBeforeNative(t *testing.T) {
	failures := 0
	control := &t422LifecycleControl{ctx: t.Context(), launch: &t422SemanticLaunch{fail: func(error) { failures++ }}}
	// No runner/collector is supplied. If unauthenticated work crosses the
	// actual bootstrap check it cannot hide behind a fake native success.
	if err := control.begin(t.Context(), t422LifecycleNormalDrive); !errors.Is(err, errT422LifecycleControl) {
		t.Fatal("unadmitted drive accepted", err)
	}
	if err := control.complete(t.Context(), t422LifecycleNormalDrive, nil); err == nil || failures != 1 || control.step != 0 {
		t.Fatal("failed operation advanced or failed more than once", err, failures)
	}
	request := httptest.NewRequest(http.MethodPost, t422LifecycleParkPath, nil)
	writer := httptest.NewRecorder()
	control.command(writer, request)
	if writer.Code != http.StatusConflict || failures != 1 {
		t.Fatal("unauthenticated command crossed native boundary")
	}
	if got, err := newT422LifecycleControl(t.Context(), control.launch, nil); err == nil || got != nil {
		t.Fatal("caller manufactured a production lifecycle binding")
	}
}

func TestT422LifecycleOrdinaryTransportUnchanged(t *testing.T) {
	called := 0
	state := t421NewExactReadAccountingState(func([]byte) error { t.Fatal("unexpected report"); return nil }, func(error) { t.Fatal("unexpected failure") })
	handler := state.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	for _, path := range []string{t422LifecycleParkPath, t422LifecycleNormalDrive, t422LifecycleNormalRead} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if called != 3 {
		t.Fatal("absent semantic lifecycle changed ordinary routing")
	}
}
