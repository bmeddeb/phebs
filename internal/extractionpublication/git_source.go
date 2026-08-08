package extractionpublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// GitSparseSource is the production selected-object reader. Unlike the legacy
// extraction corpus it never walks the complete Git tree: the validated T40.8
// member supplies exact path/object/size triples, and each requested blob is a
// single bounded immutable-object read while the repository lock is held.
type GitSparseSource struct {
	DataDir    string
	OpenDomain func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error)
}

func (source GitSparseSource) AcquirePartition(
	ctx context.Context,
	plan candidate.DomainResultPlan,
	ordinal int,
) (PartitionLease, error) {
	if !filepath.IsAbs(source.DataDir) || source.OpenDomain == nil ||
		ordinal < 0 || ordinal >= len(plan.Expected) {
		return nil, invalid("git sparse source configuration")
	}
	domain, err := source.OpenDomain(ctx, plan)
	if err != nil {
		return nil, err
	}
	if domain == nil || candidate.ValidateDomainResultPlan(plan, domain) != nil ||
		!gitobj.IsObjectID(domain.Commit()) {
		return nil, invalid("git sparse source domain authority")
	}
	partitions := domain.Partitions()
	if ordinal >= len(partitions) || partitions[ordinal].Digest != plan.Expected[ordinal].PartitionDigest {
		return nil, invalid("git sparse source partition authority")
	}
	partition := partitions[ordinal]
	records := make([]candidate.Record, 0, partition.AdmittedRecords)
	var typed *candidate.SparseTypedInput
	var typedScope *candidate.SparseTypedSourceScope
	switch partition.Kind {
	case candidate.PartitionKindCandidateMember:
		err = domain.ReadPartition(ctx, ordinal, func(record candidate.Record) error {
			records = append(records, record)
			return nil
		})
	case candidate.PartitionKindTypedInput:
		input, inputErr := domain.TypedInputPartition(ordinal)
		err = inputErr
		if inputErr == nil {
			typed = &input
			scope, scopeErr := domain.TypedSourceScope(ctx, ordinal)
			err = scopeErr
			if scopeErr == nil {
				typedScope = &scope
			}
		}
	default:
		err = invalid("git sparse source partition kind")
	}
	if err != nil {
		return nil, err
	}
	if len(records) != partition.SourceEnd-partition.SourceStart ||
		len(records) != partition.AdmittedRecords {
		return nil, invalid("git sparse source range")
	}
	repositoryDirectory, err := phebssync.SafeRepoDir(source.DataDir, plan.Repository)
	if err != nil {
		return nil, err
	}
	release, err := repowork.LockContext(ctx, repositoryDirectory)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, invalid("git sparse source lock")
	}
	if err := gitobj.RejectAlternates(repositoryDirectory); err != nil {
		release()
		return nil, err
	}
	corpus, err := newObjectCorpus(
		plan.Repository, domain.Commit(), repositoryDirectory, records, typed, typedScope,
	)
	if err != nil {
		release()
		return nil, err
	}
	memberBytes, members := int64(0), int64(0)
	if partition.Member != nil {
		memberBytes, members = partition.Member.ContentBytes, 1
	}
	return &sparseLease{
		corpus: corpus, candidateRecords: len(records), typed: typed,
		typedSourceRecords: typedScopeRecords(typedScope),
		memberBytes:        memberBytes, members: members, release: release,
	}, nil
}

type selectedObject struct {
	oid   string
	bytes int64
}

type objectCorpus struct {
	repository, commit, directory string
	paths                         []string
	objects                       map[string]selectedObject
	typed                         *candidate.SparseTypedInput
	typedScope                    *candidate.SparseTypedSourceScope
}

func newObjectCorpus(
	repository,
	commit,
	directory string,
	records []candidate.Record,
	typed *candidate.SparseTypedInput,
	typedScope *candidate.SparseTypedSourceScope,
) (*objectCorpus, error) {
	corpus := &objectCorpus{
		repository: repository, commit: commit, directory: directory,
		paths: make([]string, 0, len(records)), objects: make(map[string]selectedObject, len(records)),
		typed: typed, typedScope: typedScope,
	}
	if (typed == nil) != (typedScope == nil) {
		return nil, invalid("git sparse typed source scope")
	}
	for _, record := range records {
		if record.Path == "" || !gitobj.IsObjectID(record.OID) || record.DeclaredBytes < 0 {
			return nil, invalid("git sparse source record")
		}
		if _, duplicate := corpus.objects[record.Path]; duplicate {
			return nil, invalid("git sparse source duplicate path")
		}
		corpus.paths = append(corpus.paths, record.Path)
		corpus.objects[record.Path] = selectedObject{oid: record.OID, bytes: record.DeclaredBytes}
	}
	return corpus, nil
}

func (corpus *objectCorpus) RepoName() string { return corpus.repository }
func (corpus *objectCorpus) Commit() string   { return corpus.commit }

func (corpus *objectCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	if visit == nil {
		return errors.New("git sparse corpus visitor is nil")
	}
	if corpus.typedScope != nil {
		return corpus.typedScope.Walk(ctx, visit)
	}
	for _, path := range corpus.paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

func (corpus *objectCorpus) SCIPTypedPartition() bool { return corpus.typedScope != nil }

func (corpus *objectCorpus) Read(ctx context.Context, path string) (sdk.Blob, error) {
	object, present := corpus.objects[path]
	if !present && corpus.typedScope != nil {
		oid, declaredBytes, admitted := corpus.typedScope.Source(path)
		if admitted {
			object, present = selectedObject{oid: oid, bytes: declaredBytes}, true
		}
	}
	if !present {
		return sdk.Blob{}, store.ErrNotFound
	}
	content, err := gitobj.ReadBlob(ctx, corpus.directory, object.oid, object.bytes)
	if err != nil {
		return sdk.Blob{}, fmt.Errorf("read selected immutable object: %w", err)
	}
	if int64(len(content)) != object.bytes {
		return sdk.Blob{}, errors.New("selected immutable object size changed")
	}
	digest := sha256.Sum256(content)
	return sdk.Blob{Content: string(content), Digest: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func (corpus *objectCorpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	if corpus.typed == nil || !corpus.typed.Present {
		return sdk.SCIPInput{}, nil
	}
	content, err := gitobj.ReadBlob(
		ctx, corpus.directory, corpus.typed.ObjectID, corpus.typed.DeclaredBytes,
	)
	if err != nil {
		return sdk.SCIPInput{}, fmt.Errorf("read selected typed input: %w", err)
	}
	if int64(len(content)) != corpus.typed.DeclaredBytes {
		return sdk.SCIPInput{}, errors.New("selected typed input size changed")
	}
	digest := sha256.Sum256(content)
	return sdk.SCIPInput{
		Path: corpus.typed.Path, Present: true,
		Blob: sdk.Blob{Content: string(content), Digest: "sha256:" + hex.EncodeToString(digest[:])},
	}, nil
}

func (corpus *objectCorpus) SCIPDocumentInScope(path string) bool {
	if corpus.typedScope != nil {
		return corpus.typedScope.Contains(path)
	}
	_, present := corpus.objects[path]
	return present
}

func (corpus *objectCorpus) SCIPDocumentIncluded(path string) bool {
	return corpus.SCIPDocumentInScope(path)
}

var _ Source = GitSparseSource{}
var _ sdk.Corpus = (*objectCorpus)(nil)
var _ sdk.SCIPCorpus = (*objectCorpus)(nil)
