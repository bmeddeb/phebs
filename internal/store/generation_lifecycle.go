package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

type GenerationLifecycleSweep struct {
	Cursor  string
	Scanned int
	Deleted int
	More    bool
}

type generationLifecycleCandidate struct {
	Digest     string    `json:"digest"`
	Repository string    `json:"repository"`
	Stage      string    `json:"stage"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type generationLifecycleDelete struct {
	Deleted int `json:"deleted"`
}

// SweepGenerationScheduleLifecycle applies T35.3's deliberately reduced
// count-only policy. Schedule age remains a frozen policy input, not an active
// deletion reason, until a later ticket explicitly enables it.
func (s *Surreal) SweepGenerationScheduleLifecycle(
	ctx context.Context,
	after string,
	scanLimit, deleteLimit, retained int,
) (GenerationLifecycleSweep, error) {
	if after != "" && !validSHA256(after) {
		return GenerationLifecycleSweep{}, errors.New("sweep generation lifecycle: cursor is invalid")
	}
	if scanLimit < 1 || scanLimit > MaxGenerationClaimCandidates ||
		deleteLimit < 1 || deleteLimit > MaxGenerationFanoutPage ||
		retained < 1 || retained > 8 {
		return GenerationLifecycleSweep{}, errors.New("sweep generation lifecycle: limits are invalid")
	}
	results, err := surrealdb.Query[[]generationLifecycleCandidate](ctx, s.db, `
RETURN SELECT digest, repository, stage, updated_at FROM generation_schedule
	WHERE status != 'active' AND digest > $after
	ORDER BY digest LIMIT $limit;`, map[string]any{
		"after": after, "limit": scanLimit,
	})
	if err != nil {
		return GenerationLifecycleSweep{}, fmt.Errorf("scan generation lifecycle: %w", err)
	}
	var candidates []generationLifecycleCandidate
	for _, result := range *results {
		if len(result.Result) > 0 {
			candidates = result.Result
			break
		}
	}
	sweep := GenerationLifecycleSweep{Scanned: len(candidates)}
	if len(candidates) == 0 {
		return sweep, nil
	}
	for _, candidate := range candidates {
		if !validSHA256(candidate.Digest) || candidate.Repository == "" ||
			!validGenerationToken(candidate.Stage) || candidate.UpdatedAt.IsZero() {
			return GenerationLifecycleSweep{}, errors.New("scan generation lifecycle: candidate is malformed")
		}
		sweep.Cursor = candidate.Digest
		deleted, deleteErr := s.collectGenerationSchedule(
			ctx, candidate, deleteLimit-sweep.Deleted, retained,
		)
		if deleteErr != nil {
			return GenerationLifecycleSweep{}, deleteErr
		}
		sweep.Deleted += deleted
		if sweep.Deleted >= deleteLimit {
			sweep.More = true
			return sweep, nil
		}
	}
	if len(candidates) == scanLimit {
		sweep.More = true
		return sweep, nil
	}
	// End of the bounded key walk. Resetting only after a short page makes
	// schedules skipped because of a lease, root, or rollback floor visible on
	// a later cycle without an unbounded rescan in this tick.
	sweep.Cursor = ""
	if after != "" {
		sweep.More = true
	}
	return sweep, nil
}

func (s *Surreal) collectGenerationSchedule(
	ctx context.Context,
	candidate generationLifecycleCandidate,
	deleteLimit, retained int,
) (int, error) {
	// A collectible nonempty schedule needs room for at least one chunk and
	// its schedule row. Conservatively defer the candidate instead of allowing
	// a committed transaction to exceed the caller's remaining row budget.
	if deleteLimit < 2 {
		return 0, nil
	}
	chunkLimit := deleteLimit - 1 // reserve one deletion for the schedule row
	results, err := surrealdb.Query[[]generationLifecycleDelete](ctx, s.db, `
BEGIN;
LET $schedule = (SELECT digest, repository, stage, status, updated_at
	FROM $schedule_rid LIMIT 1)[0];
LET $current = (SELECT schedule_digest FROM $current_rid LIMIT 1)[0].schedule_digest;
LET $newest = SELECT VALUE digest FROM generation_schedule
	WHERE repository = $repository AND stage = $stage AND status != 'active'
	ORDER BY updated_at DESC, digest DESC LIMIT $retained;
LET $running = array::len(SELECT id FROM generation_schedule_chunk
	WHERE schedule_digest = $digest AND status = 'running' LIMIT 1);
LET $beyond_count = $schedule != NONE AND $digest NOT IN $newest;
LET $eligible = $schedule != NONE AND $schedule.digest = $digest AND
	$schedule.repository = $repository AND $schedule.stage = $stage AND
	$schedule.status != 'active' AND $current != $digest AND $running = 0 AND
	$beyond_count;
LET $chunk_ids = (IF $eligible THEN (SELECT VALUE id FROM generation_schedule_chunk
	WHERE schedule_digest = $digest ORDER BY offset, attempt LIMIT $chunk_limit) ELSE [] END) ?? [];
LET $deleted_chunks = IF $eligible AND array::len($chunk_ids) > 0 THEN
	(DELETE $chunk_ids RETURN BEFORE) ELSE [] END;
LET $remaining = IF $eligible THEN array::len(SELECT id FROM generation_schedule_chunk
	WHERE schedule_digest = $digest LIMIT 1) ELSE 1 END;
LET $deleted_schedule = IF $eligible AND $remaining = 0 THEN
	(DELETE $schedule_rid RETURN BEFORE) ELSE [] END;
RETURN [{ deleted: array::len($deleted_chunks) + array::len($deleted_schedule) }];
COMMIT;`, map[string]any{
		"schedule_rid": models.NewRecordID("generation_schedule", strings.TrimPrefix(candidate.Digest, "sha256:")),
		"current_rid":  models.NewRecordID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(candidate.Repository, candidate.Stage), "sha256:")),
		"repository":   candidate.Repository, "stage": candidate.Stage,
		"digest": candidate.Digest, "retained": retained, "chunk_limit": chunkLimit,
	})
	if err != nil {
		return 0, fmt.Errorf("collect generation lifecycle: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			deleted := result.Result[0].Deleted
			if deleted < 0 || deleted > deleteLimit {
				return 0, errors.New("collect generation lifecycle: invalid deletion count")
			}
			return deleted, nil
		}
	}
	return 0, errors.New("collect generation lifecycle: result is absent")
}
