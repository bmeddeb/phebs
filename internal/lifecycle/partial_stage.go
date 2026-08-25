package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
)

type ExtractionStageOwner struct {
	Root    string
	Acquire func(context.Context) (func(), error)
}

func (ExtractionStageOwner) Name() string { return PartialStageOwner }

func (owner ExtractionStageOwner) Sweep(
	ctx context.Context, now time.Time, cursor string, limits Limits,
) OwnerResult {
	if owner.Root == "" || owner.Acquire == nil {
		return OwnerResult{
			Cursor: cursor, Completeness: Unavailable,
			Err: errors.New("partial-stage lifecycle owner is incomplete"),
		}
	}
	release, err := owner.Acquire(ctx)
	if err != nil {
		return OwnerResult{
			Cursor: cursor, Completeness: Unavailable, Err: err,
		}
	}
	defer release()
	result, err := extractionpublication.SweepStageLifecycle(
		ctx, owner.Root, now, cursor, extractionpublication.StageLifecycleLimits{
			Candidates: limits.Candidates, Deletes: limits.Deletes,
			Stats: limits.Stats, Descriptors: limits.Descriptors,
			MetadataBytes: limits.MetadataBytes,
		},
	)
	if err != nil {
		return OwnerResult{
			Cursor: result.Cursor, Scanned: result.Scanned, Deleted: result.Deleted,
			More: result.More, AdvanceOnError: result.Cursor != "" && result.Cursor != cursor,
			Completeness: Unavailable, Err: err,
		}
	}
	completeness := Exact
	if result.More || result.Active {
		completeness = LowerBound
	}
	return OwnerResult{
		Cursor: result.Cursor, Scanned: result.Scanned, Deleted: result.Deleted,
		More: result.More, Completeness: completeness,
	}
}
