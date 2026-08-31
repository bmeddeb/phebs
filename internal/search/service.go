package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

// ServiceRequest is the internal T34.3 reader boundary. T34.4 owns transport
// schemas and scope selection; this request already names one repository-local
// service and one indexed selector.
type ServiceRequest struct {
	Repository       string
	ServiceKey       string
	Expression       string
	RevisionSelector string
}

type ServiceResult struct {
	Result    *Result
	Authority servicequery.Authority
}

type serviceScopeRuntime struct {
	open    func(context.Context, string, string) (servicequery.RuntimeScope, error)
	confirm func(context.Context, servicequery.RuntimeScope) error
}

// SearchService runs one exact v2 service query against an immutable direct
// whole-generation lease. Placement predicates execute before ranking; no
// result-time service filter is permitted. Every result is discarded if the
// repository, catalog/state, control roots, member identities, or active
// reader generation changes before emission.
func (s *Searcher) SearchService(
	ctx context.Context,
	request ServiceRequest,
	opts Options,
) (*ServiceResult, error) {
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("service search: runtime authority is unavailable")
	}
	runtimeStore, ok := s.st.(servicequery.RuntimeStore)
	runtime := serviceScopeRuntime{}
	if ok {
		runtime.open = func(
			ctx context.Context, repository, serviceKey string,
		) (servicequery.RuntimeScope, error) {
			return servicequery.OpenRuntimeScope(
				ctx, s.indexDir, runtimeStore, repository, serviceKey,
			)
		}
		runtime.confirm = func(ctx context.Context, scope servicequery.RuntimeScope) error {
			return servicequery.ConfirmRuntimeScope(
				ctx, s.indexDir, runtimeStore, scope,
			)
		}
	}
	return s.searchService(ctx, request, opts, runtime)
}

// SearchServiceV3 is the explicit runtime-dark segmented-catalog search seam.
// It shares the complete authorization, exact-reader, query, and result path
// with SearchService while substituting only the v3 scope open/final fence.
// No production caller selects this method; T41.9 owns runtime selection.
func (s *Searcher) SearchServiceV3(
	ctx context.Context,
	reader *store.ServiceStateV3Reader,
	request ServiceRequest,
	opts Options,
) (*ServiceResult, error) {
	if s == nil || s.st == nil {
		return nil, fmt.Errorf("service search: runtime authority is unavailable")
	}
	runtimeStore, ok := s.st.(servicequery.RuntimeRepositoryStore)
	runtime := serviceScopeRuntime{}
	if ok && reader != nil {
		runtime.open = func(
			ctx context.Context, repository, serviceKey string,
		) (servicequery.RuntimeScope, error) {
			return servicequery.OpenRuntimeScopeV3(
				ctx, s.indexDir, runtimeStore, reader, repository, serviceKey,
			)
		}
		runtime.confirm = func(ctx context.Context, scope servicequery.RuntimeScope) error {
			return servicequery.ConfirmRuntimeScopeV3(
				ctx, s.indexDir, runtimeStore, reader, scope,
			)
		}
	}
	return s.searchService(ctx, request, opts, runtime)
}

func (s *Searcher) searchService(
	ctx context.Context,
	request ServiceRequest,
	opts Options,
	runtime serviceScopeRuntime,
) (*ServiceResult, error) {
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, s.queryWallTime())
	defer cancel()
	if _, err := s.authorizeServiceRepository(ctx, request.Repository); err != nil {
		return nil, fmt.Errorf("service search: repository: %w", err)
	}
	if s.whole == nil || runtime.open == nil || runtime.confirm == nil {
		return nil, fmt.Errorf("service search: runtime authority is unavailable")
	}
	opened, err := runtime.open(ctx, request.Repository, request.ServiceKey)
	if err != nil {
		return nil, fmt.Errorf("service search: %w", err)
	}
	defer opened.Close()
	prepared, valid := opened.Prepared()
	searchGeneration, searchValid := opened.Search()
	if !valid || !searchValid {
		return nil, fmt.Errorf("service search: invalid runtime scope")
	}
	repoCache, err := s.whole.repo(request.Repository)
	if err != nil {
		return nil, fmt.Errorf("service search: %w", err)
	}
	lease, err := s.whole.acquireExact(
		ctx, repoCache, request.Repository, searchGeneration.Revisions,
	)
	if err == nil && lease != nil &&
		!lease.matchesSearchDigest(searchGeneration.Digest) {
		// A legacy-v1 entry can predate a side-by-side v2 backfill while
		// retaining identical shard bytes. Retire it and fill once under the
		// v2 source/search root before executing a service predicate.
		lease.invalidate()
		lease.release()
		lease, err = s.whole.acquireExact(
			ctx, repoCache, request.Repository, searchGeneration.Revisions,
		)
	}
	if err != nil || lease == nil {
		bindErr := err
		if bindErr == nil {
			bindErr = errors.New("no exact generation reader")
		}
		if errors.Is(bindErr, ErrWholeGenerationWarming) {
			return nil, fmt.Errorf("service search: bind reader: %w", bindErr)
		}
		if repairErr := s.requestWholeRepair(
			ctx, request.Repository, searchGeneration.Revisions,
		); repairErr != nil {
			bindErr = errors.Join(bindErr, repairErr)
		}
		return nil, fmt.Errorf(
			"service search: %w: bind reader: %v", servicequery.ErrUnavailable, bindErr,
		)
	}
	defer lease.release()
	if !lease.matchesSearchDigest(searchGeneration.Digest) {
		lease.invalidate()
		return nil, fmt.Errorf(
			"service search: %w: reader lacks exact v2 generation binding",
			servicequery.ErrUnavailable,
		)
	}
	s.clearWholeRepair(request.Repository)
	if err := runtime.confirm(ctx, opened); err != nil {
		return nil, fmt.Errorf("service search: pre-query fence: %w", err)
	}
	compiled, err := servicequery.Compile(servicequery.Request{
		Expression: request.Expression, RevisionSelector: request.RevisionSelector,
		Scopes: []servicequery.PreparedScope{prepared},
	})
	if err != nil {
		if errors.Is(err, servicequery.ErrInvalidExpression) {
			return nil, fmt.Errorf("service search: %w", errors.Join(ErrInvalidQuery, err))
		}
		return nil, fmt.Errorf("service search: %w", err)
	}
	result, err := lease.searcher.Search(ctx, compiled.Query, opts.zoektWithin(ctx))
	if err != nil {
		return nil, fmt.Errorf("service search: query: %w", err)
	}
	currentRepo, repoErr := s.authorizeServiceRepository(ctx, request.Repository)
	if repoErr != nil {
		return nil, fmt.Errorf("service search: repository reauthorization: %w", repoErr)
	}
	if !lease.current(ctx, *currentRepo, true) {
		lease.invalidate()
		return nil, fmt.Errorf(
			"service search: %w: reader generation changed", servicequery.ErrUnavailable,
		)
	}
	if err := runtime.confirm(ctx, opened); err != nil {
		return nil, fmt.Errorf("service search: final fence: %w", err)
	}
	versions := map[string]string{
		request.Repository: compiled.Authority.RevisionCommit,
	}
	filterResultVersions(result, versions)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("service search: %w", err)
	}
	if maxFiles := clamp(opts.MaxMatches, 50, 500); len(result.Files) > maxFiles {
		result.Files = result.Files[:maxFiles]
	}
	wire := toResult(result, versions)
	if s.Usage != nil {
		repositories := newRepoCollector()
		for _, file := range wire.Files {
			repositories.add(file.Repo)
		}
		s.Usage(callerCtx, usageEvent(wire.Stats, repositories))
	}
	return &ServiceResult{Result: wire, Authority: compiled.Authority}, nil
}

// authorizeServiceRepository resolves request visibility before the repository
// point lookup. Calling it again after query execution makes revocation fail
// closed before catalog/state confirmation and before any result escapes.
func (s *Searcher) authorizeServiceRepository(
	ctx context.Context,
	repository string,
) (*store.Repo, error) {
	var allow func(store.Repo) bool
	if s.Visible != nil {
		allow = s.Visible(ctx)
	}
	repo, err := s.st.GetRepo(ctx, repository)
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, errors.New("repository lookup returned nil")
	}
	if repo.Deleting || allow != nil && !allow(*repo) {
		return nil, store.ErrNotFound
	}
	return repo, nil
}
