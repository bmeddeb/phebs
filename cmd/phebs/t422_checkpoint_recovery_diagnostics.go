package main

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

type t422RecoveryFailureStage uint8

const (
	t422RecoveryUnknown t422RecoveryFailureStage = iota
	t422RecoveryReaderBinding
	t422RecoveryReaderWait
	t422RecoveryReaderState
	t422RecoveryTransitionBinding
	t422RecoveryTransitionTarget
	t422RecoveryTransitionDeadline
	t422RecoveryTransitionState
	t422RecoveryRendezvous
	t422RecoveryRendezvousBinding
	t422RecoveryReleaseWait
	t422RecoveryReleaseState
	t422RecoveryReadBinding
	t422RecoveryReadState
	t422RecoveryReadWait
	t422RecoveryRuntimeRead
	t422RecoveryRuntimeMatch
	t422RecoveryScheduleProjection
	t422RecoveryEncoding
	t422RecoveryReadReport
	t422RecoveryReportRelease
	t422RecoveryFinalSnapshot
	t422RecoveryFinalBinding
	t422RecoveryFinalState
)

func (stage t422RecoveryFailureStage) String() string {
	names := [...]string{"unknown", "reader_binding", "reader_wait", "reader_state", "transition_binding", "transition_target",
		"transition_deadline", "transition_state", "rendezvous", "rendezvous_binding", "release_wait", "release_state",
		"read_binding", "read_state", "read_wait", "runtime_read", "runtime_match", "schedule_projection", "encoding",
		"read_report", "report_release", "final_snapshot", "final_binding", "final_state"}
	if int(stage) >= len(names) {
		return names[0]
	}
	return names[stage]
}

// Control-local first refusal, not a globally ordered server cause or public
// acceptance evidence. Only closed labels and booleans survive: no raw error,
// context, event, payload, identity, path, deadline value or token is retained.
type t422CheckpointRecoveryFailure struct {
	Stage                                               t422RecoveryFailureStage
	Cause, Caller, Lifetime, Hit, Recovered, Deadline   string
	PhaseExpired                                        bool
	HitSeen, Requeued, RecoveredSeen, Reading, Reported bool
	Event                                               t422RecoveryEventChecks
}

// Every true field identifies a failed predicate in the supplied event. These
// are advisory checks against the first refusal's locked state, not authority.
type t422RecoveryEventChecks struct {
	Present                                                                  bool
	Point                                                                    string // hit/requeued/recovered/unknown only
	Repository, Stage, ResourceClass, Generation, Schedule, Identity, Offset bool
	Length, Attempt, Priority, Status, Lease, Cutoff, Row, Checkpoint, Token bool
}

func t422RecoveryContext(ctx context.Context) string {
	if ctx == nil {
		return "absent"
	}
	if ctx.Err() == nil {
		return "live"
	}
	return t422StaleCause(ctx.Err())
}

func t422RecoveryCause(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return t422StaleCause(err)
	case errors.Is(err, extractionpublication.ErrInvalid):
		return "runtime_invalid"
	case errors.Is(err, extractionpublication.ErrStale):
		return "runtime_stale"
	case errors.Is(err, store.ErrGenerationLeaseLost):
		return "store_lease"
	case errors.Is(err, store.ErrGenerationStale):
		return "store_stale"
	case readaccounting.IsError(err):
		return "read_accounting"
	default:
		return t422StaleCause(err)
	}
}

// Relative caller-deadline bucket only. The five-second acceptance predicate
// belongs to transition; this never imposes that ceiling on a request/phase.
func t422RecoveryDeadline(ctx context.Context) string {
	if ctx == nil {
		return "absent"
	}
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return "absent"
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "expired"
	}
	if remaining > 5*time.Second {
		return "over_5s"
	}
	return "within_5s"
}

// Caller holds the same existing component lock used for failure settlement.
func (control *t422CheckpointRecoveryControl) failedEvent(event *store.GenerationStaleLeaseTransition) t422RecoveryEventChecks {
	if event == nil {
		return t422RecoveryEventChecks{}
	}
	hit := control.input.Hit
	checks := t422RecoveryEventChecks{Present: true,
		Repository: event.Repository != control.launch.request.Repository, Stage: event.Stage != extractionpublication.ScheduleStage,
		ResourceClass: event.ResourceClass != store.GenerationResourceExtraction, Generation: event.Generation != hit.ScheduleGeneration,
		Schedule: event.ScheduleDigest != hit.ScheduleDigest, Identity: event.ChunkIdentity != hit.ChunkIdentity,
		Offset: event.Offset != int64(control.input.Offset), Length: event.Length != 1, Attempt: event.Attempt != 0}
	switch event.Point {
	case store.GenerationStaleLeaseTransitionHit:
		checks.Point = "hit"
		checks.Priority, checks.Status = event.Priority != store.GenerationPriorityNeverRun, event.ChunkStatus != store.GenerationChunkRunning || event.ScheduleStatus != store.GenerationScheduleActive
		checks.Lease, checks.Cutoff = !event.Leased, event.StaleBefore.IsZero()
		checks.Row, checks.Checkpoint, checks.Token = !t422SemanticDigest(event.ChunkStateDigest), !t422SemanticDigest(event.CheckpointStateDigest), !t422SemanticDigest(event.PrivateLeaseTokenDigest)
	case store.GenerationStaleLeaseTransitionRequeued:
		checks.Point = "requeued"
		checks.Priority, checks.Status = event.Priority != store.GenerationPriorityStale, event.ChunkStatus != store.GenerationChunkPending || event.ScheduleStatus != store.GenerationScheduleActive
		checks.Lease, checks.Cutoff = event.Leased, !event.StaleBefore.IsZero()
	case store.GenerationStaleLeaseTransitionRecovered:
		checks.Point = "recovered"
		checks.Priority, checks.Status = event.Priority != store.GenerationPriorityStale, event.ChunkStatus != store.GenerationChunkDone
		checks.Lease, checks.Cutoff = event.Leased, !event.StaleBefore.IsZero()
		checks.Token = !t422SemanticDigest(event.PrivateLeaseTokenDigest) || event.PrivateLeaseTokenDigest == control.killed.PrivateLeaseTokenDigest
	default:
		checks.Point = "unknown"
	}
	return checks
}

// Mirror the existing stop latch/cancel/fail ordering only for selected
// recovery. Snapshot before cancellation; later refusals never replace it or
// wait for its advisory logger. Shared stale/checkpoint controls are unchanged.
func (control *t422CheckpointRecoveryControl) refuse(ctx context.Context, stage t422RecoveryFailureStage, cause error, event *store.GenerationStaleLeaseTransition) error {
	control.mu.Lock()
	first := control.err == nil
	var failure *t422CheckpointRecoveryFailure
	if first {
		control.err = errors.Join(errT422StaleControl, cause)
		failure = &t422CheckpointRecoveryFailure{Stage: stage, Cause: t422RecoveryCause(cause),
			Caller: t422RecoveryContext(ctx), Lifetime: t422RecoveryContext(control.ctx),
			Hit: t422RecoveryContext(control.hit.observer), Recovered: t422RecoveryContext(control.recovered.observer),
			Deadline: t422RecoveryDeadline(ctx), PhaseExpired: !control.phaseEnd.IsZero() && !time.Now().Before(control.phaseEnd),
			HitSeen: control.hit.observer != nil, Requeued: control.requeueSeen, RecoveredSeen: control.recovered.observer != nil,
			Reading: control.recovered.reading, Reported: control.recovered.reported, Event: control.failedEvent(event)}
		control.failure = failure
	}
	err := control.err
	control.mu.Unlock()
	control.cancel()
	if first {
		control.launch.fail(err)
		// Do not format cause.Error(), events or contexts here. Logf contains a
		// failed destination; failure propagation already happened above.
		diagnostics.Logf("private selected checkpoint recovery first refusal: stage=%s cause=%s caller=%s lifetime=%s hit=%s recovered=%s deadline=%s phase_expired=%t hit_seen=%t requeued=%t recovered_seen=%t reading=%t reported=%t event=%+v",
			failure.Stage, failure.Cause, failure.Caller, failure.Lifetime, failure.Hit, failure.Recovered, failure.Deadline,
			failure.PhaseExpired, failure.HitSeen, failure.Requeued, failure.RecoveredSeen, failure.Reading, failure.Reported, failure.Event)
	}
	return err
}

// Same report-release predicate and mutation as the embedded stale control;
// only a selected recovery refusal gains its own first-failure label.
func (control *t422CheckpointRecoveryControl) finishRecoveryReport(ctx context.Context, barrier *t422StaleBarrier) error {
	control.mu.Lock()
	valid := (barrier == &control.hit || barrier == &control.recovered) && control.err == nil && control.ctx.Err() == nil &&
		barrier.reading && !barrier.reported && barrier.observer != nil && barrier.observer.Err() == nil
	if valid {
		barrier.reported = true
		close(barrier.release)
	}
	control.mu.Unlock()
	if !valid {
		return control.refuse(ctx, t422RecoveryReportRelease, errT422StaleControl, nil)
	}
	return nil
}
