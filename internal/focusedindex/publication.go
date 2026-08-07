package focusedindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func NewBuildWorkspace(indexDir string) (workspace, outputDir string, err error) {
	workspace, err = newLifecycleWorkspace(indexDir, buildWorkspacePrefix)
	if err != nil {
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

// PublishWhole gives the whole-repository builder the same state-commit
// boundary and publishes an exact member receipt after every shard is durable.
func PublishWhole(
	ctx context.Context,
	indexDir, stageDir, repository string,
	revisions []store.IndexedRevision,
) error {
	manifest, err := createWholeStageManifest(
		ctx, stageDir, repository, revisions,
	)
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
		if err := moveRegular(
			filepath.Join(stageDir, member.Name),
			filepath.Join(indexDir, member.Name),
		); err != nil {
			return err
		}
	}
	if err := syncDirectory(indexDir); err != nil {
		return err
	}
	if err := moveRegular(
		filepath.Join(stageDir, WholeManifestName(repository)),
		filepath.Join(indexDir, WholeManifestName(repository)),
	); err != nil {
		return err
	}
	return syncDirectory(indexDir)
}

// PublishWholeGeneration publishes the T34.1 source/search authority beside
// the unchanged whole-shard v1 receipt. The stable publication marker hides
// both roots until the caller commits the matching repository row.
func PublishWholeGeneration(
	ctx context.Context,
	indexDir, shardStageDir, sourceStageDir, repository string,
	revisions []store.IndexedRevision,
	source repositoryindex.SourceManifest,
) error {
	if err := repositoryindex.ValidateSourceGeneration(
		ctx, sourceStageDir, source,
	); err != nil {
		return err
	}
	manifest, err := createWholeStageManifest(
		ctx, shardStageDir, repository, revisions,
	)
	if err != nil {
		return err
	}
	search, err := repositoryindex.WriteSearchManifest(
		sourceStageDir, repository, revisions, source,
		wholePhysicalRoot(manifest),
	)
	if err != nil {
		return err
	}
	candidate, err := createImmutableSearchGeneration(
		ctx, indexDir, shardStageDir, sourceStageDir, repository,
		revisions, source, manifest, search, SearchBlobReaderGoGit,
	)
	if err != nil {
		return err
	}
	marker, err := prepareSearchGenerationTransition(
		ctx, indexDir, repository, candidate,
	)
	if err != nil {
		return err
	}
	if search.Digest == "" {
		return errors.New("repository search generation has no digest")
	}
	return completeSearchGenerationTransition(ctx, indexDir, marker)
}

func FinishPublication(indexDir, repository string) error {
	if err := removeSearchTransition(indexDir, repository); err != nil {
		return err
	}
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
	wholeManifest := WholeManifestName(repository)
	searchLifecycleRoot := SearchGenerationRootName(repository)
	searchTransition := searchGenerationMarkerName(repository)
	removals := make([]string, 0)
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
			if strings.HasPrefix(name, focusedBase) ||
				name == wholeManifest ||
				name == searchLifecycleRoot || name == searchTransition ||
				repositoryindex.IsArtifactName(repository, name) ||
				(strings.HasPrefix(
					name, WholeShardPrefix(repository)+"_v",
				) && strings.HasSuffix(name, ".zoekt")) {
				removals = append(removals, path)
			}
			continue
		}
		remove := strings.HasPrefix(name, focusedBase) ||
			name == wholeManifest || repositoryindex.IsArtifactName(repository, name)
		remove = remove || name == searchLifecycleRoot || name == searchTransition
		if preserveMarker && (name == searchLifecycleRoot || name == searchTransition) {
			remove = false
		}
		if strings.HasPrefix(
			name, WholeShardPrefix(repository)+"_v",
		) && strings.HasSuffix(name, ".zoekt") {
			remove = true
		}
		if preserveMarker && name == PublishingName(repository) {
			remove = false
		}
		if strings.HasSuffix(name, ".zoekt") &&
			!IsManagedShardName(name) {
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
			removals = append(removals, path)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, path := range removals {
		if err := os.Remove(path); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncDirectory(indexDir)
}

func startPublication(indexDir, repository string) error {
	if err := ensureRealDirectory(indexDir); err != nil {
		return err
	}
	path := filepath.Join(indexDir, PublishingName(repository))
	temp, err := os.CreateTemp(
		indexDir,
		"."+PublishingName(repository)+"."+lifecycleOwner+".",
	)
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(repository + "\n" + lifecycleOwner + "\n"); err != nil {
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
