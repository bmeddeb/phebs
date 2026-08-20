package extractionpublication

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	authorityFenceAcquireTimeout = 5 * time.Second
	authorityFenceProbeTimeout   = 25 * time.Millisecond
	authorityFenceRetryDelay     = 10 * time.Millisecond
)

// AuthorityFence holds the installation's short derived-publication mutation
// lock across the authority check, store root transition, and filesystem
// pointer swap. Current must re-prove candidate, source, and observation
// generations without reading corpus content.
type AuthorityFence struct {
	Store   store.GenerationSchedulerStore
	Acquire func(context.Context) (release func(), err error)
	Current func(context.Context, candidate.DomainResultPlan) error
}

func (fence AuthorityFence) FenceDomain(
	ctx context.Context,
	request FenceRequest,
) (func(), error) {
	if fence.Store == nil || fence.Acquire == nil || fence.Current == nil ||
		candidate.ValidateDomainResultPlanControl(request.Plan) != nil {
		return nil, invalid("authority fence configuration")
	}
	release, err := acquireAuthorityFence(
		ctx, fence.Acquire, authorityFenceAcquireTimeout,
		authorityFenceProbeTimeout, authorityFenceRetryDelay,
	)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, errors.New("authority fence returned no release")
	}
	fail := func(cause error) (func(), error) {
		release()
		return nil, cause
	}
	if request.Chunk.Generation != "" {
		if err := fence.Store.HeartbeatGenerationChunk(ctx, request.Chunk); err != nil {
			return fail(err)
		}
	}
	if err := fence.Current(ctx, request.Plan); err != nil {
		return fail(err)
	}
	return release, nil
}

var errAuthorityFenceBusy = errors.New("extraction authority fence is busy")

func acquireAuthorityFence(
	ctx context.Context,
	acquire func(context.Context) (func(), error),
	timeout, probe, retryDelay time.Duration,
) (func(), error) {
	if ctx == nil || acquire == nil || timeout <= 0 || probe <= 0 || probe > timeout ||
		retryDelay <= 0 {
		return nil, invalid("authority fence acquisition")
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		attempt, cancelAttempt := context.WithTimeout(wait, probe)
		release, err := acquire(attempt)
		attemptErr := attempt.Err()
		cancelAttempt()
		if err == nil {
			if release == nil {
				return nil, errors.New("authority fence returned no release")
			}
			return release, nil
		}
		if release != nil {
			release()
			return nil, errors.New("authority fence returned release with error")
		}
		if wait.Err() != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("acquire extraction authority fence: %w", ctxErr)
			}
			return nil, store.WithDeferral(fmt.Errorf(
				"%w: %w", errAuthorityFenceBusy, wait.Err(),
			))
		}
		if attemptErr == nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("acquire extraction authority fence: %w", err)
		}
		retry := time.NewTimer(retryDelay)
		select {
		case <-wait.Done():
			retry.Stop()
		case <-retry.C:
		}
	}
}

var _ Fence = AuthorityFence{}
