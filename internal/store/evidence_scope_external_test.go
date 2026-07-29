package store_test

import (
	"context"

	"github.com/bmeddeb/phebs/internal/store"
)

func beginExtractionRun(
	s *store.Surreal,
	ctx context.Context,
	repo, commit, domain, extractor string,
) (*store.ExtractionRun, error) {
	return s.BeginExtractionRun(ctx, store.ExtractionScope{
		Repository: repo,
		Commit:     commit,
		Domain:     domain,
	}, extractor)
}

func currentExtractionScope(
	ctx context.Context,
	s *store.Surreal,
	repo, domain string,
) (store.ExtractionScope, error) {
	stored, err := s.GetRepo(ctx, repo)
	if err != nil {
		return store.ExtractionScope{}, err
	}
	scope := store.ExtractionScope{
		Repository: repo,
		Commit:     stored.IndexedCommitHash,
		Domain:     domain,
	}
	if stored.IndexedAnalysisUnit != nil {
		scope.UnitDigest = stored.IndexedAnalysisUnit.Digest
	}
	return scope, nil
}

func latestPublishedRun(
	s *store.Surreal,
	ctx context.Context,
	repo, domain string,
) (*store.ExtractionRun, error) {
	scope, err := currentExtractionScope(ctx, s, repo, domain)
	if err != nil {
		return nil, err
	}
	return s.LatestPublishedRun(ctx, scope)
}

func latestExtractionAttempt(
	s *store.Surreal,
	ctx context.Context,
	repo, domain string,
) (*store.ExtractionAttempt, error) {
	scope, err := currentExtractionScope(ctx, s, repo, domain)
	if err != nil {
		return nil, err
	}
	return s.LatestExtractionAttempt(ctx, scope)
}
