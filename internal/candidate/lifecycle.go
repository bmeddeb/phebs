package candidate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	stageDirectoryPrefix      = ".phebs-candidate-stage-"
	validationDirectoryPrefix = ".phebs-candidate-validation-"
)

// NewStage creates an empty package-owned staging directory on the candidate
// root's filesystem.
func NewStage(root string) (string, error) {
	if err := ensureRealDirectory(root); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(root, stageDirectoryPrefix)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(stage, 0o700); err != nil {
		_ = os.Remove(stage)
		return "", err
	}
	if err := syncDirectory(root); err != nil {
		_ = os.Remove(stage)
		return "", err
	}
	return stage, nil
}

// CleanupStages removes abandoned package-owned staging directories and
// publication-marker temporary files. It is intended for the startup seam,
// before candidate workers are active. A symlink in either namespace is
// unlinked rather than followed.
func CleanupStages(ctx context.Context, root string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := ensureRealDirectory(root); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		name := entry.Name()
		target := filepath.Join(root, name)
		if isPublicationMarkerTemporary(name) {
			// CreateTemp only creates regular files. Do not turn a forged
			// directory in that namespace into a recursive deletion target;
			// os.Remove safely unlinks regular files and symlinks.
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			if err := os.Remove(target); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
			removed++
			continue
		}
		if !strings.HasPrefix(name, stageDirectoryPrefix) &&
			!strings.HasPrefix(name, validationDirectoryPrefix) {
			continue
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			if err := os.RemoveAll(target); err != nil {
				return removed, err
			}
		} else if err := os.Remove(target); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		if err := syncDirectory(root); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func isPublicationMarkerTemporary(name string) bool {
	const (
		prefix     = ".phebs-candidate-"
		hashLength = 64
		separator  = ".publishing."
	)
	if len(name) <= len(prefix)+hashLength+len(separator) ||
		!strings.HasPrefix(name, prefix) {
		return false
	}
	for _, value := range name[len(prefix) : len(prefix)+hashLength] {
		if (value < '0' || value > '9') &&
			(value < 'a' || value > 'f') {
			return false
		}
	}
	if name[len(prefix)+hashLength:len(prefix)+hashLength+len(separator)] != separator {
		return false
	}
	suffix := name[len(prefix)+hashLength+len(separator):]
	if len(suffix) > 1 && suffix[0] == '0' {
		return false
	}
	_, err := strconv.ParseUint(suffix, 10, 32)
	return err == nil
}

// Open validates and opens the exact expected live publication without any
// database dependency.
func Open(root string, expected Expected) (*Publication, error) {
	return OpenContext(context.Background(), root, expected)
}

// OpenContext is Open with cooperative cancellation for strict validation.
func OpenContext(
	ctx context.Context,
	root string,
	expected Expected,
) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if IsPublishing(root, expected.Repository) {
		return nil, ErrPublishing
	}
	view, err := unitView(expected)
	if err != nil {
		return nil, err
	}
	manifest, err := validatePublication(
		ctx, root, expected, nil, false, view,
	)
	if err != nil {
		return nil, err
	}
	if IsPublishing(root, expected.Repository) {
		return nil, ErrPublishing
	}
	return &Publication{root: root, manifest: manifest, state: manifest.State()}, nil
}

// OpenPublishingContext strictly validates an interrupted live publication
// while its stable marker remains installed. It is restricted to recovery:
// callers cannot use it when the marker is absent, and the marker identity is
// rechecked after all manifest/member bytes have been validated. The caller
// removes the marker only after the matching database transition succeeds.
func OpenPublishingContext(
	ctx context.Context,
	root string,
	expected Expected,
) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePublishingMarker(root, expected.Repository); err != nil {
		return nil, err
	}
	view, err := unitView(expected)
	if err != nil {
		return nil, err
	}
	manifest, err := validatePublication(
		ctx, root, expected, nil, false, view,
	)
	if err != nil {
		return nil, err
	}
	if err := validatePublishingMarker(root, expected.Repository); err != nil {
		return nil, err
	}
	return &Publication{root: root, manifest: manifest, state: manifest.State()}, nil
}

func validatePublishingMarker(root, repository string) (resultErr error) {
	if !safeRepository(repository) {
		return fmt.Errorf("%w: invalid repository marker identity", ErrPublishing)
	}
	markerPath := filepath.Join(root, PublishingName(repository))
	info, err := os.Lstat(markerPath)
	if err != nil {
		return fmt.Errorf("%w: marker unavailable: %v", ErrPublishing, err)
	}
	expectedBytes := int64(len(repository) + 1)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() != expectedBytes {
		return fmt.Errorf("%w: marker is not canonical", ErrPublishing)
	}
	source, err := openStableRegularFile(markerPath, info)
	if err != nil {
		return fmt.Errorf("%w: marker changed while opening: %v", ErrPublishing, err)
	}
	defer func() {
		if closeErr := source.file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(source.file, expectedBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read marker: %v", ErrPublishing, err)
	}
	if err := source.verifyAfterRead(int64(len(raw))); err != nil {
		return fmt.Errorf("%w: marker changed while reading: %v", ErrPublishing, err)
	}
	if string(raw) != repository+"\n" {
		return fmt.Errorf("%w: marker content is not canonical", ErrPublishing)
	}
	return nil
}

// OpenState opens a publication from an already persisted primitive pointer.
func OpenState(root string, state State) (*Publication, error) {
	return OpenStateContext(context.Background(), root, state)
}

// OpenStateContext is OpenState with cooperative cancellation.
func OpenStateContext(
	ctx context.Context,
	root string,
	state State,
) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	if IsPublishing(root, state.Repository) {
		return nil, ErrPublishing
	}
	expected := Expected{
		Repository: state.Repository, Commit: state.Commit,
		PolicyDigest:     state.PolicyDigest,
		GenerationDigest: state.GenerationDigest,
		ManifestDigest:   state.ManifestDigest,
	}
	manifest, err := validatePublication(
		ctx, root, expected, &state, false, nil,
	)
	if err != nil {
		return nil, err
	}
	if IsPublishing(root, state.Repository) {
		return nil, ErrPublishing
	}
	// State alone intentionally cannot prove unit path flags. Those are bound
	// by the manifest digest and are re-proven against the committed unit by
	// Open whenever extraction starts.
	return &Publication{root: root, manifest: manifest, state: state}, nil
}

// Publish validates the complete stage, installs a same-filesystem marker,
// removes stale repository generations, moves members, and renames the
// manifest last. The marker remains until the matching state is committed.
func Publish(root, stage string, expected Expected) (State, error) {
	return PublishContext(context.Background(), root, stage, expected)
}

// PublishContext is Publish with cooperative cancellation.
func PublishContext(
	ctx context.Context,
	root, stage string,
	expected Expected,
) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	view, err := unitView(expected)
	if err != nil {
		return State{}, err
	}
	manifest, err := validatePublication(
		ctx, stage, expected, nil, true, view,
	)
	if err != nil {
		return State{}, err
	}
	if err := ensureRealDirectory(root); err != nil {
		return State{}, err
	}
	if err := ensureRealDirectory(stage); err != nil {
		return State{}, err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return State{}, err
	}
	stageInfo, err := os.Stat(stage)
	if err != nil {
		return State{}, err
	}
	if os.SameFile(rootInfo, stageInfo) {
		return State{}, errors.New("candidate stage must differ from the publication root")
	}
	rootDevice, err := directoryDevice(root)
	if err != nil {
		return State{}, err
	}
	stageDevice, err := directoryDevice(stage)
	if err != nil {
		return State{}, err
	}
	if rootDevice != stageDevice {
		return State{}, errors.New("candidate stage must share the publication filesystem")
	}
	if err := startPublication(root, manifest.Repository); err != nil {
		return State{}, err
	}
	// Keep the prior complete generation intact until every new member is
	// durable. The marker makes both old and new bytes unavailable while this
	// transition is in flight.
	for _, member := range manifest.RepositoryMembers {
		if err := ctx.Err(); err != nil {
			return State{}, err
		}
		if err := moveRegular(
			filepath.Join(stage, member.Name), filepath.Join(root, member.Name),
		); err != nil {
			return State{}, err
		}
	}
	for _, leaf := range manifest.CallerLeaves {
		if err := ctx.Err(); err != nil {
			return State{}, err
		}
		if err := moveRegular(
			filepath.Join(stage, leaf.Name), filepath.Join(root, leaf.Name),
		); err != nil {
			return State{}, err
		}
	}
	if err := syncDirectory(root); err != nil {
		return State{}, err
	}
	// The manifest is the visibility authority and therefore switches last.
	if err := moveRegular(
		filepath.Join(stage, ManifestName(manifest.Repository)),
		filepath.Join(root, ManifestName(manifest.Repository)),
	); err != nil {
		return State{}, err
	}
	if err := syncDirectory(root); err != nil {
		return State{}, err
	}
	// Before the caller may commit state and finish the marker, remove the
	// previous generation and any debris left by an interrupted retry.
	if err := removeStaleRepositoryArtifacts(
		ctx, root, manifest,
	); err != nil {
		return State{}, err
	}
	return manifest.State(), nil
}

func FinishPublication(root, repository string) error {
	if !safeRepository(repository) {
		return errors.New("candidate repository is invalid")
	}
	if err := ensureRealDirectory(root); err != nil {
		return err
	}
	if err := os.Remove(
		filepath.Join(root, PublishingName(repository)),
	); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(root)
}

func IsPublishing(root, repository string) bool {
	if !safeRepository(repository) {
		return true
	}
	info, rootErr := os.Lstat(root)
	if rootErr != nil {
		return !errors.Is(rootErr, os.ErrNotExist)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	_, err := os.Lstat(filepath.Join(root, PublishingName(repository)))
	return !errors.Is(err, os.ErrNotExist)
}

// Remove deletes every managed artifact for one repository without following
// symlinks. It is idempotent when the candidate root is absent.
func Remove(ctx context.Context, root, repository string) error {
	return removeRepositoryArtifacts(ctx, root, repository, false)
}

func removeRepositoryArtifacts(
	ctx context.Context,
	root, repository string,
	preserveMarker bool,
) error {
	if !safeRepository(repository) {
		return errors.New("candidate repository is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ensureRealDirectory(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	base := ArtifactBase(repository)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if name != ManifestName(repository) &&
			name != PublishingName(repository) &&
			!strings.HasPrefix(name, base+"-") {
			continue
		}
		if preserveMarker && name == PublishingName(repository) {
			continue
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("candidate artifact %q is an unexpected directory", name)
		}
		if err := os.Remove(filepath.Join(root, name)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(root)
}

func removeStaleRepositoryArtifacts(
	ctx context.Context,
	root string,
	manifest Manifest,
) error {
	allowed := map[string]bool{
		ManifestName(manifest.Repository):   true,
		PublishingName(manifest.Repository): true,
	}
	for _, member := range manifest.RepositoryMembers {
		allowed[member.Name] = true
	}
	for _, leaf := range manifest.CallerLeaves {
		allowed[leaf.Name] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	base := ArtifactBase(manifest.Repository)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if allowed[name] ||
			name != ManifestName(manifest.Repository) &&
				name != PublishingName(manifest.Repository) &&
				!strings.HasPrefix(name, base+"-") {
			continue
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("candidate artifact %q is an unexpected directory", name)
		}
		if err := os.Remove(filepath.Join(root, name)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(root)
}

// ManagedArtifactNames returns a sorted, non-recursive candidate-root audit
// without trusting artifact contents. Reconciliation can compare these names
// with committed publication states and call Remove for known repositories.
func ManagedArtifactNames(root string) ([]string, error) {
	if err := ensureRealDirectory(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "phebs-candidate-") {
			result = append(result, entry.Name())
		}
	}
	sort.Strings(result)
	return result, nil
}

func startPublication(root, repository string) error {
	if err := ensureRealDirectory(root); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, "."+PublishingName(repository)+".")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(repository + "\n"); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(
		temporaryPath, filepath.Join(root, PublishingName(repository)),
	); err != nil {
		return err
	}
	return syncDirectory(root)
}

func ensureRealDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("candidate artifact root is not a real directory")
	}
	return nil
}

func moveRegular(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refuse to publish special candidate artifact")
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	file, err := os.Open(destination)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func directoryDevice(directory string) (uint64, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return 0, err
	}
	if value, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(value.Dev), nil
	}
	return 0, errors.New("candidate filesystem identity is unavailable")
}
