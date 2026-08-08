package extractionpublication

import (
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
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
	release, err := fence.Acquire(ctx)
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

var _ Fence = AuthorityFence{}
