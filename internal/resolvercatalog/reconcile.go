package resolvercatalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/reponame"
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

// ErrNondeterministicPublication means one resolver generation produced two
// different immutable manifests. Recovery must retain the store pointer and
// publication marker: clearing either would turn the rejected challenger into
// authority on the next attempt.
var ErrNondeterministicPublication = errors.New(
	"resolver catalog generation produced conflicting manifests",
)

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
	cleanup := newReconcileCleanup()
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
		publishing, publishingErr := Publishing(root, pointer.Repository)
		if publishingErr != nil {
			return report, publishingErr
		}
		if publishing {
			// The stable manifest may be a replacement made durable after
			// the still-current pointer was read but before its store commit.
			// Read that identity first, then stream its members exactly once.
			if publication, openErr := OpenMarked(
				ctx, root, pointer.Repository, expectedPacks,
			); openErr == nil {
				markedState := publication.State()
				accepted := authorityCurrent && statesEqual(markedState, state)
				if !accepted {
					publishErr := st.PublishResolverCatalog(
						ctx, storeFromState(markedState),
					)
					accepted = publishErr == nil
					if publishErr != nil && !errors.Is(publishErr, store.ErrConflict) {
						return report, fmt.Errorf(
							"recover marked resolver catalog for %q: %w",
							pointer.Repository, publishErr,
						)
					}
					if errors.Is(publishErr, store.ErrConflict) && authorityCurrent {
						// Authority may have changed after the first probe. Recheck
						// before allowing the ordinary queue-before-clear recovery.
						stillCurrent, currentErr :=
							st.ResolverCatalogPublicationCurrent(ctx, pointer)
						if currentErr != nil {
							return report, currentErr
						}
						if stillCurrent {
							if markedState.GenerationDigest == state.GenerationDigest {
								return report, fmt.Errorf(
									"%w: repository %q generation %q has manifests %q and %q",
									ErrNondeterministicPublication,
									pointer.Repository, state.GenerationDigest,
									state.ManifestDigest, markedState.ManifestDigest,
								)
							}
							return report, fmt.Errorf(
								"marked resolver catalog for %q conflicts with current authority: %w",
								pointer.Repository, publishErr,
							)
						}
						authorityCurrent = false
					}
				}
				if accepted {
					if err := finishMarkedPublication(root, publication); err != nil {
						return report, err
					}
					cleanup.keep(publication.manifest)
					report.MarkersRecovered++
					report.PublicationsCurrent++
					continue
				}
			} else if errors.Is(openErr, ErrCatalogIO) {
				return report, openErr
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
			cleanup.remove(pointer.Repository)
			continue
		}
		if !publishing && slices.Equal(
			state.ResolverPacks, expectedPacks,
		) {
			if publication, openErr := Open(
				ctx, root, state,
			); openErr == nil {
				cleanup.keep(publication.manifest)
				report.PublicationsCurrent++
				continue
			} else if errors.Is(openErr, ErrCatalogIO) {
				return report, openErr
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
		cleanup.remove(pointer.Repository)
	}

	states, markerRepositories, err := discoverStatesAndMarkers(root)
	if err != nil {
		return report, err
	}
	stateRepositories := make(map[string]struct{}, len(states))
	for _, state := range states {
		stateRepositories[state.Repository] = struct{}{}
		if _, exists := pointerByRepository[state.Repository]; exists {
			continue
		}
		marked, markedErr := Publishing(root, state.Repository)
		if markedErr != nil {
			return report, markedErr
		}
		if marked && slices.Equal(
			state.ResolverPacks, expectedPacks,
		) {
			if publication, openErr := OpenPublishing(
				ctx, root, state,
			); openErr == nil {
				publishErr := st.PublishResolverCatalog(ctx, storeFromState(state))
				if publishErr == nil {
					if err := finishMarkedPublication(
						root, publication,
					); err != nil {
						return report, err
					}
					cleanup.keep(publication.manifest)
					report.MarkersRecovered++
					report.PublicationsCurrent++
					continue
				}
				if !errors.Is(publishErr, store.ErrConflict) {
					return report, fmt.Errorf(
						"recover pointerless marked resolver catalog for %q: %w",
						state.Repository, publishErr,
					)
				}
			} else if errors.Is(openErr, ErrCatalogIO) {
				return report, openErr
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
		if marked {
			// The durable successor makes this failed prior-process marker
			// disposable. Leaving it would fence every future publisher.
			cleanup.remove(state.Repository)
		}
	}
	for _, repository := range markerRepositories {
		if _, exists := pointerByRepository[repository]; exists {
			continue
		}
		if _, foundState := stateRepositories[repository]; foundState {
			continue
		}
		if err := queueReplacement(ctx, st, repository); err != nil {
			return report, err
		}
		report.ReplacementsQueued++
		cleanup.remove(repository)
	}
	if err := cleanup.apply(ctx, root); err != nil {
		return report, err
	}
	return report, nil
}

// OpenMarked validates one repository's exact marked publication without
// mutating the catalog root. It is the runtime recovery seam for a caller that
// already holds that repository's repowork lock: it performs no stage cleanup,
// directory inventory, marker removal, store write, or foreign-repository
// access. The caller may commit the returned Publication.State, or use an error
// as the signal to deliberately clear only the named repository before
// staging a replacement.
//
// The expected pack set is copied and must already be in strict name order.
// Pack mismatch is established from the bounded manifest before any member is
// opened. OpenPublishing rechecks the existing marker before and after member
// validation, so this function never replaces or weakens another publisher's
// marker fence.
func OpenMarked(
	ctx context.Context,
	root, repository string,
	expectedPacks []ResolverPack,
) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := reponame.Validate(repository); err != nil {
		return nil, fmt.Errorf("open marked resolver catalog: repository: %w", err)
	}
	expectedPacks = append([]ResolverPack{}, expectedPacks...)
	if err := validateResolverPacks(expectedPacks); err != nil {
		return nil, fmt.Errorf("open marked resolver catalog: expected packs: %w", err)
	}
	if err := validateMarker(root, repository); err != nil {
		return nil, err
	}
	state, err := readMarkedManifestState(root, repository, expectedPacks)
	if err != nil {
		return nil, err
	}
	return OpenPublishing(ctx, root, state)
}

func readMarkedManifestState(
	root, repository string,
	expectedPacks []ResolverPack,
) (State, error) {
	manifestPath := filepath.Join(
		root, resolvercatalogid.ManifestName(repository),
	)
	raw, _, err := readStableRegular(manifestPath, maxManifestBytes)
	if err != nil {
		return State{}, err
	}
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil {
		return State{}, err
	}
	if err := validateManifest(manifest); err != nil {
		return State{}, err
	}
	if manifest.Identity.Repository != repository ||
		!slices.Equal(manifest.Identity.ResolverPacks, expectedPacks) {
		return State{}, ErrInvalidManifest
	}
	return manifest.State(), nil
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
	return nil
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
				if errors.Is(readErr, ErrCatalogIO) {
					return nil, nil, readErr
				}
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
				filepath.Join(root, name), reponame.MaxBytes+1,
			)
			if errors.Is(readErr, ErrCatalogIO) {
				return nil, nil, readErr
			}
			if readErr != nil || len(raw) < 2 || raw[len(raw)-1] != '\n' {
				continue
			}
			repository := string(raw[:len(raw)-1])
			if reponame.Validate(repository) == nil &&
				resolvercatalogid.PublishingName(repository) == name {
				markerRepositories = append(markerRepositories, repository)
			}
		}
	}
	return states, markerRepositories, nil
}

// RemoveRepository removes only artifacts in repository's cryptographic
// resolver-catalog namespace. It is the deletion counterpart to publication;
// foreign namespaces and unrelated files are never selected by decoded data.
func RemoveRepository(ctx context.Context, root, repository string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reponame.Validate(repository); err != nil {
		return err
	}
	cleanup := newReconcileCleanup()
	cleanup.remove(repository)
	return cleanup.apply(ctx, root)
}

type repositoryCleanupPlan struct {
	removeAll    bool
	memberPrefix string
	keepMembers  map[string]struct{}
}

type reconcileCleanup struct {
	byBase map[string]*repositoryCleanupPlan
}

func newReconcileCleanup() *reconcileCleanup {
	return &reconcileCleanup{
		byBase: make(map[string]*repositoryCleanupPlan),
	}
}

func (cleanup *reconcileCleanup) keep(manifest Manifest) {
	repository := manifest.Identity.Repository
	base := resolvercatalogid.ArtifactBase(repository)
	plan := cleanup.byBase[base]
	if plan != nil && plan.removeAll {
		return
	}
	if plan == nil {
		plan = &repositoryCleanupPlan{}
		cleanup.byBase[base] = plan
	}
	plan.memberPrefix = resolvercatalogid.OwnedArtifactPrefix(repository)
	plan.keepMembers = make(map[string]struct{}, len(manifest.Members))
	for _, member := range manifest.Members {
		plan.keepMembers[memberArtifactName(manifest.Identity, member.Name)] = struct{}{}
	}
}

func (cleanup *reconcileCleanup) remove(repository string) {
	cleanup.byBase[resolvercatalogid.ArtifactBase(repository)] =
		&repositoryCleanupPlan{removeAll: true}
}

func (cleanup *reconcileCleanup) apply(ctx context.Context, root string) error {
	if len(cleanup.byBase) == 0 {
		return nil
	}
	rootHandle, rootDirectory, err := openStableDirectoryRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = rootDirectory.Close()
		_ = rootHandle.Close()
	}()
	entries, err := readOpenDirectoryUpTo(rootDirectory, MaxDirectoryEntries)
	if err != nil {
		return err
	}
	baseBytes := len(resolvercatalogid.ArtifactBase(""))
	removed := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if len(name) < baseBytes {
			continue
		}
		base := name[:baseBytes]
		plan := cleanup.byBase[base]
		if plan == nil {
			continue
		}
		selected := false
		if plan.removeAll {
			selected = name == base+".manifest.json" ||
				name == base+".publishing" || strings.HasPrefix(name, base+"-")
		} else if strings.HasPrefix(name, plan.memberPrefix) {
			_, retained := plan.keepMembers[name]
			selected = !retained
		}
		if !selected {
			continue
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("resolver catalog owned path %q is a directory", name)
		}
		if err := rootHandle.Remove(name); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
		removed = true
	}
	if removed {
		return rootDirectory.Sync()
	}
	return nil
}
