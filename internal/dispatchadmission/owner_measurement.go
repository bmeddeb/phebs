package dispatchadmission

import "context"

// OwnerMeasurement retains one actual owner while every peer/request drains.
// Only its issuing turn can resume it, once. It proves neither handler parking
// nor heartbeat/SDK quiescence. Terminal ownership remains irreversible.
type OwnerMeasurement struct {
	turn OwnerTurn
	ctx  context.Context
}

func (turn OwnerTurn) FenceMeasurement(ctx context.Context) (*OwnerMeasurement, error) {
	owners := turn.owners
	if owners == nil {
		return nil, ErrConfig
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	if owners.err != nil {
		return nil, owners.err
	}
	if ctx == nil || ctx.Err() != nil || owners.ctx.Err() != nil {
		return nil, owners.failLocked(ErrCanceled)
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, owners.failLocked(ErrConfig)
	}
	mask := uint64(1) << turn.slot
	if turn.request || turn.slot >= uint8(owners.limits.Owners) || turn.generation == 0 ||
		owners.active&mask == 0 || owners.generations[turn.slot] != turn.generation || owners.measurementUsed ||
		owners.terminal != 0 || owners.measurement != 0 || owners.paused || owners.requestsFenced || owners.pausedReady || owners.requestsReady {
		return nil, owners.failLocked(ErrProtocol)
	}
	owners.measurementUsed, owners.measurement, owners.paused, owners.requestsFenced = true, mask, true, true
	owners.notifyLocked()
	for {
		if owners.err != nil {
			return nil, owners.err
		}
		if ctx.Err() != nil || owners.ctx.Err() != nil {
			return nil, owners.failLocked(ErrCanceled)
		}
		if owners.active == mask && owners.requests == 0 {
			return &OwnerMeasurement{turn: turn, ctx: ctx}, nil
		}
		changed := owners.changed
		owners.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-owners.ctx.Done():
		case <-changed:
		}
		owners.mu.Lock()
	}
}

func (measurement *OwnerMeasurement) validLocked(ctx context.Context) bool {
	turn := measurement.turn
	owners := turn.owners
	mask := uint64(1) << turn.slot
	if ctx == nil || measurement.ctx == nil {
		return false
	}
	deadline, bounded := ctx.Deadline()
	original, originalBounded := measurement.ctx.Deadline()
	return bounded && originalBounded && !deadline.After(original) && measurement.ctx.Err() == nil && ctx.Err() == nil && owners.ctx.Err() == nil && owners.err == nil &&
		owners.measurement == mask && owners.generations[turn.slot] == turn.generation &&
		owners.active == mask && owners.requests == 0 && owners.paused && owners.requestsFenced &&
		!owners.pausedReady && !owners.requestsReady && owners.terminal == 0
}

func (measurement *OwnerMeasurement) Quiescent(ctx context.Context) bool {
	if measurement == nil || measurement.turn.owners == nil {
		return false
	}
	owners := measurement.turn.owners
	owners.mu.Lock()
	defer owners.mu.Unlock()
	return measurement.validLocked(ctx)
}

// Resume is called only after the owning scheduler confirms the same lease's
// natural heartbeat. It retains the target slot; it cannot complete its turn.
func (measurement *OwnerMeasurement) Resume(ctx context.Context) error {
	if measurement == nil || measurement.turn.owners == nil {
		return ErrConfig
	}
	owners := measurement.turn.owners
	owners.mu.Lock()
	defer owners.mu.Unlock()
	if !measurement.validLocked(ctx) {
		return owners.failLocked(ErrProtocol)
	}
	owners.measurement, owners.paused, owners.requestsFenced = 0, false, false
	owners.notifyLocked()
	return nil
}
