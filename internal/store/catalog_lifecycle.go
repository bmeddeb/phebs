package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type CatalogLifecycleSweep struct {
	Cursor  string
	Scanned int
	Deleted int
	More    bool
}

type catalogLifecycleCandidate struct {
	GenerationDigest string    `json:"generation_digest"`
	Repository       string    `json:"repository"`
	RecordedAt       time.Time `json:"recorded_at"`
}

type catalogLifecycleDelete struct {
	Deleted int `json:"deleted"`
}

// SweepCatalogLifecycle applies T35.3's deliberately reduced count-only
// policy. Age and byte eligibility remain disabled until a later ticket can
// supply their owner-specific metrics without weakening the rollback floor.
func (s *Surreal) SweepCatalogLifecycle(
	ctx context.Context,
	after string,
	scanLimit, deleteLimit, retained int,
) (CatalogLifecycleSweep, error) {
	if after != "" && !validSHA256(after) {
		return CatalogLifecycleSweep{}, errors.New("sweep catalog lifecycle: cursor is invalid")
	}
	if scanLimit < 1 || scanLimit > 64 ||
		deleteLimit < 1 || deleteLimit > 16 || retained < 1 || retained > 8 {
		return CatalogLifecycleSweep{}, errors.New("sweep catalog lifecycle: limits are invalid")
	}
	results, err := storeQuery[[]catalogLifecycleCandidate](ctx, s.accounting, s.db, `
RETURN SELECT generation_digest, repository, recorded_at FROM service_catalog_generation
	WHERE generation_digest > $after ORDER BY generation_digest LIMIT $limit;`, map[string]any{
		"after": after, "limit": scanLimit,
	}, storeRead())
	if err != nil {
		return CatalogLifecycleSweep{}, fmt.Errorf("scan catalog lifecycle: %w", err)
	}
	var candidates []catalogLifecycleCandidate
	for _, result := range *results {
		if len(result.Result) > 0 {
			candidates = result.Result
			break
		}
	}
	sweep := CatalogLifecycleSweep{Scanned: len(candidates)}
	if len(candidates) == 0 {
		return sweep, nil
	}
	for _, candidate := range candidates {
		if !validSHA256(candidate.GenerationDigest) || candidate.Repository == "" ||
			candidate.RecordedAt.IsZero() {
			return CatalogLifecycleSweep{}, errors.New("scan catalog lifecycle: candidate is malformed")
		}
		sweep.Cursor = candidate.GenerationDigest
		deleted, deleteErr := s.collectCatalogGeneration(
			ctx, candidate, retained,
		)
		if deleteErr != nil {
			return CatalogLifecycleSweep{}, deleteErr
		}
		sweep.Deleted += deleted
		if deleted > 0 {
			// One immutable catalog generation per tick localizes a malformed
			// historical row and leaves cursor persistence between destructive
			// transactions. The fixed delete ceiling remains an upper bound.
			sweep.More = true
			return sweep, nil
		}
	}
	if len(candidates) == scanLimit {
		sweep.More = true
		return sweep, nil
	}
	sweep.Cursor = ""
	if after != "" {
		sweep.More = true
	}
	return sweep, nil
}

func (s *Surreal) collectCatalogGeneration(
	ctx context.Context,
	candidate catalogLifecycleCandidate,
	retained int,
) (int, error) {
	results, err := storeQuery[[]catalogLifecycleDelete](ctx, s.accounting, s.db, `
BEGIN;
LET $candidate = (SELECT generation_digest, repository, recorded_at
	FROM service_catalog_generation WHERE generation_digest = $digest LIMIT 1)[0];
LET $current = (SELECT generation_digest FROM service_catalog_current
	WHERE repository = $repository LIMIT 1)[0].generation_digest;
LET $service_ref = array::len(SELECT id FROM service_state_current
	WHERE repository = $repository AND
		(active_catalog_generation = $digest OR desired_catalog_generation = $digest)
	LIMIT 1);
LET $newest_prior = SELECT VALUE generation_digest FROM service_catalog_generation
	WHERE repository = $repository AND generation_digest != $current
	ORDER BY recorded_at DESC, generation_digest DESC LIMIT $prior_retained;
LET $beyond_count = $candidate != NONE AND $digest != $current AND
	$digest NOT IN $newest_prior;
LET $eligible = $candidate != NONE AND $candidate.repository = $repository AND
	$current != $digest AND $service_ref = 0 AND $beyond_count;
LET $deleted = IF $eligible THEN
	(DELETE service_catalog_generation WHERE generation_digest = $digest RETURN BEFORE)
	ELSE [] END;
RETURN [{ deleted: array::len($deleted) }];
COMMIT;`, map[string]any{
		"digest": candidate.GenerationDigest, "repository": candidate.Repository,
		"prior_retained": retained - 1,
	}, storeUnsupported())
	if err != nil {
		return 0, fmt.Errorf("collect catalog lifecycle: %w", err)
	}
	for _, result := range *results {
		if len(result.Result) > 0 {
			if result.Result[0].Deleted < 0 || result.Result[0].Deleted > 1 {
				return 0, errors.New("collect catalog lifecycle: invalid deletion count")
			}
			return result.Result[0].Deleted, nil
		}
	}
	return 0, errors.New("collect catalog lifecycle: result is absent")
}
