package t301

import (
	"bytes"
	"context"
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
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"
	zoektsearch "github.com/sourcegraph/zoekt/search"

	"github.com/bmeddeb/phebs/internal/gitobj"
)

const (
	manifestFileName  = "t301-shard-set.json"
	memberSuffix      = ".phebs-member.json"
	maxScopeTreeBytes = 16 << 20
	maxFocusedBlob    = 4 << 20
)

type treeRecord struct {
	mode, kind, oid, path string
}

type admittedDocument struct {
	path     string
	oid      string
	branches []string
	content  []byte
}

// BuildFocusedIndex invokes the exact pinned zoekt index.Builder over only the
// selected immutable Git blobs. Callers run it in a child process.
func BuildFocusedIndex(
	ctx context.Context,
	repoDir,
	indexDir string,
	scope Scope,
	corpus Corpus,
) (BuildResult, error) {
	started := time.Now()
	corpusDigest, err := CorpusDigest(corpus)
	if err != nil {
		return BuildResult{}, err
	}
	if scope.Repository != corpus.Repository || corpus.Digest != corpusDigest {
		return BuildResult{}, errors.New("focused child corpus identity mismatch")
	}
	unitDigest, err := scope.Digest()
	if err != nil {
		return BuildResult{}, err
	}
	generationDigest, err := GenerationDigest(scope, corpus.Revisions)
	if err != nil {
		return BuildResult{}, err
	}
	records, err := resolveScopeRecords(ctx, repoDir, scope, corpus.Revisions)
	if err != nil {
		return BuildResult{}, err
	}
	documents, openedPaths, openedBytes, err := readFocusedDocuments(
		ctx, repoDir, records, corpus.Revisions,
	)
	if err != nil {
		return BuildResult{}, err
	}
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return BuildResult{}, err
	}
	repository := zoekt.Repository{
		Name: scope.Repository,
		Metadata: map[string]string{
			"phebs.analysis_unit":    ScopeVersion,
			"phebs.unit_digest":      unitDigest,
			"phebs.index_generation": generationDigest,
			"phebs.builder_policy":   BuilderPolicyVersion,
			"phebs.corpus_digest":    corpus.Digest,
		},
	}
	for _, revision := range corpus.Revisions {
		repository.Branches = append(repository.Branches, zoekt.RepositoryBranch{
			Name: revision.Branch, Version: revision.Commit,
		})
	}
	builder, err := index.NewBuilder(index.Options{
		IndexDir:              indexDir,
		ShardPrefixOverride:   "t301-focused",
		ShardMax:              256,
		Parallelism:           1,
		DisableCTags:          true,
		RepositoryDescription: repository,
	})
	if err != nil {
		return BuildResult{}, fmt.Errorf("construct pinned zoekt builder: %w", err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = builder.Finish()
		}
	}()
	var admittedBytes int64
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return BuildResult{}, err
		}
		if err := builder.Add(index.Document{
			Name: document.path, Content: document.content,
			Branches: append([]string(nil), document.branches...),
		}); err != nil {
			return BuildResult{}, fmt.Errorf("add focused document %q: %w", document.path, err)
		}
		admittedBytes += int64(len(document.content))
	}
	if os.Getenv("T301_INJECT_CHILD_FAILURE") == "1" {
		return BuildResult{}, errors.New("injected T30.1 child failure")
	}
	if err := builder.Finish(); err != nil {
		return BuildResult{}, fmt.Errorf("finish pinned zoekt builder: %w", err)
	}
	finished = true
	manifest, shardBytes, err := publishManifest(
		indexDir, scope.Repository, unitDigest, generationDigest,
		corpus.Digest, repository.Branches,
	)
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{
		Schema: SchemaVersion, Repository: scope.Repository,
		CorpusDigest: corpus.Digest, UnitDigest: unitDigest,
		GenerationDigest: generationDigest, ManifestDigest: manifest.Digest,
		OpenedPaths: openedPaths, OpenedBlobBytes: openedBytes,
		AdmittedDocuments: len(documents), AdmittedSourceBytes: admittedBytes,
		ShardCount: len(manifest.Members), ShardBytes: shardBytes,
		BuildWallNanos: time.Since(started).Nanoseconds(),
	}, nil
}

func resolveScopeRecords(
	ctx context.Context,
	repoDir string,
	scope Scope,
	revisions []Revision,
) (map[string]map[string]treeRecord, error) {
	if _, err := scope.Canonical(); err != nil {
		return nil, err
	}
	paths := append(append([]string(nil), scope.Primary...), scope.Supporting...)
	sort.Strings(paths)
	byRevision := make(map[string]map[string]treeRecord, len(revisions))
	for _, revision := range revisions {
		current := make(map[string]treeRecord)
		for _, selected := range paths {
			exact, err := listTree(
				ctx, repoDir, revision.Commit, false, selected,
			)
			if err != nil {
				return nil, err
			}
			if len(exact) != 1 || exact[0].path != selected {
				return nil, fmt.Errorf(
					"%w at %s: %q", ErrMissingScope,
					revision.Selector, selected,
				)
			}
			switch exact[0].kind {
			case "tree":
				descendants, listErr := listTree(
					ctx, repoDir, revision.Commit, true, selected,
				)
				if listErr != nil {
					return nil, listErr
				}
				for _, record := range descendants {
					if err := admitTreeRecord(revision, selected, record, current); err != nil {
						return nil, err
					}
				}
			case "blob":
				if err := admitTreeRecord(revision, selected, exact[0], current); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf(
					"%w at %s: selected %q is %s mode %s",
					ErrSpecialEntry, revision.Selector, selected,
					exact[0].kind, exact[0].mode,
				)
			}
		}
		byRevision[revision.Selector] = current
	}
	return byRevision, nil
}

func admitTreeRecord(
	revision Revision,
	selected string,
	record treeRecord,
	destination map[string]treeRecord,
) error {
	if _, err := canonicalPaths([]string{record.path}, true); err != nil ||
		(record.path != selected && !strings.HasPrefix(record.path, selected+"/")) {
		return fmt.Errorf(
			"%w at %s: unsafe descendant %q under %q",
			ErrSpecialEntry, revision.Selector, record.path, selected,
		)
	}
	if record.kind != "blob" ||
		(record.mode != "100644" && record.mode != "100755") {
		return fmt.Errorf(
			"%w at %s: %q under %q is %s mode %s",
			ErrSpecialEntry, revision.Selector, record.path, selected,
			record.kind, record.mode,
		)
	}
	if previous, duplicate := destination[record.path]; duplicate &&
		(previous.oid != record.oid || previous.mode != record.mode) {
		return fmt.Errorf("selected path %q resolved inconsistently", record.path)
	}
	destination[record.path] = record
	return nil
}

func readFocusedDocuments(
	ctx context.Context,
	repoDir string,
	records map[string]map[string]treeRecord,
	revisions []Revision,
) ([]admittedDocument, []string, int64, error) {
	type key struct{ path, oid string }
	grouped := make(map[key]*admittedDocument)
	branchBySelector := make(map[string]string, len(revisions))
	for _, revision := range revisions {
		branchBySelector[revision.Selector] = revision.Branch
	}
	selectors := make([]string, 0, len(records))
	for selector := range records {
		selectors = append(selectors, selector)
	}
	sort.Strings(selectors)
	for _, selector := range selectors {
		paths := make([]string, 0, len(records[selector]))
		for filePath := range records[selector] {
			paths = append(paths, filePath)
		}
		sort.Strings(paths)
		for _, filePath := range paths {
			record := records[selector][filePath]
			entry := grouped[key{path: filePath, oid: record.oid}]
			if entry == nil {
				content, err := gitobj.ReadBlob(
					ctx, repoDir, record.oid, maxFocusedBlob,
				)
				if err != nil {
					return nil, nil, 0, fmt.Errorf(
						"read focused blob %s:%s: %w",
						selector, filePath, err,
					)
				}
				entry = &admittedDocument{
					path: filePath, oid: record.oid, content: content,
				}
				grouped[key{path: filePath, oid: record.oid}] = entry
			}
			entry.branches = append(entry.branches, branchBySelector[selector])
		}
	}
	documents := make([]admittedDocument, 0, len(grouped))
	opened := make([]string, 0, len(grouped))
	var openedBytes int64
	for _, document := range grouped {
		slices.Sort(document.branches)
		documents = append(documents, *document)
		opened = append(opened, document.path)
		openedBytes += int64(len(document.content))
	}
	sort.Slice(documents, func(left, right int) bool {
		if documents[left].path != documents[right].path {
			return documents[left].path < documents[right].path
		}
		return documents[left].oid < documents[right].oid
	})
	sort.Strings(opened)
	return documents, opened, openedBytes, nil
}

func listTree(
	ctx context.Context,
	repoDir,
	commit string,
	recursive bool,
	selected string,
) ([]treeRecord, error) {
	args := []string{"ls-tree", "-z", "--full-tree"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, commit, "--", ":(literal)"+selected)
	output, err := gitobj.Output(ctx, repoDir, maxScopeTreeBytes, args...)
	if err != nil {
		return nil, fmt.Errorf("list selected tree %q: %w", selected, err)
	}
	if len(output) == 0 {
		return nil, nil
	}
	if output[len(output)-1] != 0 {
		return nil, errors.New("selected tree output is truncated")
	}
	rawRecords := bytes.Split(output[:len(output)-1], []byte{0})
	result := make([]treeRecord, 0, len(rawRecords))
	for _, raw := range rawRecords {
		tab := bytes.IndexByte(raw, '\t')
		if tab < 0 {
			return nil, errors.New("selected tree record has no path")
		}
		fields := strings.Fields(string(raw[:tab]))
		if len(fields) != 3 || !validObjectID(fields[2]) {
			return nil, errors.New("selected tree record metadata is malformed")
		}
		result = append(result, treeRecord{
			mode: fields[0], kind: fields[1], oid: fields[2],
			path: string(raw[tab+1:]),
		})
	}
	return result, nil
}

func publishManifest(
	indexDir,
	repository,
	unitDigest,
	generationDigest,
	corpusDigest string,
	expectedBranches []zoekt.RepositoryBranch,
) (ShardManifest, int64, error) {
	shards, err := filepath.Glob(filepath.Join(indexDir, "*.zoekt"))
	if err != nil {
		return ShardManifest{}, 0, err
	}
	sort.Strings(shards)
	if len(shards) < 2 {
		return ShardManifest{}, 0, fmt.Errorf(
			"T30.1 spike expected a physical split, got %d shard(s)", len(shards),
		)
	}
	manifest := ShardManifest{
		Schema: ManifestVersion, Repository: repository,
		CorpusDigest: corpusDigest, UnitDigest: unitDigest,
		GenerationDigest: generationDigest, BuilderPolicy: BuilderPolicyVersion,
	}
	var shardBytes int64
	for ordinal, shardPath := range shards {
		contentDigest, size, err := digestRegularFile(shardPath)
		if err != nil {
			return ShardManifest{}, 0, err
		}
		repositories, metadata, err := index.ReadMetadataPath(shardPath)
		if err != nil {
			return ShardManifest{}, 0, err
		}
		if len(repositories) != 1 ||
			repositories[0].Name != repository ||
			!slices.Equal(repositories[0].Branches, expectedBranches) ||
			repositories[0].Metadata["phebs.analysis_unit"] != ScopeVersion ||
			repositories[0].Metadata["phebs.unit_digest"] != unitDigest ||
			repositories[0].Metadata["phebs.index_generation"] != generationDigest ||
			repositories[0].Metadata["phebs.builder_policy"] != BuilderPolicyVersion ||
			repositories[0].Metadata["phebs.corpus_digest"] != corpusDigest {
			return ShardManifest{}, 0, fmt.Errorf(
				"%w: shard %q has mismatched repository metadata",
				ErrShardSet, filepath.Base(shardPath),
			)
		}
		metadataBytes, err := json.Marshal(struct {
			Repositories []*zoekt.Repository  `json:"repositories"`
			Index        *zoekt.IndexMetadata `json:"index"`
		}{Repositories: repositories, Index: metadata})
		if err != nil {
			return ShardManifest{}, 0, err
		}
		member := ShardMember{
			Ordinal: ordinal, Count: len(shards),
			Name: filepath.Base(shardPath), ContentDigest: contentDigest,
			MetadataDigest: digestBytes("phebs-shard-metadata-v1\x00", metadataBytes),
			UnitDigest:     unitDigest, GenerationDigest: generationDigest,
		}
		sidecar, err := json.Marshal(member)
		if err != nil {
			return ShardManifest{}, 0, err
		}
		if err := os.WriteFile(shardPath+memberSuffix, append(sidecar, '\n'), 0o600); err != nil {
			return ShardManifest{}, 0, err
		}
		manifest.Members = append(manifest.Members, member)
		shardBytes += size
	}
	manifest.Digest, err = ManifestDigest(manifest)
	if err != nil {
		return ShardManifest{}, 0, err
	}
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ShardManifest{}, 0, err
	}
	tempPath := filepath.Join(indexDir, "."+manifestFileName+".tmp")
	if err := os.WriteFile(tempPath, append(content, '\n'), 0o600); err != nil {
		return ShardManifest{}, 0, err
	}
	if err := os.Rename(tempPath, filepath.Join(indexDir, manifestFileName)); err != nil {
		return ShardManifest{}, 0, err
	}
	return manifest, shardBytes, nil
}

// ValidateShardSet proves exact expected membership before a searcher exists.
func ValidateShardSet(indexDir string) (ShardManifest, error) {
	raw, err := os.ReadFile(filepath.Join(indexDir, manifestFileName))
	if err != nil {
		return ShardManifest{}, fmt.Errorf("%w: read visibility manifest: %v", ErrShardSet, err)
	}
	var manifest ShardManifest
	if err := decodeJSONStrict(raw, &manifest); err != nil {
		return ShardManifest{}, fmt.Errorf("%w: decode manifest: %v", ErrShardSet, err)
	}
	if manifest.Schema != ManifestVersion ||
		!validIdentityText(manifest.Repository) ||
		!validDigest(manifest.CorpusDigest) ||
		!validDigest(manifest.UnitDigest) ||
		!validDigest(manifest.GenerationDigest) ||
		manifest.BuilderPolicy != BuilderPolicyVersion ||
		len(manifest.Members) == 0 {
		return ShardManifest{}, fmt.Errorf("%w: invalid manifest envelope", ErrShardSet)
	}
	digest, err := ManifestDigest(manifest)
	if err != nil || digest != manifest.Digest {
		return ShardManifest{}, fmt.Errorf("%w: manifest digest mismatch", ErrShardSet)
	}
	actual, err := filepath.Glob(filepath.Join(indexDir, "*.zoekt"))
	if err != nil {
		return ShardManifest{}, err
	}
	actualNames := make([]string, len(actual))
	for index := range actual {
		actualNames[index] = filepath.Base(actual[index])
	}
	sort.Strings(actualNames)
	expectedNames := make([]string, len(manifest.Members))
	for index, member := range manifest.Members {
		if member.Ordinal != index || member.Count != len(manifest.Members) ||
			member.UnitDigest != manifest.UnitDigest ||
			member.GenerationDigest != manifest.GenerationDigest ||
			filepath.Base(member.Name) != member.Name ||
			filepath.Ext(member.Name) != ".zoekt" ||
			!validDigest(member.ContentDigest) ||
			!validDigest(member.MetadataDigest) {
			return ShardManifest{}, fmt.Errorf("%w: member ordering or identity mismatch", ErrShardSet)
		}
		expectedNames[index] = member.Name
	}
	sort.Strings(expectedNames)
	if !slices.Equal(actualNames, expectedNames) {
		return ShardManifest{}, fmt.Errorf(
			"%w: exact membership mismatch: actual=%v expected=%v",
			ErrShardSet, actualNames, expectedNames,
		)
	}
	var expectedBranches []zoekt.RepositoryBranch
	for _, member := range manifest.Members {
		shardPath := filepath.Join(indexDir, member.Name)
		contentDigest, _, err := digestRegularFile(shardPath)
		if err != nil || contentDigest != member.ContentDigest {
			return ShardManifest{}, fmt.Errorf("%w: content mismatch for %q", ErrShardSet, member.Name)
		}
		sidecarRaw, err := os.ReadFile(shardPath + memberSuffix)
		if err != nil {
			return ShardManifest{}, fmt.Errorf("%w: sidecar missing for %q", ErrShardSet, member.Name)
		}
		var sidecar ShardMember
		if err := decodeJSONStrict(sidecarRaw, &sidecar); err != nil || sidecar != member {
			return ShardManifest{}, fmt.Errorf("%w: sidecar mismatch for %q", ErrShardSet, member.Name)
		}
		repositories, metadata, err := index.ReadMetadataPath(shardPath)
		if err != nil {
			return ShardManifest{}, fmt.Errorf("%w: read metadata for %q: %v", ErrShardSet, member.Name, err)
		}
		if len(repositories) != 1 ||
			repositories[0].Name != manifest.Repository ||
			repositories[0].Metadata["phebs.analysis_unit"] != ScopeVersion ||
			repositories[0].Metadata["phebs.unit_digest"] != manifest.UnitDigest ||
			repositories[0].Metadata["phebs.index_generation"] != manifest.GenerationDigest ||
			repositories[0].Metadata["phebs.builder_policy"] != manifest.BuilderPolicy ||
			repositories[0].Metadata["phebs.corpus_digest"] != manifest.CorpusDigest {
			return ShardManifest{}, fmt.Errorf(
				"%w: repository identity mismatch for %q",
				ErrShardSet, member.Name,
			)
		}
		if expectedBranches == nil {
			expectedBranches = slices.Clone(repositories[0].Branches)
		} else if !slices.Equal(expectedBranches, repositories[0].Branches) {
			return ShardManifest{}, fmt.Errorf(
				"%w: revision metadata mismatch for %q",
				ErrShardSet, member.Name,
			)
		}
		metadataBytes, err := json.Marshal(struct {
			Repositories []*zoekt.Repository  `json:"repositories"`
			Index        *zoekt.IndexMetadata `json:"index"`
		}{Repositories: repositories, Index: metadata})
		if err != nil ||
			digestBytes("phebs-shard-metadata-v1\x00", metadataBytes) != member.MetadataDigest {
			return ShardManifest{}, fmt.Errorf("%w: metadata mismatch for %q", ErrShardSet, member.Name)
		}
	}
	return manifest, nil
}

func decodeJSONStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func validDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func SearchVisible(
	ctx context.Context,
	indexDir,
	expression string,
) ([]string, error) {
	if _, err := ValidateShardSet(indexDir); err != nil {
		return nil, err
	}
	searcher, err := zoektsearch.NewDirectorySearcher(indexDir)
	if err != nil {
		return nil, err
	}
	defer searcher.Close()
	parsed, err := query.Parse(expression)
	if err != nil {
		return nil, err
	}
	result, err := searcher.Search(ctx, parsed, &zoekt.SearchOptions{})
	if err != nil {
		return nil, err
	}
	hits := make([]string, 0, len(result.Files))
	for _, file := range result.Files {
		hits = append(hits, file.Repository+":"+file.FileName)
	}
	sort.Strings(hits)
	return hits, nil
}

func digestRegularFile(filePath string) (string, int64, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%w: %q is not a regular file", ErrShardSet, filePath)
	}
	file, err := os.Open(filePath)
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
