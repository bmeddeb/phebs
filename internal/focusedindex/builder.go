package focusedindex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	maxScopeTreeBytes  = 16 << 20
	maxFocusedBlob     = 64 << 20
	maxFocusedTrigrams = 20_000
	defaultShardMax    = 64 << 20
)

type BuildOptions struct {
	// ShardMax is test-only tuning when non-zero. Production callers and the
	// child use the fixed default.
	ShardMax int
}

type treeRecord struct {
	mode string
	kind string
	oid  string
	path string
}

type document struct {
	path     string
	oid      string
	branches []string
}

type blobKey struct {
	path string
	oid  string
}

// countingBlobReader is the trusted Git-object read boundary. It refuses and
// counts any path/object pair not established by the selected tree walk before
// invoking git cat-file.
type countingBlobReader struct {
	repoDir       string
	allowed       map[blobKey]bool
	openedCount   int
	openedBytes   int64
	outOfUnitRead int
}

func (reader *countingBlobReader) Read(ctx context.Context, path, oid string) ([]byte, error) {
	if !reader.allowed[blobKey{path: path, oid: oid}] {
		reader.outOfUnitRead++
		return nil, fmt.Errorf("refuse out-of-unit blob read for %q", path)
	}
	content, err := gitobj.ReadBlob(ctx, reader.repoDir, oid, maxFocusedBlob)
	if err != nil {
		return nil, err
	}
	reader.openedCount++
	reader.openedBytes += int64(len(content))
	return content, nil
}

func Build(ctx context.Context, request Request, options BuildOptions) (Result, error) {
	started := time.Now()
	if request.Schema != RequestSchema {
		return Result{}, errors.New("focused build request schema mismatch")
	}
	if request.Scope.Repository == "" || request.Scope.Repository != filepath.ToSlash(request.Scope.Repository) {
		return Result{}, errors.New("focused build repository identity is invalid")
	}
	if _, err := request.Scope.Canonical(); err != nil {
		return Result{}, err
	}
	if err := ValidateRevisions(request.Revisions); err != nil {
		return Result{}, err
	}
	if !filepath.IsAbs(request.RepoDir) || !filepath.IsAbs(request.OutputDir) {
		return Result{}, errors.New("focused build paths must be absolute")
	}
	if err := requireEmptyDirectory(request.OutputDir); err != nil {
		return Result{}, err
	}
	unitDigest, err := request.Scope.Digest()
	if err != nil {
		return Result{}, err
	}
	scopeState, err := request.Scope.State()
	if err != nil {
		return Result{}, err
	}
	canonicalScope := analysisunit.Scope{
		Repository: request.Scope.Repository,
		Name:       scopeState.Name,
		Primary:    scopeState.PrimaryPaths,
		Supporting: scopeState.SupportingPaths,
	}
	generationDigest, err := GenerationDigest(request.Scope, request.Revisions)
	if err != nil {
		return Result{}, err
	}
	records, err := resolveScopeRecords(
		ctx, request.RepoDir, request.Scope.Primary, request.Scope.Supporting,
		request.Revisions,
	)
	if err != nil {
		return Result{}, err
	}
	documents, reader := planDocuments(request.RepoDir, records, request.Revisions)
	repository := zoekt.Repository{
		Name: request.Scope.Repository,
		Metadata: map[string]string{
			"phebs.analysis_unit":    analysisunit.SchemaVersion,
			"phebs.unit_digest":      unitDigest,
			"phebs.index_generation": generationDigest,
			"phebs.builder_policy":   BuilderPolicy,
		},
	}
	for _, revision := range request.Revisions {
		repository.Branches = append(repository.Branches, zoekt.RepositoryBranch{
			Name: revision.Branch, Version: revision.Commit,
		})
	}
	shardMax := options.ShardMax
	if shardMax <= 0 {
		shardMax = defaultShardMax
	}
	builder, err := index.NewBuilder(index.Options{
		IndexDir:              request.OutputDir,
		ShardPrefixOverride:   ShardPrefix(request.Scope.Repository, generationDigest),
		SizeMax:               maxFocusedBlob,
		TrigramMax:            maxFocusedTrigrams,
		ShardMax:              shardMax,
		Parallelism:           1,
		DisableCTags:          true,
		RepositoryDescription: repository,
	})
	if err != nil {
		return Result{}, fmt.Errorf("construct pinned zoekt builder: %w", err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = builder.Finish()
		}
	}()
	var admittedBytes int64
	var contentChecker index.DocChecker
	for _, current := range documents {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		content, err := reader.Read(ctx, current.path, current.oid)
		if err != nil {
			return Result{}, fmt.Errorf("read focused blob %q: %w", current.path, err)
		}
		if reason := contentChecker.Check(
			content, maxFocusedTrigrams, false,
		); reason != index.SkipReasonNone {
			return Result{}, fmt.Errorf(
				"focused content policy refuses %q: %s",
				current.path, focusedSkipReason(reason),
			)
		}
		if err := builder.Add(index.Document{
			Name:     current.path,
			Content:  content,
			Branches: slices.Clone(current.branches),
		}); err != nil {
			return Result{}, fmt.Errorf("add focused document %q: %w", current.path, err)
		}
		admittedBytes += int64(len(content))
	}
	if os.Getenv("PHEBS_FOCUSED_INJECT_FAILURE") == "1" {
		return Result{}, errors.New("injected focused-index child failure")
	}
	if err := builder.Finish(); err != nil {
		return Result{}, fmt.Errorf("finish pinned zoekt builder: %w", err)
	}
	finished = true
	manifest, shardBytes, err := createManifest(
		request.OutputDir, canonicalScope, unitDigest,
		generationDigest, request.Revisions, repository.Branches,
	)
	if err != nil {
		return Result{}, err
	}
	if reader.outOfUnitRead != 0 {
		return Result{}, fmt.Errorf("focused reader attempted %d out-of-unit blob reads", reader.outOfUnitRead)
	}
	return Result{
		Schema:              ResultSchema,
		Repository:          request.Scope.Repository,
		UnitDigest:          unitDigest,
		GenerationDigest:    generationDigest,
		ManifestDigest:      manifest.Digest,
		OpenedBlobCount:     reader.openedCount,
		OpenedBlobBytes:     reader.openedBytes,
		OutOfUnitBlobReads:  reader.outOfUnitRead,
		AdmittedDocuments:   len(documents),
		AdmittedSourceBytes: admittedBytes,
		ShardCount:          len(manifest.Members),
		ShardBytes:          shardBytes,
		BuildWallNanos:      time.Since(started).Nanoseconds(),
	}, nil
}

func focusedSkipReason(reason index.SkipReason) string {
	switch reason {
	case index.SkipReasonTooLarge:
		return "exceeds the maximum size limit"
	case index.SkipReasonTooSmall:
		return "contains too few trigrams"
	case index.SkipReasonBinary:
		return "contains binary content"
	case index.SkipReasonTooManyTrigrams:
		return "contains too many distinct trigrams"
	case index.SkipReasonMissing:
		return "content is missing"
	default:
		return fmt.Sprintf("unknown skip reason %d", reason)
	}
}

func resolveScopeRecords(
	ctx context.Context,
	repoDir string,
	primary, supporting []string,
	revisions []store.IndexedRevision,
) (map[string]map[string]treeRecord, error) {
	paths := append(append([]string(nil), primary...), supporting...)
	sort.Strings(paths)
	byRevision := make(map[string]map[string]treeRecord, len(revisions))
	for _, revision := range revisions {
		current := make(map[string]treeRecord)
		for _, selected := range paths {
			exact, err := listTree(ctx, repoDir, revision.Commit, false, selected)
			if err != nil {
				return nil, err
			}
			if len(exact) != 1 || exact[0].path != selected {
				return nil, fmt.Errorf(
					"%w at %s: %q", ErrMissingScope, revision.Selector, selected,
				)
			}
			switch exact[0].kind {
			case "tree":
				descendants, listErr := listTree(ctx, repoDir, revision.Commit, true, selected)
				if listErr != nil {
					return nil, listErr
				}
				for _, record := range descendants {
					if err := admitRecord(revision.Selector, selected, record, current); err != nil {
						return nil, err
					}
				}
			case "blob":
				if err := admitRecord(revision.Selector, selected, exact[0], current); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf(
					"%w at %s: selected %q is %s mode %s",
					ErrSpecialEntry, revision.Selector, selected, exact[0].kind, exact[0].mode,
				)
			}
		}
		byRevision[revision.Selector] = current
	}
	return byRevision, nil
}

func admitRecord(
	selector, selected string,
	record treeRecord,
	destination map[string]treeRecord,
) error {
	if repopath.Validate(record.path) != nil ||
		(record.path != selected && !strings.HasPrefix(record.path, selected+"/")) {
		return fmt.Errorf(
			"%w at %s: unsafe descendant %q under %q",
			ErrSpecialEntry, selector, record.path, selected,
		)
	}
	if record.kind != "blob" || (record.mode != "100644" && record.mode != "100755") {
		return fmt.Errorf(
			"%w at %s: %q under %q is %s mode %s",
			ErrSpecialEntry, selector, record.path, selected, record.kind, record.mode,
		)
	}
	if previous, duplicate := destination[record.path]; duplicate &&
		(previous.oid != record.oid || previous.mode != record.mode) {
		return fmt.Errorf("selected path %q resolved inconsistently", record.path)
	}
	destination[record.path] = record
	return nil
}

func planDocuments(
	repoDir string,
	records map[string]map[string]treeRecord,
	revisions []store.IndexedRevision,
) ([]document, *countingBlobReader) {
	allowed := make(map[blobKey]bool)
	for _, revisionRecords := range records {
		for _, record := range revisionRecords {
			allowed[blobKey{path: record.path, oid: record.oid}] = true
		}
	}
	reader := &countingBlobReader{repoDir: repoDir, allowed: allowed}
	grouped := make(map[blobKey]*document)
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
		for path := range records[selector] {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			record := records[selector][path]
			key := blobKey{path: path, oid: record.oid}
			entry := grouped[key]
			if entry == nil {
				entry = &document{path: path, oid: record.oid}
				grouped[key] = entry
			}
			entry.branches = append(entry.branches, branchBySelector[selector])
		}
	}
	documents := make([]document, 0, len(grouped))
	for _, current := range grouped {
		sort.Strings(current.branches)
		documents = append(documents, *current)
	}
	sort.Slice(documents, func(left, right int) bool {
		if documents[left].path != documents[right].path {
			return documents[left].path < documents[right].path
		}
		return documents[left].oid < documents[right].oid
	})
	return documents, reader
}

func listTree(
	ctx context.Context,
	repoDir, commit string,
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
		if len(fields) != 3 || !gitobj.IsObjectID(fields[2]) {
			return nil, errors.New("selected tree record metadata is malformed")
		}
		result = append(result, treeRecord{
			mode: fields[0], kind: fields[1], oid: fields[2], path: string(raw[tab+1:]),
		})
	}
	return result, nil
}

func requireEmptyDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect focused staging directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("focused staging path is not a real directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("focused staging directory is not empty")
	}
	return nil
}
