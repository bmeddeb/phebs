package resolvercatalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
)

type ReconcileStore interface {
	ListResolverCatalogPublications(context.Context) ([]store.ResolverCatalogPublication, error)
	GetResolverCatalogPublication(context.Context, string) (*store.ResolverCatalogPublication, error)
	ResolverCatalogPublicationCurrent(context.Context, store.ResolverCatalogPublication) (bool, error)
	PublishResolverCatalog(context.Context, store.ResolverCatalogPublication) error
	ClearResolverCatalogPublication(context.Context, string) error
	EnqueuePending(context.Context, store.JobKind, string, bool) (*store.Job, error)
}

type ReconcileReport struct {
	StagesRemoved       int
	MarkersRecovered    int
	PublicationsCurrent int
	ReplacementsQueued  int
	PointersCleared     int
	OrphansObserved     int
}

// Reconcile repairs every publication crash boundary. Queue-before-clear is
// deliberate: a process death can leave a redundant forced successor, but
// never a cleared pointer with no durable replacement request.
func Reconcile(
	ctx context.Context,
	root string,
	st ReconcileStore,
	expectedPacks []ResolverPack,
) (ReconcileReport, error) {
	var report ReconcileReport
	expectedPacks = append([]ResolverPack{}, expectedPacks...)
	if err := validateResolverPacks(expectedPacks); err != nil {
		return report, err
	}
	removed, err := CleanupStages(ctx, root)
	if err != nil {
		return report, err
	}
	report.StagesRemoved = removed
	pointers, err := st.ListResolverCatalogPublications(ctx)
	if err != nil {
		return report, err
	}
	pointerByRepository := make(map[string]store.ResolverCatalogPublication, len(pointers))
	for _, pointer := range pointers {
		pointerByRepository[pointer.Repository] = pointer
		state := stateFromStore(pointer)
		authorityCurrent, authorityErr :=
			st.ResolverCatalogPublicationCurrent(ctx, pointer)
		if authorityErr != nil {
			return report, authorityErr
		}
		if IsPublishing(root, pointer.Repository) {
			if authorityCurrent {
				if publication, openErr := OpenPublishing(
					ctx, root, state,
				); openErr == nil &&
					slices.Equal(state.ResolverPacks, expectedPacks) {
					if err := finishMarkedPublication(
						root, publication,
					); err != nil {
						return report, err
					}
					report.MarkersRecovered++
					report.PublicationsCurrent++
					continue
				}
			}
			// The stable manifest may be a replacement made durable after
			// the still-current pointer was read but before its store commit.
			// Revalidate that manifest from its own canonical identity and
			// let the guarded store transaction decide whether it is current.
			if publication, openErr := openMarkedManifest(
				ctx, root, pointer.Repository, expectedPacks,
			); openErr == nil {
				replacement := storeFromState(publication.State())
				if publishErr := st.PublishResolverCatalog(
					ctx, replacement,
				); publishErr == nil {
					if err := finishMarkedPublication(
						root, publication,
					); err != nil {
						return report, err
					}
					report.MarkersRecovered++
					report.PublicationsCurrent++
					continue
				}
			}
		}
		if !authorityCurrent {
			if err := queueReplacement(ctx, st, pointer.Repository); err != nil {
				return report, err
			}
			report.ReplacementsQueued++
			if err := st.ClearResolverCatalogPublication(
				ctx, pointer.Repository,
			); err != nil {
				return report, err
			}
			report.PointersCleared++
			if err := removeRepositoryArtifacts(root, pointer.Repository); err != nil {
				return report, err
			}
			continue
		}
		if !IsPublishing(root, pointer.Repository) {
			if publication, openErr := Open(
				ctx, root, state,
			); openErr == nil &&
				slices.Equal(state.ResolverPacks, expectedPacks) {
				if err := CleanupRetired(
					root, publication.manifest,
				); err != nil {
					return report, err
				}
				report.PublicationsCurrent++
				continue
			}
		}
		if err := queueReplacement(ctx, st, pointer.Repository); err != nil {
			return report, err
		}
		report.ReplacementsQueued++
		if err := st.ClearResolverCatalogPublication(ctx, pointer.Repository); err != nil {
			return report, err
		}
		report.PointersCleared++
		if err := removeRepositoryArtifacts(root, pointer.Repository); err != nil {
			return report, err
		}
	}

	states, markerRepositories, err := discoverStatesAndMarkers(root)
	if err != nil {
		return report, err
	}
	for _, state := range states {
		if _, exists := pointerByRepository[state.Repository]; exists {
			continue
		}
		if IsPublishing(root, state.Repository) {
			if publication, openErr := OpenPublishing(
				ctx, root, state,
			); openErr == nil &&
				slices.Equal(state.ResolverPacks, expectedPacks) {
				if err := st.PublishResolverCatalog(ctx, storeFromState(state)); err == nil {
					if err := finishMarkedPublication(
						root, publication,
					); err != nil {
						return report, err
					}
					report.MarkersRecovered++
					report.PublicationsCurrent++
					continue
				}
			}
		}
		// An unmarked exact catalog without a pointer is restored or orphaned
		// derived state. It is never promoted: only a new worker claim may
		// establish current authority in this installation.
		if err := queueReplacement(ctx, st, state.Repository); err != nil {
			return report, err
		}
		report.ReplacementsQueued++
		report.OrphansObserved++
	}
	for _, repository := range markerRepositories {
		if _, exists := pointerByRepository[repository]; exists {
			continue
		}
		foundState := false
		for _, state := range states {
			if state.Repository == repository {
				foundState = true
				break
			}
		}
		if foundState {
			continue
		}
		if err := queueReplacement(ctx, st, repository); err != nil {
			return report, err
		}
		report.ReplacementsQueued++
		if err := removeRepositoryArtifacts(root, repository); err != nil {
			return report, err
		}
	}
	return report, nil
}

func openMarkedManifest(
	ctx context.Context,
	root, repository string,
	expectedPacks []ResolverPack,
) (*Publication, error) {
	manifestPath := filepath.Join(
		root, resolvercatalogid.ManifestName(repository),
	)
	raw, _, err := readStableRegular(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil {
		return nil, err
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	if manifest.Identity.Repository != repository ||
		!slices.Equal(manifest.Identity.ResolverPacks, expectedPacks) {
		return nil, ErrInvalidManifest
	}
	return OpenPublishing(ctx, root, manifest.State())
}

func finishMarkedPublication(root string, publication *Publication) error {
	if publication == nil {
		return ErrInvalidManifest
	}
	if err := ClearPublishing(
		root, publication.manifest.Identity.Repository,
	); err != nil {
		return err
	}
	return CleanupRetired(root, publication.manifest)
}

func validateResolverPacks(packs []ResolverPack) error {
	if len(packs) > MaxResolverPacks {
		return ErrLimit
	}
	for index, pack := range packs {
		if !validToken(pack.Name, 128) || !validToken(pack.Version, 64) ||
			index > 0 && packs[index-1].Name >= pack.Name {
			return errors.New("expected resolver packs are invalid or unordered")
		}
	}
	return nil
}

func queueReplacement(ctx context.Context, st ReconcileStore, repository string) error {
	_, err := st.EnqueuePending(ctx, store.JobResolverCatalog, repository, true)
	return err
}

func stateFromStore(publication store.ResolverCatalogPublication) State {
	declarations := make([]DeclarationPublication, len(publication.Declarations))
	for index, declaration := range publication.Declarations {
		declarations[index] = DeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
		}
	}
	packs := make([]ResolverPack, len(publication.ResolverPacks))
	for index, pack := range publication.ResolverPacks {
		packs[index] = ResolverPack{Name: pack.Name, Version: pack.Version}
	}
	return State{
		Schema: StateSchema, Repository: publication.Repository,
		Commit: publication.HeadCommit, UnitDigest: publication.UnitDigest,
		Declarations:            declarations,
		DeclarationSetDigest:    publication.DeclarationSetDigest,
		CandidateManifestDigest: publication.CandidateManifestDigest,
		SourceLanePolicy:        publication.SourceLanePolicy,
		ResolverPacks:           packs,
		ResolverPackSetDigest:   publication.ResolverPackSetDigest,
		CatalogPolicyDigest:     publication.CatalogPolicyDigest,
		GenerationDigest:        publication.GenerationDigest,
		ManifestDigest:          publication.ManifestDigest,
		Manifest:                publication.ManifestPath,
	}
}

func storeFromState(state State) store.ResolverCatalogPublication {
	declarations := make(
		[]store.ResolverCatalogDeclarationPublication, len(state.Declarations),
	)
	for index, declaration := range state.Declarations {
		declarations[index] = store.ResolverCatalogDeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
		}
	}
	packs := make([]store.ResolverCatalogPack, len(state.ResolverPacks))
	for index, pack := range state.ResolverPacks {
		packs[index] = store.ResolverCatalogPack{
			Name: pack.Name, Version: pack.Version,
		}
	}
	return store.ResolverCatalogPublication{
		Repository: state.Repository, HeadCommit: state.Commit,
		UnitDigest:              state.UnitDigest,
		Declarations:            declarations,
		DeclarationSetDigest:    state.DeclarationSetDigest,
		CandidateManifestDigest: state.CandidateManifestDigest,
		SourceLanePolicy:        state.SourceLanePolicy,
		ResolverPacks:           packs,
		ResolverPackSetDigest:   state.ResolverPackSetDigest,
		CatalogPolicyDigest:     state.CatalogPolicyDigest,
		GenerationDigest:        state.GenerationDigest,
		ManifestDigest:          state.ManifestDigest,
		ManifestPath:            state.Manifest,
	}
}

func discoverStatesAndMarkers(root string) ([]State, []string, error) {
	entries, err := readBoundedDirectory(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	var states []State
	var markerRepositories []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".manifest.json") &&
			strings.HasPrefix(name, "phebs-resolver-catalog-") {
			raw, _, readErr := readStableRegular(
				filepath.Join(root, name), maxManifestBytes,
			)
			if readErr != nil {
				continue
			}
			var manifest Manifest
			if decodeCanonical(raw, &manifest) == nil &&
				validateManifest(manifest) == nil &&
				manifest.State().Manifest == name {
				states = append(states, manifest.State())
			}
			continue
		}
		if strings.HasSuffix(name, ".publishing") &&
			strings.HasPrefix(name, "phebs-resolver-catalog-") {
			raw, _, readErr := readStableRegular(
				filepath.Join(root, name), 1024,
			)
			if readErr != nil || len(raw) < 2 || raw[len(raw)-1] != '\n' {
				continue
			}
			repository := string(raw[:len(raw)-1])
			if resolvercatalogid.PublishingName(repository) == name {
				markerRepositories = append(markerRepositories, repository)
			}
		}
	}
	return states, markerRepositories, nil
}

func removeRepositoryArtifacts(root, repository string) error {
	entries, err := readBoundedDirectory(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	base := resolvercatalogid.ArtifactBase(repository)
	for _, entry := range entries {
		name := entry.Name()
		if name != resolvercatalogid.ManifestName(repository) &&
			name != resolvercatalogid.PublishingName(repository) &&
			!strings.HasPrefix(name, base+"-") {
			continue
		}
		target := filepath.Join(root, name)
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("resolver catalog owned path %q is a directory", name)
		}
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(root)
}
