package servicequery

import (
	"context"
	"fmt"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

// GenerationStore is the bounded state transition used after an exact direct
// search publication or during startup backfill.
type GenerationStore interface {
	GetRepo(context.Context, string) (*store.Repo, error)
	GetServiceCatalog(context.Context, string) (*servicecatalog.Publication, error)
	ServiceGenerationActivationNeeded(context.Context, string, string, string, string) (bool, error)
	ActivateServiceGeneration(context.Context, string, string, string, string) (int, error)
}

type ReconcileOutcome struct {
	Activated int
	Current   bool
	Search    repositoryindex.SearchManifest
}

// ReconcileGeneration validates a replacement completely before atomically
// activating every accepted desired service. An already-current generation
// takes the bounded control-only path; it never rereads source members.
func ReconcileGeneration(
	ctx context.Context,
	indexDir string,
	st GenerationStore,
	repository string,
) (ReconcileOutcome, error) {
	repo, err := st.GetRepo(ctx, repository)
	if err != nil {
		return ReconcileOutcome{}, fmt.Errorf("reconcile service search: repository: %w", err)
	}
	revisions, err := runtimeRevisions(*repo)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	publication, err := st.GetServiceCatalog(ctx, repository)
	if err != nil {
		return ReconcileOutcome{}, fmt.Errorf("reconcile service search: catalog: %w", err)
	}
	search, source, err := focusedindex.ReadRepositorySearchGeneration(
		indexDir, repository, revisions,
	)
	if err != nil {
		return ReconcileOutcome{}, fmt.Errorf("reconcile service search: controls: %w", err)
	}
	if err := catalogMatchesSource(*publication, source); err != nil {
		return ReconcileOutcome{}, fmt.Errorf("reconcile service search: %w", err)
	}
	sourceGeneration, err := servicecatalog.SourceGenerationDigest(*publication)
	if err != nil {
		return ReconcileOutcome{}, fmt.Errorf("reconcile service search: source identity: %w", err)
	}
	needed, err := st.ServiceGenerationActivationNeeded(
		ctx, repository, publication.GenerationDigest, sourceGeneration, search.Digest,
	)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	if !needed {
		return ReconcileOutcome{Current: true, Search: search}, nil
	}
	validated, err := focusedindex.ValidateRepositorySearchGeneration(
		ctx, indexDir, repository, revisions,
	)
	if err != nil {
		return ReconcileOutcome{}, fmt.Errorf("reconcile service search: validate replacement: %w", err)
	}
	if validated.Digest != search.Digest {
		return ReconcileOutcome{}, fmt.Errorf("reconcile service search: generation changed during validation")
	}
	activated, err := st.ActivateServiceGeneration(
		ctx, repository, publication.GenerationDigest, sourceGeneration, validated.Digest,
	)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	return ReconcileOutcome{
		Activated: activated, Current: true, Search: validated,
	}, nil
}

func catalogMatchesSource(
	publication servicecatalog.Publication,
	source repositoryindex.SourceManifest,
) error {
	if source.Repository != publication.Repository || len(source.Revisions) == 0 ||
		source.Revisions[0].Commit != publication.SourceCommit ||
		len(source.RevisionMembers) == 0 ||
		source.RevisionMembers[0].RegularCount != publication.SourceFileCount {
		return fmt.Errorf("catalog source does not match repository source generation")
	}
	return nil
}
