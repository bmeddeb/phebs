package t324

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"

	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/spike/t323"
)

func compileServiceQuery(expression, revision string, placements []t323.Placement) (query.Q, error) {
	parsed, err := query.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("parse expression: %w", err)
	}
	if revision == "" || len(placements) == 0 {
		return nil, fmt.Errorf("service query requires revision and placements")
	}
	paths := make([]string, 0, len(placements))
	seen := map[string]struct{}{}
	for _, placement := range placements {
		if placement.Path == "" {
			return nil, fmt.Errorf("empty placement path")
		}
		if _, duplicate := seen[placement.Path]; duplicate {
			continue
		}
		seen[placement.Path] = struct{}{}
		paths = append(paths, placement.Path)
	}
	slices.Sort(paths)
	pathQueries := make([]query.Q, 0, len(paths))
	for _, path := range paths {
		pattern := "^(?:" + regexp.QuoteMeta(path) + ")(?:/|$)"
		compiled, err := syntax.Parse(pattern, syntax.Perl)
		if err != nil {
			return nil, fmt.Errorf("compile placement %q: %w", path, err)
		}
		pathQueries = append(pathQueries, &query.Regexp{Regexp: compiled, FileName: true, CaseSensitive: true})
	}
	return query.Simplify(query.NewAnd(
		&query.Branch{Pattern: revision, Exact: true},
		parsed,
		query.NewOr(pathQueries...),
	)), nil
}

func compileAllCodeQuery(expression, revision string) (query.Q, error) {
	parsed, err := query.Parse(expression)
	if err != nil {
		return nil, err
	}
	if revision == "" {
		return nil, fmt.Errorf("all-code query requires revision")
	}
	return query.Simplify(query.NewAnd(&query.Branch{Pattern: revision, Exact: true}, parsed)), nil
}

func queryContainsPathPredicate(q query.Q) bool {
	found := false
	query.VisitAtoms(q, func(current query.Q) {
		if value, ok := current.(*query.Regexp); ok && value.FileName && !value.Content {
			found = true
		}
	})
	return found
}
