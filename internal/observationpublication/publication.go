package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

// Publish is the bounded single-process convenience boundary. Durable workers
// may call Begin, BuildPartition, and Finalize separately for the same bytes.
func Publish(
	ctx context.Context, root, repositoryDirectory string,
	plan *sourcepartition.Plan, metrics *Metrics,
) (*Publication, error) {
	if plan == nil {
		return nil, invalid("nil source plan")
	}
	partition := plan.Manifest()
	generation, err := GenerationDigest(partition)
	if err != nil {
		return nil, err
	}
	if existing, err := openGenerationDigest(ctx, root, partition.Repository, generation); err == nil {
		if err := activate(root, existing.manifest); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if _, err := os.Lstat(generationDirectory(root, partition.Repository, generation)); err == nil {
		return nil, invalid("existing generation is incomplete or corrupt")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	stage, err := Begin(root, partition)
	if err != nil {
		return nil, err
	}
	for ordinal := range partition.Members {
		if err := stage.BuildPartition(ctx, plan, repositoryDirectory, ordinal, metrics); err != nil {
			return nil, err
		}
	}
	return stage.Finalize(ctx)
}

func openGenerationDigest(ctx context.Context, root, repository, generation string) (*Publication, error) {
	directory := generationDirectory(root, repository, generation)
	raw, err := readBoundedRegular(filepath.Join(directory, manifestName()), MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil || manifest.Repository != repository || manifest.GenerationDigest != generation {
		return nil, invalid("generation manifest")
	}
	return openGeneration(ctx, root, manifest)
}

func activate(root string, manifest Manifest) error {
	pointer := Pointer{
		Schema: PointerSchema, Repository: manifest.Repository,
		GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest,
		ManifestName: filepath.Join(strings.TrimPrefix(manifest.GenerationDigest, "sha256:"), manifestName()),
	}
	return writeAtomicCanonical(pointerPath(root, manifest.Repository), pointer)
}

// Recover resolves one marker without weakening the prior pointer. A complete
// staged generation is activated; an incomplete stage is removed and the
// prior current publication remains authoritative.
func Recover(ctx context.Context, root, repository string) (bool, error) {
	raw, err := readBoundedRegular(markerPath(root, repository), MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var marker Marker
	if err := decodeCanonical(raw, &marker); err != nil || marker.Schema != MarkerSchema ||
		marker.Repository != repository || !validDigest(marker.GenerationDigest) {
		return false, invalid("publication marker")
	}
	publication, openErr := openGenerationDigest(ctx, root, repository, marker.GenerationDigest)
	if openErr == nil {
		if err := activate(root, publication.manifest); err != nil {
			return false, err
		}
	} else {
		if err := os.RemoveAll(generationDirectory(root, repository, marker.GenerationDigest)); err != nil {
			return false, err
		}
		if err := syncDirectory(repositoryDirectory(root, repository)); err != nil {
			return false, err
		}
	}
	if err := os.Remove(markerPath(root, repository)); err != nil {
		return false, err
	}
	if err := syncDirectory(repositoryDirectory(root, repository)); err != nil {
		return false, err
	}
	return openErr == nil, nil
}

func CurrentGeneration(root, repository string) (string, error) {
	pointer, err := readPointer(root, repository)
	if err != nil {
		return "", err
	}
	return pointer.GenerationDigest, nil
}
