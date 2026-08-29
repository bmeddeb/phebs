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
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, s.queryWallTime())
	defer cancel()
	runtimeStore, ok := s.st.(servicequery.RuntimeStore)
	if !ok || s.whole == nil {
		return nil, fmt.Errorf("service search: runtime authority is unavailable")
	}
	repo, err := s.st.GetRepo(ctx, request.Repository)
	if err != nil {
		return nil, fmt.Errorf("service search: repository: %w", err)
	}
	if s.Visible != nil {
		allow := s.Visible(ctx)
		if allow != nil && !allow(*repo) {
			return nil, fmt.Errorf("service search: repository: %w", store.ErrNotFound)
		}
	}
	opened, err := servicequery.OpenRuntimeScope(
		ctx, s.indexDir, runtimeStore, request.Repository, request.ServiceKey,
	)
	if err != nil {
		return nil, fmt.Errorf("service search: %w", err)
	}
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
	if err := servicequery.ConfirmRuntimeScope(
		ctx, s.indexDir, runtimeStore, opened,
	); err != nil {
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
	currentRepo, repoErr := s.st.GetRepo(ctx, request.Repository)
	if repoErr != nil || !lease.current(ctx, *currentRepo, true) {
		lease.invalidate()
		return nil, fmt.Errorf(
			"service search: %w: reader generation changed", servicequery.ErrUnavailable,
		)
	}
	if err := servicequery.ConfirmRuntimeScope(
		ctx, s.indexDir, runtimeStore, opened,
	); err != nil {
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
