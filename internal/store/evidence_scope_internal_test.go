package store

import "context"

func beginExtractionRun(
	s *Surreal,
	ctx context.Context,
	repo, commit, domain, extractor string,
) (*ExtractionRun, error) {
	return s.BeginExtractionRun(ctx, ExtractionScope{
		Repository: repo,
		Commit:     commit,
		Domain:     domain,
	}, extractor)
}

func currentExtractionScope(
	ctx context.Context,
	s *Surreal,
	repo, domain string,
) (ExtractionScope, error) {
	stored, err := s.GetRepo(ctx, repo)
	if err != nil {
		return ExtractionScope{}, err
	}
	scope := ExtractionScope{
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
	s *Surreal,
	ctx context.Context,
	repo, domain string,
) (*ExtractionRun, error) {
	scope, err := currentExtractionScope(ctx, s, repo, domain)
	if err != nil {
		return nil, err
	}
	return s.LatestPublishedRun(ctx, scope)
}
