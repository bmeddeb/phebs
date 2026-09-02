package servicecatalogingest

import (
	"context"
	"errors"
	"fmt"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type v3RepositoryStore interface {
	store.ServiceCatalogV3CandidateStore
	GetRepo(context.Context, string) (*store.Repo, error)
	ListRepos(context.Context) ([]store.Repo, error)
}

// V3Reconciler publishes only the T41.3 candidate pointer. The runtime
// controller may build from it, but the candidate never selects itself.
type V3Reconciler struct {
	DataDir       string
	Store         v3RepositoryStore
	Selections    map[string]config.ServiceCatalog
	BeforePublish func(context.Context, string) error
}

func (r *V3Reconciler) Reconcile(ctx context.Context) (Report, error) {
	if r.Store == nil {
		return Report{}, errors.New("service catalog v3 reconciler requires a store")
	}
	repositories, err := r.Store.ListRepos(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list repositories: %w", err)
	}
	report := Report{Failures: []Failure{}}
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		outcome, reconcileErr := r.reconcile(ctx, repository)
		if reconcileErr != nil {
			report.Failures = append(report.Failures, Failure{
				Repository: repository.Name,
				Err:        reconcileErr,
			})
			continue
		}
		report.add(outcome)
	}
	return report, nil
}

func (r *V3Reconciler) ReconcileRepository(
	ctx context.Context,
	repository string,
) (Outcome, error) {
	if r.Store == nil {
		return "", errors.New("service catalog v3 reconciler requires a store")
	}
	current, err := r.Store.GetRepo(ctx, repository)
	if err != nil {
		return "", err
	}
	return r.reconcile(ctx, *current)
}

func (r *V3Reconciler) reconcile(
	ctx context.Context,
	repository store.Repo,
) (Outcome, error) {
	if repository.Deleting || repository.IndexedCommitHash == "" {
		return OutcomeNotReady, nil
	}
	selection, selected := r.Selections[repository.Name]
	if !selected {
		return OutcomeUnselected, nil
	}
	content, err := readSelectedFile(selection.Path, servicecatalogv3.MaxPublicationBytes)
	if errors.Is(err, errSelectedCatalogEncodedLimit) {
		return "", servicecatalogv3.ErrLimit
	}
	if err != nil {
		return "", err
	}
	catalog, err := servicecatalogv3.DecodeCatalog(content)
	if err != nil {
		return "", fmt.Errorf("decode selected catalog v3 input: %w", err)
	}
	wantAuthority := servicecatalog.Authority{
		Kind: selection.Kind, ID: selection.ID, Version: selection.Version,
	}
	if selection.Kind == servicecatalog.AuthorityCommitted {
		wantAuthority.Version = repository.IndexedCommitHash
	}
	if catalog.Authority != wantAuthority {
		return "", errors.New("selected catalog authority does not match explicit configuration")
	}

	current, currentErr := r.Store.GetServiceCatalogV3CandidateRoot(ctx, repository.Name)
	if currentErr != nil && !errors.Is(currentErr, store.ErrNotFound) {
		return "", fmt.Errorf("strict-open prior v3 candidate: %w", currentErr)
	}
	if current != nil && current.Root.Binding.Authority == catalog.Authority &&
		sameOverride(current.Root.Binding.Override, catalog.Override) {
		candidate, buildErr := servicecatalogv3.Build(current.Root.Binding, catalog)
		if buildErr != nil {
			return "", buildErr
		}
		if candidate.Root.LogicalDigest != current.Root.LogicalDigest {
			return "", fmt.Errorf("service catalog v3 authority version was reused with different bytes: %w", store.ErrConflict)
		}
		if current.Root.Binding.Source.Kind == selection.Kind &&
			current.Root.Binding.Source.Path == selection.Path &&
			current.Root.Binding.Source.Commit == repository.IndexedCommitHash &&
			candidate.Root.Digest == current.Root.Digest {
			return OutcomeCurrent, nil
		}
	}

	base := Reconciler{DataDir: r.DataDir}
	census, err := base.censusValidated(
		ctx, repository.Name, repository.IndexedCommitHash, catalog, false,
		servicecatalogv3.ValidateCatalog,
	)
	if err != nil {
		return "", err
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository.Name,
		Source: servicecatalogv3.Source{
			Kind: selection.Kind, Path: selection.Path,
			Commit: repository.IndexedCommitHash, CensusDigest: census.Digest,
			FileCount: census.FileCount, AcceptedFileCount: census.AcceptedFileCount,
			UnownedFileCount: census.UnownedFileCount,
		},
		Authority: catalog.Authority,
		Override:  catalog.Override,
	}, catalog)
	if err != nil {
		return "", err
	}
	if err := servicecatalogv3.ValidateGeneration(generation); err != nil {
		return "", err
	}
	if r.BeforePublish != nil {
		if err := r.BeforePublish(ctx, repository.Name); err != nil {
			return "", err
		}
	}
	if err := r.Store.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		return "", err
	}
	persisted, err := r.Store.GetServiceCatalogV3CandidateRoot(ctx, repository.Name)
	if err != nil || persisted.Root.Digest != generation.Root.Digest {
		return "", fmt.Errorf("strict-open published v3 candidate: %w", errors.Join(err, store.ErrConflict))
	}
	return OutcomePublished, nil
}

func sameOverride(
	left, right *servicecatalog.OperatorOverride,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
