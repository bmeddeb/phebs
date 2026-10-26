package main

import (
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

// Supplied malformed events test routing only. The real-store HTTP regression
// separately proves an actual deferred/reclaimed sibling and selected report.
func TestT422CheckpointRecoveryRoutingRejectsMalformedEvents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*store.GenerationStaleLeaseTransition)
	}{
		{"unrelated_exact", func(*store.GenerationStaleLeaseTransition) {}},
		{"selected_identity", func(e *store.GenerationStaleLeaseTransition) {
			e.ChunkIdentity = t422CheckpointRecoveryFixture().Hit.ChunkIdentity
		}},
		{"selected_offset", func(e *store.GenerationStaleLeaseTransition) { e.Offset = 27 }},
		{"forged_identity", func(e *store.GenerationStaleLeaseTransition) { e.ChunkIdentity = t422CheckpointTestDigest }},
		{"negative_offset", func(e *store.GenerationStaleLeaseTransition) { e.Offset = -1 }},
		{"repository", func(e *store.GenerationStaleLeaseTransition) { e.Repository = "foreign/repo" }},
		{"stage", func(e *store.GenerationStaleLeaseTransition) { e.Stage = "foreign" }},
		{"resource", func(e *store.GenerationStaleLeaseTransition) { e.ResourceClass = store.GenerationResourceCPU }},
		{"generation", func(e *store.GenerationStaleLeaseTransition) { e.Generation = t422CheckpointTestDigest }},
		{"schedule", func(e *store.GenerationStaleLeaseTransition) { e.ScheduleDigest = "invalid" }},
		{"length", func(e *store.GenerationStaleLeaseTransition) { e.Length = 2 }},
		{"attempt", func(e *store.GenerationStaleLeaseTransition) { e.Attempt = 1 }},
		{"priority", func(e *store.GenerationStaleLeaseTransition) { e.Priority = store.GenerationPriorityRetry }},
		{"status", func(e *store.GenerationStaleLeaseTransition) { e.ChunkStatus = store.GenerationChunkRunning }},
		{"lease", func(e *store.GenerationStaleLeaseTransition) { e.Leased = true }},
		{"cutoff", func(e *store.GenerationStaleLeaseTransition) { e.StaleBefore = time.Now() }},
		{"schedule_status", func(e *store.GenerationStaleLeaseTransition) { e.ScheduleStatus = store.GenerationScheduleSettled }},
		{"row_digest", func(e *store.GenerationStaleLeaseTransition) { e.ChunkStateDigest = t422CheckpointTestDigest }},
		{"checkpoint_digest", func(e *store.GenerationStaleLeaseTransition) { e.CheckpointStateDigest = t422CheckpointTestDigest }},
		{"token", func(e *store.GenerationStaleLeaseTransition) { e.PrivateLeaseTokenDigest = "invalid" }},
		{"hit", func(e *store.GenerationStaleLeaseTransition) { e.Point = store.GenerationStaleLeaseTransitionHit }},
		{"requeued", func(e *store.GenerationStaleLeaseTransition) { e.Point = store.GenerationStaleLeaseTransitionRequeued }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, failures := t422StaleTestControl(t)
			base.launch.request.Repository = "test/repo"
			control := &t422CheckpointRecoveryControl{t422StaleControl: base, input: t422CheckpointRecoveryFixture()}
			hit := control.input.Hit
			identity, err := store.GenerationChunkIdentity(hit.ScheduleDigest, 28, 0)
			if err != nil {
				t.Fatal(err)
			}
			event := store.GenerationStaleLeaseTransition{Point: store.GenerationStaleLeaseTransitionRecovered, Repository: "test/repo", Stage: extractionpublication.ScheduleStage,
				ResourceClass: store.GenerationResourceExtraction, Generation: hit.ScheduleGeneration, ScheduleDigest: hit.ScheduleDigest,
				ChunkIdentity: identity, Offset: 28, Length: 1, Priority: store.GenerationPriorityStale, ChunkStatus: store.GenerationChunkDone,
				PrivateLeaseTokenDigest: t422CheckpointTestDigest}
			test.mutate(&event)
			err = control.observeTransition(t.Context(), event)
			if test.name == "unrelated_exact" {
				if err != nil || failures.Load() != 0 || control.failure != nil || control.err != nil || control.recovered.observer != nil || control.recovered.reported {
					t.Fatal("unrelated completion poisoned or credited selected control", err)
				}
			} else if err == nil || failures.Load() != 1 || control.failure == nil {
				t.Fatal("malformed/selected event bypassed strict control", err)
			}
		})
	}
}
