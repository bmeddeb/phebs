package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/store"
)

const t422MarkerMaximum = 4 * time.Hour

var errT422MarkerControl = errors.New("T42.2 selected marker continuation refused")

type t422MarkerStage uint8

const (
	t422MarkerArmed t422MarkerStage = iota + 1
	t422MarkerHandling
	t422MarkerHit
	t422MarkerRecovering
	t422MarkerRecovered
	t422MarkerAdvancing
	t422MarkerComplete
)

type t422MarkerBarrier struct {
	ready    chan struct{}
	release  chan struct{}
	reading  bool
	reported bool
}

// One private control belongs to epoch three's single return-A publication.
// Main installs it before any relationship worker starts. Its two barriers
// are not HTTP handlers, read-accounting issuers, or scheduler completion proof.
type t422MarkerControl struct {
	ctx          context.Context
	cancel       context.CancelFunc
	launch       *t422SemanticLaunch
	runtime      *relationshippublication.Runtime
	services     *serviceRuntimeController
	exclusive    func(context.Context) (func(), error)
	continuation error

	mu                sync.Mutex
	stage             t422MarkerStage
	chunk             store.GenerationChunk
	target            relationshippublication.PublicationTransitionTargetV3
	hit               t422MarkerBarrier
	recovered         t422MarkerBarrier
	recoveryCommitted bool
	err               error
}

func newT422MarkerControl(
	ctx context.Context,
	launch *t422SemanticLaunch,
	runtime *relationshippublication.Runtime,
	services *serviceRuntimeController,
	exclusive func(context.Context) (func(), error),
) (*t422MarkerControl, error) {
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil ||
		launch.request.ServerEpoch != 3 || launch.initial.Phase != 6 ||
		runtime == nil || runtime.Store == nil || runtime.Acquire == nil || runtime.AfterV3MarkerInstall != nil ||
		services == nil || services.store == nil || runtime.Store != services.store || services.relationship != runtime ||
		services.acquire == nil || services.v3Catalog == nil || exclusive == nil ||
		!filepath.IsAbs(runtime.DataDir) || filepath.Clean(runtime.DataDir) != runtime.DataDir || services.dataDir != runtime.DataDir ||
		services.selections[launch.request.Repository].RuntimeVersion() != config.ServiceCatalogRuntimeV3 {
		return nil, errT422MarkerControl
	}
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil || !launch.matches(current) || current.Phase != 6 {
		return nil, errT422MarkerControl
	}
	lifetime, cancel := context.WithCancel(ctx)
	control := &t422MarkerControl{
		ctx: lifetime, cancel: cancel, launch: launch, runtime: runtime, services: services,
		exclusive: exclusive, continuation: errors.New("private selected marker continuation"), stage: t422MarkerArmed,
		hit:       t422MarkerBarrier{ready: make(chan struct{}), release: make(chan struct{})},
		recovered: t422MarkerBarrier{ready: make(chan struct{}), release: make(chan struct{})},
	}
	runtime.AfterV3MarkerInstall = control.markerInstalled
	return control, nil
}

// HandleV3 replaces only the selected V3 Handle/Advance pair. It never returns
// its private continuation to the scheduler, retries a marker, or skips the
// concrete service-runtime tail. The original scheduler heartbeat/owner stays
// alive until this method returns; the scheduler still owns its one Complete.
func (control *t422MarkerControl) HandleV3(ctx context.Context, chunk store.GenerationChunk) (retErr error) {
	if control == nil {
		return errT422MarkerControl
	}
	if ctx == nil || ctx.Err() != nil || !control.current() ||
		chunk.Repository != control.launch.request.Repository || chunk.Stage != relationshippublication.ScheduleStageV3 {
		return control.stop(errT422MarkerControl)
	}
	control.mu.Lock()
	if control.err != nil || control.stage != t422MarkerArmed {
		control.mu.Unlock()
		return control.stop(errT422MarkerControl)
	}
	control.stage, control.chunk = t422MarkerHandling, chunk
	control.mu.Unlock()
	operation, finish := control.operationContext(ctx)
	defer finish()
	complete := false
	defer func() {
		// Preserve native panic semantics, but never leave a waiter observing a
		// successful continuation when a native callback unwinds by panic.
		if !complete {
			retErr = control.stop(retErr)
		}
	}()
	err := control.runtime.HandleV3(operation, chunk)
	if !t422OnlyMarkerContinuation(err, control.continuation) {
		return err
	}
	if operation.Err() != nil || !control.current() {
		return errT422MarkerControl
	}
	control.mu.Lock()
	if control.err != nil || control.stage != t422MarkerHit || !control.hit.reported {
		control.mu.Unlock()
		return errT422MarkerControl
	}
	control.stage = t422MarkerRecovering
	control.mu.Unlock()
	if err := control.recoverSelected(operation); err != nil {
		return err
	}
	if operation.Err() != nil || !control.current() {
		return errT422MarkerControl
	}
	control.mu.Lock()
	if control.err != nil || control.stage != t422MarkerRecovered || !control.recovered.reported {
		control.mu.Unlock()
		return errT422MarkerControl
	}
	control.stage = t422MarkerAdvancing
	control.mu.Unlock()
	// recoverSelected has released the exclusive fence. Advance acquires the
	// existing shared fence itself: no upgrade, recursive lock, or fake tail.
	if err := control.services.Advance(operation, chunk.Repository); err != nil {
		return err
	}
	if operation.Err() != nil || !control.current() {
		return errT422MarkerControl
	}
	control.mu.Lock()
	if control.err != nil || operation.Err() != nil {
		control.mu.Unlock()
		return errT422MarkerControl
	}
	control.stage = t422MarkerComplete
	control.mu.Unlock()
	complete = true
	return nil
}

func (control *t422MarkerControl) markerInstalled(ctx context.Context, target relationshippublication.PublicationTransitionTargetV3) error {
	if ctx.Err() != nil || !control.current() {
		return control.stop(errT422MarkerControl)
	}
	control.mu.Lock()
	if control.err != nil || control.stage != t422MarkerHandling ||
		target.Request.Point != relationshippublication.PublicationTransitionHitV3 ||
		target.Request.Repository != control.chunk.Repository || target.ScheduleDigest != control.chunk.ScheduleDigest {
		control.mu.Unlock()
		return control.stop(errT422MarkerControl)
	}
	control.target, control.stage = target, t422MarkerHit
	close(control.hit.ready)
	control.mu.Unlock()
	if err := control.awaitReport(ctx, &control.hit); err != nil {
		return err
	}
	return control.continuation
}

func (control *t422MarkerControl) recoverSelected(ctx context.Context) error {
	release, err := control.exclusive(ctx)
	if err != nil {
		return err
	}
	if release == nil {
		return errT422MarkerControl
	}
	defer release()
	recovered, err := control.runtime.RecoverSelectedV3(ctx, control.chunk, control.target,
		func(ctx context.Context, target relationshippublication.PublicationTransitionRecoveryTargetV3) error {
			if ctx.Err() != nil || !control.current() {
				return control.stop(errT422MarkerControl)
			}
			control.mu.Lock()
			expected := control.target.Request
			if control.err != nil || control.stage != t422MarkerRecovering ||
				target.Repository != expected.Repository || target.TargetGenerationDigest != expected.TargetGenerationDigest ||
				target.TargetRootDigest != expected.TargetRootDigest || target.FormerStageName != expected.FormerStageName {
				control.mu.Unlock()
				return control.stop(errT422MarkerControl)
			}
			control.recoveryCommitted, control.stage = true, t422MarkerRecovered
			close(control.recovered.ready)
			control.mu.Unlock()
			return control.awaitReport(ctx, &control.recovered)
		})
	control.mu.Lock()
	control.recoveryCommitted = control.recoveryCommitted || recovered
	control.mu.Unlock()
	if !recovered && err == nil {
		return errT422MarkerControl
	}
	return err
}

func (control *t422MarkerControl) ReadHit(ctx context.Context) ([]byte, func(error), error) {
	return control.read(ctx, relationshippublication.PublicationTransitionHitV3)
}

func (control *t422MarkerControl) ReadRecovered(ctx context.Context) ([]byte, func(error), error) {
	return control.read(ctx, relationshippublication.PublicationTransitionRecoveredV3)
}

type t422MarkerObservation struct {
	Schema                 string `json:"schema"`
	Point                  string `json:"point"`
	PlanDigest             string `json:"plan_digest"`
	ScheduleDigest         string `json:"schedule_digest"`
	PriorGenerationDigest  string `json:"prior_generation_digest"`
	PriorRootDigest        string `json:"prior_root_digest"`
	TargetGenerationDigest string `json:"target_generation_digest"`
	TargetRootDigest       string `json:"target_root_digest"`
	TargetAuthorityDigest  string `json:"target_authority_digest"`
}

func (control *t422MarkerControl) read(ctx context.Context, point relationshippublication.PublicationTransitionPointV3) ([]byte, func(error), error) {
	if control == nil {
		return nil, nil, errT422MarkerControl
	}
	if ctx == nil || ctx.Err() != nil || !control.current() || !control.launch.requestCurrent(ctx) {
		return nil, nil, control.stop(errT422MarkerControl)
	}
	barrier, err := control.claimRead(point)
	if err != nil {
		return nil, nil, control.stop(err)
	}
	operation, finish := control.operationContext(ctx)
	select {
	case <-operation.Done():
		finish()
		return nil, nil, control.stop(operation.Err())
	case <-barrier.ready:
	}
	control.mu.Lock()
	target, failed := control.target, control.err != nil
	control.mu.Unlock()
	if failed || operation.Err() != nil || !control.launch.requestCurrent(ctx) {
		finish()
		return nil, nil, control.stop(errT422MarkerControl)
	}
	request := target.Request
	request.Point = point
	snapshot, err := relationshippublication.ReadPublicationTransitionV3(operation,
		filepath.Join(control.runtime.DataDir, "relationships"), request)
	if err != nil {
		finish()
		return nil, nil, control.stop(err)
	}
	body, err := json.Marshal(t422MarkerObservation{
		Schema: "t422-relationship-marker-observation-v3", Point: string(snapshot.Point),
		PlanDigest: target.PlanDigest, ScheduleDigest: target.ScheduleDigest,
		PriorGenerationDigest: snapshot.PriorGenerationDigest, PriorRootDigest: snapshot.PriorRootDigest,
		TargetGenerationDigest: snapshot.TargetGenerationDigest, TargetRootDigest: snapshot.TargetRootDigest,
		TargetAuthorityDigest: snapshot.TargetAuthorityDigest,
	})
	if err != nil {
		finish()
		return nil, nil, control.stop(err)
	}
	// Ownership of the operation context moves to the one after-report call.
	// In particular this is NOT the existing final-cache pre-report commit.
	return append(body, '\n'), func(reportErr error) {
		defer finish()
		if reportErr != nil || operation.Err() != nil || !control.current() || !control.launch.requestCurrent(ctx) {
			_ = control.stop(errors.Join(reportErr, operation.Err(), errT422MarkerControl))
			return
		}
		_ = control.finishReport(barrier)
	}, nil
}

func (control *t422MarkerControl) claimRead(point relationshippublication.PublicationTransitionPointV3) (*t422MarkerBarrier, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	barrier := &control.hit
	if point == relationshippublication.PublicationTransitionRecoveredV3 {
		if !control.hit.reported {
			return nil, errT422MarkerControl
		}
		barrier = &control.recovered
	} else if point != relationshippublication.PublicationTransitionHitV3 {
		return nil, errT422MarkerControl
	}
	if control.err != nil || control.stage == t422MarkerComplete || barrier.reading {
		return nil, errT422MarkerControl
	}
	barrier.reading = true
	return barrier, nil
}

func (control *t422MarkerControl) finishReport(barrier *t422MarkerBarrier) error {
	control.mu.Lock()
	valid := barrier == &control.hit && control.stage == t422MarkerHit ||
		barrier == &control.recovered && control.stage == t422MarkerRecovered && control.recoveryCommitted
	if control.err != nil || !valid || !barrier.reading || barrier.reported || control.ctx.Err() != nil {
		control.mu.Unlock()
		return control.stop(errT422MarkerControl)
	}
	barrier.reported = true
	close(barrier.release)
	control.mu.Unlock()
	return nil
}

func (control *t422MarkerControl) awaitReport(ctx context.Context, barrier *t422MarkerBarrier) error {
	select {
	case <-ctx.Done():
		return control.stop(ctx.Err())
	case <-control.ctx.Done():
		return control.stop(control.ctx.Err())
	case <-barrier.release:
	}
	control.mu.Lock()
	valid := control.err == nil && barrier.reported
	control.mu.Unlock()
	if !valid || ctx.Err() != nil || control.ctx.Err() != nil {
		return control.stop(errT422MarkerControl)
	}
	return nil
}

func (control *t422MarkerControl) current() bool {
	current, err := dispatchadmission.ProductionSemanticState()
	return control.ctx.Err() == nil && err == nil && control.launch.matches(current) && current.Phase == 6
}

func (control *t422MarkerControl) stop(cause error) error {
	control.mu.Lock()
	first := control.err == nil
	if first {
		control.err = errors.Join(errT422MarkerControl, cause)
	}
	err := control.err
	control.mu.Unlock()
	control.cancel()
	if first {
		control.launch.fail(err)
	}
	return err
}

// Both caller cancellation and the shared exact-mode lifetime cancel native
// work. The fixed ceiling is intersected with an earlier caller deadline; it
// never resets or extends the parent's unchanged overall phase deadline.
func (control *t422MarkerControl) operationContext(caller context.Context) (context.Context, func()) {
	deadline := time.Now().Add(t422MarkerMaximum)
	if earlier, ok := caller.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	if earlier, ok := control.ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	// Retain the request's exact read ledger, semantic reservation and worker
	// values. The shared lifetime supplies cancellation, not replacement values.
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

// HandleV3 wraps its callback and joins deferred cleanup errors. Accept only
// this instance's sole continuation leaf, never continuation + cleanup error.
func t422OnlyMarkerContinuation(err, continuation error) bool {
	for err != nil {
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			children := wrapped.Unwrap()
			if len(children) != 1 {
				return false
			}
			err = children[0]
		case interface{ Unwrap() error }:
			err = wrapped.Unwrap()
		default:
			return err == continuation
		}
	}
	return false
}
