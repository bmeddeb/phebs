package callerpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

type DiscoveryReport struct {
	OmittedPublications int        `json:"omitted_publications"`
	OmittedArtifacts    int        `json:"omitted_artifacts"`
	StaleMarkers        int        `json:"stale_markers"`
	Details             []Omission `json:"details,omitempty"`
	TruncatedDetails    int        `json:"truncated_details"`
}

// Discovery retains parsed publication identities for its caller. Bound the
// cumulative manifest + leaf reference set independently of the per-repository
// directory cap so a wide installation cannot multiply memory without limit.
const (
	MaxInstallationPublicationRefs = callerpublicationid.InstallationPublicationRefs
	MaxInstallationCanonicalBytes  = callerpublicationid.InstallationCanonicalBytes
	// A publication transition performs at most one bounded residue batch.
	// Further immutable residue remains non-authoritative and is resumed by a
	// later Observe/startup pass instead of turning a reader release or publish
	// into an installation-wide multi-terabyte manifest scan.
	MaxTransitionCleanupManifests = 32
	MaxTransitionCleanupPairRefs  = 65_536
)

func (report *DiscoveryReport) omit(name, reason string, publication bool) {
	if publication {
		report.OmittedPublications++
	} else {
		report.OmittedArtifacts++
	}
	if len(report.Details) < MaxOmissionDetails {
		report.Details = append(report.Details, Omission{Name: name, Reason: reason})
	} else {
		report.TruncatedDetails++
	}
}

// Discover cold-validates every unambiguous, marker-free complete publication
// under its physical cryptographic owner. Multiple immutable manifests in one
// repository are a crash residue, not authority; offline discovery omits that
// repository rather than guessing which store pointer was current.
func Discover(
	ctx context.Context,
	root string,
) ([]*Publication, DiscoveryReport, error) {
	return discoverWithAdmitted(
		ctx, root, nil, MaxInstallationPublicationRefs,
	)
}

type discoveryAdmissionKey struct {
	repositoryDirectory string
	manifest            string
}

// discoverWithAdmitted performs the same physical inventory as Discover, but
// may reuse an exact publication already cold-admitted earlier in the same
// reconciliation pass. The snapshot still has to be descriptor-current; a
// mismatch falls back to the ordinary content-open path.
func discoverWithAdmitted(
	ctx context.Context,
	root string,
	admitted map[discoveryAdmissionKey]*Publication,
	artifactLimit int,
) ([]*Publication, DiscoveryReport, error) {
	var report DiscoveryReport
	if artifactLimit <= 0 {
		return nil, report, callerleaf.ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, report, err
	}
	if err := ensureRealDirectory(root, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*Publication{}, report, nil
		}
		return nil, report, err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return nil, report, err
	}
	defer rootAuthority.close()
	repositories, err := readRootDirectory(
		rootAuthority.root, callerleaf.MaxRepositoryDirectoryEntries,
	)
	if err != nil {
		return nil, report, err
	}
	publications := make([]*Publication, 0)
	discoveredArtifactRefs := 0
	discoveredCanonicalBytes := int64(0)
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		if !ValidRepositoryDirectoryName(repository.Name()) ||
			!repository.IsDir() || repository.Type()&os.ModeSymlink != 0 {
			continue
		}
		authority, err := openChildDirectoryAuthority(
			rootAuthority, repository.Name(), nil,
		)
		if err != nil {
			return nil, report, err
		}
		entries, err := readRootDirectory(
			authority.root, callerleaf.MaxRepositoryDirectoryEntries,
		)
		if err != nil {
			authority.close()
			return nil, report, err
		}
		manifestNames := make([]string, 0)
		leafNames := make([]string, 0)
		marked := false
		for _, entry := range entries {
			name := entry.Name()
			switch {
			case name == callerpublicationid.PublishingName,
				name == callerpublicationid.DeletingName:
				marked = true
				report.StaleMarkers++
			case callerpublicationid.ValidManifestName(name):
				manifestNames = append(manifestNames, name)
			case ValidLeafArtifactName(name):
				leafNames = append(leafNames, name)
			}
		}
		slices.Sort(manifestNames)
		slices.Sort(leafNames)
		physicalPrefix := repository.Name() + "/"
		if marked || len(manifestNames) != 1 {
			reason := "ambiguous_publication"
			if marked {
				reason = "publication_marker"
			}
			for _, name := range manifestNames {
				report.omit(physicalPrefix+name, reason, true)
			}
			for _, name := range leafNames {
				report.omit(physicalPrefix+name, "unreferenced_artifact", false)
			}
			authority.close()
			continue
		}
		manifestName := manifestNames[0]
		raw, _, readErr := readStableRegularAt(
			authority, manifestName, MaxManifestBytes,
		)
		manifest, decodeErr := decodeManifest(raw)
		if errors.Is(readErr, ErrPublicationIO) {
			authority.close()
			return nil, report, readErr
		}
		if readErr != nil || decodeErr != nil ||
			manifest.State().Manifest != manifestName ||
			callerpublicationid.RepositoryDirectory(
				manifest.Generation.Repository,
			) != repository.Name() {
			report.omit(physicalPrefix+manifestName, "invalid_manifest", true)
			for _, name := range leafNames {
				report.omit(physicalPrefix+name, "unreferenced_artifact", false)
			}
			authority.close()
			continue
		}
		publicationRefs := len(manifest.Pairs) + 1
		if publicationRefs > artifactLimit-discoveredArtifactRefs {
			authority.close()
			return nil, report, callerleaf.ErrLimit
		}
		if manifest.Aggregate.CanonicalBytes >
			MaxInstallationCanonicalBytes-discoveredCanonicalBytes {
			authority.close()
			return nil, report, callerleaf.ErrLimit
		}
		discoveredArtifactRefs += publicationRefs
		discoveredCanonicalBytes += manifest.Aggregate.CanonicalBytes
		authority.close()
		publication := admitted[discoveryAdmissionKey{
			repositoryDirectory: repository.Name(), manifest: manifestName,
		}]
		if publication != nil &&
			(!reflect.DeepEqual(publication.state, manifest.State()) ||
				!publication.Current()) {
			publication = nil
		}
		var openErr error
		if publication == nil {
			publication, openErr = Open(ctx, root, manifest.State())
		}
		if openErr != nil {
			if errors.Is(openErr, ErrPublicationIO) {
				return nil, report, openErr
			}
			report.omit(physicalPrefix+manifestName, "invalid_publication", true)
			for _, name := range leafNames {
				report.omit(physicalPrefix+name, "unreferenced_artifact", false)
			}
			continue
		}
		referenced := make(map[string]struct{}, len(publication.manifest.Pairs))
		for _, pair := range publication.manifest.Pairs {
			referenced[pair.Receipt.Name] = struct{}{}
		}
		for _, name := range leafNames {
			if _, ok := referenced[name]; !ok {
				report.omit(physicalPrefix+name, "unreferenced_artifact", false)
			}
		}
		publications = append(publications, publication)
	}
	slices.SortFunc(publications, func(left, right *Publication) int {
		leftKey := left.state.Generation.Repository + "\x00" +
			left.state.Generation.Digest + "\x00" + left.state.Manifest
		rightKey := right.state.Generation.Repository + "\x00" +
			right.state.Generation.Digest + "\x00" + right.state.Manifest
		return strings.Compare(leftKey, rightKey)
	})
	return publications, report, nil
}

// RemoveManifest removes only one exact package-derived immutable manifest.
// Leaf retirement is separately lease-gated.
func RemoveManifest(root string, state State) error {
	if err := ValidateState(state); err != nil {
		return err
	}
	_, authority, err := openRepositoryAuthority(
		root, state.Generation.Repository, false,
	)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer authority.close()
	if err := authority.root.Remove(state.Manifest); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func cleanupPublicationResidue(
	ctx context.Context,
	root string,
	current State,
	keepLeaves map[string]struct{},
	keepManifests map[string]struct{},
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, authority, err := openRepositoryAuthority(
		root, current.Generation.Repository, false,
	)
	if err != nil {
		return err
	}
	defer authority.close()
	entries, err := readRootDirectory(
		authority.root, callerleaf.MaxRepositoryDirectoryEntries,
	)
	if err != nil {
		return err
	}
	cleanedManifests := 0
	cleanedPairRefs := 0
	for index, entry := range entries {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		name := entry.Name()
		if name == current.Manifest || !callerpublicationid.ValidManifestName(name) {
			continue
		}
		if _, retained := keepManifests[name]; retained {
			continue
		}
		if cleanedManifests == MaxTransitionCleanupManifests {
			break
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("%w: retired manifest %q is special", ErrInvalidManifest, name)
		}
		raw, _, readErr := readStableRegularAt(authority, name, MaxManifestBytes)
		retired, decodeErr := decodeManifest(raw)
		removedPairRefs := 0
		if readErr == nil && decodeErr == nil &&
			retired.Generation.Repository == current.Generation.Repository &&
			retired.State().Manifest == name {
			if len(retired.Pairs) > MaxTransitionCleanupPairRefs-cleanedPairRefs {
				break
			}
			removedPairRefs = len(retired.Pairs)
			for pairIndex, pair := range retired.Pairs {
				if pairIndex%256 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if _, retained := keepLeaves[pair.Receipt.Name]; retained {
					continue
				}
				if err := authority.root.Remove(pair.Receipt.Name); err != nil &&
					!errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		} else if errors.Is(readErr, ErrPublicationIO) {
			return readErr
		}
		if err := authority.root.Remove(name); err != nil {
			return err
		}
		cleanedManifests++
		cleanedPairRefs += removedPairRefs
	}
	return syncRootDirectory(authority.root, ".")
}

// RemoveArtifactRefs removes only exact derived basenames under the manifest's
// cryptographic repository owner. The caller must first establish that no
// active lease retains these leaf artifacts.
func RemoveArtifactRefs(
	ctx context.Context,
	root string,
	manifest Manifest,
	removeManifest bool,
) error {
	return removeArtifactRefsExcept(ctx, root, manifest, removeManifest, nil)
}

func removeArtifactRefsExcept(
	ctx context.Context,
	root string,
	manifest Manifest,
	removeManifest bool,
	keep map[string]struct{},
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	_, authority, err := openRepositoryAuthority(
		root, manifest.Generation.Repository, false,
	)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer authority.close()
	for index, pair := range manifest.Pairs {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if _, retained := keep[pair.Receipt.Name]; retained {
			continue
		}
		if err := authority.root.Remove(pair.Receipt.Name); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if removeManifest {
		if err := authority.root.Remove(manifest.State().Manifest); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncRootDirectory(authority.root, ".")
}

// CleanupStages removes only bounded package-shaped publication stages. It
// ignores leaf stages, immutable artifacts, and foreign entries.
func CleanupStages(ctx context.Context, root string) (int, error) {
	if err := ensureRealDirectory(root, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return 0, err
	}
	defer rootAuthority.close()
	repositories, err := readRootDirectory(
		rootAuthority.root, callerleaf.MaxRepositoryDirectoryEntries,
	)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !ValidRepositoryDirectoryName(repository.Name()) ||
			!repository.IsDir() || repository.Type()&os.ModeSymlink != 0 {
			continue
		}
		authority, err := openChildDirectoryAuthority(
			rootAuthority, repository.Name(), nil,
		)
		if err != nil {
			return removed, err
		}
		entries, err := readRootDirectory(
			authority.root, callerleaf.MaxRepositoryDirectoryEntries,
		)
		if err != nil {
			authority.close()
			return removed, err
		}
		for _, entry := range entries {
			if !callerpublicationid.ValidStageName(entry.Name()) {
				continue
			}
			if err := removeOwnedStageAt(authority, entry.Name(), nil); err != nil {
				authority.close()
				return removed, err
			}
			removed++
		}
		authority.close()
	}
	return removed, nil
}
