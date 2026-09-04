package focusedindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

const WholeManifestSchema = "phebs-whole-shard-set-v1"

// WholeShardMember is the immutable member identity for a whole-repository
// publication. Whole shards are renamed into a repository-keyed namespace
// before publication so cleanup can identify ownership even if bytes become
// unreadable.
type WholeShardMember struct {
	Ordinal        int    `json:"ordinal"`
	Count          int    `json:"count"`
	Name           string `json:"name"`
	ContentDigest  string `json:"content_digest"`
	ContentBytes   int64  `json:"content_bytes"`
	MetadataDigest string `json:"metadata_digest"`
}

// WholeManifest gives the asynchronous whole-repository reader an exact,
// durable generation receipt. Digest identifies publication content, not
// semantic Git identity: builder timestamps can change bytes across rebuilds.
type WholeManifest struct {
	Schema     string                  `json:"schema"`
	Repository string                  `json:"repository"`
	Revisions  []store.IndexedRevision `json:"revisions"`
	Members    []WholeShardMember      `json:"members"`
	Digest     string                  `json:"digest"`
}

func WholeManifestName(repository string) string {
	return "phebs-whole-" + repositoryKey(repository) + ".manifest.json"
}

func WholeShardPrefix(repository string) string {
	return "phebs-whole-" + repositoryKey(repository)
}

func WholeShardName(
	repository string,
	formatVersion, ordinal int,
) string {
	return fmt.Sprintf(
		"%s_v%d.%05d.zoekt",
		WholeShardPrefix(repository), formatVersion, ordinal,
	)
}

func validWholeShardName(
	repository, name string,
	ordinal int,
) bool {
	prefix := WholeShardPrefix(repository) + "_v"
	suffix := fmt.Sprintf(".%05d.zoekt", ordinal)
	if !strings.HasPrefix(name, prefix) ||
		!strings.HasSuffix(name, suffix) {
		return false
	}
	version := strings.TrimSuffix(
		strings.TrimPrefix(name, prefix), suffix,
	)
	parsed, err := strconv.Atoi(version)
	return err == nil && parsed > 0
}

func WholeManifestDigest(manifest WholeManifest) (string, error) {
	manifest.Digest = ""
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	return digestBytes("phebs-whole-shard-set-v1\x00", canonical), nil
}

// ReadWholeManifest validates the canonical control-plane identity without
// re-hashing member bytes. Search uses it for its warm identity check; exact
// transition binding calls ValidateWholePublishedContext before mmap.
func ReadWholeManifest(
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (WholeManifest, error) {
	return ReadWholeManifestContext(
		context.Background(), indexDir, repository, revisions,
	)
}

// ReadWholeManifestContext is the request-accounted form of ReadWholeManifest.
func ReadWholeManifestContext(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (WholeManifest, error) {
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return WholeManifest{}, err
	}
	var manifest WholeManifest
	if err := readControlFile(
		filepath.Join(indexDir, WholeManifestName(repository)), &manifest,
	); err != nil {
		return WholeManifest{}, err
	}
	if err := validateWholeManifestIdentity(
		manifest, repository, revisions,
	); err != nil {
		return WholeManifest{}, err
	}
	return manifest, nil
}

func ReadWholeManifestSelfContained(
	indexDir, repository string,
) (WholeManifest, error) {
	var manifest WholeManifest
	if err := readControlFile(
		filepath.Join(indexDir, WholeManifestName(repository)), &manifest,
	); err != nil {
		return WholeManifest{}, err
	}
	if err := validateWholeManifestIdentity(
		manifest, repository, manifest.Revisions,
	); err != nil {
		return WholeManifest{}, err
	}
	return manifest, nil
}

func createWholeStageManifest(
	ctx context.Context,
	stageDir, repository string,
	revisions []store.IndexedRevision,
) (WholeManifest, error) {
	if err := ValidateRevisions(revisions); err != nil {
		return WholeManifest{}, err
	}
	shards, err := filepath.Glob(filepath.Join(stageDir, "*.zoekt"))
	if err != nil {
		return WholeManifest{}, err
	}
	sort.Strings(shards)
	if len(shards) == 0 {
		return WholeManifest{}, errors.New(
			"whole-repository builder produced no shards",
		)
	}
	expectedBranches := wholeExpectedBranches(revisions)
	manifest := WholeManifest{
		Schema: WholeManifestSchema, Repository: repository,
		Revisions: append([]store.IndexedRevision(nil), revisions...),
		Members:   make([]WholeShardMember, 0, len(shards)),
	}
	for ordinal, shard := range shards {
		if err := ctx.Err(); err != nil {
			return WholeManifest{}, err
		}
		info, err := os.Lstat(shard)
		if err != nil || !info.Mode().IsRegular() {
			return WholeManifest{}, errors.New(
				"whole-repository stage contains a special shard",
			)
		}
		repositories, metadata, err := index.ReadMetadataPath(shard)
		if err != nil {
			return WholeManifest{}, err
		}
		if err := validateWholeRepositoryMetadata(
			repositories, repository, expectedBranches,
		); err != nil {
			return WholeManifest{}, err
		}
		if metadata == nil || metadata.IndexFormatVersion <= 0 {
			return WholeManifest{}, errors.New(
				"whole-repository staged shard has no format identity",
			)
		}
		name := WholeShardName(
			repository, metadata.IndexFormatVersion, ordinal,
		)
		canonicalPath := filepath.Join(stageDir, name)
		if err := os.Rename(shard, canonicalPath); err != nil {
			return WholeManifest{}, err
		}
		contentDigest, contentBytes, err := DigestRegularFileContext(
			ctx, canonicalPath,
		)
		if err != nil {
			return WholeManifest{}, err
		}
		metadataDigest, err := shardMetadataDigest(repositories, metadata)
		if err != nil {
			return WholeManifest{}, err
		}
		if err := syncFile(canonicalPath); err != nil {
			return WholeManifest{}, err
		}
		manifest.Members = append(manifest.Members, WholeShardMember{
			Ordinal: ordinal, Count: len(shards),
			Name:          name,
			ContentDigest: contentDigest, ContentBytes: contentBytes,
			MetadataDigest: metadataDigest,
		})
	}
	manifest.Digest, err = WholeManifestDigest(manifest)
	if err != nil {
		return WholeManifest{}, err
	}
	if err := WriteControlFile(
		filepath.Join(stageDir, WholeManifestName(repository)), manifest,
	); err != nil {
		return WholeManifest{}, err
	}
	if err := syncDirectory(stageDir); err != nil {
		return WholeManifest{}, err
	}
	return manifest, nil
}

func ValidateWholePublishedContext(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (WholeManifest, error) {
	return validateWholePublishedContext(
		ctx, indexDir, repository, revisions, false,
	)
}

// ValidateRepositorySearchGeneration proves the T34.1 source/search root
// against the already strict whole-shard publication it names.
func ValidateRepositorySearchGeneration(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (repositoryindex.SearchManifest, error) {
	whole, err := ValidateWholePublishedContext(
		ctx, indexDir, repository, revisions,
	)
	if err != nil {
		return repositoryindex.SearchManifest{}, err
	}
	return repositoryindex.ValidatePublished(
		ctx, indexDir, repository, revisions, wholePhysicalRoot(whole),
	)
}

// ReadRepositorySearchGeneration opens only the closed v2 control roots. It
// deliberately does not hash shard or source-member content; hot readers pair
// it with a whole-cache lease whose fill already performed the complete
// generation validation and retained identity fences for every named file.
func ReadRepositorySearchGeneration(
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (repositoryindex.SearchManifest, repositoryindex.SourceManifest, error) {
	return ReadRepositorySearchGenerationContext(
		context.Background(), indexDir, repository, revisions,
	)
}

// ReadRepositorySearchGenerationContext is the request-accounted form of
// ReadRepositorySearchGeneration.
func ReadRepositorySearchGenerationContext(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (repositoryindex.SearchManifest, repositoryindex.SourceManifest, error) {
	if IsPublishing(indexDir, repository) {
		return repositoryindex.SearchManifest{}, repositoryindex.SourceManifest{},
			errors.New("whole-repository publication is in progress")
	}
	whole, err := ReadWholeManifestContext(ctx, indexDir, repository, revisions)
	if err != nil {
		return repositoryindex.SearchManifest{}, repositoryindex.SourceManifest{}, err
	}
	search, err := repositoryindex.ReadSearchManifestContext(ctx, indexDir, repository)
	if err != nil {
		return repositoryindex.SearchManifest{}, repositoryindex.SourceManifest{}, err
	}
	source, err := repositoryindex.ReadSourceManifestContext(ctx, indexDir, repository)
	if err != nil {
		return repositoryindex.SearchManifest{}, repositoryindex.SourceManifest{}, err
	}
	if err := repositoryindex.ValidateSearchControls(
		search, source, revisions, wholePhysicalRoot(whole),
	); err != nil {
		return repositoryindex.SearchManifest{}, repositoryindex.SourceManifest{}, err
	}
	return search, source, nil
}

// ValidateCommittedRepositorySearchGeneration permits only the prior-process
// publication marker used by startup recovery. Every v2 source/search/shard
// byte must otherwise validate before the marker can be retired.
func ValidateCommittedRepositorySearchGeneration(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (repositoryindex.SearchManifest, error) {
	whole, err := validateWholePublishedContext(
		ctx, indexDir, repository, revisions, true,
	)
	if err != nil {
		return repositoryindex.SearchManifest{}, err
	}
	return repositoryindex.ValidatePublished(
		ctx, indexDir, repository, revisions, wholePhysicalRoot(whole),
	)
}

func wholePhysicalRoot(manifest WholeManifest) repositoryindex.PhysicalRoot {
	root := repositoryindex.PhysicalRoot{
		Schema:         WholeManifestSchema,
		ManifestName:   WholeManifestName(manifest.Repository),
		ManifestDigest: manifest.Digest,
		Members:        make([]repositoryindex.PhysicalMember, 0, len(manifest.Members)),
	}
	for _, member := range manifest.Members {
		root.Members = append(root.Members, repositoryindex.PhysicalMember{
			Ordinal: member.Ordinal, Count: member.Count, Name: member.Name,
			ContentDigest:  member.ContentDigest,
			ContentBytes:   member.ContentBytes,
			MetadataDigest: member.MetadataDigest,
		})
	}
	return root
}

// ValidateCommittedWholePublication proves a whole publication while
// deliberately allowing a prior-process marker. Reconciliation uses it only
// to decide whether that marker can be removed.
func ValidateCommittedWholePublication(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (WholeManifest, error) {
	return validateWholePublishedContext(
		ctx, indexDir, repository, revisions, true,
	)
}

func validateWholePublishedContext(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
	allowPublicationMarker bool,
) (WholeManifest, error) {
	if err := ctx.Err(); err != nil {
		return WholeManifest{}, err
	}
	if !allowPublicationMarker && IsPublishing(indexDir, repository) {
		return WholeManifest{}, errors.New(
			"whole-repository publication is in progress",
		)
	}
	manifest, err := ReadWholeManifestContext(ctx, indexDir, repository, revisions)
	if err != nil {
		return WholeManifest{}, err
	}
	expectedBranches := wholeExpectedBranches(revisions)
	for _, member := range manifest.Members {
		if err := ctx.Err(); err != nil {
			return WholeManifest{}, err
		}
		path := filepath.Join(indexDir, member.Name)
		contentDigest, contentBytes, err := DigestRegularFileContext(ctx, path)
		if err != nil || contentDigest != member.ContentDigest ||
			contentBytes != member.ContentBytes {
			if err != nil {
				return WholeManifest{}, err
			}
			return WholeManifest{}, fmt.Errorf(
				"whole-repository content mismatch for %q", member.Name,
			)
		}
		repositories, metadata, err := index.ReadMetadataPath(path)
		if err != nil {
			return WholeManifest{}, err
		}
		if err := validateWholeRepositoryMetadata(
			repositories, repository, expectedBranches,
		); err != nil {
			return WholeManifest{}, err
		}
		metadataDigest, err := shardMetadataDigest(repositories, metadata)
		if err != nil || metadataDigest != member.MetadataDigest {
			if err != nil {
				return WholeManifest{}, err
			}
			return WholeManifest{}, fmt.Errorf(
				"whole-repository metadata mismatch for %q", member.Name,
			)
		}
	}
	return manifest, nil
}

// ValidateWholeReceipt checks the control identity, declared file identities,
// and decoded metadata without reading complete shard contents.
// Reconciliation uses this repository-local check on every pass; the search
// transition binder performs the full content-digest validation before opening
// an exact reader.
func ValidateWholeReceipt(
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (WholeManifest, error) {
	if IsPublishing(indexDir, repository) {
		return WholeManifest{}, errors.New(
			"whole-repository publication is in progress",
		)
	}
	manifest, err := ReadWholeManifest(indexDir, repository, revisions)
	if err != nil {
		return WholeManifest{}, err
	}
	expectedBranches := wholeExpectedBranches(revisions)
	for _, member := range manifest.Members {
		path := filepath.Join(indexDir, member.Name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() ||
			info.Size() != member.ContentBytes {
			return WholeManifest{}, fmt.Errorf(
				"whole-repository member %q identity mismatch",
				member.Name,
			)
		}
		repositories, metadata, err := index.ReadMetadataPath(path)
		if err != nil {
			return WholeManifest{}, fmt.Errorf(
				"read whole-repository member %q metadata: %w",
				member.Name, err,
			)
		}
		if err := validateWholeRepositoryMetadata(
			repositories, repository, expectedBranches,
		); err != nil {
			return WholeManifest{}, fmt.Errorf(
				"validate whole-repository member %q metadata: %w",
				member.Name, err,
			)
		}
		metadataDigest, err := shardMetadataDigest(repositories, metadata)
		if err != nil || metadataDigest != member.MetadataDigest {
			if err != nil {
				return WholeManifest{}, fmt.Errorf(
					"digest whole-repository member %q metadata: %w",
					member.Name, err,
				)
			}
			return WholeManifest{}, fmt.Errorf(
				"whole-repository member %q metadata digest mismatch",
				member.Name,
			)
		}
	}
	return manifest, nil
}

func validateWholeManifestIdentity(
	manifest WholeManifest,
	repository string,
	revisions []store.IndexedRevision,
) error {
	if err := ValidateRevisions(revisions); err != nil {
		return err
	}
	if manifest.Schema != WholeManifestSchema ||
		manifest.Repository != repository ||
		!slices.Equal(manifest.Revisions, revisions) ||
		len(manifest.Members) == 0 ||
		!validDigest(manifest.Digest) {
		return errors.New("whole-repository manifest identity mismatch")
	}
	digest, err := WholeManifestDigest(manifest)
	if err != nil || digest != manifest.Digest {
		return errors.New("whole-repository manifest digest mismatch")
	}
	names := make(map[string]bool, len(manifest.Members))
	var previousName string
	for ordinal, member := range manifest.Members {
		if member.Ordinal != ordinal ||
			member.Count != len(manifest.Members) ||
			filepath.Base(member.Name) != member.Name ||
			filepath.Ext(member.Name) != ".zoekt" ||
			!validWholeShardName(repository, member.Name, ordinal) ||
			!validDigest(member.ContentDigest) ||
			member.ContentBytes <= 0 ||
			!validDigest(member.MetadataDigest) ||
			names[member.Name] ||
			(ordinal > 0 && member.Name <= previousName) {
			return errors.New(
				"whole-repository manifest member identity mismatch",
			)
		}
		names[member.Name] = true
		previousName = member.Name
	}
	return nil
}

func validateWholeRepositoryMetadata(
	repositories []*zoekt.Repository,
	repository string,
	expectedBranches []zoekt.RepositoryBranch,
) error {
	if len(repositories) != 1 ||
		repositories[0].Name != repository ||
		!slices.Equal(repositories[0].Branches, expectedBranches) {
		return errors.New(
			"whole-repository staged shard metadata mismatch",
		)
	}
	if strings.TrimSpace(
		repositories[0].Metadata["phebs.analysis_unit"],
	) != "" {
		return errors.New(
			"whole-repository staged shard carries focused metadata",
		)
	}
	return nil
}

func wholeExpectedBranches(
	revisions []store.IndexedRevision,
) []zoekt.RepositoryBranch {
	expected := make([]zoekt.RepositoryBranch, 0, len(revisions))
	for _, revision := range revisions {
		expected = append(expected, zoekt.RepositoryBranch{
			Name: revision.Branch, Version: revision.Commit,
		})
	}
	return expected
}
