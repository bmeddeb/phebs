package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t422ActivationHitPath       = "/api/t422/logical-activation/hit"
	t422ActivationRecoveredPath = "/api/t422/logical-activation/recovered"
	t422ActivationMaximum       = 4 * time.Hour
)

var errT422ActivationControl = errors.New("T42.2 selected activation control refused")

// One native phase-five operation. The actual committed chunk and pinned
// selectors own the identities; no request supplies a target or readiness flag.
type t422ActivationControl struct {
	ctx                                 context.Context
	cancel                              context.CancelFunc
	launch                              *t422SemanticLaunch
	services                            *serviceRuntimeController
	release                             error
	mu                                  sync.Mutex
	hit                                 t422MarkerBarrier
	chunk                               store.GenerationChunk
	prior                               store.ServiceRuntimeSelector
	releaseAccepted                     bool
	recoveredReading, recoveredReported bool
	err                                 error
}

func newT422ActivationControl(ctx context.Context, launch *t422SemanticLaunch, services *serviceRuntimeController) (*t422ActivationControl, error) {
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || launch.request.ServerEpoch != 2 ||
		launch.initial.ProducerID != 3 || launch.initial.Phase != 5 || services == nil || services.store == nil ||
		services.acquire == nil || services.v3Catalog == nil || services.afterActivationTransitionCommit != nil ||
		services.selections[launch.request.Repository].RuntimeVersion() != config.ServiceCatalogRuntimeV3 {
		return nil, errT422ActivationControl
	}
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil || !t422ActivationPhase(launch, current) {
		return nil, errT422ActivationControl
	}
	lifetime, cancel := context.WithTimeout(ctx, t422ActivationMaximum)
	control := &t422ActivationControl{ctx: lifetime, cancel: cancel, launch: launch, services: services,
		release: errors.New("T42.2 selected activation interruption"), hit: t422MarkerBarrier{ready: make(chan struct{}), release: make(chan struct{})}}
	services.afterActivationTransitionCommit = control.committed
	return control, nil
}

// Called under the existing service controller and shared mutation locks.
// Keep the scheduler's lease/heartbeat/owner until the exact hit report joins.
func (control *t422ActivationControl) committed(ctx context.Context, chunk store.GenerationChunk) error {
	if ctx == nil || ctx.Err() != nil || !control.current() {
		return control.stop(errT422ActivationControl)
	}
	if err := control.captureCommitted(chunk); err != nil {
		return err
	}
	operation, finish := control.operationContext(ctx)
	defer finish()
	select {
	case <-operation.Done():
		return control.stop(operation.Err())
	case <-control.hit.release:
	}
	control.mu.Lock()
	valid := control.err == nil && control.hit.reported
	control.mu.Unlock()
	if !valid || operation.Err() != nil || !control.current() {
		return control.stop(errT422ActivationControl)
	}
	return control.release
}

// The caller still owns services.mu and the native committed chunk's lease.
func (control *t422ActivationControl) captureCommitted(chunk store.GenerationChunk) error {
	pins := control.services.pins[chunk.Repository]
	if pins == nil || pins.selector.Backend != store.ServiceRuntimeV3 || pins.selector.Repository != control.launch.request.Repository ||
		chunk.Repository != pins.selector.Repository || chunk.Stage != store.ServiceStateV3ActivateStage ||
		chunk.Offset != store.ServiceStateV3ActivationTransitionTargetOffset || chunk.Length != 1 || chunk.Attempt != 0 ||
		chunk.Priority != store.GenerationPriorityNeverRun || chunk.Status != store.GenerationChunkRunning ||
		!t422SemanticDigest(chunk.Generation) || !t422SemanticDigest(chunk.ScheduleDigest) || !t422SemanticDigest(chunk.Identity) {
		return control.stop(errT422ActivationControl)
	}
	control.mu.Lock()
	if control.err != nil || control.chunk.Identity != "" {
		control.mu.Unlock()
		return control.stop(errT422ActivationControl)
	}
	control.chunk, control.prior = chunk, pins.selector
	close(control.hit.ready)
	control.mu.Unlock()
	return nil
}

// The scheduler calls only after the original heartbeat has joined. Exact
// identity is deliberate: a wrapped/mixed interruption is not release authority.
func (control *t422ActivationControl) controlledRelease(ctx context.Context, chunk store.GenerationChunk, cause error) (bool, error) {
	if cause != control.release {
		if errors.Is(cause, control.release) {
			return false, control.stop(errT422ActivationControl)
		}
		return false, nil
	}
	if ctx == nil || ctx.Err() != nil || !control.current() {
		return false, control.stop(errT422ActivationControl)
	}
	if err := control.acceptRelease(chunk); err != nil {
		return false, err
	}
	return true, nil
}

func (control *t422ActivationControl) acceptRelease(chunk store.GenerationChunk) error {
	control.mu.Lock()
	valid := control.err == nil && control.hit.reported && !control.releaseAccepted && chunk == control.chunk
	if valid {
		control.releaseAccepted = true
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422ActivationControl)
	}
	return nil
}

type t422ActivationObservation struct {
	Schema                 string `json:"schema"`
	Point                  string `json:"point"`
	SelectorDigest         string `json:"selector_digest"`
	CatalogRootDigest      string `json:"catalog_root_digest"`
	SearchGenerationDigest string `json:"search_generation_digest"`
	PlanDigest             string `json:"plan_digest"`
	ScheduleDigest         string `json:"schedule_digest"`
	UnitDigest             string `json:"unit_digest"`
}

func (control *t422ActivationControl) ReadHit(ctx context.Context) ([]byte, func(error), error) {
	return control.read(ctx, store.ServiceStateV3ActivationTransitionHit)
}

func (control *t422ActivationControl) ReadRecovered(ctx context.Context) ([]byte, func(error), error) {
	return control.read(ctx, store.ServiceStateV3ActivationTransitionRecovered)
}

func (control *t422ActivationControl) read(ctx context.Context, point store.ServiceStateV3ActivationTransitionPoint) ([]byte, func(error), error) {
	if ctx == nil || ctx.Err() != nil || !control.current() || !control.launch.requestCurrent(ctx) {
		return nil, nil, control.stop(errT422ActivationControl)
	}
	control.mu.Lock()
	valid := control.err == nil
	switch point {
	case store.ServiceStateV3ActivationTransitionHit:
		valid = valid && !control.hit.reading && !control.releaseAccepted
		if valid {
			control.hit.reading = true
		}
	case store.ServiceStateV3ActivationTransitionRecovered:
		valid = valid && control.releaseAccepted && !control.recoveredReading
		if valid {
			control.recoveredReading = true
		}
	default:
		valid = false
	}
	control.mu.Unlock()
	if !valid {
		return nil, nil, control.stop(errT422ActivationControl)
	}
	operation, finish := control.operationContext(ctx)
	select {
	case <-operation.Done():
		finish()
		return nil, nil, control.stop(operation.Err())
	case <-control.hit.ready:
	}
	control.mu.Lock()
	chunk, prior, failed := control.chunk, control.prior, control.err != nil
	control.mu.Unlock()
	if failed || operation.Err() != nil {
		finish()
		return nil, nil, control.stop(errT422ActivationControl)
	}
	selector := prior
	if point == store.ServiceStateV3ActivationTransitionRecovered {
		current, stateErr := dispatchadmission.ProductionSemanticState()
		admitted, admittedOK := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
		if stateErr != nil || !admittedOK || !current.OrdinaryOwnersDrained || !admitted.OrdinaryOwnersDrained || !control.launch.sameRequest(admitted, current) {
			finish()
			return nil, nil, control.stop(errT422ActivationControl)
		}
		// The parent calls once after its ready T. Copy a genuine current pin;
		// the native five-read validator confirms it around the durable rows.
		control.services.mu.Lock()
		pins := control.services.pins[chunk.Repository]
		if pins != nil {
			selector = pins.selector
		}
		control.services.mu.Unlock()
		if selector == prior || selector.Backend != store.ServiceRuntimeV3 || selector.Repository != prior.Repository ||
			selector.CatalogRootDigest == prior.CatalogRootDigest || selector.SearchGenerationDigest != prior.SearchGenerationDigest {
			finish()
			return nil, nil, control.stop(errT422ActivationControl)
		}
	}
	snapshot, err := control.services.store.ReadServiceStateV3ActivationTransition(operation, store.ServiceStateV3ActivationTransitionRequest{
		Point: point, ExpectedSelector: selector, PlanDigest: chunk.Generation, ScheduleDigest: chunk.ScheduleDigest, UnitDigest: chunk.Identity})
	if err != nil {
		finish()
		return nil, nil, control.stop(err)
	}
	body, err := json.Marshal(t422ActivationObservation{Schema: "t422-activation-observation-v1", Point: string(snapshot.Point),
		SelectorDigest: snapshot.SelectorDigest, CatalogRootDigest: snapshot.CatalogRootDigest, SearchGenerationDigest: snapshot.SearchGenerationDigest,
		PlanDigest: snapshot.PlanDigest, ScheduleDigest: snapshot.ScheduleDigest, UnitDigest: snapshot.UnitDigest})
	if err != nil {
		finish()
		return nil, nil, control.stop(err)
	}
	return append(body, '\n'), func(reportErr error) {
		defer finish()
		if reportErr != nil || operation.Err() != nil || !control.current() || !control.launch.requestCurrent(ctx) {
			_ = control.stop(errors.Join(reportErr, operation.Err(), errT422ActivationControl))
			return
		}
		_ = control.finishReport(point)
	}, nil
}

func (control *t422ActivationControl) finishReport(point store.ServiceStateV3ActivationTransitionPoint) error {
	control.mu.Lock()
	valid := control.err == nil && control.ctx.Err() == nil
	switch point {
	case store.ServiceStateV3ActivationTransitionHit:
		valid = valid && control.hit.reading && !control.hit.reported && control.chunk.Identity != "" && !control.releaseAccepted
		if valid {
			control.hit.reported = true
			close(control.hit.release)
		}
	case store.ServiceStateV3ActivationTransitionRecovered:
		valid = valid && control.releaseAccepted && control.recoveredReading && !control.recoveredReported
		if valid {
			control.recoveredReported = true
		}
	default:
		valid = false
	}
	control.mu.Unlock()
	if !valid {
		return control.stop(errT422ActivationControl)
	}
	return nil
}

func (control *t422ActivationControl) current() bool {
	current, err := dispatchadmission.ProductionSemanticState()
	return control.ctx.Err() == nil && err == nil && t422ActivationPhase(control.launch, current)
}

func t422ActivationPhase(launch *t422SemanticLaunch, current dispatchadmission.ProductionSemanticSnapshot) bool {
	return launch != nil && launch.request.ServerEpoch == 2 && launch.matches(current) && current.ProducerID == 3 && current.Phase == 5
}

func (control *t422ActivationControl) stop(cause error) error {
	control.mu.Lock()
	first := control.err == nil
	if first {
		control.err = errors.Join(errT422ActivationControl, cause)
	}
	err := control.err
	control.mu.Unlock()
	control.cancel()
	if first {
		control.launch.fail(err)
	}
	return err
}

func (control *t422ActivationControl) operationContext(caller context.Context) (context.Context, func()) {
	deadline, _ := control.ctx.Deadline()
	ctx, cancel := context.WithDeadline(caller, deadline)
	joined := make(chan struct{})
	stop := context.AfterFunc(control.ctx, func() { cancel(); close(joined) })
	if control.ctx.Err() != nil {
		cancel()
	}
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			if !stop() {
				<-joined
			}
		})
	}
}

func (state *t421ExactReadAccountingState) activationRead(request *http.Request) func(context.Context) ([]byte, func(error), error) {
	if state.activation == nil || state.semantic == nil || state.activation.launch != state.semantic || request == nil || request.URL == nil ||
		request.Method != http.MethodGet || request.URL.Path != request.URL.EscapedPath() || request.URL.RawQuery != "" || request.URL.ForceQuery ||
		request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		return nil
	}
	switch request.URL.Path {
	case t422ActivationHitPath:
		return state.activation.ReadHit
	case t422ActivationRecoveredPath:
		return state.activation.ReadRecovered
	default:
		return nil
	}
}
