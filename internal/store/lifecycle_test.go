package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestLifecycleCursorCompareAndSwap(t *testing.T) {
	store := newRunnerStore(t)
	value, revision, err := store.GetLifecycleCursor(t.Context(), "owner:generation-schedules")
	if err != nil || value != "" || revision != 0 {
		t.Fatalf("initial cursor = %q/%d, %v", value, revision, err)
	}
	if err := store.CompareAndSwapLifecycleCursor(
		t.Context(), "owner:generation-schedules", 0, "sha256:"+fmt.Sprintf("%064d", 1),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwapLifecycleCursor(
		t.Context(), "owner:generation-schedules", 0, "stale",
	); !errors.Is(err, ErrLifecycleCursorConflict) {
		t.Fatalf("stale cursor CAS = %v", err)
	}
	value, revision, err = store.GetLifecycleCursor(t.Context(), "owner:generation-schedules")
	if err != nil || revision != 1 || value == "" {
		t.Fatalf("stored cursor = %q/%d, %v", value, revision, err)
	}
}

func TestGenerationLifecycleProtectsCurrentRollbackFloorAndRunningLease(t *testing.T) {
	store := newRunnerStore(t)
	repository := "example.invalid/lifecycle"
	var specs []GenerationScheduleSpec
	for index := range 4 {
		spec := generationSpec(repository, "sha256:"+fmt.Sprintf("%064d", index+100))
		spec.TotalItems = 1
		spec.ChunkItems = 1
		if _, err := store.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ExpandGenerationSchedule(
			t.Context(), spec.Repository, spec.Stage, spec.Generation,
		); err != nil {
			t.Fatal(err)
		}
		specs = append(specs, spec)
	}

	// Claim the oldest superseded generation directly before collection. It is
	// no longer claimable through the public current-pointer path, so the test
	// installs the exact running lease as an adversarial restored/in-flight row.
	oldDigest := generationScheduleDigest(specs[0])
	if _, err := surrealdb.Query[any](t.Context(), store.db, `
UPDATE generation_schedule_chunk SET status = 'running', claimed_by = 'worker',
	lease_token = 'lease', claimed_at = time::now(), heartbeat_at = time::now()
	WHERE schedule_digest = $digest RETURN NONE;`, map[string]any{"digest": oldDigest}); err != nil {
		t.Fatal(err)
	}

	sweep, err := store.SweepGenerationScheduleLifecycle(
		t.Context(), "", 64, 16, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sweep.Deleted != 0 {
		t.Fatalf("lease-protected sweep deleted %d rows", sweep.Deleted)
	}
	if !generationScheduleExists(t, store, oldDigest) {
		t.Fatal("lease-protected oldest schedule was deleted")
	}

	if _, err := surrealdb.Query[any](t.Context(), store.db, `
UPDATE generation_schedule_chunk SET status = 'canceled', claimed_by = '',
	lease_token = '', claimed_at = NONE, heartbeat_at = NONE, finished_at = time::now()
		WHERE schedule_digest = $digest RETURN NONE;`, map[string]any{"digest": oldDigest}); err != nil {
		t.Fatal(err)
	}
	// One remaining row of budget cannot safely collect a nonempty schedule:
	// deleting its final chunk would also make the schedule row collectible.
	sweep, err = store.SweepGenerationScheduleLifecycle(
		t.Context(), "", 64, 1, 2,
	)
	if err != nil || sweep.Deleted != 0 || !generationScheduleExists(t, store, oldDigest) {
		t.Fatalf("one-row schedule budget = %+v, %v; want deferred", sweep, err)
	}
	for range 3 { // one keyspace wrap is sufficient; extra turns pin idempotence
		sweep, err = store.SweepGenerationScheduleLifecycle(
			t.Context(), sweep.Cursor, 64, 16, 2,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	if generationScheduleExists(t, store, oldDigest) {
		t.Fatal("unleased schedule beyond the rollback floor was retained")
	}
	for _, spec := range specs[1:] {
		if !generationScheduleExists(t, store, generationScheduleDigest(spec)) {
			t.Fatalf("rollback/current schedule %s was deleted", spec.Generation)
		}
	}
}

func TestGenerationLifecycleCollectsUnexpandedSupersededSchedule(t *testing.T) {
	state := newRunnerStore(t)
	repository := "example.invalid/unexpanded-lifecycle"
	var digests []string
	for index := range 4 {
		spec := generationSpec(repository, "sha256:"+fmt.Sprintf("%064d", index+200))
		spec.TotalItems = 1
		spec.ChunkItems = 1
		if _, err := state.EnqueueGenerationSchedule(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
		digests = append(digests, generationScheduleDigest(spec))
	}

	sweep, err := state.SweepGenerationScheduleLifecycle(
		t.Context(), "", 64, 16, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sweep.Deleted < 1 || generationScheduleExists(t, state, digests[0]) {
		t.Fatalf("unexpanded sweep = %+v, oldest schedule retained", sweep)
	}
	for _, digest := range digests[2:] {
		if !generationScheduleExists(t, state, digest) {
			t.Fatalf("rollback/current schedule %s was deleted", digest)
		}
	}
}

func TestJobLifecycleScanAndTerminalDeletionAreBounded(t *testing.T) {
	store := newRunnerStore(t)
	now := time.Now().UTC()
	for index := range 3 {
		if _, err := surrealdb.Query[any](t.Context(), store.db, `
CREATE connection_sync_job CONTENT {
	target: $target, status: 'done', attempts: 1,
	created_at: $created, finished_at: $finished
} RETURN NONE;`, map[string]any{
			"target":   fmt.Sprintf("old-%d", index),
			"created":  now.Add(-40 * 24 * time.Hour),
			"finished": now.Add(-35 * 24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := surrealdb.Query[any](t.Context(), store.db, `
CREATE connection_sync_job CONTENT {
	target: 'recent', status: 'done', attempts: 1,
	created_at: $created, finished_at: $finished
} RETURN NONE;
CREATE connection_sync_job CONTENT {
	target: 'running', status: 'running', attempts: 1,
	created_at: $created, finished_at: NONE
} RETURN NONE;`, map[string]any{
		"created": now.Add(-time.Hour), "finished": now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	page, err := store.ScanJobLifecycle(t.Context(), JobSync, "", 2)
	if err != nil || len(page.Rows) != 2 || page.Next == "" {
		t.Fatalf("first job lifecycle page = %+v, %v", page, err)
	}
	second, err := store.ScanJobLifecycle(t.Context(), JobSync, page.Next, 64)
	if err != nil || len(second.Rows) != 3 || second.Next != "" {
		t.Fatalf("second job lifecycle page = %+v, %v", second, err)
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	deleted, err := store.DeleteOldestTerminalJobs(t.Context(), JobSync, &cutoff, 2)
	if err != nil || deleted != 2 {
		t.Fatalf("first terminal deletion = %d, %v", deleted, err)
	}
	deleted, err = store.DeleteOldestTerminalJobs(t.Context(), JobSync, &cutoff, 2)
	if err != nil || deleted != 1 {
		t.Fatalf("second terminal deletion = %d, %v", deleted, err)
	}
	page, err = store.ScanJobLifecycle(t.Context(), JobSync, "", 64)
	if err != nil || len(page.Rows) != 2 {
		t.Fatalf("retained terminal/running rows = %+v, %v", page, err)
	}
}

func generationScheduleExists(t *testing.T, store *Surreal, digest string) bool {
	t.Helper()
	results, err := surrealdb.Query[[]string](t.Context(), store.db,
		"RETURN SELECT VALUE digest FROM generation_schedule WHERE digest = $digest LIMIT 1",
		map[string]any{"digest": digest})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			return true
		}
	}
	return false
}
