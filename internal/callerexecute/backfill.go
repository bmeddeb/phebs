package callerexecute

import (
	"context"
	"errors"
	"fmt"

	"github.com/bmeddeb/phebs/internal/store"
)

// BackfillStore is the startup boundary needed to close the upgrade gap for
// repositories whose resolver catalog predates the caller-leaf queue.
type BackfillStore interface {
	ListRepos(context.Context) ([]store.Repo, error)
	GetResolverCatalogPublication(
		context.Context, string,
	) (*store.ResolverCatalogPublication, error)
	ResolverCatalogPublicationCurrent(
		context.Context, store.ResolverCatalogPublication,
	) (bool, error)
	EnqueuePending(
		context.Context, store.JobKind, string, bool,
	) (*store.Job, error)
}

// EnqueueBackfill idempotently ensures one caller-leaf job for every live,
// indexed repository with a current resolver catalog. Missing and retired
// catalogs are skipped: the resolver publication transaction owns the later
// crash-safe successor.
func EnqueueBackfill(ctx context.Context, state BackfillStore) error {
	if state == nil {
		return fmt.Errorf("backfill caller leaves: state is required")
	}
	repositories, err := state.ListRepos(ctx)
	if err != nil {
		return fmt.Errorf("backfill caller leaves: list repositories: %w", err)
	}
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("backfill caller leaves: %w", err)
		}
		if repository.Deleting || repository.IndexedCommitHash == "" {
			continue
		}
		publication, err := state.GetResolverCatalogPublication(
			ctx, repository.Name,
		)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf(
				"backfill caller leaves for %s: load resolver catalog: %w",
				repository.Name, err,
			)
		}
		if publication == nil {
			return fmt.Errorf(
				"backfill caller leaves for %s: resolver catalog store returned nil",
				repository.Name,
			)
		}
		current, err := state.ResolverCatalogPublicationCurrent(ctx, *publication)
		if err != nil {
			return fmt.Errorf(
				"backfill caller leaves for %s: validate resolver catalog: %w",
				repository.Name, err,
			)
		}
		if !current {
			continue
		}
		if _, err := state.EnqueuePending(
			ctx, store.JobCallerLeaf, repository.Name, false,
		); err != nil {
			return fmt.Errorf(
				"backfill caller leaves for %s: %w", repository.Name, err,
			)
		}
	}
	return nil
}
