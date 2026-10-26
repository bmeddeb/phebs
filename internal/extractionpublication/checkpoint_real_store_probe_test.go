package extractionpublication_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

// Ordinary unprivileged component test: real SurrealDB, Git, runtime files,
// evidence and SDK queries. Source/observation authority inputs remain fixture
// values. No selected-process SA/custody/RSS or native hard-death claim follows.
func TestCheckpointRecoveryRealStoreProbe(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Fatal("existing pinned SurrealDB binary is required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	fixture := newRecoveryPreparationFixture(t, ctx)
	generation, err := fixture.reconciler.Reconcile(ctx, fixture.repository)
	if err != nil {
		t.Fatal("initial reconciliation refused")
	}
	fixture.finishSchedule(t, ctx, fixture.schedule(t, ctx))
	prior := fixture.schedule(t, ctx)
	baseline := fixture.publication(t, ctx)
	acquisitions, appends := fixture.source.acquisitions, fixture.evidence.appends
	fixture.reconciler.RecoveryPreparationEnabled = true
	schedule, err := fixture.reconciler.PrepareRecovery(ctx, extractionpublication.RecoveryPreparationRequest{
		Schema: extractionpublication.RecoveryPreparationSchema, Authority: fixture.authority,
		GenerationDigest: generation, PriorScheduleDigest: prior.Digest,
		Roots: []extractionpublication.RecoveryPreparationRoot{{Domain: baseline.root.Domain, PlanDigest: baseline.root.PlanDigest, RootDigest: baseline.root.Digest}},
		Mode:  extractionpublication.RecoveryPreparationCheckpoint, TargetDomain: baseline.root.Domain, TargetOrdinal: 0,
	})
	if err != nil {
		t.Fatal("real checkpoint preparation refused")
	}
	if _, err := fixture.state.ExpandGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage, schedule.Generation); err != nil {
		t.Fatal("real schedule expansion refused")
	}
	claimed, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "checkpoint-probe-first")
	if err != nil || claimed == nil || claimed.Offset != 0 || claimed.Length != 1 {
		t.Fatal("real checkpoint claim refused")
	}
	cut := errors.New("test-only checkpoint cut")
	var checkpoint store.GenerationStaleLeaseTransition
	fixture.runtime.OnPartitionCheckpoint = func(_ context.Context, event store.GenerationStaleLeaseTransition) error {
		checkpoint = event
		return cut
	}
	if err := fixture.runtime.Handle(ctx, *claimed); !errors.Is(err, cut) || checkpoint.Point != store.GenerationStaleLeaseTransitionCheckpointHit {
		t.Fatal("actual reused-result checkpoint hook refused")
	}
	request := extractionpublication.CheckpointRestartTransitionRequest{Transition: checkpoint, TargetGeneration: generation,
		PriorScheduleDigest: prior.Digest, Domain: baseline.root.Domain, Ordinal: 0,
		PlanDigest: baseline.root.PlanDigest, ResultIdentity: baseline.root.Results[0].Identity}
	read := func(request extractionpublication.CheckpointRestartTransitionRequest) extractionpublication.CheckpointRestartTransition {
		t.Helper()
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		accounted, ledger, err := readaccounting.Start(bounded, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
		if err != nil {
			t.Fatal("read ledger refused")
		}
		observed, projection := readaccounting.CaptureRecoverySchedule(accounted)
		value, readErr := fixture.runtime.ReadCheckpointRestartTransition(observed, request)
		counts, finishErr := ledger.Finish()
		snapshot, available := projection.Observation()
		if readErr != nil || finishErr != nil || counts != (readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4}) || !available || snapshot.Chunks != 1 || snapshot.ScheduleSHA256 != schedule.Digest {
			t.Fatalf("actual transition read refused: read_error=%t accounting_error=%t counts=%+v projection=%t", readErr != nil, finishErr != nil, counts, available)
		}
		t.Logf("actual_transition point=%s controls=%d store=%d chunks=%d successes=%d", value.Point, counts.ControlFileReads, counts.StoreReadAttempts, snapshot.Chunks, snapshot.Successes)
		return value
	}
	hit := read(request)
	if hit.CompletionBitSet || hit.RootExists || hit.Current || hit.ChunkIdentity != claimed.Identity {
		t.Fatal("checkpoint snapshot granted completion credit")
	}
	// Match the production scheduler defaults: heartbeat every five seconds,
	// stale after twenty. The query cutoff uses the pinned SDK's whole-second
	// time.Time encoding, so wait through one extra fractional-second boundary.
	// This changes neither the twenty-second lease policy nor callback budgets.
	timer := time.NewTimer(21*time.Second + 100*time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("probe lease-expiry bound")
	}
	reaper, stopReaper := context.WithTimeout(ctx, 5*time.Second)
	var points []store.GenerationStaleLeaseTransitionPoint
	var observedReaper context.Context
	reaped, err := fixture.state.ReapStaleGenerationChunksObserved(reaper, store.GenerationResourceExtraction, 20*time.Second,
		func(call context.Context, event store.GenerationStaleLeaseTransition) error {
			observedReaper = call
			if event.ChunkIdentity != claimed.Identity || event.ScheduleDigest != schedule.Digest || event.Attempt != 0 {
				return errors.New("foreign actual reaper event")
			}
			points = append(points, event.Point)
			return nil
		})
	stopReaper()
	if err != nil || reaped != 1 || len(points) != 2 || points[0] != store.GenerationStaleLeaseTransitionHit || points[1] != store.GenerationStaleLeaseTransitionRequeued || observedReaper == nil || !errors.Is(observedReaper.Err(), context.Canceled) {
		t.Fatalf("actual observed reaper sequence refused: error=%t reaped=%d points=%v observed=%t canceled=%t stale=%t lease_lost=%t", err != nil, reaped, points, observedReaper != nil, observedReaper != nil && errors.Is(observedReaper.Err(), context.Canceled), errors.Is(err, store.ErrGenerationStale), errors.Is(err, store.ErrGenerationLeaseLost))
	}
	if err := fixture.state.CompleteGenerationChunk(ctx, *claimed); !errors.Is(err, store.ErrGenerationLeaseLost) {
		t.Fatal("old lease was not fenced after committed requeue")
	}
	recovered, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "checkpoint-probe-recovered")
	if err != nil || recovered == nil || recovered.Identity != claimed.Identity || recovered.Priority != store.GenerationPriorityStale || recovered.Attempt != 0 || recovered.LeaseToken == claimed.LeaseToken || recovered.LeaseToken == "" {
		t.Fatal("actual reclaimed identity/lease refused")
	}
	fixture.runtime.OnPartitionCheckpoint = func(context.Context, store.GenerationStaleLeaseTransition) error {
		return errors.New("recovered path retriggered checkpoint")
	}
	if err := fixture.runtime.Handle(ctx, *recovered); err != nil {
		t.Fatal("actual recovered runtime refused")
	}
	if err := fixture.state.CompleteGenerationChunk(ctx, *recovered); err != nil {
		t.Fatal("actual recovered completion refused")
	}
	request.Transition = store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionRecovered,
		Repository: recovered.Repository, Stage: recovered.Stage, Generation: recovered.Generation, ResourceClass: recovered.ResourceClass,
		ScheduleDigest: recovered.ScheduleDigest, ChunkIdentity: recovered.Identity, Offset: recovered.Offset, Length: recovered.Length,
		Attempt: recovered.Attempt, Priority: recovered.Priority, ChunkStatus: store.GenerationChunkDone,
		PrivateLeaseTokenDigest: store.GenerationLeaseTokenDigest(recovered.LeaseToken)}
	value := read(request)
	if !value.CompletionBitSet || !value.RootExists || !value.Current || value.RootDigest != baseline.root.Digest || value.ResultIdentity != hit.ResultIdentity ||
		value.PrivateLeaseTokenDigest == checkpoint.PrivateLeaseTokenDigest || fixture.source.acquisitions != acquisitions || fixture.evidence.appends != appends {
		t.Fatal("actual recovered read changed identity, authority, or reused content")
	}
	for _, mode := range []string{"foreign_chunk", "wrong_plan", "canceled_read"} {
		t.Run(mode, func(t *testing.T) {
			bad := request
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			defer stop()
			accounted, ledger, err := readaccounting.Start(bounded, readaccounting.Counts{ControlFileReads: 7, StoreReadAttempts: 4})
			if err != nil {
				t.Fatal("negative read ledger refused")
			}
			if mode == "foreign_chunk" {
				bad.Transition.ChunkIdentity = recoveryPreparationDigest("foreign chunk")
			}
			if mode == "wrong_plan" {
				bad.PlanDigest = recoveryPreparationDigest("foreign plan")
			}
			if mode == "canceled_read" {
				stop()
			}
			value, readErr := fixture.runtime.ReadCheckpointRestartTransition(accounted, bad)
			counts, _ := ledger.Finish()
			if readErr == nil || value != (extractionpublication.CheckpointRestartTransition{}) {
				t.Fatal("negative actual-store read gained credit")
			}
			t.Logf("actual_refusal mode=%s controls=%d store=%d context_canceled=%t", mode, counts.ControlFileReads, counts.StoreReadAttempts, errors.Is(readErr, context.Canceled))
		})
	}
	t.Log("actual_real_store_summary same_chunk=true changed_lease=true old_lease_fenced=true actual_sdk_read_accounting=true; no selected/native acceptance")
}
