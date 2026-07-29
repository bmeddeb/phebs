package focusedindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/store"
)

func createManifest(
	indexDir string,
	scope analysisunit.Scope,
	unitDigest, generationDigest string,
	revisions []store.IndexedRevision,
	expectedBranches []zoekt.RepositoryBranch,
) (Manifest, int64, error) {
	repository := scope.Repository
	shards, err := filepath.Glob(filepath.Join(
		indexDir, ShardPrefix(repository, generationDigest)+"*.zoekt",
	))
	if err != nil {
		return Manifest{}, 0, err
	}
	sort.Strings(shards)
	if len(shards) == 0 {
		return Manifest{}, 0, errors.New("focused builder produced no shards")
	}
	manifest := Manifest{
		Schema:           ManifestSchema,
		Repository:       repository,
		Scope:            scope,
		UnitDigest:       unitDigest,
		GenerationDigest: generationDigest,
		BuilderPolicy:    BuilderPolicy,
		Revisions:        cloneRevisions(revisions),
	}
	var shardBytes int64
	for ordinal, shardPath := range shards {
		contentDigest, size, err := DigestRegularFile(shardPath)
		if err != nil {
			return Manifest{}, 0, err
		}
		repositories, metadata, err := index.ReadMetadataPath(shardPath)
		if err != nil {
			return Manifest{}, 0, err
		}
		if err := validateRepositoryMetadata(
			repositories, repository, unitDigest, generationDigest,
			expectedBranches,
		); err != nil {
			return Manifest{}, 0, fmt.Errorf(
				"%w: shard %q: %v", ErrShardSet, filepath.Base(shardPath), err,
			)
		}
		metadataDigest, err := shardMetadataDigest(repositories, metadata)
		if err != nil {
			return Manifest{}, 0, err
		}
		member := ShardMember{
			Ordinal:          ordinal,
			Count:            len(shards),
			Name:             filepath.Base(shardPath),
			ContentDigest:    contentDigest,
			MetadataDigest:   metadataDigest,
			UnitDigest:       unitDigest,
			GenerationDigest: generationDigest,
		}
		if err := WriteControlFile(shardPath+MemberSuffix, member); err != nil {
			return Manifest{}, 0, err
		}
		if err := syncFile(shardPath); err != nil {
			return Manifest{}, 0, err
		}
		manifest.Members = append(manifest.Members, member)
		shardBytes += size
	}
	manifest.Digest, err = ManifestDigest(manifest)
	if err != nil {
		return Manifest{}, 0, err
	}
	if err := WriteControlFile(filepath.Join(indexDir, ManifestName(repository)), manifest); err != nil {
		return Manifest{}, 0, err
	}
	if err := syncDirectory(indexDir); err != nil {
		return Manifest{}, 0, err
	}
	return manifest, shardBytes, nil
}

func ValidatePublished(
	indexDir, repository string,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
) (Manifest, error) {
	if state == nil || state.SearchIndexPosture != analysisunit.SearchIndexFocused {
		return Manifest{}, fmt.Errorf("%w: repository has no focused committed state", ErrShardSet)
	}
	if err := state.Validate(repository); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrShardSet, err)
	}
	if IsPublishing(indexDir, repository) {
		return Manifest{}, fmt.Errorf("%w: repository publication is in progress", ErrShardSet)
	}
	return validate(indexDir, repository, state, revisions)
}

func ValidateStage(
	indexDir, repository string,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
) (Manifest, error) {
	if state == nil || state.SearchIndexPosture != analysisunit.SearchIndexFocused {
		return Manifest{}, fmt.Errorf("%w: staged state is not focused", ErrShardSet)
	}
	return validate(indexDir, repository, state, revisions)
}

func validate(
	indexDir, repository string,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
) (Manifest, error) {
	if err := ValidateRevisions(revisions); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrShardSet, err)
	}
	scope := analysisunit.Scope{
		Repository: repository,
		Name:       state.Name,
		Primary:    state.PrimaryPaths,
		Supporting: state.SupportingPaths,
	}
	generationDigest, err := GenerationDigest(scope, revisions)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrShardSet, err)
	}
	manifestPath := filepath.Join(indexDir, ManifestName(repository))
	var manifest Manifest
	if err := readControlFile(manifestPath, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: read manifest: %v", ErrShardSet, err)
	}
	if manifest.Schema != ManifestSchema ||
		manifest.Repository != repository ||
		manifest.Scope.Repository != repository ||
		manifest.UnitDigest != state.Digest ||
		manifest.GenerationDigest != generationDigest ||
		manifest.BuilderPolicy != BuilderPolicy ||
		!slices.Equal(manifest.Revisions, revisions) ||
		len(manifest.Members) == 0 ||
		!validDigest(manifest.Digest) {
		return Manifest{}, fmt.Errorf("%w: manifest identity mismatch", ErrShardSet)
	}
	manifestState, err := manifest.Scope.State()
	if err != nil || !analysisunit.EqualState(manifestState, state) {
		return Manifest{}, fmt.Errorf("%w: manifest scope mismatch", ErrShardSet)
	}
	digest, err := ManifestDigest(manifest)
	if err != nil || digest != manifest.Digest {
		return Manifest{}, fmt.Errorf("%w: manifest digest mismatch", ErrShardSet)
	}
	expectedNames := make(map[string]bool, len(manifest.Members))
	expectedBranches := make([]zoekt.RepositoryBranch, 0, len(revisions))
	for _, revision := range revisions {
		expectedBranches = append(expectedBranches, zoekt.RepositoryBranch{
			Name: revision.Branch, Version: revision.Commit,
		})
	}
	for ordinal, member := range manifest.Members {
		if member.Ordinal != ordinal || member.Count != len(manifest.Members) ||
			member.UnitDigest != manifest.UnitDigest ||
			member.GenerationDigest != manifest.GenerationDigest ||
			filepath.Base(member.Name) != member.Name ||
			!strings.HasPrefix(member.Name, ShardPrefix(repository, generationDigest)) ||
			filepath.Ext(member.Name) != ".zoekt" ||
			!validDigest(member.ContentDigest) || !validDigest(member.MetadataDigest) ||
			expectedNames[member.Name] {
			return Manifest{}, fmt.Errorf("%w: member ordering or identity mismatch", ErrShardSet)
		}
		expectedNames[member.Name] = true
	}
	actualNames, err := repositoryShardNames(indexDir, repository)
	if err != nil {
		return Manifest{}, err
	}
	expectedSorted := make([]string, 0, len(expectedNames))
	for name := range expectedNames {
		expectedSorted = append(expectedSorted, name)
	}
	sort.Strings(expectedSorted)
	if !slices.Equal(actualNames, expectedSorted) {
		return Manifest{}, fmt.Errorf(
			"%w: exact membership mismatch: actual=%v expected=%v",
			ErrShardSet, actualNames, expectedSorted,
		)
	}
	if err := rejectExtraFocusedArtifacts(indexDir, repository, manifest); err != nil {
		return Manifest{}, err
	}
	for _, member := range manifest.Members {
		shardPath := filepath.Join(indexDir, member.Name)
		contentDigest, _, err := DigestRegularFile(shardPath)
		if err != nil || contentDigest != member.ContentDigest {
			return Manifest{}, fmt.Errorf("%w: content mismatch for %q", ErrShardSet, member.Name)
		}
		var sidecar ShardMember
		if err := readControlFile(shardPath+MemberSuffix, &sidecar); err != nil || sidecar != member {
			return Manifest{}, fmt.Errorf("%w: sidecar mismatch for %q", ErrShardSet, member.Name)
		}
		repositories, metadata, err := index.ReadMetadataPath(shardPath)
		if err != nil {
			return Manifest{}, fmt.Errorf("%w: read metadata for %q: %v", ErrShardSet, member.Name, err)
		}
		if err := validateRepositoryMetadata(
			repositories, repository, manifest.UnitDigest,
			manifest.GenerationDigest, expectedBranches,
		); err != nil {
			return Manifest{}, fmt.Errorf("%w: repository identity for %q: %v", ErrShardSet, member.Name, err)
		}
		metadataDigest, err := shardMetadataDigest(repositories, metadata)
		if err != nil || metadataDigest != member.MetadataDigest {
			return Manifest{}, fmt.Errorf("%w: metadata mismatch for %q", ErrShardSet, member.Name)
		}
	}
	return manifest, nil
}

// ValidateSelfContained is used by backup/restore before the database is
// available. The canonical scope embedded in the manifest re-derives both the
// unit and generation identities; startup reconciliation subsequently binds
// the same bytes to committed repository state.
func ValidateSelfContained(indexDir, repository string) (Manifest, error) {
	if IsPublishing(indexDir, repository) {
		return Manifest{}, fmt.Errorf("%w: repository publication is in progress", ErrShardSet)
	}
	var manifest Manifest
	if err := readControlFile(
		filepath.Join(indexDir, ManifestName(repository)), &manifest,
	); err != nil {
		return Manifest{}, fmt.Errorf("%w: read manifest: %v", ErrShardSet, err)
	}
	if manifest.Repository != repository || manifest.Scope.Repository != repository {
		return Manifest{}, fmt.Errorf("%w: self-contained repository mismatch", ErrShardSet)
	}
	state, err := manifest.Scope.State()
	if err != nil || state.Digest != manifest.UnitDigest {
		return Manifest{}, fmt.Errorf("%w: self-contained unit mismatch", ErrShardSet)
	}
	generation, err := GenerationDigest(manifest.Scope, manifest.Revisions)
	if err != nil || generation != manifest.GenerationDigest {
		return Manifest{}, fmt.Errorf("%w: self-contained generation mismatch", ErrShardSet)
	}
	return validate(indexDir, repository, state, manifest.Revisions)
}

func repositoryShardNames(indexDir, repository string) ([]string, error) {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return nil, fmt.Errorf("%w: read index directory: %v", ErrShardSet, err)
	}
	names := make(map[string]bool)
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: symlinked index artifact %q", ErrShardSet, entry.Name())
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zoekt") {
			continue
		}
		shardPath := filepath.Join(indexDir, entry.Name())
		repositories, _, readErr := index.ReadMetadataPath(shardPath)
		if readErr != nil {
			return nil, fmt.Errorf(
				"%w: unreadable shard %q while proving exact membership",
				ErrShardSet, entry.Name(),
			)
		}
		for _, indexedRepository := range repositories {
			if indexedRepository.Name == repository {
				names[entry.Name()] = true
				break
			}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func rejectExtraFocusedArtifacts(indexDir, repository string, manifest Manifest) error {
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return err
	}
	allowed := map[string]bool{ManifestName(repository): true}
	for _, member := range manifest.Members {
		allowed[member.Name] = true
		allowed[member.Name+MemberSuffix] = true
	}
	prefix := ArtifactBase(repository)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && !allowed[entry.Name()] {
			return fmt.Errorf("%w: undeclared focused artifact %q", ErrShardSet, entry.Name())
		}
	}
	return nil
}

func validateRepositoryMetadata(
	repositories []*zoekt.Repository,
	repository, unitDigest, generationDigest string,
	expectedBranches []zoekt.RepositoryBranch,
) error {
	if len(repositories) != 1 || repositories[0].Name != repository ||
		!slices.Equal(repositories[0].Branches, expectedBranches) ||
		repositories[0].Metadata["phebs.analysis_unit"] != analysisunit.SchemaVersion ||
		repositories[0].Metadata["phebs.unit_digest"] != unitDigest ||
		repositories[0].Metadata["phebs.index_generation"] != generationDigest ||
		repositories[0].Metadata["phebs.builder_policy"] != BuilderPolicy {
		return errors.New("focused shard metadata disagrees with committed identity")
	}
	return nil
}

func shardMetadataDigest(
	repositories []*zoekt.Repository,
	metadata *zoekt.IndexMetadata,
) (string, error) {
	raw, err := json.Marshal(struct {
		Repositories []*zoekt.Repository  `json:"repositories"`
		Index        *zoekt.IndexMetadata `json:"index"`
	}{Repositories: repositories, Index: metadata})
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-shard-metadata-v1\x00", raw), nil
}

func DigestRegularFile(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: %q is not a regular file", ErrShardSet, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
