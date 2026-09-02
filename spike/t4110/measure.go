package t4110

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func measurePhase(
	ctx context.Context,
	dataRoot string,
	name string,
	work func(context.Context, *PhaseCost) error,
) (MeasuredPhase, error) {
	if err := ctx.Err(); err != nil {
		return MeasuredPhase{}, err
	}
	sampler, err := startPhaseProcessSampler(ctx)
	if err != nil {
		return MeasuredPhase{}, fmt.Errorf("T41.10 phase %s RSS sampler: %w", name, err)
	}
	started := time.Now()
	var cost PhaseCost
	workErr := work(ctx, &cost)
	elapsed := time.Since(started).Milliseconds()
	rss, sampleErr := sampler.stop()
	if err := errors.Join(workErr, sampleErr); err != nil {
		return MeasuredPhase{}, fmt.Errorf("T41.10 phase %s: %w", name, err)
	}
	if elapsed < 1 {
		elapsed = 1
	}
	cost.WallMilliseconds = elapsed
	cost.PeakRSSBytes = rss
	logical, allocated, err := diskUsage(dataRoot)
	if err != nil {
		return MeasuredPhase{}, fmt.Errorf("T41.10 phase %s disk: %w", name, err)
	}
	cost.DataLogicalBytes = logical
	cost.DataAllocatedBytes = allocated
	return MeasuredPhase{Name: name, Outcome: StepPassed, Cost: cost}, nil
}
