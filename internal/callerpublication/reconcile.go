package callerpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

// ReconcileStore is intentionally expressed in complete-publication types so
// this filesystem package does not import store and create a dependency cycle.
// The store or worker may provide a narrow adapter.
type ReconcileStore interface {
	ListCallerPublicationRepositoriesPage(
		context.Context, string, int,
	) ([]string, error)
	CallerPublicationRepositoryEligible(context.Context, string) (bool, error)
	ListCallerPublications(context.Context) ([]State, error)
	CallerPublicationCurrent(context.Context, State) (bool, error)
	ClearCallerPublication(context.Context, string) error
	ForceCallerReplacement(context.Context, string) error
}

const reconcileRepositoryPageSize = 512

type Expected struct {
	Extractors []callerleaf.ExtractorIdentity
}

type ReconcileReport struct {
	StagesRemoved       int
	MarkersRecovered    int
	PublicationsCurrent int
	ReplacementsQueued  int
	PointersCleared     int
	OrphansObserved     int
}

// OpenMarked derives the only admissible manifest name from a canonical marker
// inside repository's cryptographic directory, then performs matching-marker
// cold validation. The marker never selects another repository or path.
func OpenMarked(
	ctx context.Context,
	root, repository string,
) (*Publication, error) {
	manifest, err := readMarkedManifest(ctx, root, repository)
	if err != nil {
		return nil, err
	}
	return OpenPublishing(ctx, root, manifest.State())
}

// readMarkedManifest validates the exact durable cleanup receipt selected by
// a repository's canonical marker without opening any leaf content. Recovery
// uses OpenMarked for full admission; startup reclamation uses this narrower
// form so an interrupted, already-partially-removed ineligible publication is
// still resumable from its manifest.
func readMarkedManifest(
	ctx context.Context,
	root, repository string,
) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, err
		}
		return Manifest{}, publicationIOError(err)
	}
	observed, _, err := readMarkerAt(authority)
	if err != nil {
		authority.close()
		return Manifest{}, err
	}
	if observed.Repository != repository {
		authority.close()
		return Manifest{}, fmt.Errorf("%w: marker repository differs from physical owner", ErrInvalidManifest)
	}
	raw, _, err := readStableRegularAt(authority, observed.Manifest, MaxManifestBytes)
	authority.close()
	if err != nil {
		if errors.Is(err, ErrPublicationIO) {
			return Manifest{}, err
		}
		return Manifest{}, fmt.Errorf("%w: marked manifest: %w", ErrInvalidManifest, err)
	}
	manifest, err := decodeManifest(raw)
	if err != nil || markerForState(manifest.State()) != observed ||
		manifest.Generation.Repository != repository {
		return Manifest{}, fmt.Errorf("%w: marked manifest identity differs", ErrInvalidManifest)
	}
	return manifest, nil
}

// RecoverPublishing is the claimed-worker recovery seam. The caller holds its
// repository work lock; a successful callback is an exact store publish/no-op.
// Any callback failure preserves the marker and all bytes.
func RecoverPublishing(
	ctx context.Context,
	root, repository string,
	expectedExtractors []callerleaf.ExtractorIdentity,
	commit func(context.Context, State) error,
) (*Publication, error) {
	if commit == nil {
		return nil, errors.New("caller publication recovery commit is required")
	}
	publication, err := OpenMarked(ctx, root, repository)
	if err != nil {
		return nil, err
	}
	if !validateExpected(publication.state.Generation, expectedExtractors) {
		return nil, fmt.Errorf("%w: marked caller policy or extractor set is retired", ErrInvalidManifest)
	}
	if err := commit(ctx, publication.state); err != nil {
		return nil, fmt.Errorf("recover caller publication pointer: %w", err)
	}
	if err := ClearPublishing(root, publication.state); err != nil {
		return nil, err
	}
	if _, err := cleanupRepositoryStages(ctx, root, repository); err != nil {
		return nil, err
	}
	if !publication.Current() {
		return nil, fmt.Errorf("%w: recovered caller publication changed", ErrInvalidManifest)
	}
	return publication, nil
}

// AbandonPublishing removes a failed prior-process marker. Eligible callers
// durably queue a forced successor first; startup may instead use it for an
// ineligible repository that cannot accept work. A decoded marked manifest is
// removed unless it is the explicitly retained current state.
func AbandonPublishing(
	ctx context.Context,
	root, repository string,
	keep *State,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if keep != nil {
		if err := ValidateState(*keep); err != nil ||
			keep.Generation.Repository != repository {
			return errors.New("caller publication abandon keep state is invalid")
		}
	}
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	observed, _, markerErr := readMarkerAt(authority)
	if errors.Is(markerErr, ErrPublicationIO) {
		authority.close()
		return markerErr
	}
	if err := clearPublishingAt(authority, marker{}, true); err != nil {
		authority.close()
		return err
	}
	if markerErr == nil && callerpublicationid.ValidManifestName(observed.Manifest) &&
		(keep == nil || keep.Manifest != observed.Manifest) {
		if err := authority.root.Remove(observed.Manifest); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			authority.close()
			return err
		}
	}
	entries, inventoryErr := readRootDirectory(
		authority.root, callerleaf.MaxRepositoryDirectoryEntries,
	)
	if inventoryErr != nil {
		authority.close()
		return inventoryErr
	}
	for _, entry := range entries {
		name := entry.Name()
		if !callerpublicationid.ValidManifestName(name) ||
			keep != nil && keep.Manifest == name {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			authority.close()
			return fmt.Errorf("%w: abandoned manifest %q is special", ErrInvalidManifest, name)
		}
		if err := authority.root.Remove(name); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			authority.close()
			return err
		}
	}
	if err := syncRootDirectory(authority.root, "."); err != nil {
		authority.close()
		return err
	}
	authority.close()
	_, err = cleanupRepositoryStages(ctx, root, repository)
	return err
}

// Reconcile covers startup and may also be called under a higher-level worker
// serialization boundary. Unmarked pointerless bytes are never promoted.
func Reconcile(
	ctx context.Context,
	root string,
	state ReconcileStore,
	expected Expected,
	registry *Registry,
) (ReconcileReport, error) {
	var report ReconcileReport
	if state == nil || expected.Extractors == nil {
		return report, errors.New("caller publication reconciliation is not configured")
	}
	if registry == nil {
		registry = NewRegistry(root)
	}
	if err := reconcileDeletionMarkers(
		ctx, root, state.CallerPublicationRepositoryEligible,
	); err != nil {
		return report, err
	}
	pointers, err := state.ListCallerPublications(ctx)
	if err != nil {
		return report, err
	}
	pointerByRepository := make(map[string]State, len(pointers))
	queuedRepositories := make(map[string]struct{})
	forceReplacement := func(repository string) error {
		if _, queued := queuedRepositories[repository]; queued {
			return nil
		}
		if err := state.ForceCallerReplacement(ctx, repository); err != nil {
			return err
		}
		queuedRepositories[repository] = struct{}{}
		report.ReplacementsQueued++
		return nil
	}
	admitted := make(map[discoveryAdmissionKey]*Publication, len(pointers))
	remember := func(publication *Publication) {
		if publication == nil {
			return
		}
		admitted[discoveryAdmissionKey{
			repositoryDirectory: callerpublicationid.RepositoryDirectory(
				publication.state.Generation.Repository,
			),
			manifest: publication.state.Manifest,
		}] = publication
	}
	for _, pointer := range pointers {
		if err := ValidateState(pointer); err != nil {
			return report, err
		}
		repository := pointer.Generation.Repository
		pointerByRepository[repository] = pointer
		current, err := state.CallerPublicationCurrent(ctx, pointer)
		if err != nil {
			return report, err
		}
		current = current && validateExpected(pointer.Generation, expected.Extractors)
		marked, err := Publishing(root, repository)
		if err != nil {
			return report, err
		}
		if marked {
			publication, openErr := OpenMarked(ctx, root, repository)
			if openErr == nil && current &&
				reflect.DeepEqual(publication.state, pointer) {
				// Store commit completed before the crash. Startup may clear the
				// matching marker without invoking the job-lease-fenced writer.
				if err := ClearPublishing(root, pointer); err != nil {
					return report, err
				}
				if _, err := cleanupRepositoryStages(ctx, root, repository); err != nil {
					return report, err
				}
				if err := registry.Observe(ctx, publication); err != nil {
					return report, err
				}
				remember(publication)
				report.MarkersRecovered++
				report.PublicationsCurrent++
				continue
			}
			if errors.Is(openErr, ErrPublicationIO) {
				return report, openErr
			}
			if openErr == nil {
				// A valid manifest-before-store (or different replacement) must be
				// left marked for a claimed worker whose job lease can publish it.
				if err := forceReplacement(repository); err != nil {
					return report, err
				}
				continue
			}
			// Preserve a still-current prior pointer if its immutable bytes remain
			// valid beneath the replacement marker. The forced successor must be
			// durable before discarding failed marked authority.
			var (
				prior    *Publication
				priorErr error
			)
			if current {
				prior, priorErr = openWhileMarked(ctx, root, pointer)
				if errors.Is(priorErr, ErrPublicationIO) {
					return report, priorErr
				}
			}
			if err := forceReplacement(repository); err != nil {
				return report, err
			}
			if prior != nil {
				if err := AbandonPublishing(ctx, root, repository, &pointer); err != nil {
					return report, err
				}
				if err := registry.Observe(ctx, prior); err != nil {
					return report, err
				}
				remember(prior)
				report.PublicationsCurrent++
				continue
			}
			if err := state.ClearCallerPublication(ctx, repository); err != nil {
				return report, err
			}
			delete(pointerByRepository, repository)
			report.PointersCleared++
			if err := registry.Retire(ctx, repository); err != nil {
				return report, err
			}
			if err := AbandonPublishing(ctx, root, repository, nil); err != nil {
				return report, err
			}
			continue
		}
		if current {
			publication, openErr := Open(ctx, root, pointer)
			if openErr == nil {
				if err := registry.Observe(ctx, publication); err != nil {
					return report, err
				}
				remember(publication)
				report.PublicationsCurrent++
				continue
			}
			if errors.Is(openErr, ErrPublicationIO) {
				return report, openErr
			}
		}
		if err := forceReplacement(repository); err != nil {
			return report, err
		}
		if err := state.ClearCallerPublication(ctx, repository); err != nil {
			return report, err
		}
		delete(pointerByRepository, repository)
		report.PointersCleared++
		if err := registry.Retire(ctx, repository); err != nil {
			return report, err
		}
	}
	retainedMarkers := make(map[string]struct{})
	after := ""
	for {
		repositories, err := state.ListCallerPublicationRepositoriesPage(
			ctx, after, reconcileRepositoryPageSize,
		)
		if err != nil {
			return report, err
		}
		for _, repository := range repositories {
			if _, exists := pointerByRepository[repository]; exists {
				continue
			}
			marked, err := Publishing(root, repository)
			if err != nil {
				return report, err
			}
			if !marked {
				continue
			}
			_, openErr := OpenMarked(ctx, root, repository)
			if errors.Is(openErr, ErrPublicationIO) {
				return report, openErr
			}
			if err := forceReplacement(repository); err != nil {
				return report, err
			}
			if openErr == nil {
				// Pointerless manifest-before-store is valid derived state. Startup
				// never promotes it; retain marker/bytes for the claimed worker.
				retainedMarkers[repository] = struct{}{}
				continue
			}
			if err := AbandonPublishing(ctx, root, repository, nil); err != nil {
				return report, err
			}
		}
		if len(repositories) < reconcileRepositoryPageSize {
			break
		}
		next := repositories[len(repositories)-1]
		if next <= after {
			return report, errors.New("caller publication repository page did not advance")
		}
		after = next
	}

	// The paged live-repository pass above identifies even a malformed marker
	// for an eligible repository without retaining the installation's entire
	// repository set. Any remaining valid marker is package-owned residue for
	// an absent, unindexed, or deleting repository. When its complete manifest
	// exists, reclaim leaves first while that receipt remains restart-resumable;
	// marker-before-manifest residue has no complete leaf authority to follow.
	markedRepositories, err := discoverAndRepairMarkedRepositories(ctx, root)
	if err != nil {
		return report, err
	}
	for _, repository := range markedRepositories {
		if _, retained := retainedMarkers[repository]; retained {
			continue
		}
		if _, exists := pointerByRepository[repository]; exists {
			continue
		}
		eligible, err := state.CallerPublicationRepositoryEligible(ctx, repository)
		if err != nil {
			return report, err
		}
		if eligible {
			if err := forceReplacement(repository); err != nil {
				return report, err
			}
			continue
		}
		manifest, err := readMarkedManifest(ctx, root, repository)
		if err != nil {
			if errors.Is(err, ErrPublicationIO) {
				return report, err
			}
			// A marker linked before the manifest rename is a normal crash
			// boundary. This repository cannot accept replacement work, so discard
			// only package-owned marker/manifests and let ordinary residue cleanup
			// handle any independently durable leaf bytes.
			if err := AbandonPublishing(ctx, root, repository, nil); err != nil {
				return report, err
			}
			continue
		}
		// Remove leaves and their exact cleanup receipt before clearing the
		// transition marker. A crash at any point therefore remains resumable:
		// either the manifest still names the remaining leaves, or the surviving
		// marker is repaired as marker-before-manifest on the next startup.
		if err := RemoveArtifactRefs(ctx, root, manifest, true); err != nil {
			return report, err
		}
		if err := ClearPublishing(root, manifest.State()); err != nil {
			return report, err
		}
	}

	// Valid unmarked pointerless bytes include restored publications. They are
	// useful immutable reuse inputs only for an indexed, non-deleting repository,
	// and only a new claimed worker may establish authority in this installation.
	// Exact residue for every other repository is rebuildable and reclaimed.
	publications, _, err := discoverWithAdmitted(
		ctx, root, admitted, MaxInstallationPublicationRefs,
	)
	if err != nil {
		return report, err
	}
	for _, publication := range publications {
		repository := publication.state.Generation.Repository
		pointer, exists := pointerByRepository[repository]
		if exists && reflect.DeepEqual(pointer, publication.state) {
			continue
		}
		report.OrphansObserved++
		eligible, err := state.CallerPublicationRepositoryEligible(ctx, repository)
		if err != nil {
			return report, err
		}
		if !eligible {
			// Restored or crash-residue bytes do not make an unindexed,
			// deleting, or absent repository eligible for caller work. Reclaim
			// only the exact validated manifest and leaf basenames discovered
			// above; unrelated repository-directory entries remain untouched.
			if err := RemoveArtifactRefs(
				ctx, root, publication.manifest, true,
			); err != nil {
				return report, err
			}
			continue
		}
		if err := forceReplacement(repository); err != nil {
			return report, err
		}
	}
	removed, err := CleanupStages(ctx, root)
	if err != nil {
		return report, err
	}
	report.StagesRemoved = removed
	return report, nil
}

func discoverAndRepairMarkedRepositories(
	ctx context.Context,
	root string,
) ([]string, error) {
	if err := ensureRealDirectory(root, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return nil, err
	}
	defer rootAuthority.close()
	entries, err := readRootDirectory(
		rootAuthority.root, callerpublicationid.InstallationPublicationRepositories,
	)
	if err != nil {
		return nil, err
	}
	repositories := make([]string, 0)
	for index, entry := range entries {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if !ValidRepositoryDirectoryName(entry.Name()) || !entry.IsDir() ||
			entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		authority, err := openChildDirectoryAuthority(rootAuthority, entry.Name(), nil)
		if err != nil {
			return nil, err
		}
		marker, _, markerErr := readMarkerAt(authority)
		if errors.Is(markerErr, os.ErrNotExist) {
			authority.close()
			continue
		}
		if errors.Is(markerErr, ErrPublicationIO) {
			authority.close()
			return nil, markerErr
		}
		if markerErr != nil || callerpublicationid.RepositoryDirectory(
			marker.Repository,
		) != entry.Name() {
			// A corrupt marker cannot select a repository or manifest. Remove only
			// its canonical descriptor-owned path. Valid unmarked manifests are
			// evaluated later against store eligibility; operational I/O remains a
			// hard failure rather than being mistaken for deterministic damage.
			removeErr := removeDerivedMarkerAt(
				authority, callerpublicationid.PublishingName,
			)
			authority.close()
			if removeErr != nil {
				return nil, removeErr
			}
			continue
		}
		authority.close()
		repositories = append(repositories, marker.Repository)
	}
	slices.Sort(repositories)
	return repositories, nil
}

func openWhileMarked(
	ctx context.Context,
	root string,
	expected State,
) (*Publication, error) {
	// Snapshot the marker identity, validate the prior immutable manifest with
	// its own exact marker checks temporarily relaxed, then require the same
	// marker still exists. This is reconciliation-only and never exposes the
	// result until the failed marker is removed.
	_, authority, err := openRepositoryAuthority(
		root, expected.Generation.Repository, false,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, publicationIOError(err)
	}
	before, markerInfo, err := readStableRegularAt(
		authority, callerpublicationid.PublishingName, maxMarkerBytes,
	)
	if err != nil {
		authority.close()
		return nil, err
	}
	authority.close()
	publication, err := openIgnoringMarker(ctx, root, expected)
	if err != nil {
		return nil, err
	}
	_, authority, err = openRepositoryAuthority(
		root, expected.Generation.Repository, false,
	)
	if err != nil {
		return nil, err
	}
	after, currentInfo, err := readStableRegularAt(
		authority, callerpublicationid.PublishingName, maxMarkerBytes,
	)
	authority.close()
	if err != nil || !slices.Equal(before, after) || !sameFile(markerInfo, currentInfo) {
		return nil, fmt.Errorf("%w: marker changed while validating prior pointer", ErrInvalidManifest)
	}
	return publication, nil
}

func openIgnoringMarker(
	ctx context.Context,
	root string,
	expected State,
) (*Publication, error) {
	if err := ValidateState(expected); err != nil {
		return nil, err
	}
	_, authority, err := openRepositoryAuthority(
		root, expected.Generation.Repository, false,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, publicationIOError(err)
	}
	raw, manifestInfo, err := readStableRegularAt(
		authority, expected.Manifest, MaxManifestBytes,
	)
	if err != nil {
		authority.close()
		return nil, err
	}
	manifest, err := decodeManifest(raw)
	if err != nil || !reflect.DeepEqual(manifest.State(), expected) {
		authority.close()
		return nil, ErrInvalidManifest
	}
	leaves := make([]*callerleaf.Publication, len(manifest.Pairs))
	for index, pair := range manifest.Pairs {
		leaf, err := callerleaf.Open(
			ctx, root, manifest.Generation, pair.Pair, pair.Receipt, nil,
		)
		if err != nil {
			authority.close()
			return nil, classifyLeafOpenError(err)
		}
		leaves[index] = leaf
	}
	publication := &Publication{
		root: root, manifest: manifest, state: expected,
		directoryInfo: authority.info, manifestInfo: manifestInfo, leaves: leaves,
	}
	authority.close()
	return publication, nil
}

func cleanupRepositoryStages(
	ctx context.Context,
	root, repository string,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer authority.close()
	entries, err := readRootDirectory(
		authority.root, callerleaf.MaxRepositoryDirectoryEntries,
	)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !callerpublicationid.ValidStageName(entry.Name()) {
			continue
		}
		if err := removeOwnedStageAt(authority, entry.Name(), nil); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
