package candidate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmeddeb/phebs/internal/gitobj"
)

// ValidateStage performs strict identity and artifact validation over an
// unpublished generation directory.
func ValidateStage(directory string, expected Expected) (Manifest, error) {
	return ValidateStageContext(context.Background(), directory, expected)
}

// ValidateStageContext is ValidateStage with cooperative cancellation for
// large candidate generations.
func ValidateStageContext(
	ctx context.Context,
	directory string,
	expected Expected,
) (Manifest, error) {
	view, err := unitView(expected)
	if err != nil {
		return Manifest{}, err
	}
	return validatePublication(ctx, directory, expected, nil, true, view)
}

func validatePublication(
	ctx context.Context,
	directory string,
	expected Expected,
	state *State,
	exactDirectory bool,
	unit *analysisUnitView,
) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if err := ensureRealDirectory(directory); err != nil {
		return Manifest{}, err
	}
	manifestPath := filepath.Join(directory, ManifestName(expected.Repository))
	if state != nil {
		manifestPath = filepath.Join(directory, state.Manifest)
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := validateManifestIdentity(manifest, expected, state); err != nil {
		return Manifest{}, err
	}
	if err := validateArtifacts(
		ctx, directory, manifest, exactDirectory, unit,
	); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readManifest(filePath string) (_ Manifest, resultErr error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > maxManifestBytes {
		return Manifest{}, errors.New("candidate manifest is special or oversized")
	}
	source, err := openStableRegularFile(filePath, info)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		if closeErr := source.file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(source.file, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, err
	}
	if err := source.verifyAfterRead(int64(len(raw))); err != nil {
		return Manifest{}, err
	}
	if len(raw) > maxManifestBytes {
		return Manifest{}, errors.New("candidate manifest is oversized")
	}
	var manifest Manifest
	if err := strictDecode(bytes.NewReader(raw), maxManifestBytes, &manifest); err != nil {
		return Manifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return Manifest{}, errors.New("candidate manifest is not canonical")
	}
	return manifest, nil
}

// stableRegularFile binds reads to one path identity. The initial Lstat keeps
// symlinks out, the descriptor Stat closes the Lstat-to-Open race, and the
// final descriptor/path checks reject replacement or mutation during a read.
type stableRegularFile struct {
	path   string
	file   *os.File
	before os.FileInfo
}

func openStableRegularFile(
	filePath string,
	before os.FileInfo,
) (*stableRegularFile, error) {
	if before == nil || !before.Mode().IsRegular() ||
		before.Mode()&os.ModeSymlink != 0 || before.Size() < 0 {
		return nil, errors.New("candidate file is not a regular file")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	current, lstatErr := os.Lstat(filePath)
	if statErr != nil || lstatErr != nil {
		closeErr := file.Close()
		return nil, errors.Join(statErr, lstatErr, closeErr)
	}
	if !sameRegularFileIdentity(before, opened) ||
		!sameRegularFileIdentity(opened, current) {
		_ = file.Close()
		return nil, errors.New("candidate file changed while opening")
	}
	return &stableRegularFile{
		path: filePath, file: file, before: before,
	}, nil
}

func (source *stableRegularFile) verifyAfterRead(readBytes int64) error {
	if source == nil || source.file == nil || source.before == nil {
		return errors.New("candidate file identity is incomplete")
	}
	after, statErr := source.file.Stat()
	current, lstatErr := os.Lstat(source.path)
	if statErr != nil || lstatErr != nil {
		return errors.Join(statErr, lstatErr)
	}
	if readBytes != source.before.Size() ||
		!sameRegularFileIdentity(source.before, after) ||
		!sameRegularFileIdentity(after, current) {
		return errors.New("candidate file changed while reading")
	}
	return nil
}

func sameRegularFileIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil &&
		left.Mode().IsRegular() && right.Mode().IsRegular() &&
		left.Mode() == right.Mode() &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime()) &&
		os.SameFile(left, right)
}

func validateManifestIdentity(manifest Manifest, expected Expected, state *State) error {
	if manifest.Schema != ManifestSchema || !safeRepository(manifest.Repository) ||
		!gitobj.IsObjectID(manifest.Commit) ||
		(manifest.UnitDigest != "" && !validDigest(manifest.UnitDigest)) ||
		!validDigest(manifest.PolicyDigest) || !validDigest(manifest.GenerationDigest) ||
		!validDigest(manifest.Digest) || manifest.PartitionPolicy != frozenPartitionPolicy() {
		return fmt.Errorf("%w: malformed envelope", ErrInvalidManifest)
	}
	canonicalPolicies := slices.Clone(manifest.Policies)
	if err := canonicalizePolicyIdentities(canonicalPolicies); err != nil ||
		!slices.Equal(canonicalPolicies, manifest.Policies) {
		return fmt.Errorf("%w: policies are not canonical", ErrInvalidManifest)
	}
	policyDigest, err := PolicyDigest(manifest.Policies)
	if err != nil || policyDigest != manifest.PolicyDigest {
		return fmt.Errorf("%w: policy digest mismatch", ErrInvalidManifest)
	}
	generationDigest, err := GenerationDigest(
		manifest.Repository, manifest.Commit, manifest.UnitDigest, manifest.Policies,
	)
	if err != nil || generationDigest != manifest.GenerationDigest {
		return fmt.Errorf("%w: generation digest mismatch", ErrInvalidManifest)
	}
	manifestDigest, err := ManifestDigest(manifest)
	if err != nil || manifestDigest != manifest.Digest {
		return fmt.Errorf("%w: manifest digest mismatch", ErrInvalidManifest)
	}
	if state != nil {
		if err := state.Validate(); err != nil ||
			manifest.Repository != state.Repository || manifest.Commit != state.Commit ||
			manifest.UnitDigest != state.UnitDigest ||
			manifest.PolicyDigest != state.PolicyDigest ||
			manifest.GenerationDigest != state.GenerationDigest ||
			manifest.Digest != state.ManifestDigest {
			return fmt.Errorf("%w: persisted state mismatch", ErrInvalidManifest)
		}
		return nil
	}
	if expected.Repository != "" && manifest.Repository != expected.Repository {
		return fmt.Errorf("%w: repository mismatch", ErrInvalidManifest)
	}
	if expected.Commit != "" && manifest.Commit != expected.Commit {
		return fmt.Errorf("%w: commit mismatch", ErrInvalidManifest)
	}
	if expected.Unit != nil {
		unitDigest, unitErr := validateUnit(expected.Repository, expected.Unit)
		if unitErr != nil || manifest.UnitDigest != unitDigest {
			return fmt.Errorf("%w: unit mismatch", ErrInvalidManifest)
		}
	} else if expected.Repository != "" && manifest.UnitDigest != "" {
		return fmt.Errorf("%w: unexpected analysis unit", ErrInvalidManifest)
	}
	if len(expected.Policies) > 0 {
		canonicalExpected := slices.Clone(expected.Policies)
		if err := canonicalizePolicyIdentities(canonicalExpected); err != nil ||
			!slices.Equal(canonicalExpected, manifest.Policies) {
			return fmt.Errorf("%w: policy set mismatch", ErrInvalidManifest)
		}
	}
	for _, pair := range []struct{ actual, wanted string }{
		{manifest.PolicyDigest, expected.PolicyDigest},
		{manifest.GenerationDigest, expected.GenerationDigest},
		{manifest.Digest, expected.ManifestDigest},
	} {
		if pair.wanted != "" && pair.actual != pair.wanted {
			return fmt.Errorf("%w: expected digest mismatch", ErrInvalidManifest)
		}
	}
	return nil
}

// validateArtifacts is completed in reader.go; the signature lives here so
// build and lifecycle callers share exactly one validation entry point.
func validateArtifacts(
	ctx context.Context,
	directory string,
	manifest Manifest,
	exactDirectory bool,
	unit *analysisUnitView,
) error {
	return validateArtifactSet(ctx, directory, manifest, exactDirectory, unit)
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
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
