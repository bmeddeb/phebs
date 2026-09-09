package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/servicecatalogingest"
	"github.com/bmeddeb/phebs/internal/store"
)

// Source-free barrier/route fixtures only. They cannot satisfy native bootstrap
// admission or return a durable activation observation; store tests own that.
func t422ActivationTestControl(t *testing.T) (*t422ActivationControl, *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	failures := new(atomic.Int32)
	return &t422ActivationControl{ctx: ctx, cancel: cancel, release: errors.New("private release"),
		launch: &t422SemanticLaunch{fail: func(error) { failures.Add(1) }},
		hit:    t422MarkerBarrier{ready: make(chan struct{}), release: make(chan struct{})}}, failures
}

func TestT422ActivationReportBarrier(t *testing.T) {
	for _, mode := range []string{"hit", "recovered", "before_target", "before_read", "recovered_before_release", "canceled", "failed_report"} {
		t.Run(mode, func(t *testing.T) {
			control, failures := t422ActivationTestControl(t)
			control.chunk.Identity = "native-target-fixture"
			control.hit.reading = true
			point := store.ServiceStateV3ActivationTransitionHit
			if mode == "recovered" || mode == "recovered_before_release" {
				point = store.ServiceStateV3ActivationTransitionRecovered
				control.hit.reported, control.releaseAccepted, control.recoveredReading = true, true, true
			}
			switch mode {
			case "before_target":
				control.chunk.Identity = ""
			case "before_read":
				control.hit.reading = false
			case "recovered_before_release":
				control.releaseAccepted = false
			case "canceled":
				control.cancel()
			case "failed_report":
				_ = control.stop(errors.New("report sink failed"))
			}
			select {
			case <-control.hit.release:
				t.Fatal("body/read claim released target before report")
			default:
			}
			err := control.finishReport(point)
			good := mode == "hit" || mode == "recovered"
			if (err == nil) != good {
				t.Fatal("report barrier admission differs", err)
			}
			if good {
				if point == store.ServiceStateV3ActivationTransitionHit {
					select {
					case <-control.hit.release:
					default:
						t.Fatal("successful report did not release hit")
					}
				} else if !control.recoveredReported {
					t.Fatal("recovered report missing")
				}
				if err := control.finishReport(point); err == nil {
					t.Fatal("report replay admitted")
				}
			}
			if failures.Load() != 1 {
				t.Fatal("failure/replay did not latch exactly once", failures.Load())
			}
		})
	}
}

func TestT422ActivationOperationCancellationKeepsRequestValues(t *testing.T) {
	control, _ := t422ActivationTestControl(t)
	type key struct{}
	caller := context.WithValue(t.Context(), key{}, "reserved request")
	ctx, finish := control.operationContext(caller)
	if ctx.Value(key{}) != "reserved request" {
		t.Fatal("native operation dropped request binding/ledger")
	}
	deadline, _ := ctx.Deadline()
	maximum, _ := control.ctx.Deadline()
	if deadline != maximum {
		t.Fatal("operation reset the four-hour absolute ceiling")
	}
	control.cancel()
	<-ctx.Done()
	finish()
	finish()
}

func TestT422ActivationReleaseDoesNotAcceptForeignOrMixedErrors(t *testing.T) {
	for _, mode := range []string{"foreign", "wrapped", "mixed", "no_live_semantics"} {
		t.Run(mode, func(t *testing.T) {
			control, failures := t422ActivationTestControl(t)
			cause := errors.New("ordinary failure")
			switch mode {
			case "wrapped":
				cause = errors.Join(control.release)
			case "mixed":
				cause = errors.Join(control.release, cause)
			case "no_live_semantics":
				cause = control.release
			}
			accepted, err := control.controlledRelease(t.Context(), store.GenerationChunk{}, cause)
			if accepted || (err == nil) != (mode == "foreign") || control.releaseAccepted {
				t.Fatal("unowned release authority admitted", accepted, err)
			}
			if failures.Load() != map[bool]int32{true: 0, false: 1}[mode == "foreign"] {
				t.Fatal("classification did not distinguish foreign error from owned refusal")
			}
		})
	}
}

func TestT422ActivationPhaseAndClosedRoutes(t *testing.T) {
	raw, current := t422SemanticTestRequest(t)
	raw = bytes.Replace(raw, []byte(`"server_epoch":1`), []byte(`"server_epoch":2`), 1)
	current.ProducerID, current.Phase, current.InputSHA256 = 3, 5, sha256.Sum256(raw)
	launch, err := decodeT422SemanticLaunch(raw, current)
	if err != nil {
		t.Fatal(err)
	}
	for phase := uint32(0); phase <= 16; phase++ {
		value := current
		value.Phase = phase
		if t422ActivationPhase(launch, value) != (phase == 5) {
			t.Fatal("phase admitted", phase)
		}
	}
	for _, change := range []func(*dispatchadmission.ProductionSemanticSnapshot){
		func(v *dispatchadmission.ProductionSemanticSnapshot) { v.ProducerID = 2 },
		func(v *dispatchadmission.ProductionSemanticSnapshot) { v.InputSHA256[0] ^= 1 },
		func(v *dispatchadmission.ProductionSemanticSnapshot) { v.Mode = "" },
	} {
		v := current
		change(&v)
		if t422ActivationPhase(launch, v) {
			t.Fatal("unbound phase admitted")
		}
	}
	state := &t421ExactReadAccountingState{semantic: launch, activation: &t422ActivationControl{launch: launch}}
	for _, path := range []string{t422ActivationHitPath, t422ActivationRecoveredPath} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
		request.Header.Set(t421ExactReadOrdinalHeader, "1")
		if state.activationRead(request) == nil || !t422SemanticRequestRoute(request) {
			t.Fatal("fixed exact route missing")
		}
		for _, bad := range []*http.Request{
			httptest.NewRequest(http.MethodPost, path, nil), httptest.NewRequest(http.MethodGet, path+"?target=x", nil),
			httptest.NewRequest(http.MethodGet, path+"?", nil), httptest.NewRequest(http.MethodGet, path, bytes.NewBufferString("target")),
		} {
			if state.activationRead(bad) != nil {
				t.Fatal("open activation request admitted")
			}
		}
	}
	if state.activationRead(nil) != nil || state.activationRead(httptest.NewRequest(http.MethodGet, "/api/t422/logical-activation/%68it", nil)) != nil {
		t.Fatal("missing/encoded request admitted")
	}
}

func TestT422ActivationConstructorRequiresNativeBootstrap(t *testing.T) {
	repository := "local/tmp/activation"
	services := &serviceRuntimeController{store: &store.Surreal{}, acquire: func(context.Context) (func(), error) { t.Fatal("constructor took a native lock"); return nil, nil },
		v3Catalog: &servicecatalogingest.V3Reconciler{}, selections: map[string]config.ServiceCatalog{repository: {Runtime: config.ServiceCatalogRuntimeV3}}}
	launch := &t422SemanticLaunch{request: t422SemanticLaunchRequest{ServerEpoch: 2, Repository: repository},
		initial: dispatchadmission.ProductionSemanticSnapshot{ProducerID: 3, Phase: 5}, fail: func(error) {}}
	for _, ctx := range []context.Context{nil, t.Context()} {
		if control, err := newT422ActivationControl(ctx, launch, services); err == nil || control != nil || services.afterActivationTransitionCommit != nil {
			t.Fatal("constructor manufactured admission or installed partial hook")
		}
	}
}

func TestT422ActivationCapturesOnlyOwnedCommittedTarget(t *testing.T) {
	for _, mode := range []string{"complete", "missing_pin", "wrong_backend", "wrong_repo", "wrong_stage", "wrong_offset", "wrong_length", "wrong_attempt", "wrong_priority", "wrong_status", "bad_plan", "bad_schedule", "bad_unit", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			control, _ := t422ActivationTestControl(t)
			const repository = "example.com/mono"
			control.launch.request.Repository = repository
			selector := store.ServiceRuntimeSelector{Repository: repository, Backend: store.ServiceRuntimeV3, Digest: "sha256:" + strings.Repeat("a", 64)}
			control.services = &serviceRuntimeController{pins: map[string]*serviceRuntimeProcessPins{repository: {selector: selector}}}
			chunk := store.GenerationChunk{Repository: repository, Stage: store.ServiceStateV3ActivateStage, Offset: 9, Length: 1, Attempt: 0,
				Priority: store.GenerationPriorityNeverRun, Status: store.GenerationChunkRunning, Generation: "sha256:" + strings.Repeat("b", 64),
				ScheduleDigest: "sha256:" + strings.Repeat("c", 64), Identity: "sha256:" + strings.Repeat("d", 64), LeaseToken: "native-fixture-lease"}
			switch mode {
			case "missing_pin":
				delete(control.services.pins, repository)
			case "wrong_backend":
				control.services.pins[repository].selector.Backend = store.ServiceRuntimeV2
			case "wrong_repo":
				chunk.Repository = "other"
			case "wrong_stage":
				chunk.Stage = store.ServiceStateV3ReconcileStage
			case "wrong_offset":
				chunk.Offset--
			case "wrong_length":
				chunk.Length++
			case "wrong_attempt":
				chunk.Attempt++
			case "wrong_priority":
				chunk.Priority = store.GenerationPriorityStale
			case "wrong_status":
				chunk.Status = store.GenerationChunkDone
			case "bad_plan":
				chunk.Generation = "missing"
			case "bad_schedule":
				chunk.ScheduleDigest = "missing"
			case "bad_unit":
				chunk.Identity = "missing"
			case "duplicate":
				control.chunk = chunk
			}
			err := control.captureCommitted(chunk)
			if (err == nil) != (mode == "complete") {
				t.Fatal("wrong target capture", err)
			}
			if mode == "complete" {
				if control.chunk != chunk || control.prior != selector {
					t.Fatal("native fixture identities replaced")
				}
				if control.acceptRelease(chunk) == nil {
					t.Fatal("captured body alone authorized scheduler release")
				}
			}
		})
	}
}

func TestT422ActivationReleaseRequiresExactUnchangedClaim(t *testing.T) {
	for _, mode := range []string{"complete", "no_report", "other_unit", "other_lease", "other_attempt", "already_released", "failed"} {
		t.Run(mode, func(t *testing.T) {
			control, _ := t422ActivationTestControl(t)
			chunk := store.GenerationChunk{Identity: "owned", LeaseToken: "owned-lease", Attempt: 0}
			control.chunk, control.hit.reported = chunk, true
			switch mode {
			case "no_report":
				control.hit.reported = false
			case "other_unit":
				chunk.Identity = "other"
			case "other_lease":
				chunk.LeaseToken = "other"
			case "other_attempt":
				chunk.Attempt++
			case "already_released":
				control.releaseAccepted = true
			case "failed":
				control.err = errT422ActivationControl
			}
			err := control.acceptRelease(chunk)
			if (err == nil) != (mode == "complete") {
				t.Fatal("wrong exact-claim release admission", err)
			}
			if mode == "complete" && (!control.releaseAccepted || control.acceptRelease(chunk) == nil) {
				t.Fatal("one-shot scheduler release not preserved")
			}
		})
	}
}
