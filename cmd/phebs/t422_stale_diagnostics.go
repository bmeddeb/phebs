package main

import (
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/store"
)

// One component-local first stop, not a globally ordered cause or public
// evidence. Only closed classes, failed predicates and three PCs are retained;
// no raw errors, source paths, identities, lease tokens or arguments.
type t422StaleFailure struct {
	Cause, Context string
	Checks         t422StaleFailedChecks
	Callers        [3]uintptr
}

// Transition/Point identify the checked guard; every other true field names a
// failed predicate. Other stop sites retain a zero (unobserved) set.
type t422StaleFailedChecks struct {
	Transition                                                                bool
	Point                                                                     string // hit/requeued/recovered/unknown only
	Stopped, Unarmed, MissingOld                                              bool
	Repository, Stage, ResourceClass, Generation, Schedule                    bool
	Identity, Offset, Length, Attempt                                         bool
	Observer, Requeue, Reclaim, Report, Priority, Status, Lease, UnknownPoint bool
}

func t422StaleCause(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, errT422StaleControl):
		return "stale_control"
	default:
		return "other"
	}
}

// Caller holds control.mu after a failed transition guard. Re-evaluation is
// failure-only against that identical locked state; it changes no predicate.
func (control *t422StaleControl) transitionFailedChecks(event store.GenerationStaleLeaseTransition) t422StaleFailedChecks {
	old := control.old
	checks := t422StaleFailedChecks{
		Transition: true, Stopped: control.err != nil, Unarmed: !control.armed, MissingOld: old.Identity == "",
		Repository: event.Repository != old.Repository, Stage: event.Stage != old.Stage,
		ResourceClass: event.ResourceClass != old.ResourceClass, Generation: event.Generation != old.Generation,
		Schedule: event.ScheduleDigest != old.ScheduleDigest, Identity: event.ChunkIdentity != old.Identity,
		Offset: event.Offset != old.Offset, Length: event.Length != 1, Attempt: event.Attempt != 0,
	}
	switch event.Point {
	case store.GenerationStaleLeaseTransitionHit:
		checks.Point = "hit"
		checks.Observer, checks.Requeue = control.hit.observer != nil, control.requeueSeen
		checks.Priority, checks.Status = event.Priority != store.GenerationPriorityNeverRun, event.ChunkStatus != store.GenerationChunkRunning
		checks.Lease = !event.Leased || event.PrivateLeaseTokenDigest != store.GenerationLeaseTokenDigest(old.LeaseToken)
	case store.GenerationStaleLeaseTransitionRequeued:
		checks.Point = "requeued"
		checks.Report, checks.Requeue = !control.hit.reported, control.requeueSeen
		checks.Priority, checks.Status = event.Priority != store.GenerationPriorityStale, event.ChunkStatus != store.GenerationChunkPending
		checks.Lease = event.Leased
	case store.GenerationStaleLeaseTransitionRecovered:
		checks.Point = "recovered"
		checks.Requeue, checks.Reclaim, checks.Observer = !control.requeueSeen, !control.reclaimed, control.recovered.observer != nil
		checks.Priority, checks.Status = event.Priority != store.GenerationPriorityStale, event.ChunkStatus != store.GenerationChunkDone
		checks.Lease = event.Leased || event.PrivateLeaseTokenDigest != control.reclaimedLease
	default:
		checks.Point, checks.UnknownPoint = "unknown", true
	}
	return checks
}
