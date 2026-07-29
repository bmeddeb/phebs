package focusedindex

import (
	"context"
	"errors"
	"fmt"
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

func NewBuildWorkspace(indexDir string) (workspace, outputDir string, err error) {
	if err := ensureRealDirectory(indexDir); err != nil {
		return "", "", err
	}
	workspace, err = os.MkdirTemp(indexDir, ".phebs-build-")
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return "", "", err
	}
	outputDir = filepath.Join(workspace, "shards")
	if err := os.Mkdir(outputDir, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return "", "", err
	}
	return workspace, outputDir, nil
}

// PublishFocused validates the complete staged set, marks the repository
// unavailable, replaces every prior shard, and renames the manifest last. The
// caller must remove the marker with FinishPublication only after committing
// the matching database state.
func PublishFocused(
	ctx context.Context,
	indexDir, stageDir, repository string,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
) error {
	manifest, err := ValidateStage(stageDir, repository, state, revisions)
	if err != nil {
		return err
	}
	if err := startPublication(indexDir, repository); err != nil {
		return err
	}
	if err := removeRepositoryArtifacts(ctx, indexDir, repository, true); err != nil {
		return err
	}
	for _, member := range manifest.Members {
		for _, name := range []string{member.Name, member.Name + MemberSuffix} {
			if err := moveRegular(
				filepath.Join(stageDir, name), filepath.Join(indexDir, name),
			); err != nil {
				return err
			}
		}
	}
	if err := syncDirectory(indexDir); err != nil {
		return err
	}
	if err := moveRegular(
		filepath.Join(stageDir, ManifestName(repository)),
		filepath.Join(indexDir, ManifestName(repository)),
	); err != nil {
		return err
	}
	return syncDirectory(indexDir)
}

// PublishWhole gives the legacy whole-repository builder the same state-commit
// boundary. It intentionally publishes no focused manifest.
func PublishWhole(
	ctx context.Context,
	indexDir, stageDir, repository string,
	revisions []store.IndexedRevision,
) error {
	shards, err := validateWholeStage(stageDir, repository, revisions)
	if err != nil {
		return err
	}
	if err := startPublication(indexDir, repository); err != nil {
		return err
	}
	if err := removeRepositoryArtifacts(ctx, indexDir, repository, true); err != nil {
		return err
	}
	for _, shard := range shards {
		if err := moveRegular(shard, filepath.Join(indexDir, filepath.Base(shard))); err != nil {
			return err
		}
	}
	return syncDirectory(indexDir)
}

func FinishPublication(indexDir, repository string) error {
	path := filepath.Join(indexDir, PublishingName(repository))
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(indexDir)
}

func IsPublishing(indexDir, repository string) bool {
	_, err := os.Lstat(filepath.Join(indexDir, PublishingName(repository)))
	return !errors.Is(err, os.ErrNotExist)
}

func RemoveRepository(ctx context.Context, indexDir, repository string) error {
	return removeRepositoryArtifacts(ctx, indexDir, repository, false)
}

func removeRepositoryArtifacts(
	ctx context.Context,
	indexDir, repository string,
	preserveMarker bool,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(indexDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	focusedBase := strings.TrimSuffix(ManifestName(repository), ".manifest.json")
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		path := filepath.Join(indexDir, name)
		if entry.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if strings.HasPrefix(name, focusedBase) {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			continue
		}
		remove := strings.HasPrefix(name, focusedBase)
		if preserveMarker && name == PublishingName(repository) {
			remove = false
		}
		if strings.HasSuffix(name, ".zoekt") {
			repositories, _, readErr := index.ReadMetadataPath(path)
			if readErr != nil {
				remove = remove || strings.HasPrefix(name, RepositoryPrefix(repository))
			} else {
				contains := false
				allTarget := len(repositories) > 0
				for _, indexedRepository := range repositories {
					contains = contains || indexedRepository.Name == repository
					allTarget = allTarget && indexedRepository.Name == repository
				}
				if contains && !allTarget {
					return fmt.Errorf("shard %q mixes repository %q with another repository", name, repository)
				}
				remove = remove || allTarget
			}
		}
		if remove {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return syncDirectory(indexDir)
}

func startPublication(indexDir, repository string) error {
	if err := ensureRealDirectory(indexDir); err != nil {
		return err
	}
	path := filepath.Join(indexDir, PublishingName(repository))
	temp, err := os.CreateTemp(indexDir, "."+PublishingName(repository)+".")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(repository + "\n"); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectory(indexDir)
}

func validateWholeStage(
	stageDir, repository string,
	revisions []store.IndexedRevision,
) ([]string, error) {
	if err := ValidateRevisions(revisions); err != nil {
		return nil, err
	}
	shards, err := filepath.Glob(filepath.Join(stageDir, "*.zoekt"))
	if err != nil {
		return nil, err
	}
	sort.Strings(shards)
	if len(shards) == 0 {
		return nil, errors.New("whole-repository builder produced no shards")
	}
	expectedBranches := make([]zoekt.RepositoryBranch, 0, len(revisions))
	for _, revision := range revisions {
		expectedBranches = append(expectedBranches, zoekt.RepositoryBranch{
			Name: revision.Branch, Version: revision.Commit,
		})
	}
	for _, shard := range shards {
		info, err := os.Lstat(shard)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("whole-repository stage contains a special shard")
		}
		repositories, _, err := index.ReadMetadataPath(shard)
		if err != nil {
			return nil, err
		}
		if len(repositories) != 1 || repositories[0].Name != repository ||
			!slices.Equal(repositories[0].Branches, expectedBranches) {
			return nil, errors.New("whole-repository staged shard metadata mismatch")
		}
		if len(repositories[0].Metadata) > 0 &&
			repositories[0].Metadata["phebs.analysis_unit"] != "" {
			return nil, errors.New("whole-repository staged shard carries focused metadata")
		}
		if err := syncFile(shard); err != nil {
			return nil, err
		}
	}
	return shards, nil
}

func moveRegular(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refuse to publish special artifact %q", filepath.Base(source))
	}
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	return syncFile(destination)
}

func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed index path is not a real directory")
	}
	return nil
}
