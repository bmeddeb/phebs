package lifecycle

import (
	"context"
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
		}
		if cycleNeedsRetry || !result.CycleComplete || !cycleStarted || pressureAccelerated {
			delay = backlogDelay
		} else {
			delay = idleInterval
			cycleStarted = false
			cycleNeedsRetry = false
		}
	}
}
