package t4110

import (
	"context"
	"errors"
	"fmt"

	"github.com/bmeddeb/phebs/internal/store"
)

func runServiceStatePlan(
	ctx context.Context,
	state *store.Surreal,
	begin store.ServiceStateV3Begin,
	worker string,
) (PhaseCost, error) {
	return runServiceStatePlanWithFirstChunkHook(ctx, state, begin, worker, nil)
}

func runServiceStatePlanWithFirstChunkHook(
	ctx context.Context,
	state *store.Surreal,
	begin store.ServiceStateV3Begin,
	worker string,
	afterFirst func(context.Context) error,
) (PhaseCost, error) {
	if begin.Noop {
		return PhaseCost{}, nil
	}
	if begin.Plan == nil || begin.Schedule == nil || worker == "" {
		return PhaseCost{}, errors.New("T41.10 service-state plan is incomplete")
	}
	schedule := begin.Schedule
	for schedule.NextOffset < schedule.TotalItems {
		var err error
		schedule, err = state.ExpandGenerationSchedule(
			ctx,
			schedule.Repository,
			schedule.Stage,
			schedule.Generation,
		)
		if err != nil {
			return PhaseCost{}, fmt.Errorf("expand service-state schedule: %w", err)
		}
	}

	var cost PhaseCost
	first := true
	for {
		chunk, err := state.ClaimGenerationChunk(ctx, store.GenerationResourceCPU, worker)
		if err != nil {
			return PhaseCost{}, fmt.Errorf("claim service-state chunk: %w", err)
		}
		if chunk.ScheduleDigest != schedule.Digest {
			return PhaseCost{}, errors.New("T41.10 service-state worker claimed another schedule")
		}
		result, err := state.ProcessServiceStateV3Chunk(ctx, *chunk)
		if err != nil {
			return PhaseCost{}, fmt.Errorf("process service-state chunk: %w", err)
		}
		cost.StateChunkTransactions++
		cost.StateRowsRead += uint64(result.Read)
		cost.StateRowsApplied += uint64(result.Applied)
		cost.ChangedRows += uint64(result.Applied)
		if err := state.CompleteGenerationChunk(ctx, *chunk); err != nil {
			return PhaseCost{}, fmt.Errorf("complete service-state chunk: %w", err)
		}
		if first && afterFirst != nil {
			if err := afterFirst(ctx); err != nil {
				return PhaseCost{}, fmt.Errorf("service-state first-chunk fence: %w", err)
			}
		}
		first = false
		settled, err := state.GetGenerationSchedule(
			ctx,
			schedule.Repository,
			schedule.Stage,
		)
		if err != nil {
			return PhaseCost{}, fmt.Errorf("read service-state schedule: %w", err)
		}
		if settled.Status == store.GenerationScheduleSettled {
			return cost, nil
		}
	}
}
