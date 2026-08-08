package extractionpublication

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

// SparseSource is the injectable T40.8 content boundary used by tests and
// alternate immutable stores. Production Git uses GitSparseSource. OpenDomain reopens
// the exact sparse controls named by the plan; OpenCorpus returns an immutable
// commit-bound corpus and a release function (normally the mirror lock). No
// callback is invoked during Runtime.Reconcile.
type SparseSource struct {
	OpenDomain func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error)
	OpenCorpus func(context.Context, candidate.DomainResultPlan) (sdk.Corpus, func(), error)
}

func (source SparseSource) AcquirePartition(
	ctx context.Context,
	plan candidate.DomainResultPlan,
	ordinal int,
) (PartitionLease, error) {
	if source.OpenDomain == nil || source.OpenCorpus == nil || ordinal < 0 || ordinal >= len(plan.Expected) {
		return nil, invalid("sparse source configuration")
	}
	domain, err := source.OpenDomain(ctx, plan)
	if err != nil {
		return nil, err
	}
	if domain == nil || candidate.ValidateDomainResultPlan(plan, domain) != nil {
		return nil, invalid("sparse source domain authority")
	}
	partitions := domain.Partitions()
	if ordinal >= len(partitions) || partitions[ordinal].Digest != plan.Expected[ordinal].PartitionDigest {
		return nil, invalid("sparse source partition authority")
	}
	partition := partitions[ordinal]
	paths := make([]string, 0, partition.AdmittedRecords)
	var typed *candidate.SparseTypedInput
	var typedScope *candidate.SparseTypedSourceScope
	switch partition.Kind {
	case candidate.PartitionKindCandidateMember:
		err = domain.ReadPartition(ctx, ordinal, func(record candidate.Record) error {
			paths = append(paths, record.Path)
			return nil
		})
	case candidate.PartitionKindTypedInput:
		var input candidate.SparseTypedInput
		input, err = domain.TypedInputPartition(ordinal)
		if err == nil {
			typed = &input
			scope, scopeErr := domain.TypedSourceScope(ctx, ordinal)
			err = scopeErr
			if scopeErr == nil {
				typedScope = &scope
			}
		}
	default:
		err = invalid("sparse source partition kind")
	}
	if err != nil {
		return nil, err
	}
	if len(paths) != partition.SourceEnd-partition.SourceStart || len(paths) != partition.AdmittedRecords {
		return nil, invalid("sparse source range")
	}
	corpus, release, err := source.OpenCorpus(ctx, plan)
	if err != nil {
		return nil, err
	}
	if corpus == nil || release == nil || corpus.RepoName() != plan.Repository {
		if release != nil {
			release()
		}
		return nil, invalid("sparse source corpus")
	}
	memberBytes, members := int64(0), int64(0)
	if partition.Member != nil {
		memberBytes, members = partition.Member.ContentBytes, 1
	}
	return &sparseLease{
		corpus: &partitionCorpus{
			inner: corpus, paths: paths, allowed: slicesToSet(paths), typed: typed,
			typedScope: typedScope,
		},
		candidateRecords: len(paths), typedSourceRecords: typedScopeRecords(typedScope),
		typed: typed, memberBytes: memberBytes, members: members,
		release: release,
	}, nil
}

func typedScopeRecords(scope *candidate.SparseTypedSourceScope) int {
	if scope == nil {
		return 0
	}
	return scope.Records()
}

func slicesToSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

type sparseLease struct {
	once               sync.Once
	corpus             sdk.Corpus
	candidateRecords   int
	typedSourceRecords int
	typed              *candidate.SparseTypedInput
	memberBytes        int64
	members            int64
	release            func()
}

func (lease *sparseLease) Corpus() sdk.Corpus      { return lease.corpus }
func (lease *sparseLease) CandidateRecords() int   { return lease.candidateRecords }
func (lease *sparseLease) TypedSourceRecords() int { return lease.typedSourceRecords }
func (lease *sparseLease) TypedInput() *candidate.SparseTypedInput {
	if lease.typed == nil {
		return nil
	}
	copy := *lease.typed
	return &copy
}
func (lease *sparseLease) MemberBytes() int64 { return lease.memberBytes }
func (lease *sparseLease) Members() int64     { return lease.members }
func (lease *sparseLease) Release()           { lease.once.Do(lease.release) }

type partitionCorpus struct {
	inner      sdk.Corpus
	paths      []string
	allowed    map[string]struct{}
	typed      *candidate.SparseTypedInput
	typedScope *candidate.SparseTypedSourceScope
}

func (corpus *partitionCorpus) RepoName() string { return corpus.inner.RepoName() }
func (corpus *partitionCorpus) Commit() string   { return corpus.inner.Commit() }

func (corpus *partitionCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	if visit == nil {
		return errors.New("partition corpus visitor is nil")
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

func (corpus *partitionCorpus) SCIPTypedPartition() bool { return corpus.typedScope != nil }

func (corpus *partitionCorpus) Read(ctx context.Context, path string) (sdk.Blob, error) {
	_, allowed := corpus.allowed[path]
	if !allowed && corpus.typedScope != nil {
		allowed = corpus.typedScope.Contains(path)
	}
	if !allowed {
		return sdk.Blob{}, store.ErrNotFound
	}
	return corpus.inner.Read(ctx, path)
}

func (corpus *partitionCorpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	if corpus.typed == nil {
		return sdk.SCIPInput{Present: false}, nil
	}
	typed, ok := corpus.inner.(sdk.SCIPCorpus)
	if !ok {
		return sdk.SCIPInput{}, errors.New("partition corpus has no typed-input capability")
	}
	input, err := typed.ReadSCIPIndex(ctx)
	if err != nil {
		return sdk.SCIPInput{}, err
	}
	if !input.Present || input.Path != corpus.typed.Path {
		return sdk.SCIPInput{}, fmt.Errorf("partition typed input changed: %w", ErrStale)
	}
	return input, nil
}

func (corpus *partitionCorpus) SCIPDocumentInScope(path string) bool {
	if corpus.typedScope != nil {
		return corpus.typedScope.Contains(path)
	}
	if scoped, ok := corpus.inner.(sdk.SCIPDocumentScope); ok {
		return slices.Contains(corpus.paths, path) && scoped.SCIPDocumentInScope(path)
	}
	return slices.Contains(corpus.paths, path)
}

func (corpus *partitionCorpus) SCIPDocumentIncluded(path string) bool {
	if !corpus.SCIPDocumentInScope(path) {
		return false
	}
	if filtered, ok := corpus.inner.(sdk.SCIPDocumentFilter); ok {
		return filtered.SCIPDocumentIncluded(path)
	}
	if corpus.typedScope != nil {
		return true
	}
	return slices.Contains(corpus.paths, path)
}

var _ Source = SparseSource{}
var _ sdk.Corpus = (*partitionCorpus)(nil)
var _ sdk.SCIPCorpus = (*partitionCorpus)(nil)
