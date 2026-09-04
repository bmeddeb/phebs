package callerpublication

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/reponame"
)

const (
	stageManifestName = "manifest.json"
	stageMarkerName   = "marker.json"
)

type directoryAuthority struct {
	root *os.Root
	info os.FileInfo
	once sync.Once
}

func (authority *directoryAuthority) close() {
	if authority != nil {
		authority.once.Do(func() { _ = authority.root.Close() })
	}
}

// Prepared owns one bounded, synced complete-manifest stage and the exact leaf
// publications validated for it. Once installation begins, any failure leaves
// the marker and bytes for reconciliation rather than guessing at rollback.
type Prepared struct {
	root       string
	repository string
	manifest   Manifest
	state      State
	authority  *directoryAuthority
	stageName  string
	stageInfo  os.FileInfo
	leaves     []*callerleaf.Publication
	started    bool
}

// Publication is a descriptor-stable cold admission. Current performs only
// marker, directory, manifest, and leaf file-identity checks; it never opens
// or hashes leaf content.
type Publication struct {
	root          string
	manifest      Manifest
	state         State
	directoryInfo os.FileInfo
	manifestInfo  os.FileInfo
	leaves        []*callerleaf.Publication
}

type ArtifactRef struct {
	RepositoryDirectory string `json:"repository_directory"`
	Name                string `json:"name"`
}

func (reference ArtifactRef) RelativePath() string {
	if !ValidRepositoryDirectoryName(reference.RepositoryDirectory) ||
		filepath.Base(reference.Name) != reference.Name {
		return ""
	}
	return path.Join(reference.RepositoryDirectory, reference.Name)
}

func ArtifactRefs(manifest Manifest) []ArtifactRef {
	if ValidateManifest(manifest) != nil {
		return nil
	}
	parent := callerpublicationid.RepositoryDirectory(manifest.Generation.Repository)
	result := make([]ArtifactRef, 0, len(manifest.Pairs)+1)
	result = append(result, ArtifactRef{
		RepositoryDirectory: parent, Name: manifest.State().Manifest,
	})
	for _, pair := range manifest.Pairs {
		result = append(result, ArtifactRef{
			RepositoryDirectory: parent, Name: pair.Receipt.Name,
		})
	}
	slices.SortFunc(result, func(left, right ArtifactRef) int {
		return strings.Compare(left.RelativePath(), right.RelativePath())
	})
	return result
}

func (publication *Publication) ArtifactRefs() []ArtifactRef {
	if publication == nil {
		return nil
	}
	return ArtifactRefs(publication.manifest)
}

func (publication *Publication) Manifest() Manifest {
	if publication == nil {
		return Manifest{}
	}
	return cloneManifest(publication.manifest)
}

func (publication *Publication) State() State {
	if publication == nil {
		return State{}
	}
	return cloneState(publication.state)
}

// Prepare performs the first descriptor-stable cold admission of every exact
// artifact and creates the complete-manifest stage. It hashes each leaf once.
func Prepare(ctx context.Context, root string, manifest Manifest) (*Prepared, error) {
	return PrepareWithValidated(ctx, root, manifest, nil)
}

// PrepareWithValidated reuses exact leaf publications already cold-opened by
// the caller worker. Missing or stale entries are opened once here.
func PrepareWithValidated(
	ctx context.Context,
	root string,
	manifest Manifest,
	validated map[string]*callerleaf.Publication,
) (*Prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	directory, authority, err := openRepositoryAuthority(
		root, manifest.Generation.Repository, true,
	)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Prepared, error) {
		authority.close()
		return nil, err
	}
	entries, err := readRootDirectory(authority.root, callerleaf.MaxRepositoryDirectoryEntries)
	if err != nil {
		return fail(err)
	}
	// Stage + marker + final manifest are simultaneously present at the
	// rename-before-stage-removal crash boundary.
	if len(entries) > callerleaf.MaxRepositoryDirectoryEntries-3 {
		return fail(callerleaf.ErrCapacity)
	}
	if _, err := authority.root.Lstat(callerpublicationid.PublishingName); err == nil {
		return fail(ErrPublishing)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	leaves := make([]*callerleaf.Publication, len(manifest.Pairs))
	for index, pair := range manifest.Pairs {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		publication := validated[pair.Pair.Digest]
		if publication == nil || !publication.Matches(
			root, manifest.Generation.Repository, pair.Receipt,
		) || !publication.Current() {
			if testArtifactColdOpen != nil {
				testArtifactColdOpen(pair.Receipt.Name)
			}
			publication, err = callerleaf.Open(
				ctx, root, manifest.Generation, pair.Pair, pair.Receipt, nil,
			)
			if err != nil {
				return fail(classifyLeafOpenError(err))
			}
		}
		leaves[index] = publication
	}
	for _, publication := range leaves {
		if !publication.Current() {
			return fail(fmt.Errorf("%w: leaf changed during complete admission", ErrInvalidManifest))
		}
	}
	if current, err := os.Lstat(directory); err != nil || !sameDirectory(authority.info, current) {
		return fail(fmt.Errorf("%w: repository directory changed", ErrInvalidManifest))
	}
	stageName, stageInfo, err := createStage(authority)
	if err != nil {
		return fail(err)
	}
	prepared := &Prepared{
		root: root, repository: manifest.Generation.Repository,
		manifest: manifest, state: manifest.State(), authority: authority,
		stageName: stageName, stageInfo: stageInfo, leaves: leaves,
	}
	if err := prepared.writeStage(); err != nil {
		_ = prepared.discardStage()
		authority.close()
		return nil, err
	}
	return prepared, nil
}

func (prepared *Prepared) writeStage() error {
	stage, err := openChildDirectoryAuthority(
		prepared.authority, prepared.stageName, prepared.stageInfo,
	)
	if err != nil {
		return err
	}
	defer stage.close()
	manifestRaw, err := json.Marshal(prepared.manifest)
	if err != nil {
		return err
	}
	manifestRaw = append(manifestRaw, '\n')
	markerRaw, err := json.Marshal(markerForState(prepared.state))
	if err != nil {
		return err
	}
	markerRaw = append(markerRaw, '\n')
	if len(manifestRaw) > MaxManifestBytes || len(markerRaw) > maxMarkerBytes {
		return callerleaf.ErrLimit
	}
	for name, raw := range map[string][]byte{
		stageManifestName: manifestRaw, stageMarkerName: markerRaw,
	} {
		file, openErr := stage.root.OpenFile(
			name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
		)
		if openErr != nil {
			return openErr
		}
		written, writeErr := file.Write(raw)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || written != len(raw) || syncErr != nil || closeErr != nil {
			return errors.Join(writeErr, syncErr, closeErr)
		}
	}
	return syncRootDirectory(stage.root, ".")
}

// Publish installs the marker and immutable manifest, commits the matching
// store pointer, then clears only that exact marker. Commit failure preserves
// the marker for runtime/startup recovery.
func (prepared *Prepared) Publish(
	ctx context.Context,
	commit func(context.Context, State) error,
) (*Publication, error) {
	if commit == nil {
		return nil, errors.New("caller publication store commit is required")
	}
	publication, err := prepared.install(ctx)
	if err != nil {
		prepared.authority.close()
		prepared.authority = nil
		return nil, err
	}
	if err := commit(ctx, prepared.state); err != nil {
		prepared.authority.close()
		prepared.authority = nil
		return nil, fmt.Errorf("commit caller publication pointer: %w", err)
	}
	if testAfterStoreCommit != nil {
		testAfterStoreCommit(prepared.state)
	}
	if !publication.current(true) {
		prepared.authority.close()
		prepared.authority = nil
		return nil, fmt.Errorf("%w: installed publication changed before marker clear", ErrInvalidManifest)
	}
	if err := clearPublishingAt(prepared.authority, markerForState(prepared.state), false); err != nil {
		prepared.authority.close()
		prepared.authority = nil
		return nil, fmt.Errorf("clear caller publication marker: %w", err)
	}
	if err := prepared.discardStage(); err != nil {
		prepared.authority.close()
		prepared.authority = nil
		return nil, fmt.Errorf("remove caller publication stage: %w", err)
	}
	prepared.authority.close()
	prepared.authority = nil
	if !publication.Current() {
		return nil, fmt.Errorf("%w: installed publication is not current", ErrInvalidManifest)
	}
	return publication, nil
}

func (prepared *Prepared) install(ctx context.Context) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.authority == nil || prepared.started ||
		ValidateManifest(prepared.manifest) != nil {
		return nil, errors.New("invalid or reused caller publication stage")
	}
	prepared.started = true
	if !prepared.repositoryCurrent() {
		return nil, fmt.Errorf("%w: repository directory authority changed", ErrInvalidManifest)
	}
	if err := prepared.authority.root.Link(
		prepared.stageName+"/"+stageMarkerName,
		callerpublicationid.PublishingName,
	); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrPublishing
		}
		return nil, err
	}
	if err := syncRootDirectory(prepared.authority.root, "."); err != nil {
		return nil, err
	}
	if testAfterMarkerInstall != nil {
		if err := testAfterMarkerInstall(prepared.state); err != nil {
			return nil, err
		}
	}
	for _, leaf := range prepared.leaves {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !leaf.Current() {
			return nil, fmt.Errorf("%w: leaf changed before manifest install", ErrInvalidManifest)
		}
	}
	manifestInfo, err := prepared.installManifest()
	if err != nil {
		return nil, err
	}
	if testAfterManifestInstall != nil {
		if err := testAfterManifestInstall(prepared.state); err != nil {
			return nil, err
		}
	}
	return &Publication{
		root: prepared.root, manifest: prepared.manifest, state: prepared.state,
		directoryInfo: prepared.authority.info, manifestInfo: manifestInfo,
		leaves: slices.Clone(prepared.leaves),
	}, nil
}

func (prepared *Prepared) installManifest() (os.FileInfo, error) {
	name := prepared.state.Manifest
	if _, err := prepared.authority.root.Lstat(name); err == nil {
		raw, info, readErr := readStableRegularAt(
			prepared.authority, name, MaxManifestBytes,
		)
		if readErr != nil || !bytes.Equal(raw, canonicalManifest(prepared.manifest)) {
			return nil, fmt.Errorf("%w: immutable manifest target differs", ErrInvalidManifest)
		}
		if err := prepared.removeStageFile(stageManifestName); err != nil {
			return nil, err
		}
		return info, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := prepared.authority.root.Rename(
		prepared.stageName+"/"+stageManifestName, name,
	); err != nil {
		return nil, fmt.Errorf("install caller publication manifest: %w", err)
	}
	if err := syncRootDirectory(prepared.authority.root, "."); err != nil {
		return nil, err
	}
	raw, info, err := readStableRegularAt(prepared.authority, name, MaxManifestBytes)
	if err != nil || !bytes.Equal(raw, canonicalManifest(prepared.manifest)) {
		return nil, fmt.Errorf("%w: installed manifest differs", ErrInvalidManifest)
	}
	return info, nil
}

func (prepared *Prepared) repositoryCurrent() bool {
	if prepared == nil || prepared.authority == nil {
		return false
	}
	rootAuthority, err := openStableDirectoryRoot(prepared.root)
	if err != nil {
		return false
	}
	defer rootAuthority.close()
	current, err := rootAuthority.root.Lstat(
		callerpublicationid.RepositoryDirectory(prepared.repository),
	)
	return err == nil && sameDirectory(prepared.authority.info, current)
}

func (prepared *Prepared) removeStageFile(name string) error {
	stage, err := openChildDirectoryAuthority(
		prepared.authority, prepared.stageName, prepared.stageInfo,
	)
	if err != nil {
		return err
	}
	defer stage.close()
	if err := stage.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(stage.root, ".")
}

func (prepared *Prepared) Discard() error {
	if prepared == nil || prepared.authority == nil {
		return nil
	}
	if prepared.started {
		return nil
	}
	err := prepared.discardStage()
	prepared.authority.close()
	prepared.authority = nil
	return err
}

func (prepared *Prepared) discardStage() error {
	if prepared == nil || prepared.authority == nil || prepared.stageName == "" {
		return nil
	}
	err := removeOwnedStageAt(
		prepared.authority, prepared.stageName, prepared.stageInfo,
	)
	if err == nil {
		prepared.stageName = ""
	}
	return err
}

// Open performs marker-free descriptor-stable cold validation of the exact
// complete state and every referenced leaf artifact.
func Open(ctx context.Context, root string, expected State) (*Publication, error) {
	return open(ctx, root, expected, false)
}

// OpenPublishing validates an exact manifest made durable before its store
// pointer commit. The matching marker is checked before and after all content.
func OpenPublishing(ctx context.Context, root string, expected State) (*Publication, error) {
	return open(ctx, root, expected, true)
}

func open(
	ctx context.Context,
	root string,
	expected State,
	allowPublishing bool,
) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateState(expected); err != nil {
		return nil, err
	}
	directory, authority, err := openRepositoryAuthority(
		root, expected.Generation.Repository, false,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, publicationIOError(err)
	}
	defer authority.close()
	wantedMarker := markerForState(expected)
	if err := checkPublishingContextAt(ctx, authority, wantedMarker, allowPublishing); err != nil {
		return nil, err
	}
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return nil, err
	}
	raw, manifestInfo, err := readStableRegularAt(
		authority, expected.Manifest, MaxManifestBytes,
	)
	if err != nil {
		if errors.Is(err, ErrPublicationIO) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: manifest: %w", ErrInvalidManifest, err)
	}
	manifest, err := decodeManifest(raw)
	if err != nil || !reflect.DeepEqual(manifest.State(), expected) {
		return nil, fmt.Errorf("%w: manifest differs from expected state", ErrInvalidManifest)
	}
	leaves := make([]*callerleaf.Publication, len(manifest.Pairs))
	for index, pair := range manifest.Pairs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if testArtifactColdOpen != nil {
			testArtifactColdOpen(pair.Receipt.Name)
		}
		leaf, openErr := callerleaf.Open(
			ctx, root, manifest.Generation, pair.Pair, pair.Receipt, nil,
		)
		if openErr != nil {
			return nil, classifyLeafOpenError(openErr)
		}
		leaves[index] = leaf
	}
	if err := checkPublishingContextAt(ctx, authority, wantedMarker, allowPublishing); err != nil {
		return nil, err
	}
	currentManifest, err := authority.root.Lstat(expected.Manifest)
	if err != nil {
		if operationalErr := currentOperationalError(err); operationalErr != nil {
			return nil, operationalErr
		}
		return nil, fmt.Errorf("%w: manifest changed during admission", ErrInvalidManifest)
	}
	if !sameFile(manifestInfo, currentManifest) {
		return nil, fmt.Errorf("%w: manifest changed during admission", ErrInvalidManifest)
	}
	leavesCurrent, err := leavesCurrentResultAt(
		root, manifest, authority, leaves,
	)
	if err != nil {
		return nil, err
	}
	if !leavesCurrent {
		return nil, fmt.Errorf("%w: leaf changed during admission", ErrInvalidManifest)
	}
	currentDirectory, err := os.Lstat(directory)
	if err != nil {
		if operationalErr := currentOperationalError(err); operationalErr != nil {
			return nil, operationalErr
		}
		return nil, fmt.Errorf("%w: repository changed during admission", ErrInvalidManifest)
	}
	if !sameDirectory(authority.info, currentDirectory) {
		return nil, fmt.Errorf("%w: repository changed during admission", ErrInvalidManifest)
	}
	return &Publication{
		root: root, manifest: manifest, state: expected,
		directoryInfo: authority.info, manifestInfo: manifestInfo, leaves: leaves,
	}, nil
}

func (publication *Publication) Current() bool {
	current, _ := publication.CurrentResult()
	return current
}

// CurrentResult performs the same descriptor-stable identity fence as Current,
// but preserves operational filesystem failures for product-read boundaries.
// Missing or replaced derived authority is an ordinary transition and returns
// (false, nil); permission, device, and other path I/O failures remain errors.
func (publication *Publication) CurrentResult() (bool, error) {
	return publication.currentResult(context.Background(), false)
}

// CurrentResultContext is the exact-read form of CurrentResult. It charges the
// bounded publication-marker control attempt while retaining metadata-only
// identity checks for the manifest and leaf artifacts.
func (publication *Publication) CurrentResultContext(ctx context.Context) (bool, error) {
	return publication.currentResult(ctx, false)
}

func (publication *Publication) current(allowPublishing bool) bool {
	current, _ := publication.currentResult(context.Background(), allowPublishing)
	return current
}

func (publication *Publication) currentResult(
	ctx context.Context,
	allowPublishing bool,
) (bool, error) {
	if publication == nil || ValidateState(publication.state) != nil {
		return false, nil
	}
	_, authority, err := openRepositoryAuthority(
		publication.root, publication.state.Generation.Repository, false,
	)
	if err != nil {
		return false, currentOperationalError(err)
	}
	defer authority.close()
	if !sameDirectory(publication.directoryInfo, authority.info) {
		return false, nil
	}
	if err := checkPublishingContextAt(
		ctx, authority, markerForState(publication.state), allowPublishing,
	); err != nil {
		return false, currentOperationalError(err)
	}
	info, err := authority.root.Lstat(publication.state.Manifest)
	if err != nil {
		return false, currentOperationalError(err)
	}
	if !sameFile(publication.manifestInfo, info) {
		return false, nil
	}
	repositoryDirectory := filepath.Join(
		publication.root,
		callerpublicationid.RepositoryDirectory(
			publication.state.Generation.Repository,
		),
	)
	if testAfterCurrentRepositoryOpen != nil {
		testAfterCurrentRepositoryOpen(repositoryDirectory)
	}
	leavesCurrent, err := leavesCurrentResultAt(
		publication.root, publication.manifest, authority, publication.leaves,
	)
	if err != nil || !leavesCurrent {
		return false, err
	}
	if testAfterCurrentLeafChecks != nil {
		testAfterCurrentLeafChecks(repositoryDirectory)
	}
	currentDirectory, err := os.Lstat(repositoryDirectory)
	if err != nil {
		return false, currentOperationalError(err)
	}
	if !sameDirectory(authority.info, currentDirectory) {
		return false, nil
	}
	return true, nil
}

func leavesCurrentResultAt(
	root string,
	manifest Manifest,
	authority *directoryAuthority,
	leaves []*callerleaf.Publication,
) (bool, error) {
	if authority == nil || authority.root == nil || len(leaves) != len(manifest.Pairs) {
		return false, nil
	}
	repositoryDirectory := filepath.Join(
		root,
		callerpublicationid.RepositoryDirectory(manifest.Generation.Repository),
	)
	for index, pair := range manifest.Pairs {
		info, err := authority.root.Lstat(pair.Receipt.Name)
		if err != nil {
			return false, currentOperationalError(err)
		}
		if !leaves[index].CurrentAtRepository(
			repositoryDirectory, pair.Receipt, authority.info, info,
		) {
			return false, nil
		}
	}
	return true, nil
}

// currentOperationalError separates an ordinary authority transition from an
// inability to inspect authority. Stable-shape helpers use plain deterministic
// errors; actual path operations retain *os.PathError (or ErrPublicationIO).
func currentOperationalError(err error) error {
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if errors.Is(err, ErrPublicationIO) {
		return err
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return publicationIOError(err)
	}
	return nil
}

func Publishing(root, repository string) (bool, error) {
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer authority.close()
	_, err = authority.root.Lstat(callerpublicationid.PublishingName)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, publicationIOError(err)
	}
	// Any existing canonical marker path fences admission. OpenMarked performs
	// the stricter regular-file and payload validation before recovery.
	return true, nil
}

// ClearPublishing removes only a marker whose canonical payload matches the
// supplied exact state. A missing marker is idempotent.
func ClearPublishing(root string, expected State) error {
	if err := ValidateState(expected); err != nil {
		return err
	}
	_, authority, err := openRepositoryAuthority(
		root, expected.Generation.Repository, false,
	)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer authority.close()
	return clearPublishingAt(authority, markerForState(expected), false)
}

func checkPublishingAt(
	authority *directoryAuthority,
	wanted marker,
	required bool,
) error {
	return checkPublishingContextAt(context.Background(), authority, wanted, required)
}

func checkPublishingContextAt(
	ctx context.Context,
	authority *directoryAuthority,
	wanted marker,
	required bool,
) error {
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return err
	}
	observed, _, err := readMarkerAt(authority)
	if errors.Is(err, os.ErrNotExist) {
		if required {
			return ErrPublishing
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !required {
		return ErrPublishing
	}
	if observed != wanted {
		return fmt.Errorf("%w: publication marker differs", ErrInvalidManifest)
	}
	return nil
}

func clearPublishingAt(
	authority *directoryAuthority,
	wanted marker,
	allowMismatch bool,
) error {
	observed, _, err := readMarkerAt(authority)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		if !allowMismatch {
			return err
		}
	} else if observed != wanted && !allowMismatch {
		return fmt.Errorf("%w: publication marker differs", ErrInvalidManifest)
	}
	return removeDerivedMarkerAt(authority, callerpublicationid.PublishingName)
}

// removeDerivedMarkerAt removes one package-owned transition marker beneath an
// already pinned repository directory and durably records the removal. Exact
// completion paths call it after payload validation; startup may call it after
// a descriptor-stable read classifies the bytes as deterministically invalid.
// Operational read or directory errors remain failures at those boundaries.
func removeDerivedMarkerAt(authority *directoryAuthority, name string) error {
	if authority == nil || authority.root == nil || filepath.Base(name) != name {
		return errors.New("invalid derived marker removal")
	}
	if err := authority.root.Remove(name); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func readMarkerAt(authority *directoryAuthority) (marker, os.FileInfo, error) {
	raw, info, err := readStableRegularAt(
		authority, callerpublicationid.PublishingName, maxMarkerBytes,
	)
	if err != nil {
		return marker{}, nil, err
	}
	var value marker
	if err := decodeCanonical(raw, &value); err != nil || validateMarker(value) != nil {
		return marker{}, nil, fmt.Errorf("%w: invalid publication marker", ErrInvalidManifest)
	}
	return value, info, nil
}

func markerForState(state State) marker {
	return marker{
		Schema: MarkerSchema, Repository: state.Generation.Repository,
		GenerationDigest: state.Generation.Digest,
		ManifestDigest:   state.ManifestDigest, Manifest: state.Manifest,
	}
}

func validateMarker(value marker) error {
	if value.Schema != MarkerSchema ||
		value.Manifest != callerpublicationid.ManifestName(
			value.GenerationDigest, value.ManifestDigest,
		) || !validDigest(value.GenerationDigest) || !validDigest(value.ManifestDigest) ||
		reponame.Validate(value.Repository) != nil {
		return errors.New("invalid caller publication marker")
	}
	return nil
}

func decodeManifest(raw []byte) (Manifest, error) {
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil {
		return Manifest{}, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func canonicalManifest(manifest Manifest) []byte {
	raw, _ := json.Marshal(manifest)
	return append(raw, '\n')
}

func decodeCanonical(raw []byte, destination any) error {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return errors.New("canonical JSON is missing its newline")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw[:len(raw)-1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("canonical JSON has trailing content")
	}
	wanted, err := json.Marshal(destination)
	if err != nil {
		return err
	}
	wanted = append(wanted, '\n')
	if !bytes.Equal(raw, wanted) {
		return errors.New("JSON is not canonical")
	}
	return nil
}

func createStage(authority *directoryAuthority) (string, os.FileInfo, error) {
	for attempt := 0; attempt < 128; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := callerpublicationid.StagePrefix + hex.EncodeToString(random[:])
		if err := authority.root.Mkdir(name, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", nil, err
		}
		info, err := authority.root.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			_ = authority.root.Remove(name)
			return "", nil, errors.New("caller publication stage changed during creation")
		}
		return name, info, nil
	}
	return "", nil, errors.New("caller publication stage collisions exceeded retry bound")
}

func removeOwnedStageAt(
	authority *directoryAuthority,
	name string,
	expected os.FileInfo,
) error {
	if !callerpublicationid.ValidStageName(name) {
		return errors.New("caller publication stage is not package-owned")
	}
	info, err := authority.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		expected != nil && !sameDirectory(expected, info) {
		return errors.New("caller publication stage is not stable")
	}
	stage, err := openChildDirectoryAuthority(authority, name, info)
	if err != nil {
		return err
	}
	entries, err := readRootDirectory(stage.root, 3)
	if err != nil {
		stage.close()
		return err
	}
	for _, entry := range entries {
		if (entry.Name() != stageManifestName && entry.Name() != stageMarkerName) ||
			entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			stage.close()
			return errors.New("caller publication stage has unexpected contents")
		}
		if err := stage.root.Remove(entry.Name()); err != nil {
			stage.close()
			return err
		}
	}
	stage.close()
	current, err := authority.root.Lstat(name)
	if err != nil || !sameDirectory(info, current) {
		return errors.New("caller publication stage changed before removal")
	}
	if err := authority.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func openRepositoryAuthority(
	root, repository string,
	create bool,
) (string, *directoryAuthority, error) {
	if err := ensureRealDirectory(root, create); err != nil {
		return "", nil, err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return "", nil, err
	}
	repositoryName := callerpublicationid.RepositoryDirectory(repository)
	repositoryAuthority, err := openChildDirectoryAuthority(rootAuthority, repositoryName, nil)
	rootAuthority.close()
	if create && errors.Is(err, os.ErrNotExist) {
		if err := callerleaf.EnsureRepository(root, repository); err != nil {
			return "", nil, err
		}
		return openRepositoryAuthority(root, repository, false)
	}
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(root, repositoryName), repositoryAuthority, nil
}

func ensureRealDirectory(directory string, create bool) error {
	absolute, err := filepath.Abs(directory)
	if err != nil || filepath.Clean(absolute) != filepath.Clean(directory) {
		return errors.New("caller publication root must be absolute and clean")
	}
	if create {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("caller publication path is not a real directory")
	}
	return nil
}

func openStableDirectoryRoot(directory string) (*directoryAuthority, error) {
	before, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("caller publication directory is not real")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	after, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil {
		_ = root.Close()
		return nil, publicationIOError(statErr)
	}
	if closeErr != nil {
		_ = root.Close()
		return nil, publicationIOError(closeErr)
	}
	if !sameDirectory(before, after) {
		_ = root.Close()
		return nil, errors.New("caller publication directory changed while opening")
	}
	return &directoryAuthority{root: root, info: after}, nil
}

func openChildDirectoryAuthority(
	parent *directoryAuthority,
	name string,
	expected os.FileInfo,
) (*directoryAuthority, error) {
	if parent == nil || parent.root == nil || filepath.Base(name) != name {
		return nil, errors.New("caller publication child directory authority is invalid")
	}
	before, err := parent.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 ||
		expected != nil && !sameDirectory(expected, before) {
		return nil, errors.New("caller publication child directory is not stable")
	}
	root, err := parent.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	after, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil {
		_ = root.Close()
		return nil, publicationIOError(statErr)
	}
	if closeErr != nil {
		_ = root.Close()
		return nil, publicationIOError(closeErr)
	}
	if !sameDirectory(before, after) {
		_ = root.Close()
		return nil, errors.New("caller publication child directory changed while opening")
	}
	return &directoryAuthority{root: root, info: after}, nil
}

func readStableRegularAt(
	authority *directoryAuthority,
	name string,
	limit int,
) ([]byte, os.FileInfo, error) {
	if authority == nil || authority.root == nil || filepath.Base(name) != name || limit <= 0 {
		return nil, nil, errors.New("invalid stable file request")
	}
	before, err := authority.root.Lstat(name)
	if err != nil {
		return nil, nil, publicationIOError(err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() <= 0 || before.Size() > int64(limit) {
		return nil, nil, errors.New("stable file is special, empty, or oversized")
	}
	file, err := authority.root.Open(name)
	if err != nil {
		return nil, nil, publicationIOError(err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, publicationIOError(err)
	}
	if !sameFile(before, opened) {
		return nil, nil, errors.New("stable file changed while opening")
	}
	if testAfterStableOpen != nil {
		testAfterStableOpen(filepath.Join(authority.root.Name(), name))
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrPublicationIO, err)
	}
	if len(raw) > limit || int64(len(raw)) != opened.Size() {
		return nil, nil, errors.New("stable file length differs")
	}
	after, err := file.Stat()
	if err != nil {
		return nil, nil, publicationIOError(err)
	}
	if !sameFile(opened, after) {
		return nil, nil, errors.New("stable file changed while reading")
	}
	current, err := authority.root.Lstat(name)
	if err != nil {
		return nil, nil, publicationIOError(err)
	}
	if !sameFile(opened, current) {
		return nil, nil, errors.New("stable file path changed while reading")
	}
	return raw, opened, nil
}

func readRootDirectory(root *os.Root, limit int) ([]os.DirEntry, error) {
	if root == nil || limit <= 0 {
		return nil, callerleaf.ErrLimit
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	entries := make([]os.DirEntry, 0, 256)
	for {
		batch, readErr := directory.ReadDir(256)
		if len(entries)+len(batch) > limit {
			return nil, callerleaf.ErrLimit
		}
		entries = append(entries, batch...)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	return entries, nil
}

func syncRootDirectory(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		return errors.New("caller publication sync target is not a directory")
	}
	return directory.Sync()
}

func sameDirectory(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.IsDir() && right.IsDir() &&
		left.Mode()&os.ModeSymlink == 0 && right.Mode()&os.ModeSymlink == 0 &&
		os.SameFile(left, right)
}

func sameFile(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() &&
		right.Mode().IsRegular() && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime()) &&
		os.SameFile(left, right)
}

func ValidRepositoryDirectoryName(name string) bool {
	const prefix = "phebs-caller-overlay-"
	return filepath.Base(name) == name && strings.HasPrefix(name, prefix) &&
		len(name) == len(prefix)+64 && validLowerHex(name[len(prefix):])
}

func ValidLeafArtifactName(name string) bool {
	const prefix, suffix = "leaf-v1-", ".ndjson"
	if filepath.Base(name) != name || !strings.HasPrefix(name, prefix) ||
		!strings.HasSuffix(name, suffix) || len(name) != len(prefix)+64+1+64+len(suffix) {
		return false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	return len(raw) == 129 && raw[64] == '-' &&
		validLowerHex(raw[:64]) && validLowerHex(raw[65:])
}

func validLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}

func publicationIOError(err error) error {
	if err == nil || errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, ErrPublicationIO) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrPublicationIO, err)
}

func classifyLeafOpenError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, callerleaf.ErrInvalidArtifact) ||
		errors.Is(err, callerleaf.ErrLimit) {
		return err
	}
	return publicationIOError(err)
}

var (
	testAfterStableOpen            func(string)
	testArtifactColdOpen           func(string)
	testAfterCurrentRepositoryOpen func(string)
	testAfterCurrentLeafChecks     func(string)
	testAfterMarkerInstall         func(State) error
	testAfterManifestInstall       func(State) error
	testAfterStoreCommit           func(State)
)
