package lifecycle

import (
	"context"
	"errors"
	"log"
	"time"
)

const (
	DefaultIdleInterval = time.Hour
	DefaultBacklogDelay = 5 * time.Second
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
	if controller == nil || idleInterval <= 0 || backlogDelay <= 0 {
		return
	}
	delay := time.Duration(0)
	cycleStarted := false
	cycleNeedsRetry := false
	pressureRecoveryCycle := false
	pressureRecoveryNormalCycle := false
	capacityRetryCycle := false
	priorCapacityExactNormal := false
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		result := controller.Tick(ctx)
		if ctx.Err() != nil {
			return
		}
		if report != nil {
			func() {
				defer func() { _ = recover() }()
				report(result)
			}()
		} else if result.Err != nil {
			log.Printf("lifecycle owner %q: %v", result.Owner, result.Err)
		}
		if result.CycleStart {
			cycleStarted = true
			cycleNeedsRetry = false
		}
		if cycleStarted && (result.Err != nil || result.More) {
			cycleNeedsRetry = true
		}
		pressureAccelerated := false
		if gate != nil {
			capacity, capacityErr := gate.Check(ctx, 0)
			if reportCapacity != nil {
				reportCapacity(capacity, capacityErr)
			}
			pressureAccelerated = capacity.Pressure == PressureCollect ||
				capacity.Pressure == PressureRefuse
			capacityExact := capacity.Pressure != PressureUnavailable &&
				(capacityErr == nil || errors.Is(capacityErr, ErrPressureRefusal))
			capacityExactNormal := capacityExact && capacity.Pressure == PressureNormal
			if !capacityExact {
				capacityRetryCycle = true
			} else if result.CycleComplete {
				capacityRetryCycle = false
			}
			if pressureAccelerated {
				pressureRecoveryCycle = true
				pressureRecoveryNormalCycle = false
			}
			if pressureRecoveryCycle {
				if result.CycleStart {
					pressureRecoveryNormalCycle = priorCapacityExactNormal && capacityExactNormal
				} else if !capacityExactNormal {
					pressureRecoveryNormalCycle = false
				}
				if result.CycleComplete && pressureRecoveryNormalCycle && !cycleNeedsRetry {
					pressureRecoveryCycle = false
					pressureRecoveryNormalCycle = false
				}
			}
			priorCapacityExactNormal = capacityExactNormal
		}
		if cycleNeedsRetry || !result.CycleComplete || !cycleStarted ||
			pressureAccelerated || pressureRecoveryCycle || capacityRetryCycle {
			delay = backlogDelay
		} else {
			delay = idleInterval
			cycleStarted = false
			cycleNeedsRetry = false
		}
	}
}
