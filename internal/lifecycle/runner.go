package lifecycle

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const (
	DefaultIdleInterval          = time.Hour
	DefaultBacklogDelay          = 5 * time.Second
	DefaultPressureRecoveryDelay = 250 * time.Millisecond
)

type Reporter func(OwnerResult)
type CapacityReporter func(Capacity, error)

func Run(
	ctx context.Context,
	controller *Controller,
	gate *Gate,
	idleInterval, backlogDelay time.Duration,
	report Reporter,
	reportCapacity CapacityReporter,
) {
	RunWithOwners(ctx, controller, gate, idleInterval, backlogDelay, report, reportCapacity, nil)
}

// RunWithOwners adds an optional complete-turn boundary without restarting the
// runner or resetting its fair-cycle and pressure-recovery state. Resume does
// not itself wake an idle timer or add a capacity probe.
func RunWithOwners(
	ctx context.Context,
	controller *Controller,
	gate *Gate,
	idleInterval, backlogDelay time.Duration,
	report Reporter,
	reportCapacity CapacityReporter,
	owners *dispatchadmission.Owners,
) {
	RunWithControl(ctx, controller, gate, idleInterval, backlogDelay, report, reportCapacity, owners, nil)
}

// RunWithControl adds an optional exact-mode rendezvous to this same runner.
// Constructing control grants no execution authority; its caller must own the
// complete controlled request and park before draining ordinary owners. A
// newly attached control starts parked and requires explicit Resume.
func RunWithControl(
	ctx context.Context,
	controller *Controller,
	gate *Gate,
	idleInterval, backlogDelay time.Duration,
	report Reporter,
	reportCapacity CapacityReporter,
	owners *dispatchadmission.Owners,
	control *RunnerControl,
) {
	if control != nil {
		if !control.attach() {
			return
		}
		defer close(control.done)
	}
	if controller == nil || idleInterval <= 0 || backlogDelay <= 0 {
		return
	}
	state := runnerState{idleInterval: idleInterval, backlogDelay: backlogDelay}
	var due time.Time
	parked := control != nil
	for {
		if control != nil {
			command, ok := control.next(ctx, due, parked)
			if !ok {
				return
			}
			if command != nil {
				if !control.execute(ctx, command, &parked, &state, &due, controller, gate, report, reportCapacity) {
					return
				}
				continue
			}
		} else if state.delay > 0 {
			timer := time.NewTimer(state.delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		turn, err := owners.Enter(ctx)
		if err != nil {
			return
		}
		completed := state.turn(ctx, controller, gate, report, reportCapacity, nil)
		turn.End()
		if !completed {
			return
		}
		if control != nil {
			due = time.Now().Add(state.delay)
		}
	}
}

// The same state survives ordinary turns, parking, and controlled turns.
type runnerState struct {
	idleInterval, backlogDelay, delay                  time.Duration
	cycleStarted, cycleNeedsRetry                      bool
	pressureRecoveryCycle, pressureRecoveryNormalCycle bool
	capacityRetryCycle, priorCapacityExactNormal       bool
}

func (state *runnerState) turn(ctx context.Context, controller *Controller, gate *Gate,
	report Reporter, reportCapacity CapacityReporter, collector *CycleCollector,
) bool {
	result := controller.Tick(ctx)
	if ctx.Err() != nil {
		return false
	}
	if report != nil {
		func() {
			defer func() { _ = recover() }()
			report(result)
		}()
	} else if result.Err != nil {
		log.Printf("lifecycle owner %q: %v", result.Owner, result.Err)
	}
	if collector != nil {
		collector.ObserveOwner(result)
	}
	if result.CycleStart {
		state.cycleStarted = true
		state.cycleNeedsRetry = false
	}
	if state.cycleStarted && (result.Err != nil || result.More) {
		state.cycleNeedsRetry = true
	}
	pressureAccelerated := false
	if gate != nil {
		capacity, capacityErr := gate.Check(ctx, 0)
		if reportCapacity != nil {
			reportCapacity(capacity, capacityErr)
		}
		if collector != nil {
			collector.ObserveCapacity(capacity, capacityErr)
		}
		pressureAccelerated = capacity.Pressure == PressureCollect ||
			capacity.Pressure == PressureRefuse
		capacityExact := capacity.Pressure != PressureUnavailable &&
			(capacityErr == nil || errors.Is(capacityErr, ErrPressureRefusal))
		capacityExactNormal := capacityExact && capacity.Pressure == PressureNormal
		if result.CycleStart {
			state.capacityRetryCycle = false
		}
		if !capacityExact {
			state.capacityRetryCycle = true
		}
		if pressureAccelerated {
			state.pressureRecoveryCycle = true
			state.pressureRecoveryNormalCycle = false
		}
		if state.pressureRecoveryCycle {
			if result.CycleStart {
				state.pressureRecoveryNormalCycle = state.priorCapacityExactNormal && capacityExactNormal
			} else if !capacityExactNormal {
				state.pressureRecoveryNormalCycle = false
			}
			if result.CycleComplete && state.pressureRecoveryNormalCycle && !state.cycleNeedsRetry {
				state.pressureRecoveryCycle = false
				state.pressureRecoveryNormalCycle = false
			}
		}
		state.priorCapacityExactNormal = capacityExactNormal
	}
	if state.cycleNeedsRetry || !result.CycleComplete || !state.cycleStarted ||
		pressureAccelerated || state.pressureRecoveryCycle || state.capacityRetryCycle {
		state.delay = runnerBacklogDelay(state.backlogDelay, pressureAccelerated || state.pressureRecoveryCycle)
	} else {
		state.delay = state.idleInterval
		state.cycleStarted = false
		state.cycleNeedsRetry = false
	}
	return true
}

func runnerBacklogDelay(backlogDelay time.Duration, pressureRecovery bool) time.Duration {
	if pressureRecovery && backlogDelay > DefaultPressureRecoveryDelay {
		return DefaultPressureRecoveryDelay
	}
	return backlogDelay
}
