package store_test

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func sweepEvidenceRun(
	ctx context.Context, s *store.Surreal, now time.Time, staleStagedAfter time.Duration,
) (int, error) {
	for stepCount := 0; stepCount < 100_000; stepCount++ {
		step, err := s.SweepEvidence(ctx, now, staleStagedAfter)
		if err != nil {
			return 0, err
		}
		if step.RunsDeleted == 1 {
			return 1, nil
		}
		if !step.DidWork() {
			return 0, nil
		}
	}
	return 0, errors.New("evidence sweep did not complete within the test step bound")
}
